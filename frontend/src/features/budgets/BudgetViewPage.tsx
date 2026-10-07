import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { Pencil, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { Budget, BudgetAnalysis, BudgetRow } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, LinkButton } from '@/components/ui/Button';
import { Field, Select, TextArea } from '@/components/ui/Field';
import { ActionMenu, Alert, Amount, KV, PageHeader, Panel, StatusBadge, Tabs } from '@/components/ui/Misc';
import { ErrorState, SkeletonPage } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { Stepper, type Step } from '@/components/ui/Steps';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { Attachments } from '@/components/Attachments';
import { BarChart, Meter } from '@/components/charts/BarChart';
import { useSubmit } from '@/lib/useSubmit';
import { cmp, isNeg, sub } from '@/lib/decimal';
import { DrillDialog, ForecastMethodField, usePeriodName, ManualForecastDialog, useAnalysisColumns, useAnalysisExport, useRowLabel } from './common';

const versionFlow = ['draft', 'submitted', 'approved', 'active', 'closed'] as const;

const actionPerm: Record<string, string> = {
  submit: 'budgets.submit', approve: 'budgets.approve', reject: 'budgets.approve', activate: 'budgets.activate', revise: 'budgets.create', close: 'budgets.close', archive: 'budgets.close',
};

