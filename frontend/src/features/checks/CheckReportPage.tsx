import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { ArrowLeftRight, Building2, ChevronRight, Clock, Landmark, Plus, Users } from 'lucide-react';
import { api } from '@/api/client';
import type { CheckPage, CheckSummary } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field, Select } from '@/components/ui/Field';
import { Money, PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { LinkButton } from '@/components/ui/Button';
import { DataTable, Pagination, type Sort } from '@/components/ui/DataTable';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { ServerExport } from '@/components/ServerExport';
import { BarChart } from '@/components/charts/BarChart';
import { addDaysIso, todayIso } from '@/lib/dates';
import { AdvancedCheckFilters, BasicCheckFilters, useCheckColumns, useUrlFilters } from './common';

const tabs = ['all', 'received', 'issued', 'due', 'overdue', 'returned', 'in-custody'] as const;
type Tab = (typeof tabs)[number];
const analytical = ['by-bank', 'by-party', 'cash-flow'] as const;
type Analytical = (typeof analytical)[number];
const PAGE = 50;

/** The tabs that are a check type or a fixed status narrow the status choices. */
const tabType = (tab: Tab) => (tab === 'received' || tab === 'in-custody' ? 'received' : tab === 'issued' ? 'issued' : undefined);

export function CheckReportPage() {
  const { report } = useParams();
  if (!report) return <CheckReport />;
  // Older links to the list reports open the matching tab.
  if ((tabs as readonly string[]).includes(report)) return <Navigate to={`/checks/reports?tab=${report}${window.location.search ? `&${window.location.search.slice(1)}` : ''}`} replace />;
  if (!(analytical as readonly string[]).includes(report)) return <Navigate to="/checks/reports" replace />;
  return <AnalyticalReport report={report as Analytical} />;
}

function useGeneratedAt(updatedAt: number) {
  const f = useFormat();
  const { t } = useI18n();
  if (!updatedAt) return null;
  return <><Clock size={13} aria-hidden /> {t('ui.updatedAt', { time: f.dateTime(new Date(updatedAt).toISOString()) })}</>;
}

/** /checks/reports — professional check report: KPIs, grouped filters, tabbed views, full-dataset export. */
function CheckReport() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const u = useUrlFilters();
  const tab = (u.sp.get('tab') as Tab) || 'all';
  const page = Number(u.sp.get('page')) || 1;
  const sort: Sort = { key: u.sp.get('sort') || 'due_date', desc: u.sp.get('order') === 'desc' };
  const setParam = (patch: Record<string, string | null>) => {
    const n = new URLSearchParams(u.sp);
    for (const [k, v] of Object.entries(patch)) (v ? n.set(k, v) : n.delete(k));
    u.setSp(n, { replace: true });
  };
  const report = tab === 'all' ? '' : tab;
  const query = { ...u.query, report };
  const rows = useQuery({
    queryKey: ['checks', 'report', query, sort, page],
    queryFn: () => api.get<CheckPage>(report ? `/checks/reports/${report}` : '/checks', { ...u.query, page, page_size: PAGE, sort: sort.key, order: sort.desc ? 'desc' : 'asc' }),
    placeholderData: keepPreviousData,
  });
  // KPIs reflect the active tab; tab counts reflect the filters across all tabs.
  const kpis = useQuery({ queryKey: ['check-summary', 'report', query], queryFn: () => api.get<CheckSummary>('/checks/summary', query), placeholderData: keepPreviousData });
  const counts = useQuery({ queryKey: ['check-summary', 'report', u.query], queryFn: () => api.get<CheckSummary>('/checks/summary', u.query), placeholderData: keepPreviousData });
  const s = kpis.data;
  const c = counts.data;
  const columns = useCheckColumns({ showType: !tabType(tab) });
  const updated = useGeneratedAt(rows.dataUpdatedAt);
  const sortQuery = { sort: sort.key, order: sort.desc ? 'desc' : 'asc' };
  return (
    <div className="page">
      <PageHeader
        title={t('checks.reportsTitle')}
        subtitle={t('checks.reportSubtitle')}
        meta={updated}
        actions={<>
          <ServerExport path="/checks/export" query={{ ...query, ...sortQuery }} name={`checks-${report || 'all'}`} permission="checks.export" />
          {can('checks.create') && <LinkButton to="/checks/new?type=received" icon={<Plus size={16} />}>{t('checks.newCheck')}</LinkButton>}
        </>}
      />

      {kpis.error && !s ? (
        <Panel bodyless><ErrorState error={kpis.error} title={t('ui.reportFailed')} onRetry={() => void kpis.refetch()} /></Panel>
      ) : (
        <KpiGrid loading={!s} count={4}>
          {s && (
            <>
              <Kpi label={t('checks.kpi.total')} value={f.count(s.count)} sub={t('checks.kpi.live') + ': ' + f.count(s.live.count)} tone="primary" />
              <Kpi label={t('checks.kpi.amount')} value={f.money(s.amount)} sub={f.currencyLabel} />
              <Kpi label={t('checks.kpi.dueSoon')} value={f.money(s.due_soon.amount)} sub={t('checks.dashboard.count', { count: f.count(s.due_soon.count) })} tone="warning"
                to="/checks/reports?tab=due" />
              <Kpi label={t('checks.kpi.overdue')} value={f.money(s.overdue.amount)} sub={t('checks.dashboard.count', { count: f.count(s.overdue.count) })}
                tone={s.overdue.count > 0 ? 'danger' : 'neutral'} to="/checks/reports?tab=overdue" />
            </>
          )}
        </KpiGrid>
      )}

      <Panel bodyless>
        <FilterBar basic={<BasicCheckFilters u={u} type={tabType(tab)} />} advanced={<AdvancedCheckFilters u={u} />} activeCount={u.active}
          advancedActive={u.advancedActive} onReset={u.reset} />
        <Tabs<Tab> label={t('checks.reportTabs')} value={tab} onChange={(k) => setParam({ tab: k === 'all' ? null : k, page: null, status: null })}
          tabs={[
            { key: 'all', label: t('checks.tabs.all'), count: c && f.count(c.count) },
            { key: 'received', label: t('checks.tabs.received'), count: c && f.count(c.received.count) },
            { key: 'issued', label: t('checks.tabs.issued'), count: c && f.count(c.issued.count) },
            { key: 'due', label: t('checks.tabs.due') },
            { key: 'overdue', label: t('checks.tabs.overdue'), count: c && c.overdue.count > 0 ? f.count(c.overdue.count) : undefined, tone: 'danger' },
            { key: 'returned', label: t('checks.tabs.returned'), count: c && c.returned.count > 0 ? f.count(c.returned.count) : undefined, tone: 'warning' },
            { key: 'in-custody', label: t('checks.tabs.in-custody') },
          ]} />
        <DataTable
          caption={t('checks.reportsTitle')}
          columnsKey="checks.report"
          rows={rows.data?.items}
          loading={rows.isFetching}
          error={rows.error}
          onRetry={() => void rows.refetch()}
          rowKey={(x) => x.id}
          sort={sort}
          onSort={(x) => setParam({ sort: x.key, order: x.desc ? 'desc' : null, page: null })}
          onRowClick={(x) => navigate(`/checks/${x.id}`)}
          rowClassName={(x) => (x.overdue ? 'danger-row' : undefined)}
          columns={columns}
          maxHeight="68vh"
          empty={u.active > 0 || tab !== 'all' ? (
            <EmptyState filtered title={t('checks.emptyFiltered')} body={t('checks.emptyFilteredBody')}
              action={u.active > 0 ? <button type="button" className="btn btn-secondary" onClick={u.reset}>{t('ui.clearFilters')}</button> : undefined} />
          ) : (
            <EmptyState title={t('checks.emptyTitle')} body={t('checks.emptyBody')}
              action={can('checks.create') ? <LinkButton to="/checks/new?type=received" variant="accent" icon={<Plus size={16} />}>{t('checks.newCheck')}</LinkButton> : undefined} />
          )}
        />
        {rows.data && rows.data.total > 0 && s && (
          <div className="totals-bar" aria-label={t('common.totals')}>
            {!tabType(tab) && (
              <>
                <span>{t('checks.received')}</span><strong className="num">{f.money(s.received.amount)}</strong>
                <span className="sep" aria-hidden />
                <span>{t('checks.issued')}</span><strong className="num">{f.money(s.issued.amount)}</strong>
                <span className="sep" aria-hidden />
              </>
            )}
            <span>{t('common.total')}</span>
            <strong className="num">{f.moneyWithCurrency(rows.data.total_amount)}</strong>
          </div>
        )}
        <Pagination page={page} pageSize={PAGE} total={rows.data?.total ?? 0} onPage={(p) => setParam({ page: p > 1 ? String(p) : null })} />
      </Panel>

      <Panel title={t('checks.groupedReports')} bodyless className="no-print">
        {analytical.map((r) => (
          <Link key={r} to={`/checks/reports/${r}${u.sp.toString() ? `?${u.sp.toString()}` : ''}`} className="link-row">
            <span className="row" style={{ gap: 12 }}>
              <span className="kpi-icon">{r === 'by-bank' ? <Landmark size={18} /> : r === 'by-party' ? <Users size={18} /> : <ArrowLeftRight size={18} />}</span>
              <span>
                <span className="lr-title" style={{ display: 'block' }}>{t(`checks.reports.names.${r}`)}</span>
                <span className="lr-desc">{t(`checks.reports.descriptions.${r}`)}</span>
              </span>
            </span>
            <ChevronRight size={16} className="icon-directional muted" aria-hidden />
          </Link>
        ))}
      </Panel>
    </div>
  );
}

