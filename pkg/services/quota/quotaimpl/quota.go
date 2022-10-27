package quotaimpl

import (
	"context"
	"fmt"
	"sync"

	"github.com/grafana/grafana/pkg/infra/db"
	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/models"
	"github.com/grafana/grafana/pkg/services/quota"
	"github.com/grafana/grafana/pkg/setting"
	"golang.org/x/sync/errgroup"
)

type Service struct {
	store  store
	Cfg    *setting.Cfg
	Logger log.Logger

	mutex     sync.RWMutex
	reporters map[quota.TargetSrv]quota.UsageReporterFunc

	defaultLimits *quota.Map
}

type ServiceDisabled struct{}

func (s *ServiceDisabled) QuotaReached(c *models.ReqContext, target string) (bool, error) {
	return false, quota.ErrDisabled
}

func (s *ServiceDisabled) Get(ctx context.Context, scope string, id int64) ([]quota.QuotaDTO, error) {
	return nil, quota.ErrDisabled
}

func (s *ServiceDisabled) Update(ctx context.Context, cmd *quota.UpdateQuotaCmd) error {
	return quota.ErrDisabled
}

func (s *ServiceDisabled) CheckQuotaReached(ctx context.Context, target string, scopeParams *quota.ScopeParameters) (bool, error) {
	return false, quota.ErrDisabled
}

func (s *ServiceDisabled) DeleteByUser(ctx context.Context, userID int64) error {
	return quota.ErrDisabled
}

func (s *ServiceDisabled) AddReporter(_ context.Context, e *quota.NewQuotaReporter) error {
	return nil
}

func ProvideService(db db.DB, cfg *setting.Cfg) quota.Service {
	s := Service{
		store:         &sqlStore{db: db},
		Cfg:           cfg,
		Logger:        log.New("quota_service"),
		reporters:     make(map[quota.TargetSrv]quota.UsageReporterFunc),
		defaultLimits: &quota.Map{},
	}

	if s.IsDisabled() {
		return &ServiceDisabled{}
	}

	return &s
}

func (s *Service) IsDisabled() bool {
	return !s.Cfg.Quota.Enabled
}

// QuotaReached checks that quota is reached for a target. Runs CheckQuotaReached and take context and scope parameters from the request context
func (s *Service) QuotaReached(c *models.ReqContext, target string) (bool, error) {
	// No request context means this is a background service, like LDAP Background Sync
	if c == nil {
		return false, nil
	}

	var params *quota.ScopeParameters
	if c.IsSignedIn {
		params = &quota.ScopeParameters{
			OrgID:  c.OrgID,
			UserID: c.UserID,
		}
	}
	return s.CheckQuotaReached(c.Req.Context(), target, params)
}

func (s *Service) Get(ctx context.Context, scope string, id int64) ([]quota.QuotaDTO, error) {
	quotaScope := quota.Scope(scope)
	if err := quotaScope.Validate(); err != nil {
		return nil, err
	}

	q := make([]quota.QuotaDTO, 0)

	scopeParams := quota.ScopeParameters{}
	if quotaScope == quota.OrgScope {
		scopeParams.OrgID = id
	} else if quotaScope == quota.UserScope {
		scopeParams.UserID = id
	}

	customLimits, err := s.store.Get(ctx, &scopeParams)
	if err != nil {
		return nil, err
	}

	u, err := s.getUsage(ctx, &scopeParams)
	if err != nil {
		return nil, err
	}

	for item := range s.defaultLimits.Iter() {
		limit := item.Value

		scp, err := item.Tag.GetScope()
		if err != nil {
			return nil, err
		}

		if scp != quota.Scope(scope) {
			continue
		}

		if targetCustomLimit, ok := customLimits.Get(item.Tag); ok {
			limit = targetCustomLimit
		}

		target, err := item.Tag.GetTarget()
		if err != nil {
			return nil, err
		}

		srv, err := item.Tag.GetSrv()
		if err != nil {
			return nil, err
		}

		used, _ := u.Get(item.Tag)
		q = append(q, quota.QuotaDTO{
			Target:  string(target),
			Limit:   limit,
			OrgId:   scopeParams.OrgID,
			UserId:  scopeParams.UserID,
			Used:    used,
			Service: string(srv),
			Scope:   scope,
		})
	}

	return q, nil
}

