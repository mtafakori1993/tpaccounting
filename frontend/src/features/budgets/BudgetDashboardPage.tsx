import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { X } from 'lucide-react';
import { api } from '@/api/client';
import type { BudgetDashboard, BudgetRow } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Field, Select } from '@/components/ui/Field';
import { Amount, EmptyState, PageHeader, Panel } from '@/components/ui/Misc';
import { ErrorState } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { DataTable } from '@/components/ui/DataTable';
import { BarChart, Meter } from '@/components/charts/BarChart';
import { ExportMenu } from '@/components/ExportMenu';
import { cmp, isNeg, isZero } from '@/lib/decimal';
import { DrillDialog, usePeriodName, useAnalysisColumns, useAnalysisExport, useFiscalYears } from './common';

export function BudgetDashboardPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { me } = useAuth();
  const navigate = useNavigate();
  const years = useFiscalYears();
  const [fy, setFy] = useState<number | null>(me?.fiscal_year?.id ?? null);
  const [cc, setCc] = useState<{ id: number | null; name: string } | null>(null);
  const [month, setMonth] = useState<number | null>(null);
  const [drill, setDrill] = useState<BudgetRow | null>(null);
  const q = useQuery({ queryKey: ['budget-dashboard', fy], queryFn: () => api.get<BudgetDashboard>('/budgets/dashboard', { fiscal_year_id: fy }) });
  const monthQ = useQuery({
    queryKey: ['budget-dashboard', fy, 'month', month],
    queryFn: () => api.get<BudgetDashboard>('/budgets/dashboard', { fiscal_year_id: fy, from_seq: month, to_seq: month }),
    enabled: month !== null,
  });
  const cols = useAnalysisColumns();
  const exp = useAnalysisExport();
  const d = q.data;
  const pct = (v: string) => f.percent(v);
  const periodName = usePeriodName();
  const base = month !== null ? monthQ.data : d;
  const lines = (base?.rows ?? []).filter((r) => !cc || (r.cost_center_id ?? null) === cc.id);
  const kpi = (label: string, value: string, sub?: string, lead?: boolean, neg?: boolean) => (
    <Kpi label={label} value={value} sub={sub} tone={neg ? 'danger' : lead ? 'primary' : 'neutral'} />
  );
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('budgets.title'), to: '/budgets' }]} title={t('budgets.dashboardTitle')} subtitle={t('budgets.dashboardSubtitle')} actions={<>
        <Field label={t('budgets.fiscalYear')}>
          <Select value={fy ?? ''} onChange={(e) => { setFy(e.target.value ? Number(e.target.value) : null); setCc(null); setMonth(null); }}>
            {years.data?.map((y) => <option key={y.id} value={y.id}>{f.digits(y.name)}</option>)}
          </Select>
        </Field>
      </>} />
      {q.error && !d && <Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel>}
      {q.isLoading && <KpiGrid loading count={6} />}
      {d && d.budgets.length === 0 && <Panel bodyless><EmptyState title={t('budgets.noActive')} action={<Link className="btn btn-secondary" to="/budgets">{t('budgets.title')}</Link>} /></Panel>}
      {d && d.budgets.length > 0 && (
        <>
          <div className="kpi-grid">
            {kpi(t('budgets.utilization'), pct(d.expense.utilization ?? '0'), `${t('budgets.ytd')}: ${f.money(d.expense.actual)} / ${f.money(d.expense.budget)}`, true)}
            {kpi(t('budgets.totalBudget'), f.money(d.expense.annual), t('budgets.annualBudget'))}
            {kpi(t('budgets.actualSpending'), f.money(d.expense.actual), t('budgets.ytd'))}
            {kpi(t('budgets.remainingBudget'), f.money(d.expense.remaining), undefined, false, isNeg(d.expense.remaining))}
            {kpi(t('budgets.overBudget'), f.count(d.over_count), t('budgets.underCount', { count: f.count(d.under_count) }), false, d.over_count > 0)}
            {kpi(t('budgets.incomeVsBudget'), pct(d.income.utilization ?? '0'), `${f.money(d.income.actual)} / ${f.money(d.income.budget)}`)}
          </div>
          <div className="dash-grid">
            <Panel title={t('budgets.monthlyTrend')} className={!isZero(d.income.annual) || !isZero(d.income.actual) ? '' : 'full'} actions={month !== null && <Button size="sm" variant="quiet" icon={<X size={14} />} onClick={() => setMonth(null)}>{periodName(d.periods[month - 1], true)}</Button>}>
              <BarChart format={(v) => f.money(v)} overLabel={t('budgets.legendOver')} onSelect={(g) => setMonth(Number(g.key))}
                series={[{ key: 'budget', label: t('budgets.legendBudget'), className: 'budget' }, { key: 'actual', label: t('budgets.legendActual'), className: 'actual' }]}
                groups={d.monthly.map((m) => ({ key: String(m.seq), label: periodName(d.periods.find((p) => p.seq === m.seq)), values: { budget: m.expense_budget, actual: m.expense_actual },
                  over: cmp(m.expense_actual, m.expense_budget) > 0 ? ['actual'] : [] }))} />
            </Panel>
            {(!isZero(d.income.annual) || !isZero(d.income.actual)) && <Panel title={t('budgets.incomeTrend')}>
              <BarChart format={(v) => f.money(v)}
                series={[{ key: 'budget', label: t('budgets.legendBudget'), className: 'budget' }, { key: 'actual', label: t('budgets.legendActual'), className: 'actual' }]}
                groups={d.monthly.map((m) => ({ key: String(m.seq), label: periodName(d.periods.find((p) => p.seq === m.seq)), values: { budget: m.income_budget, actual: m.income_actual } }))} />
            </Panel>}
            <Panel bodyless title={t('budgets.byCostCenter')}>
              <DataTable rows={d.by_cost_center.filter((r) => r.kind === 'expense')} rowKey={(r) => r.key}
                onRowClick={(r) => setCc({ id: r.cost_center_id ?? null, name: r.label || t('reports.unassigned') })}
                rowClassName={(r) => (cc && (r.cost_center_id ?? null) === cc.id ? 'selected-row' : undefined)}
                columns={[
                  { key: 'n', header: t('budgets.costCenter'), cell: (r) => <>{r.label || t('reports.unassigned')} {r.cost_center_kind && <span className="muted">· {t(`costCenters.kinds.${r.cost_center_kind as 'project'}`)}</span>}</> },
                  { key: 'b', header: t('budgets.budget'), align: 'end', cell: (r) => <Amount value={r.budget} /> },
                  { key: 'a', header: t('budgets.actual'), align: 'end', cell: (r) => <Amount value={r.actual} /> },
                  { key: 'u', header: t('budgets.utilization'), cell: (r) => <Meter percent={r.utilization} format={pct} />, width: 160 },
                ]} />
            </Panel>
            <Panel bodyless title={t('budgets.highestVariance')}>
              <DataTable rows={d.top_variances} rowKey={(r) => r.key} onRowClick={setDrill}
                columns={[
                  { key: 'n', header: t('common.name'), cell: (r) => `${f.digits(r.account_code ?? '')} ${r.account_name ?? ''}${r.cost_center_name ? ` — ${r.cost_center_name}` : ''}` },
                  { key: 'v', header: t('budgets.variance'), align: 'end', cell: (r) => <Amount value={r.variance} signed /> },
                  { key: 'u', header: t('budgets.utilization'), cell: (r) => <Meter percent={r.utilization} format={pct} />, width: 150 },
                ]} />
            </Panel>
            <Panel bodyless className="full" title={`${t('budgets.lines')}${cc ? ` — ${cc.name}` : ''}${month !== null ? ` — ${periodName(d.periods[month - 1], true)}` : ''}`}
              actions={<>
                {cc && <Button size="sm" variant="quiet" icon={<X size={14} />} onClick={() => setCc(null)}>{cc.name}</Button>}
                <ExportMenu name="budget-dashboard" permission="budgets.export" rows={lines} columns={exp} />
              </>}>
              <div className="toolbar"><span className="muted">{t('budgets.drillHelp')}</span></div>
              <DataTable rows={lines} loading={month !== null && monthQ.isLoading} rowKey={(r) => r.key} onRowClick={setDrill} columns={cols} rowClassName={(r) => (r.over ? 'danger-row' : undefined)} />
            </Panel>
            <Panel bodyless className="full" title={t('budgets.byBudget')}>
              <DataTable rows={d.by_budget} rowKey={(r) => r.key} onRowClick={(r) => r.budget_id && navigate(`/budgets/${r.budget_id}`)}
                columns={[
                  { key: 'n', header: t('budgets.name'), cell: (r) => `${r.label} — ${t(`budgets.kinds.${r.kind}`)}` },
                  { key: 'an', header: t('budgets.annualBudget'), align: 'end', cell: (r) => <Amount value={r.annual_budget} /> },
                  { key: 'a', header: t('budgets.actualYtd'), align: 'end', cell: (r) => <Amount value={r.actual} /> },
                  { key: 'f', header: t('budgets.forecast'), align: 'end', cell: (r) => <Amount value={r.forecast} /> },
                  { key: 'u', header: t('budgets.utilization'), cell: (r) => <Meter percent={r.utilization} format={pct} />, width: 170 },
                ]} />
            </Panel>
          </div>
        </>
      )}
      {drill && <DrillDialog row={drill} fromSeq={month ?? 1} toSeq={month ?? undefined} onClose={() => setDrill(null)} />}
    </div>
  );
}