interface GroupRow { id: number | null; label: string; count: number; amount: string; live_count: number; live_amount: string; overdue_amount: string }
interface Flow { group_by: string; overdue_net: string; rows: { key: string; from: string; inflow: string; outflow: string; net: string; cumulative: string; in_count: number; out_count: number }[]; inflow: string; outflow: string; net: string }

/** by-bank, by-party and cash-flow reports, in the same layout. */
function AnalyticalReport({ report }: { report: Analytical }) {
  const { t } = useI18n();
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('checks.reportsTitle'), to: '/checks/reports' }]} title={t(`checks.reports.names.${report}`)} subtitle={t(`checks.reports.descriptions.${report}`)} />
      {report === 'cash-flow' ? <CashFlow /> : <Grouped report={report} />}
    </div>
  );
}

function Grouped({ report }: { report: 'by-bank' | 'by-party' }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const u = useUrlFilters();
  const [sp] = useSearchParams();
  const params = Object.fromEntries(sp.entries());
  const q = useQuery({ queryKey: ['checks', 'grouped', report, params], queryFn: () => api.get<{ rows: GroupRow[]; total: GroupRow }>(`/checks/reports/${report}`, params), placeholderData: keepPreviousData });
  const d = q.data;
  const isParty = report === 'by-party';
  const label = (r: GroupRow) => r.label || tryT('common.none') || '—';
  const name = t(`checks.reports.names.${report}`);
  return (
    <>
      <KpiGrid loading={!d} count={3}>
        {d && (
          <>
            <Kpi label={t('checks.kpi.total')} value={f.count(d.total.count)} sub={f.moneyWithCurrency(d.total.amount)} icon={isParty ? <Users size={16} /> : <Building2 size={16} />} tone="primary" />
            <Kpi label={t('checks.liveAmount')} value={f.money(d.total.live_amount)} sub={t('checks.dashboard.count', { count: f.count(d.total.live_count) })} />
            <Kpi label={t('checks.overdueAmount')} value={f.money(d.total.overdue_amount)} tone={Number(d.total.overdue_amount) > 0 ? 'danger' : 'neutral'} />
          </>
        )}
      </KpiGrid>
      <Panel bodyless actions={<ExportMenu name={`checks-${report}`} permission="checks.export" title={name} rows={d?.rows} columns={[
        { header: isParty ? t('checks.party') : t('checks.bank'), value: label },
        { header: t('checks.count'), value: (r) => r.count, numeric: true },
        { header: t('common.total'), value: (r) => r.amount, numeric: true },
        { header: t('checks.liveCount'), value: (r) => r.live_count, numeric: true },
        { header: t('checks.liveAmount'), value: (r) => r.live_amount, numeric: true },
        { header: t('checks.overdueAmount'), value: (r) => r.overdue_amount, numeric: true },
      ]} />} title={name}>
        <FilterBar basic={<BasicCheckFilters u={u} />} advanced={<AdvancedCheckFilters u={u} />} activeCount={u.active} advancedActive={u.advancedActive} onReset={u.reset} />
        <DataTable rows={d?.rows} loading={q.isFetching} error={q.error} onRetry={() => void q.refetch()} rowKey={(r, i) => `${r.id}-${i}`} showFooter caption={name}
          empty={<EmptyState filtered={u.active > 0} title={t('checks.emptyFiltered')} body={t('checks.emptyFilteredBody')} />}
          columns={[
            { key: 'label', header: isParty ? t('checks.party') : t('checks.bank'), cell: (r) => (isParty && r.id ? <Link to={`/checks/reports?party_id=${r.id}`}>{label(r)}</Link> : label(r)), footer: t('common.total') },
            { key: 'count', header: t('checks.count'), align: 'end', cell: (r) => <span className="num">{f.count(r.count)}</span>, footer: <span className="num">{f.count(d?.total.count ?? 0)}</span> },
            { key: 'amount', header: t('common.total'), align: 'end', cell: (r) => <Money value={r.amount} />, footer: <Money value={d?.total.amount} /> },
            { key: 'live_count', header: t('checks.liveCount'), align: 'end', cell: (r) => <span className="num">{f.count(r.live_count)}</span>, footer: <span className="num">{f.count(d?.total.live_count ?? 0)}</span> },
            { key: 'live', header: t('checks.liveAmount'), align: 'end', cell: (r) => <Money value={r.live_amount} />, footer: <Money value={d?.total.live_amount} /> },
            { key: 'over', header: t('checks.overdueAmount'), align: 'end', cell: (r) => <Money value={r.overdue_amount} blankZero />, footer: <Money value={d?.total.overdue_amount} /> },
          ]} />
      </Panel>
    </>
  );
}

