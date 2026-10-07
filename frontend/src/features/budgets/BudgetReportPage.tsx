import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, Navigate, useParams, useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import type { Budget, BudgetAnalysis, BudgetRow } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Checkbox, Field, Select } from '@/components/ui/Field';
import { Alert, PageHeader, Panel } from '@/components/ui/Misc';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { ChevronRight, Clock } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { BarChart } from '@/components/charts/BarChart';
import { CostCenterSelect } from '@/components/pickers/Pickers';
import { cmp, isNeg } from '@/lib/decimal';
import { DrillDialog, ForecastMethodField, usePeriodName, useAnalysisColumns, useAnalysisExport, useFiscalYears } from './common';

const reports = ['actual-vs-budget', 'variance', 'expense', 'income', 'cost-center', 'project', 'monthly', 'annual', 'over-budget', 'forecast'] as const;
type Report = (typeof reports)[number];

export function BudgetReportPage() {
  const { report } = useParams();
  const { t } = useI18n();
  if (!report) {
    return (
      <div className="page">
        <PageHeader crumbs={[{ label: t('budgets.title'), to: '/budgets' }]} title={t('budgets.reportsTitle')} subtitle={t('budgets.reportsSubtitle')} />
        <div className="report-groups">
          {[reports.slice(0, 5), reports.slice(5)].map((group, gi) => (
            <Panel key={gi} bodyless>
              {group.map((r) => (
                <Link key={r} to={`/budgets/reports/${r}`} className="link-row">
                  <span>
                    <span className="lr-title" style={{ display: 'block' }}>{t(`budgets.reports.names.${r}`)}</span>
                    <span className="lr-desc">{t(`budgets.reports.descriptions.${r}`)}</span>
                  </span>
                  <ChevronRight size={16} className="icon-directional muted" aria-hidden />
                </Link>
              ))}
            </Panel>
          ))}
        </div>
      </div>
    );
  }
  if (!reports.includes(report as Report)) return <Navigate to="/budgets/reports" replace />;
  return <ReportView report={report as Report} />;
}

