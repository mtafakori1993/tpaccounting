import { useEffect, useRef, useState } from 'react';
import { ApiError, setBudgetHandlers, type BudgetDecision } from '@/api/client';
import type { BudgetWarning } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useToast } from '@/components/ui/Toast';
import { Button } from '@/components/ui/Button';
import { Dialog } from '@/components/ui/Dialog';
import { Alert } from '@/components/ui/Misc';
import { Field, TextArea } from '@/components/ui/Field';

/**
 * Global budget-control UI. Any request that posts to the ledger may be stopped
 * by budget control; this asks the user to confirm (soft limit) or to give an
 * override reason (hard limit) and the API client retries the same request.
 * Warnings of successful postings are shown as toasts.
 */
export function BudgetPrompt() {
  const { t } = useI18n();
  const f = useFormat();
  const toast = useToast();
  const [err, setErr] = useState<ApiError | null>(null);
  const [reason, setReason] = useState('');
  const resolver = useRef<((d: BudgetDecision) => void) | null>(null);
  const ft = useRef({ t, f, toast });
  ft.current = { t, f, toast };

  useEffect(() => {
    setBudgetHandlers(
      (e) => new Promise<BudgetDecision>((resolve) => {
        resolver.current = resolve;
        setReason('');
        setErr(e);
      }),
      (ws) => {
        const { t, f, toast } = ft.current;
        for (const w of ws as BudgetWarning[]) {
          toast.info(t('budgets.prompt.warning', { account: `${f.digits(w.account_code)} ${w.account_name}`, utilization: f.digits(String(w.utilization)), budget: w.budget }));
        }
      },
    );
    return () => setBudgetHandlers(null, null);
  }, []);

  if (!err) return null;
  const finish = (d: BudgetDecision) => {
    resolver.current?.(d);
    resolver.current = null;
    setErr(null);
  };
  const p = err.params;
  const params = {
    account: String(p.account ?? ''), budget_name: String(p.budget_name ?? ''), budget: f.money(String(p.budget ?? 0)),
    actual: f.money(String(p.actual ?? 0)), excess: f.money(String(p.excess ?? 0)), utilization: f.digits(String(p.utilization ?? '')),
  };
  const hard = err.code === 'budget_exceeded';
  return (
    <Dialog title={hard ? t('budgets.prompt.overrideTitle') : t('budgets.prompt.confirmTitle')} onClose={() => finish(null)}
      footer={<>
        <Button onClick={() => finish(null)}>{t('common.cancel')}</Button>
        {hard
          ? <Button variant="negative" disabled={!reason.trim()} onClick={() => finish({ override: reason.trim() })}>{t('budgets.prompt.override')}</Button>
          : <Button variant="accent" onClick={() => finish({ confirm: true })}>{t('budgets.prompt.confirm')}</Button>}
      </>}>
      <div className="stack">
        <Alert kind="warning">{hard ? t('budgets.prompt.overrideBody', params) : t('budgets.prompt.confirmBody', params)}</Alert>
        {hard && (
          <>
            <p className="muted">{t('budgets.prompt.overrideAllowed')}</p>
            <Field label={t('budgets.prompt.reason')} required>
              <TextArea value={reason} onChange={(e) => setReason(e.target.value)} rows={3} autoFocus />
            </Field>
          </>
        )}
      </div>
    </Dialog>
  );
}