function CashFlow() {
  const { t } = useI18n();
  const f = useFormat();
  const [sp, setSp] = useSearchParams();
  const set = (patch: Record<string, string>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) n.set(k, v);
    setSp(n, { replace: true });
  };
  const from = sp.get('from') || todayIso();
  const to = sp.get('to') || addDaysIso(todayIso(), 180);
  const group = sp.get('group_by') || 'month';
  const q = useQuery({ queryKey: ['checks', 'flow', from, to, group], queryFn: () => api.get<Flow>('/checks/reports/cash-flow', { from, to, group_by: group }), placeholderData: keepPreviousData });
  const d = q.data;
  const label = (r: Flow['rows'][number]) => (group === 'month' ? f.digits(r.key.replace('-', '/')) : group === 'week' ? t('checks.week', { date: f.date(r.from) }) : f.date(r.from));
  return (
    <>
      <KpiGrid loading={!d} count={4}>
        {d && (
          <>
            <Kpi label={t('checks.overdueOpening')} value={f.money(d.overdue_net)} tone={Number(d.overdue_net) !== 0 ? 'warning' : 'neutral'} />
            <Kpi label={t('checks.inflow')} value={f.money(d.inflow)} tone="success" />
            <Kpi label={t('checks.outflow')} value={f.money(d.outflow)} tone="info" />
            <Kpi label={t('checks.net')} value={f.money(d.net)} sub={f.currencyLabel} tone={Number(d.net) < 0 ? 'danger' : 'primary'} />
          </>
        )}
      </KpiGrid>
      <Panel bodyless title={t('checks.reports.names.cash-flow')} actions={<ExportMenu name="checks-cash-flow" permission="checks.export" title={t('checks.reports.names.cash-flow')} rows={d?.rows} columns={[
        { header: t('common.date'), value: label },
        { header: t('checks.inflow'), value: (r) => r.inflow, numeric: true },
        { header: t('checks.outflow'), value: (r) => r.outflow, numeric: true },
        { header: t('checks.net'), value: (r) => r.net, numeric: true },
        { header: t('checks.cumulative'), value: (r) => r.cumulative, numeric: true },
      ]} />}>
        <FilterBar basic={<>
          <DateRange from={from} to={to} onChange={(a, b) => set({ from: a, to: b })} presets={false} />
          <Field label={t('reports.groupBy')}>
            <Select value={group} onChange={(e) => set({ group_by: e.target.value })}>
              <option value="day">{t('checks.groupDay')}</option>
              <option value="week">{t('checks.groupWeek')}</option>
              <option value="month">{t('checks.groupMonth')}</option>
            </Select>
          </Field>
        </>} />
        {d && d.rows.length > 0 && (
          <div className="panel-body">
            <BarChart format={(v) => f.money(v)} series={[{ key: 'inflow', label: t('checks.inflow'), className: 'actual' }, { key: 'outflow', label: t('checks.outflow'), className: 'budget' }]}
              groups={d.rows.map((r) => ({ key: r.key, label: label(r), values: { inflow: r.inflow, outflow: r.outflow } }))} />
          </div>
        )}
        <DataTable rows={d?.rows} loading={q.isFetching} error={q.error} onRetry={() => void q.refetch()} rowKey={(r) => r.key} showFooter caption={t('checks.reports.names.cash-flow')}
          empty={<EmptyState title={t('checks.none')} body={t('checks.emptyFilteredBody')} />}
          columns={[
            { key: 'k', header: t('common.date'), cell: label, footer: t('common.total') },
            { key: 'in', header: t('checks.inflow'), align: 'end', cell: (r) => <Money value={r.inflow} blankZero />, footer: <Money value={d?.inflow} /> },
            { key: 'out', header: t('checks.outflow'), align: 'end', cell: (r) => <Money value={r.outflow} blankZero />, footer: <Money value={d?.outflow} /> },
            { key: 'net', header: t('checks.net'), align: 'end', cell: (r) => <Money value={r.net} signed />, footer: <Money value={d?.net} signed /> },
            { key: 'cum', header: t('checks.cumulative'), align: 'end', cell: (r) => <Money value={r.cumulative} signed /> },
          ]} />
      </Panel>
    </>
  );
}
