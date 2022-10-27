import { css, cx } from '@emotion/css';
import { flattenDeep, compact } from 'lodash';
import React, { FC, useCallback, useEffect, useMemo, useState } from 'react';
import { useFieldArray, useFormContext } from 'react-hook-form';

import { GrafanaTheme, SelectableValue } from '@grafana/data';
import { Button, Field, InlineLabel, Label, useStyles } from '@grafana/ui';
import { useDispatch } from 'app/types';
import { RulerRuleGroupDTO } from 'app/types/unified-alerting-dto';

import { useUnifiedAlertingSelector } from '../../hooks/useUnifiedAlertingSelector';
import { fetchRulerRulesIfNotFetchedYet } from '../../state/actions';
import { RuleFormValues } from '../../types/rule-form';
import { GRAFANA_RULES_SOURCE_NAME } from '../../utils/datasource';
import AlertLabelDropdown from '../AlertLabelDropdown';

interface Props {
  className?: string;
}

const useGetCustomLabels = () => {
  const dispatch = useDispatch();

  useEffect(() => {
    dispatch(fetchRulerRulesIfNotFetchedYet(GRAFANA_RULES_SOURCE_NAME));
  }, [dispatch]);

  const rulerRuleRequests = useUnifiedAlertingSelector((state) => state.rulerRules);

  const rulerRequest = rulerRuleRequests[GRAFANA_RULES_SOURCE_NAME];

  if (!rulerRequest || rulerRequest.loading) {
    return;
  }

  const result = rulerRequest.result || {};

  //store all labels in a flat array and remove empty values
  const labels = compact(
    flattenDeep(
      Object.keys(result).map((ruleGroupKey) =>
        result[ruleGroupKey].map((ruleItem: RulerRuleGroupDTO) => ruleItem.rules.map((item) => item.labels))
      )
    )
  );

  const labelsByKey: Record<string, string[]> = {};

  labels.forEach((label: Record<string, string>) => {
    Object.entries(label).forEach(([key, value]) => {
      labelsByKey[key] = [...(labelsByKey[key] || []), value];
    });
  });

  return labelsByKey;
};

function mapLabelsToOptions(items: string[] = []): Array<SelectableValue<string>> {
  return items.map((item) => ({ label: item, value: item }));
}

const LabelsField: FC<Props> = ({ className }) => {
  const styles = useStyles(getStyles);
  const {
    register,
    control,
    watch,
    formState: { errors },
    setValue,
  } = useFormContext<RuleFormValues>();

  const labels = watch('labels');
  const { fields, append, remove } = useFieldArray({ control, name: 'labels' });

  const labelsByKey = useGetCustomLabels();

  const [selectedKey, setSelectedKey] = useState('');

  const keys = useMemo(() => {
    if (!labelsByKey) {
      return [];
    }
    return mapLabelsToOptions(Object.keys(labelsByKey));
  }, [labelsByKey]);

  const getValuesForLabel = useCallback(
    (key: string) => {
      if (!labelsByKey || !key) {
        return [];
      }

      return mapLabelsToOptions(labelsByKey[key]);
    },
    [labelsByKey]
  );

  const values = useMemo(() => {
    return getValuesForLabel(selectedKey);
  }, [selectedKey, getValuesForLabel]);

  return (
    <div className={cx(className, styles.wrapper)}>
      <Label>Custom Labels</Label>
      <>
        <div className={styles.flexRow}>
          <InlineLabel width={18}>Labels</InlineLabel>
          <div className={styles.flexColumn}>
            {fields.map((field, index) => {
              return (
                <div key={field.id}>
                  <div className={cx(styles.flexRow, styles.centerAlignRow)}>
                    <Field
                      className={styles.labelInput}
                      invalid={Boolean(errors.labels?.[index]?.key?.message)}
                      error={errors.labels?.[index]?.key?.message}
                      data-testid={`label-key-${index}`}
                    >
                      <AlertLabelDropdown
                        {...register(`labels.${index}.key`, {
                          required: { value: Boolean(labels[index]?.value), message: 'Required.' },
                        })}
                        defaultValue={field.key ? { label: field.key, value: field.key } : undefined}
                        options={keys}
                        onChange={(newValue: SelectableValue) => {
                          setValue(`labels.${index}.key`, newValue.value);
                          setSelectedKey(newValue.value);
                        }}
                        type="key"
                      />
                    </Field>
                    <InlineLabel className={styles.equalSign}>=</InlineLabel>
                    <Field
                      className={styles.labelInput}
                      invalid={Boolean(errors.labels?.[index]?.value?.message)}
                      error={errors.labels?.[index]?.value?.message}
                      data-testid={`label-value-${index}`}
                    >
                      <AlertLabelDropdown
                        {...register(`labels.${index}.value`, {
                          required: { value: Boolean(labels[index]?.key), message: 'Required.' },
                        })}
                        defaultValue={field.value ? { label: field.value, value: field.value } : undefined}
                        options={values}
                        onChange={(newValue: SelectableValue) => {
                          setValue(`labels.${index}.value`, newValue.value);
                        }}
                        onOpenMenu={() => {
                          setSelectedKey(labels[index].key);
                        }}
                        type="value"
                      />
                    </Field>

                    <Button
                      className={styles.deleteLabelButton}
                      aria-label="delete label"
                      icon="trash-alt"
                      data-testid={`delete-label-${index}`}
                      variant="secondary"
                      onClick={() => {
                        remove(index);
                      }}
                    />
                  </div>
                </div>
              );
            })}
            <Button
              className={styles.addLabelButton}
              icon="plus-circle"
              type="button"
              variant="secondary"
              onClick={() => {
                append({});
              }}
            >
              Add label
            </Button>
          </div>
        </div>
      </>
    </div>
  );
};

const getStyles = (theme: GrafanaTheme) => {
  return {
    wrapper: css`
      margin-bottom: ${theme.spacing.xl};
    `,
    flexColumn: css`
      display: flex;
      flex-direction: column;
    `,
    flexRow: css`
      display: flex;
      flex-direction: row;
      justify-content: flex-start;

      & + button {
        margin-left: ${theme.spacing.xs};
      }
    `,
    deleteLabelButton: css`
      margin-left: ${theme.spacing.xs};
      align-self: flex-start;
    `,
    addLabelButton: css`
      flex-grow: 0;
      align-self: flex-start;
    `,
    centerAlignRow: css`
      align-items: baseline;
    `,
    equalSign: css`
      align-self: flex-start;
      width: 28px;
      justify-content: center;
      margin-left: ${theme.spacing.xs};
    `,
    labelInput: css`
      width: 175px;
      margin-bottom: ${theme.spacing.sm};
      & + & {
        margin-left: ${theme.spacing.sm};
      }
    `,
  };
};

export default LabelsField;
