import { useQuery, keepPreviousData } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { CalendarDays, FileBarChart, LayoutDashboard, Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { CheckPage, CheckSummary } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { DataTable, Pagination, type Sort } from '@/components/ui/DataTable';
import { EmptyState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { ServerExport } from '@/components/ServerExport';
import { ActionMenu } from '@/components/ui/Misc';
import { AdvancedCheckFilters, BasicCheckFilters, useCheckColumns, useUrlFilters } from './common';

type Tab = 'all' | 'received' | 'issued';
const PAGE = 50;

/** The check register: every check, filtered, sorted and paginated on the server. */
export function ChecksListPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const u = useUrlFilters();
  const tab = (u.sp.get('type') as Tab) || 'all';
  const page = Number(u.sp.get('page')) || 1;
  const sort: Sort = { key: u.sp.get('sort') || 'due_date', desc: u.sp.get('order') === 'desc' };
  const setParam = (patch: Record<string, string | null>) => {
    const n = new URLSearchParams(u.sp);
    for (const [k, v] of Object.entries(patch)) (v ? n.set(k, v) : n.delete(k));
    u.setSp(n, { replace: true });
  };
  const type = tab === 'all' ? '' : tab;
  const base = { ...u.query, check_type: type };
  const list = useQuery({
    queryKey: ['checks', 'register', base, sort, page],
    queryFn: () => api.get<CheckPage>('/checks', { ...base, page, page_size: PAGE, sort: sort.key, order: sort.desc ? 'desc' : 'asc' }),
    placeholderData: keepPreviousData,
  });
  // Counts for the tabs and KPIs come from one aggregate query over the same filters.
  const summary = useQuery({ queryKey: ['check-summary', 'register', u.query], queryFn: () => api.get<CheckSummary>('/checks/summary', u.query), placeholderData: keepPreviousData });
  const s = summary.data;
  const columns = useCheckColumns({ showType: tab === 'all' });
  const canReceive = can('checks.create') && can('checks.post');
  const canIssue = can('checks.create');
  const filtered = u.active > 0;
  const bucket = tab === 'received' ? s?.received : tab === 'issued' ? s?.issued : s ? { count: s.count, amount: s.amount } : undefined;
  return (
    <div className="page">
      <PageHeader
        title={t('checks.listTitle')}
        subtitle={t('checks.registerSubtitle')}
        actions={<>
          <ServerExport path="/checks/export" query={{ ...base, sort: sort.key, order: sort.desc ? 'desc' : 'asc' }} name="checks" permission="checks.export" />
          <ActionMenu items={[
            { label: t('checks.dashboardTitle'), icon: <LayoutDashboard size={16} />, to: '/checks/dashboard' },
            { label: t('checks.calendarTitle'), icon: <CalendarDays size={16} />, to: '/checks/calendar' },
            { label: t('checks.reportsTitle'), icon: <FileBarChart size={16} />, to: '/checks/reports' },
          ]} />
          {canReceive && <LinkButton to="/checks/new?type=received" icon={<Plus size={16} />}>{t('checks.newReceived')}</LinkButton>}
          {canIssue && <LinkButton to="/checks/new?type=issued" variant="accent" icon={<Plus size={16} />}>{t('checks.newIssued')}</LinkButton>}
        </>}
      />
      <KpiGrid loading={!s && summary.isLoading} count={4}>
        {s && (
          <>
            <Kpi label={t('checks.kpi.total')} value={f.count(bucket?.count ?? 0)} sub={f.moneyWithCurrency(bucket?.amount ?? 0)} tone="primary" />
            <Kpi label={t('checks.kpi.live')} value={f.money(s.live.amount)} sub={t('checks.dashboard.count', { count: f.count(s.live.count) })} />
            <Kpi label={t('checks.kpi.dueSoon')} value={f.money(s.due_soon.amount)} sub={t('checks.dashboard.count', { count: f.count(s.due_soon.count) })} tone="warning" />
            <Kpi label={t('checks.kpi.overdue')} value={f.money(s.overdue.amount)} sub={t('checks.dashboard.count', { count: f.count(s.overdue.count) })} tone={s.overdue.count > 0 ? 'danger' : 'neutral'} />
          </>
        )}
      </KpiGrid>
      <Panel bodyless>
        <Tabs<Tab> label={t('checks.reportTabs')} value={tab} onChange={(k) => setParam({ type: k === 'all' ? null : k, status: null, page: null })}
          tabs={[
            { key: 'all', label: t('checks.tabs.all'), count: s && f.count(s.count) },
            { key: 'received', label: t('checks.tabs.received'), count: s && f.count(s.received.count) },
            { key: 'issued', label: t('checks.tabs.issued'), count: s && f.count(s.issued.count) },
          ]} />
        <FilterBar basic={<BasicCheckFilters u={u} type={type} />} advanced={<AdvancedCheckFilters u={u} />} activeCount={u.active} advancedActive={u.advancedActive} onReset={u.reset} />
        <DataTable
          caption={t('checks.listTitle')}
          columnsKey="checks.register"
          rows={list.data?.items}
          loading={list.isFetching}
          error={list.error}
          onRetry={() => void list.refetch()}
          rowKey={(c) => c.id}
          sort={sort}
          onSort={(x) => setParam({ sort: x.key, order: x.desc ? 'desc' : null, page: null })}
          onRowClick={(c) => navigate(`/checks/${c.id}`)}
          rowClassName={(c) => (c.overdue ? 'danger-row' : undefined)}
          columns={columns}
          empty={filtered ? (
            <EmptyState filtered title={t('checks.emptyFiltered')} body={t('checks.emptyFilteredBody')} action={<button type="button" className="btn btn-secondary" onClick={u.reset}>{t('ui.clearFilters')}</button>} />
          ) : (
            <EmptyState title={t('checks.emptyTitle')} body={t('checks.emptyBody')}
              action={<>{canReceive && <LinkButton to="/checks/new?type=received" variant="accent" icon={<Plus size={16} />}>{t('checks.newReceived')}</LinkButton>}
                {canIssue && <LinkButton to="/checks/new?type=issued" icon={<Plus size={16} />}>{t('checks.newIssued')}</LinkButton>}</>} />
          )}
        />
        {list.data && list.data.total > 0 && (
          <div className="totals-bar">
            <span>{t('common.total')}</span>
            <strong className="num">{f.moneyWithCurrency(list.data.total_amount)}</strong>
          </div>
        )}
        <Pagination page={page} pageSize={PAGE} total={list.data?.total ?? 0} onPage={(p) => setParam({ page: p > 1 ? String(p) : null })} />
      </Panel>
    </div>
  );
}