func (s *Service) Update(ctx context.Context, cmd *quota.UpdateQuotaCmd) error {
	targetFound := false
	knownTargets, err := s.defaultLimits.Targets()
	if err != nil {
		return err
	}

	for t := range knownTargets {
		if t == quota.Target(cmd.Target) {
			targetFound = true
		}
	}
	if !targetFound {
		return quota.ErrInvalidTarget.Errorf("unknown quota target: %s", cmd.Target)
	}
	return s.store.Update(ctx, cmd)
}

// CheckQuotaReached check that quota is reached for a target. If ScopeParameters are not defined, only global scope is checked
func (s *Service) CheckQuotaReached(ctx context.Context, target string, scopeParams *quota.ScopeParameters) (bool, error) {
	targetSrvLimits, err := s.getOverridenLimits(ctx, quota.TargetSrv(target), scopeParams)
	if err != nil {
		return false, err
	}

	usageReporterFunc, ok := s.getReporter(quota.TargetSrv(target))
	if !ok {
		return false, quota.ErrInvalidTargetSrv
	}
	targetUsage, err := usageReporterFunc(ctx, scopeParams)
	if err != nil {
		return false, err
	}

	for t, limit := range targetSrvLimits {
		switch {
		case limit < 0:
			continue
		case limit == 0:
			return true, nil
		default:
			u, ok := targetUsage.Get(t)
			if !ok {
				return false, fmt.Errorf("no usage for target:%s", t)
			}
			if u >= limit {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) DeleteByUser(ctx context.Context, userID int64) error {
	return s.store.DeleteByUser(ctx, userID)
}

func (s *Service) AddReporter(_ context.Context, e *quota.NewQuotaReporter) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	_, ok := s.reporters[e.TargetSrv]
	if ok {
		return quota.ErrTargetSrvConflict.Errorf("target service: %s already exists", e.TargetSrv)
	}

	s.reporters[e.TargetSrv] = e.Reporter

	s.defaultLimits.Merge(e.DefaultLimits)

	return nil
}

func (s *Service) getReporter(target quota.TargetSrv) (quota.UsageReporterFunc, bool) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	r, ok := s.reporters[target]
	return r, ok
}

type reporter struct {
	target       quota.TargetSrv
	reporterFunc quota.UsageReporterFunc
}

func (s *Service) getReporters() <-chan reporter {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	ch := make(chan reporter)
	go func() {
		defer close(ch)
		for t, r := range s.reporters {
			ch <- reporter{target: t, reporterFunc: r}
		}
	}()

	return ch
}

func (s *Service) getOverridenLimits(ctx context.Context, targetSrv quota.TargetSrv, scopeParams *quota.ScopeParameters) (map[quota.Tag]int64, error) {
	targetSrvLimits := make(map[quota.Tag]int64)

	customLimits, err := s.store.Get(ctx, scopeParams)
	if err != nil {
		return targetSrvLimits, err
	}

	for item := range s.defaultLimits.Iter() {
		srv, err := item.Tag.GetSrv()
		if err != nil {
			return nil, err
		}

		if srv != targetSrv {
			continue
		}

		defaultLimit := item.Value

		if customLimit, ok := customLimits.Get(item.Tag); ok {
			targetSrvLimits[item.Tag] = customLimit
		} else {
			targetSrvLimits[item.Tag] = defaultLimit
		}
	}

	return targetSrvLimits, nil
}

func (s *Service) getUsage(ctx context.Context, scopeParams *quota.ScopeParameters) (*quota.Map, error) {
	usage := &quota.Map{}
	g, ctx := errgroup.WithContext(ctx)

	for r := range s.getReporters() {
		r := r
		g.Go(func() error {
			u, err := r.reporterFunc(ctx, scopeParams)
			if err != nil {
				return err
			}
			usage.Merge(u)
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return usage, nil
}
