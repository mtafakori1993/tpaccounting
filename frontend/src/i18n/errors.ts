import { ApiError } from '@/api/client';
import { useI18n, type Params } from './I18nProvider';
import { useFormat } from './format';

const amountParams = new Set(['difference', 'debit', 'credit', 'balance', 'budget', 'actual', 'excess']);

/** Translates API errors into user-facing messages; raw server text is never shown. */
export function useErrorMessage() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  return (err: unknown): string => {
    if (!(err instanceof ApiError)) return t('errors.internal');
    const params: Params = {};
    for (const [k, v] of Object.entries(err.params)) {
      if (amountParams.has(k)) params[k] = f.money(String(v));
      else if (k === 'date') params[k] = f.date(String(v));
      else if (k === 'available' || k === 'requested') params[k] = f.qty(String(v));
      else if (k === 'entity') params[k] = tryT(`entities.${v}`) ?? String(v);
      else if (k === 'key') params[k] = tryT(`settings.mappingKeys.${v}`) ?? String(v);
      else if (k === 'utilization') params[k] = f.digits(String(v));
      else if (k === 'action') params[k] = tryT(`checks.actions.${v}`) ?? tryT(`payroll.actions.${v}`) ?? tryT(`budgets.actions.${v}`) ?? String(v);
      else if (k === 'status') params[k] = tryT(`status.${v}`) ?? String(v);
      else params[k] = typeof v === 'number' ? f.count(v) : String(v);
    }
    return tryT(`errors.${err.code}`, params) ?? t('errors.internal');
  };
}

/** Translates a field-level validation code. */
export function useFieldError() {
  const { tryT, t } = useI18n();
  return (code?: string) => (code ? tryT(`validation.${code}`) ?? tryT(`errors.${code}`) ?? t('validation.invalid') : undefined);
}