function ReportView({ report }: { report: Report }) {
  const { t } = useI18n();
  const f = useFormat();
  const { me } = useAuth();
  const years = useFiscalYears();
  const [sp, setSp] = useSearchParams();
  const [drill, setDrill] = useState<BudgetRow | null>(null);
  const get = (k: string) => sp.get(k) ?? '';
  const set = (patch: Record<string, string>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) (v ? n.set(k, v) : n.delete(k));
    setSp(n, { replace: true });
  };
  const fy = get('fiscal_year_id') || String(me?.fiscal_year?.id ?? '');
  const budgetsQ = useQuery({ queryKey: ['budgets', 'fy', fy], queryFn: () => api.get<Budget[]>('/budgets', { fiscal_year_id: fy }), enabled: !!fy });
  const groupable = report === 'actual-vs-budget' || report === 'variance' || report === 'expense' || report === 'income' || report === 'forecast' || report === 'over-budget';
  const params = {
    fiscal_year_id: get('budget_id') ? '' : fy, budget_id: get('budget_id'), from_seq: get('from_seq'), to_seq: get('to_seq'), cost_center_id: get('cost_center_id'),
    group_by: groupable ? get('group_by') : '', over_only: get('over_only'), kind: get('kind'), forecast_method: get('forecast'),
  };
  const showsForecast = report === 'forecast' || report === 'annual';
  const q = useQuery({ queryKey: ['budget-report', report, params], queryFn: () => api.get<BudgetAnalysis>(`/budgets/reports/${report}`, params) });
  const a = q.data;
  const baseCols = useAnalysisColumns({ forecast: report === 'forecast' || report === 'annual', kind: report !== 'expense' && report !== 'income' });
  const exp = useAnalysisExport();
  const cols: Column<BudgetRow>[] = report === 'monthly'
    ? [{ key: 'm', header: t('budgets.groups.month'), cell: (r) => `${f.digits(r.label)} — ${t(`budgets.kinds.${r.kind}`)}` }, ...baseCols.slice(1)]
    : baseCols;
  const title = t(`budgets.reports.names.${report}`);
  const periods = a?.periods ?? [];
  const periodName = usePeriodName();
  const rowsForChart = report === 'monthly' ? a?.monthly : null;
  const advancedActive = ['from_seq', 'to_seq', 'group_by', 'over_only', 'forecast'].filter((k) => get(k)).length;
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('budgets.reportsTitle'), to: '/budgets/reports' }]} title={title} subtitle={t(`budgets.reports.descriptions.${report}`)}
        meta={q.dataUpdatedAt ? <><Clock size={13} aria-hidden /> {t('ui.updatedAt', { time: f.dateTime(new Date(q.dataUpdatedAt).toISOString()) })}</> : null}
        actions={<ExportMenu name={`budget-${report}`} permission="budgets.export" title={title} rows={a?.rows} columns={exp} />} />
      {q.error && !a ? <Panel bodyless><ErrorState error={q.error} title={t('ui.reportFailed')} onRetry={() => void q.refetch()} /></Panel> : (
        <>
          <KpiGrid loading={!a} count={4}>
            {a && (
              <>
                <Kpi label={t('budgets.expenseTotal')} value={f.money(a.expense.budget)} sub={f.currencyLabel} tone="info" />
                <Kpi label={t('budgets.actualSpending')} value={f.money(a.expense.actual)} sub={`${t('budgets.utilization')}: ${f.percent(a.expense.utilization)}`} />
                <Kpi label={t('budgets.variance')} value={f.money(a.expense.variance)} tone={isNeg(a.expense.variance) ? 'danger' : 'success'}
                  sub={isNeg(a.expense.variance) ? t('budgets.unfavourable') : t('budgets.favourable')} />
                {showsForecast
                  ? <Kpi label={t('budgets.kpi.forecast')} value={f.money(a.expense.forecast)} sub={t(`budgets.forecastMethods.${(a.forecast_method || 'straight_line') as 'manual'}`)} tone="primary" />
                  : <Kpi label={t('budgets.incomeVsBudget')} value={f.money(a.income.actual)} sub={`${t('budgets.budget')}: ${f.money(a.income.budget)}`} />}
              </>
            )}
          </KpiGrid>
          <Panel bodyless>
            <FilterBar
              activeCount={['budget_id', 'cost_center_id', 'from_seq', 'to_seq', 'group_by', 'over_only', 'forecast'].filter((k) => get(k)).length}
              advancedActive={advancedActive}
              onReset={() => set({ budget_id: '', cost_center_id: '', from_seq: '', to_seq: '', group_by: '', over_only: '', forecast: '' })}
              basic={<>
                <Field label={t('budgets.fiscalYear')}>
                  <Select value={fy} onChange={(e) => set({ fiscal_year_id: e.target.value, budget_id: '' })}>
                    {years.data?.map((y) => <option key={y.id} value={y.id}>{f.digits(y.name)}</option>)}
                  </Select>
                </Field>
                <Field label={t('budgets.title')}>
                  <Select value={get('budget_id')} onChange={(e) => set({ budget_id: e.target.value })}>
                    <option value="">{t('budgets.allBudgets')}</option>
                    {budgetsQ.data?.map((b) => <option key={b.id} value={b.id}>{`${b.name} — ${t('budgets.versionN', { n: f.int(b.version) })} (${t(`budgets.statuses.${b.status as 'draft'}`)})`}</option>)}
                  </Select>
                </Field>
                <Field label={t('budgets.costCenter')}><CostCenterSelect emptyLabel={t('common.all')} value={Number(get('cost_center_id')) || null} onChange={(x) => set({ cost_center_id: x ? String(x) : '' })} /></Field>
                {showsForecast && <ForecastMethodField value={get('forecast') || 'straight_line'} methods={a?.forecast_methods} onChange={(m) => set({ forecast: m === 'straight_line' ? '' : m })} />}
              </>}
              advanced={<>
                <Field label={t('budgets.fromPeriod')}>
                  <Select value={get('from_seq')} onChange={(e) => set({ from_seq: e.target.value })}>
                    <option value="">—</option>
                    {periods.map((p) => <option key={p.seq} value={p.seq}>{periodName(p, true)}</option>)}
                  </Select>
                </Field>
                <Field label={t('budgets.toPeriod')}>
                  <Select value={get('to_seq')} onChange={(e) => set({ to_seq: e.target.value })}>
                    <option value="">—</option>
                    {periods.map((p) => <option key={p.seq} value={p.seq}>{periodName(p, true)}</option>)}
                  </Select>
                </Field>
                {groupable && (
                  <Field label={t('budgets.groupBy')}>
                    <Select value={get('group_by') || 'line'} onChange={(e) => set({ group_by: e.target.value === 'line' ? '' : e.target.value })}>
                      {(['line', 'account', 'cost_center', 'budget', 'month'] as const).map((g) => <option key={g} value={g}>{t(`budgets.groups.${g}`)}</option>)}
                    </Select>
                  </Field>
                )}
                {report !== 'over-budget' && <div style={{ alignSelf: 'end', paddingBottom: 6 }}><Checkbox label={t('budgets.overOnly')} checked={get('over_only') === 'true'} onChange={(x) => set({ over_only: x ? 'true' : '' })} /></div>}
              </>}
            />
            {a && a.budgets.length === 0 && <div style={{ padding: '12px 20px 0' }}><Alert kind="info">{t('budgets.noActive')}</Alert></div>}
            {rowsForChart && rowsForChart.length > 0 && (
              <div className="panel-body">
                <BarChart format={(v) => f.money(v)} overLabel={t('budgets.legendOver')}
                  series={[{ key: 'budget', label: t('budgets.legendBudget'), className: 'budget' }, { key: 'actual', label: t('budgets.legendActual'), className: 'actual' }]}
                  groups={rowsForChart.map((m) => ({ key: String(m.seq), label: periodName(periods.find((p) => p.seq === m.seq)), values: { budget: m.expense_budget, actual: m.expense_actual },
                    over: cmp(m.expense_actual, m.expense_budget) > 0 ? ['actual'] : [] }))} />
              </div>
            )}
            <DataTable rows={a?.rows} loading={q.isFetching} error={q.error} onRetry={() => void q.refetch()} rowKey={(r) => r.key} columns={cols} onRowClick={(r) => r.line_id && setDrill(r)}
              rowClassName={(r) => (r.over ? 'danger-row' : undefined)} caption={title} columnsKey={`budget.report.${report}`}
              empty={<EmptyState compact filtered={advancedActive > 0} title={t('common.noResults')} body={a && a.budgets.length === 0 ? t('budgets.noActive') : t('common.noResultsHint')} />} />
          </Panel>
        </>
      )}
      {drill && <DrillDialog row={drill} fromSeq={a?.from_seq} toSeq={a?.to_seq} onClose={() => setDrill(null)} />}
    </div>
  );
}
