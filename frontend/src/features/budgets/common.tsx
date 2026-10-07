import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Info, PencilLine } from 'lucide-react';
import { Link } from 'react-router-dom';
import { api } from '@/api/client';
import type { BudgetRow, DrillRow, ManualForecast } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { Alert, Amount, Badge, LoadingBlock } from '@/components/ui/Misc';
import { Field, MoneyInput, Select, TextArea } from '@/components/ui/Field';
import { EmptyState } from '@/components/ui/Feedback';
import { useSubmit } from '@/lib/useSubmit';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable, type Column } from '@/components/ui/DataTable';
import type { ExportColumn } from '@/components/export';
import { Meter } from '@/components/charts/BarChart';
import { sourceRoute } from '@/features/journal/sourceLinks';
import { add } from '@/lib/decimal';
import { calendarParts, monthNames } from '@/lib/dates';

export interface FiscalYearLite { id: number; name: string; start_date: string; end_date: string; status: string }
export const useFiscalYears = () => useQuery({ queryKey: ['fiscal-years'], queryFn: () => api.get<FiscalYearLite[]>('/company/fiscal-years'), staleTime: 60_000 });

/** Label of an analysis row: account (with cost center), cost center, budget or month. */
export function useRowLabel() {
  const { t, lang } = useI18n();
  const f = useFormat();
  return (r: BudgetRow) => {
    if (r.account_code) {
      const acc = `${f.digits(r.account_code)} ${lang === 'en' && r.account_name_en ? r.account_name_en : r.account_name ?? ''}`;
      return r.line_id && r.cost_center_name ? `${acc} — ${r.cost_center_name}` : acc;
    }
    return f.digits(r.label || t('reports.unassigned'));
  };
}

/** Forecast basis in words, e.g. "Run rate of 4 months". */
export function useForecastBasis() {
  const { tryT } = useI18n();
  const f = useFormat();
  return (r: Pick<BudgetRow, 'forecast_basis' | 'forecast_periods'>) =>
    r.forecast_basis ? tryT(`budgets.forecastBasis.${r.forecast_basis}`, { n: f.count(r.forecast_periods ?? 0) }) ?? r.forecast_basis : '';
}

/** Budget vs actual table columns shared by the view, reports and dashboard. */
export function useAnalysisColumns(opts: { forecast?: boolean; kind?: boolean; onForecast?: (r: BudgetRow) => void } = {}): Column<BudgetRow>[] {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const label = useRowLabel();
  const basis = useForecastBasis();
  return [
    { key: 'label', header: t('common.name'), cell: (r) => <>{label(r)} {r.over && <Badge tone="danger">{t('budgets.overBudget')}</Badge>}</> },
    ...(opts.kind ? [{ key: 'kind', header: t('common.type'), optional: true, cell: (r: BudgetRow) => t(`budgets.kinds.${r.kind}`), width: 80 }] : []),
    { key: 'budget', header: t('budgets.budget'), align: 'end', cell: (r) => <Amount value={r.budget} /> },
    { key: 'actual', header: t('budgets.actual'), align: 'end', cell: (r) => <Amount value={r.actual} /> },
    { key: 'variance', header: t('budgets.variance'), align: 'end', cell: (r) => <Amount value={r.variance} signed /> },
    { key: 'util', header: t('budgets.utilization'), cell: (r) => <Meter percent={r.utilization} format={(v) => f.percent(v)} />, width: 170 },
    ...(opts.forecast ? [
      { key: 'annual', header: t('budgets.annualBudget'), align: 'end' as const, optional: true, cell: (r: BudgetRow) => <Amount value={r.annual_budget} /> },
      { key: 'ytd', header: t('budgets.actualYtd'), align: 'end' as const, optional: true, defaultHidden: true, cell: (r: BudgetRow) => <Amount value={r.actual_ytd} /> },
      {
        key: 'forecast', header: t('budgets.forecast'), align: 'end' as const, cell: (r: BudgetRow) => (
          <span className="stack-sm" style={{ gap: 0, alignItems: 'flex-end' }}>
            <Amount value={r.forecast} />
            {r.forecast_basis && <span className="muted" style={{ fontSize: 'var(--font-size-xs)' }}>{basis(r)}</span>}
          </span>
        ),
      },
      {
        key: 'coverage', header: t('ui.dataBasis'), optional: true, cell: (r: BudgetRow) => r.forecast_coverage ? (
          <span title={t('budgets.coverageHelp')}><Badge tone={r.forecast_coverage === 'high' ? 'success' : r.forecast_coverage === 'low' ? 'warning' : r.forecast_coverage === 'manual' ? 'info' : undefined}>
            {t(`budgets.coverage.${r.forecast_coverage as 'high'}`)}
          </Badge></span>
        ) : null,
      },
      { key: 'fvar', header: t('budgets.forecastVariance'), align: 'end' as const, cell: (r: BudgetRow) => <Amount value={r.forecast_variance} signed /> },
      ...(opts.onForecast && can('budgets.forecast') ? [{
        key: 'set', header: '', align: 'end' as const, cell: (r: BudgetRow) => r.line_id ? (
          <button type="button" className="btn btn-quiet btn-icon btn-sm" title={t('budgets.setForecast')} aria-label={`${t('budgets.setForecast')} — ${label(r)}`}
            onClick={(e) => { e.stopPropagation(); opts.onForecast!(r); }}>
            <PencilLine size={15} />
          </button>
        ) : null,
      }] : []),
    ] : []),
  ];
}