export function BudgetViewPage() {
  const { id } = useParams();
  const bid = Number(id);
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [tab, setTab] = useState<'analysis' | 'plan'>('analysis');
  const [action, setAction] = useState<string | null>(null);
  const [drill, setDrill] = useState<BudgetRow | null>(null);
  const [forecasting, setForecasting] = useState<BudgetRow | null>(null);
  const [sp, setSp] = useSearchParams();
  const method = sp.get('forecast') || 'straight_line';
  const range = sp.get('range') === 'year' ? 'year' : 'ytd';
  const setParam = (k: string, v: string | null) => { const n = new URLSearchParams(sp); if (v) n.set(k, v); else n.delete(k); setSp(n, { replace: true }); };
  const del = useSubmit();
  const q = useQuery({ queryKey: ['budget', bid], queryFn: () => api.get<Budget>(`/budgets/${bid}`) });
  // Year to date compares the budget of the months started so far with their actuals.
  const today = new Date().toISOString().slice(0, 10);
  const elapsed = q.data?.periods?.filter((p) => p.start_date.slice(0, 10) <= today).length ?? 0;
  const toSeq = range === 'ytd' && elapsed > 0 ? elapsed : undefined;
  const an = useQuery({
    queryKey: ['budget', bid, 'analysis', method, toSeq],
    queryFn: () => api.get<BudgetAnalysis>(`/budgets/${bid}/analysis`, { forecast_method: method, to_seq: toSeq }),
    enabled: !!q.data,
  });
  const cols = useAnalysisColumns({ forecast: true, kind: true, onForecast: (r) => setForecasting(r) });
  const exp = useAnalysisExport();
  const label = useRowLabel();
  const periodName = usePeriodName();
  const b = q.data;
  if (q.error) return <div className="page"><Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel></div>;
  if (!b) return <div className="page"><SkeletonPage kpis={6} /></div>;
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['budget', bid] });
    void qc.invalidateQueries({ queryKey: ['budgets'] });
  };
  const a = an.data;
  const flowIdx = versionFlow.indexOf(b.status as (typeof versionFlow)[number]);
  const steps: Step[] = b.status === 'archived'
    ? versionFlow.map((s) => ({ key: s, label: t(`budgets.statuses.${s}`), state: s === 'closed' ? 'skipped' : 'done' }))
    : versionFlow.map((s, i) => ({ key: s, label: t(`budgets.statuses.${s}`), state: i < flowIdx || (s === 'closed' && b.status === 'closed') ? 'done' : i === flowIdx ? 'current' : 'upcoming' }));
  const exp_ = a?.expense;
  const primary = b.allowed_actions.filter((x) => can(actionPerm[x]) && ['submit', 'approve', 'activate'].includes(x));
  const secondary = b.allowed_actions.filter((x) => can(actionPerm[x]) && !['submit', 'approve', 'activate'].includes(x));
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('budgets.title'), to: '/budgets' }]}
        title={<span className="row" style={{ gap: 12 }}>{b.name} <span className="muted num" style={{ fontWeight: 400 }}>{t('budgets.versionN', { n: f.int(b.version) })}</span> <StatusBadge status={b.status} /></span>}
        subtitle={<>{f.digits(b.code)} · {t('budgets.fiscalYear')} {f.digits(b.fiscal_year_name)}{b.cost_center_name ? ` · ${b.cost_center_name}` : ''}</>}
        actions={<>
          {b.status === 'draft' && can('budgets.edit') && <LinkButton to={`/budgets/${b.id}/edit`} icon={<Pencil size={16} />}>{t('common.edit')}</LinkButton>}
          {primary.map((x) => <Button key={x} variant="accent" onClick={() => setAction(x)}>{t(`budgets.actions.${x as 'submit'}`)}</Button>)}
          <ActionMenu items={[
            ...secondary.map((x) => ({ label: t(`budgets.actions.${x as 'submit'}`), danger: x === 'reject' || x === 'close', onClick: () => setAction(x) })),
            ...(b.status === 'draft' && can('budgets.delete') ? [{ label: t('common.delete'), icon: <Trash2 size={16} />, danger: true, onClick: () => setAction('delete') }] : []),
          ]} />
        </>} />
      {del.error && <div className="section-gap"><Alert>{del.error}</Alert></div>}
      <Panel bodyless className="section-gap">
        <Stepper steps={steps} label={t('budgets.versions')} />
        {b.status !== 'draft' && <div className="next-step"><div className="ns-text"><div className="ns-body">{t('budgets.readOnlyHelp')}</div></div></div>}
      </Panel>
      <KpiGrid loading={!a} count={6}>
        {exp_ && (
          <>
            <Kpi label={`${t('budgets.budget')} — ${range === 'ytd' ? t('budgets.ytd') : t('budgets.annualBudget')}`} value={f.money(exp_.budget)} sub={`${t('budgets.annualBudget')}: ${f.money(exp_.annual)}`} tone="info" />
            <Kpi label={t('budgets.kpi.actual')} value={f.money(exp_.actual)} sub={f.currencyLabel} />
            <Kpi label={t('budgets.kpi.remaining')} value={f.money(exp_.remaining)} tone={isNeg(exp_.remaining) ? 'danger' : 'success'} />
            <Kpi label={t('budgets.kpi.variance')} value={f.money(exp_.variance)} sub={isNeg(exp_.variance) ? t('budgets.unfavourable') : t('budgets.favourable')} tone={isNeg(exp_.variance) ? 'danger' : 'success'} />
            <Kpi label={t('budgets.kpi.utilization')} value={f.percent(exp_.utilization)} footer={<Meter percent={exp_.utilization} format={() => ''} />} tone={Number(exp_.utilization ?? 0) > 100 ? 'danger' : 'neutral'} />
            <Kpi label={t('budgets.kpi.forecast')} value={f.money(exp_.forecast)} sub={`${t(`budgets.forecastMethods.${method as 'manual'}`)} · ${t('budgets.forecastVariance')}: ${f.money(sub(exp_.annual, exp_.forecast))}`}
              tone={cmp(exp_.forecast, exp_.annual) > 0 ? 'warning' : 'primary'} />
          </>
        )}
      </KpiGrid>
      <div className="grid-main">
        <Panel bodyless>
          <Tabs value={tab} onChange={setTab} tabs={[{ key: 'analysis', label: `${t('budgets.budget')} / ${t('budgets.actual')}` }, { key: 'plan', label: t('budgets.periodAmounts') }]} />
          {tab === 'analysis' ? (
            <>
              <div className="filter-bar">
                <div className="filter-row">
                  <Field label={t('budgets.comparePeriod')}>
                    <Select value={range} onChange={(e) => setParam('range', e.target.value === 'year' ? 'year' : null)}>
                      <option value="ytd">{t('budgets.ytd')}</option>
                      <option value="year">{t('budgets.annualBudget')}</option>
                    </Select>
                  </Field>
                  <ForecastMethodField value={method} methods={a?.forecast_methods} onChange={(m) => setParam('forecast', m === 'straight_line' ? null : m)} />
                  <div className="filter-actions">
                    <ExportMenu name={`budget-${b.code}-v${b.version}`} permission="budgets.export" title={b.name} subtitle={t(`budgets.forecastMethods.${method as 'manual'}`)} rows={a?.rows} columns={exp} />
                  </div>
                </div>
                {method === 'previous_year' && a && !a.has_prior_year && <Alert kind="info">{t('budgets.forecastBasis.fallback_no_prior_year')}</Alert>}
                <span className="muted" style={{ fontSize: 'var(--font-size-sm)' }}>{t(`budgets.forecastMethodHelp.${method as 'manual'}`)} {t('budgets.coverageHelp')} {t('budgets.drillHelp')}</span>
              </div>
              {a && a.rows.some((r) => r.kind === 'expense') && (
                <div className="panel-body">
                  <div className="form-section-title" style={{ marginBottom: 8 }}>{t('budgets.monthly')}</div>
                  <BarChart format={(v) => f.money(v)} overLabel={t('budgets.legendOver')}
                    series={[{ key: 'budget', label: t('budgets.legendBudget'), className: 'budget' }, { key: 'actual', label: t('budgets.legendActual'), className: 'actual' }]}
                    groups={a.monthly.map((m) => ({ key: String(m.seq), label: periodName(a.periods.find((p) => p.seq === m.seq)), values: { budget: m.expense_budget, actual: m.expense_actual },
                      over: cmp(m.expense_actual, m.expense_budget) > 0 ? ['actual'] : [] }))} />
                </div>
              )}
              <DataTable rows={a?.rows} loading={an.isFetching} error={an.error} onRetry={() => void an.refetch()} rowKey={(r) => r.key} onRowClick={(r) => setDrill(r)} columns={cols}
                rowClassName={(r) => (r.over ? 'danger-row' : undefined)} columnsKey="budget.view" caption={t('budgets.linesTitle')} />
            </>
          ) : (
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('budgets.account')}</th>
                    <th>{t('budgets.method')}</th>
                    <th className="align-end">{t('budgets.annual')}</th>
                    {b.periods?.map((p) => <th key={p.seq} className="align-end">{periodName(p)}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {b.lines?.map((l) => (
                    <tr key={l.id}>
                      <td>{label({ key: '', label: '', kind: 'expense', account_code: l.account_code, account_name: l.account_name, account_name_en: l.account_name_en, line_id: l.id, cost_center_name: l.cost_center_name } as BudgetRow)}</td>
                      <td>{t(`budgets.methods.${l.allocation_method as 'equal'}`)}</td>
                      <td className="align-end"><Amount value={l.annual_amount} /></td>
                      {l.amounts.map((x, i) => <td key={i} className="align-end"><Amount value={x} blankZero /></td>)}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
        <div className="stack">
          <Panel title={t('common.details')}>
            <KV items={[
              { label: t('budgets.owner'), value: b.cost_center_name },
              { label: t('budgets.expenseTotal'), value: <Amount value={b.expense_total} /> },
              { label: t('budgets.incomeTotal'), value: <Amount value={b.income_total} /> },
              { label: t('budgets.revisionReason'), value: b.revision_reason, hidden: !b.revision_reason },
              { label: t('status.approved'), value: b.approved_by_name && `${b.approved_by_name} — ${f.dateTime(b.approved_at)}` },
              { label: t('common.description'), value: b.description },
            ]} />
          </Panel>
          <Panel bodyless title={t('budgets.versions')}>
            <DataTable rows={b.versions} rowKey={(v) => v.id} onRowClick={(v) => navigate(`/budgets/${v.id}`)} rowClassName={(v) => (v.id === b.id ? 'selected-row' : undefined)}
              columns={[
                { key: 'v', header: t('budgets.version'), cell: (v) => <span className="num">{t('budgets.versionN', { n: f.int(v.version) })}</span> },
                { key: 's', header: t('common.status'), cell: (v) => <StatusBadge status={v.status} /> },
                { key: 'r', header: t('budgets.revisionReason'), cell: (v) => v.revision_reason },
              ]} />
          </Panel>
          <Attachments entity="budget" id={b.id} canEdit={can('budgets.edit')} />
          <Link to={`/budgets/reports/actual-vs-budget?budget_id=${b.id}`}>{t('budgets.reportsTitle')}</Link>
        </div>
      </div>
      {drill && <DrillDialog row={drill} fromSeq={a?.from_seq} toSeq={a?.to_seq} onClose={() => setDrill(null)} />}
      {forecasting && <ManualForecastDialog row={forecasting} onClose={() => setForecasting(null)} onSaved={() => { setForecasting(null); void qc.invalidateQueries({ queryKey: ['budget', bid, 'analysis'] }); }} />}
      {action === 'delete' && (
        <ConfirmDialog title={t('common.delete')} body={t('common.confirmDelete')} confirmLabel={t('common.yesDelete')} negative pending={del.pending} error={del.error} onClose={() => setAction(null)}
          onConfirm={() => void del.run(() => api.del(`/budgets/${b.id}`).then(() => { void qc.invalidateQueries({ queryKey: ['budgets'] }); navigate('/budgets'); }), t('common.deleted'))} />
      )}
      {action === 'revise' && <ReviseDialog budget={b} onClose={() => setAction(null)} onDone={(nb) => { setAction(null); refresh(); navigate(`/budgets/${nb.id}/edit`); }} />}
      {action && !['delete', 'revise'].includes(action) && (
        <WorkflowDialog budget={b} action={action} lang={lang} onClose={() => setAction(null)} onDone={() => { setAction(null); refresh(); }} />
      )}
    </div>
  );
}

function WorkflowDialog({ budget, action, onClose, onDone }: { budget: Budget; action: string; lang: string; onClose: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const { run, pending, error } = useSubmit();
  const label = t(`budgets.actions.${action as 'submit'}`);
  return (
    <ConfirmDialog title={`${label} — ${budget.name}`} body={t('budgets.confirmAction', { action: label })} confirmLabel={label} negative={action === 'reject' || action === 'close'}
      pending={pending} error={error} onClose={onClose}
      onConfirm={async () => { const ok = await run(() => api.post(`/budgets/${budget.id}/${action}`).then(() => true), t('common.saved')); if (ok) onDone(); }} />
  );
}

function ReviseDialog({ budget, onClose, onDone }: { budget: Budget; onClose: () => void; onDone: (b: Budget) => void }) {
  const { t } = useI18n();
  const [reason, setReason] = useState('');
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  return (
    <Dialog title={`${t('budgets.revise')} — ${budget.name}`} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending}
        onClick={async () => { const b = await run(() => api.post<Budget>(`/budgets/${budget.id}/revise`, { reason }), t('common.saved')); if (b) onDone(b); }}>{t('budgets.revise')}</Button></>}>
      <div className="stack">
        <Alert kind="info">{t('budgets.reviseHelp')}</Alert>
        {error && <Alert>{error}</Alert>}
        <Field label={t('budgets.revisionReason')} required error={errors.reason}><TextArea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} autoFocus /></Field>
      </div>
    </Dialog>
  );
}