/** Forecast method choice with an explanation of what the method computes. */
export function ForecastMethodField({ value, onChange, methods, help }: { value: string; onChange: (m: string) => void; methods?: string[]; help?: boolean }) {
  const { t } = useI18n();
  const list = methods?.length ? methods : ['straight_line', 'historical_average', 'previous_year', 'manual'];
  return (
    <Field label={t('budgets.forecastMethod')} help={help ? <span className="row" style={{ gap: 4 }}><Info size={12} aria-hidden /> {t(`budgets.forecastMethodHelp.${value as 'manual'}`)}</span> : undefined}>
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        {list.map((m) => <option key={m} value={m}>{t(`budgets.forecastMethods.${m as 'manual'}`)}</option>)}
      </Select>
    </Field>
  );
}

/** A manager's full-year forecast for one budget line, with its history. */
export function ManualForecastDialog({ row, onClose, onSaved }: { row: BudgetRow; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const label = useRowLabel();
  const history = useQuery({ queryKey: ['budget-forecasts', row.line_id], queryFn: () => api.get<ManualForecast[]>(`/budgets/lines/${row.line_id}/forecasts`) });
  const [v, setV] = useState({ amount: row.manual_forecast?.amount ?? row.forecast, reason: '' });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const ok = await run(() => api.post(`/budgets/lines/${row.line_id}/forecasts`, v).then(() => true), t('budgets.forecastSaved'));
    if (ok) onSaved();
  };
  return (
    <Dialog size="wide" title={t('budgets.setForecastTitle', { line: label(row) })} description={t('budgets.forecastMethodHelp.manual')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <dl className="dl-grid">
          <dt>{t('budgets.annualBudget')}</dt><dd><Amount value={row.annual_budget} /></dd>
          <dt>{t('budgets.actualYtd')}</dt><dd><Amount value={row.actual_ytd} /></dd>
          <dt>{t('budgets.forecast')}</dt><dd><Amount value={row.forecast} /></dd>
        </dl>
        <div className="form-grid">
          <Field label={`${t('budgets.forecastAmount')} (${f.currencyLabel})`} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => setV({ ...v, amount: x })} autoFocus /></Field>
          <Field label={t('budgets.forecastReason')} required error={errors.reason} className="span-all"><TextArea rows={2} value={v.reason} onChange={(e) => setV({ ...v, reason: e.target.value })} /></Field>
        </div>
        <div className="form-section-title">{t('budgets.forecastHistory')}</div>
        <DataTable rows={history.data} loading={history.isLoading} rowKey={(h) => h.id} dense
          empty={<EmptyState compact title={t('budgets.noForecastHistory')} />}
          columns={[
            { key: 'd', header: t('common.date'), cell: (h) => <span className="num">{f.dateTime(h.created_at)}</span> },
            { key: 'a', header: t('common.amount'), align: 'end', cell: (h) => <Amount value={h.amount} /> },
            { key: 'r', header: t('budgets.forecastReason'), cell: (h) => h.reason },
            { key: 'u', header: t('settings.user'), cell: (h) => h.user_name ?? '' },
          ]} />
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

export function useAnalysisExport(): ExportColumn<BudgetRow>[] {
  const { t } = useI18n();
  const label = useRowLabel();
  return [
    { header: t('common.name'), value: label },
    { header: t('common.type'), value: (r) => t(`budgets.kinds.${r.kind}`) },
    { header: t('budgets.budget'), value: (r) => r.budget, numeric: true },
    { header: t('budgets.actual'), value: (r) => r.actual, numeric: true },
    { header: t('budgets.variance'), value: (r) => r.variance, numeric: true },
    { header: t('budgets.utilization'), value: (r) => r.utilization, numeric: true },
    { header: t('budgets.annualBudget'), value: (r) => r.annual_budget, numeric: true },
    { header: t('budgets.forecast'), value: (r) => r.forecast, numeric: true },
    { header: t('budgets.forecastMethod'), value: (r) => (r.forecast_method ? t(`budgets.forecastMethods.${r.forecast_method as 'manual'}`) : '') },
    { header: t('ui.dataBasis'), value: (r) => (r.forecast_coverage ? t(`budgets.coverage.${r.forecast_coverage as 'high'}`) : '') },
    { header: t('budgets.forecastVariance'), value: (r) => r.forecast_variance, numeric: true },
  ];
}

/** The journal lines behind a budget line's actual, linking to entries and source documents. */
export function DrillDialog({ row, fromSeq, toSeq, onClose }: { row: BudgetRow; fromSeq?: number; toSeq?: number; onClose: () => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const label = useRowLabel();
  const q = useQuery({
    queryKey: ['budget-drill', row.line_id, fromSeq, toSeq],
    queryFn: () => api.get<DrillRow[]>('/budgets/drilldown', { line_id: row.line_id, from_seq: fromSeq, to_seq: toSeq }),
    enabled: !!row.line_id,
  });
  const total = q.data?.reduce((s, r) => add(s, r.amount), '0');
  return (
    <Dialog wide title={`${t('budgets.drilldown')} — ${label(row)}`} onClose={onClose} footer={<Button onClick={onClose}>{t('common.close')}</Button>}>
      {q.error && <Alert>{err(q.error)}</Alert>}
      {q.isLoading ? <LoadingBlock /> : (
        <DataTable rows={q.data} rowKey={(r, i) => `${r.entry_id}-${i}`} showFooter
          columns={[
            { key: 'date', header: t('common.date'), cell: (r) => <span className="num">{f.date(r.date)}</span>, width: 110 },
            { key: 'entry', header: t('budgets.journalEntry'), cell: (r) => <Link to={`/journal/${r.entry_id}`} className="num">{f.digits(String(r.entry_number))}</Link>, width: 80 },
            {
              key: 'src', header: t('journal.source'), cell: (r) => {
                const to = sourceRoute(r.source_type, r.source_id);
                const name = tryT(`journal.sources.${r.source_type}`) ?? r.source_type;
                return to ? <Link to={to}>{name}</Link> : name;
              },
            },
            { key: 'desc', header: t('common.description'), cell: (r) => r.line_description || r.description, footer: t('common.total') },
            { key: 'acc', header: t('journal.account'), cell: (r) => `${f.digits(r.account_code)} ${r.account_name}` },
            { key: 'cc', header: t('journal.costCenter'), cell: (r) => r.cost_center_name ?? '' },
            { key: 'amount', header: t('common.amount'), align: 'end', cell: (r) => <Amount value={r.amount} signed />, footer: <Amount value={total} signed /> },
          ]} />
      )}
    </Dialog>
  );
}

/** A fiscal period's month name in the UI language (stored period names are Persian). */
export function usePeriodName() {
  const { lang } = useI18n();
  const f = useFormat();
  return (p?: { start_date: string; name: string }, withYear = false) => {
    if (!p) return '';
    const parts = calendarParts(p.start_date.slice(0, 10), f.calendar);
    const m = monthNames(f.calendar, lang)[parts.m - 1];
    return withYear ? f.digits(`${m} ${parts.y}`) : m;
  };
}
