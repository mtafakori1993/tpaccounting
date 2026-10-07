import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { AlertTriangle, CalendarDays, Clock, FileBarChart, Plus, Undo2, Vault } from 'lucide-react';
import { api } from '@/api/client';
import type { Check, CheckDashboard } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { Money, PageHeader, Panel } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { BarChart } from '@/components/charts/BarChart';
import { addDaysIso, todayIso } from '@/lib/dates';
import { DueCell } from './common';

interface Flow { rows: { key: string; from: string; inflow: string; outflow: string; net: string; cumulative: string }[]; overdue_net: string }

/** /checks/dashboard — what is due, overdue and outstanding today. */
export function ChecksDashboardPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const q = useQuery({ queryKey: ['check-dashboard'], queryFn: () => api.get<CheckDashboard>('/checks/dashboard') });
  const flow = useQuery({
    queryKey: ['check-dashboard', 'flow'],
    queryFn: () => api.get<Flow>('/checks/reports/cash-flow', { group_by: 'week', from: todayIso(), to: addDaysIso(todayIso(), 83) }),
  });
  const d = q.data;
  const count = (n: number) => t('checks.dashboard.count', { count: f.count(n) });
  const list = (rows: Check[] | undefined) => (
    <DataTable rows={rows} rowKey={(c) => c.id} onRowClick={(c) => navigate(`/checks/${c.id}`)} dense
      empty={<EmptyState compact title={t('checks.none')} />}
      columns={[
        { key: 'due', header: t('checks.dueDate'), cell: (c) => <DueCell c={c} />, width: 130 },
        { key: 'party', header: t('checks.party'), cell: (c) => c.party_name },
        { key: 'no', header: t('checks.checkNumber'), cell: (c) => <span className="num" dir="ltr">{f.digits(c.check_number)}</span> },
        { key: 'amount', header: t('checks.amount'), align: 'end', cell: (c) => <Money value={c.amount} /> },
      ]} />
  );
  return (
    <div className="page">
      <PageHeader title={t('checks.dashboardTitle')} subtitle={t('checks.dashboardSubtitle')}
        meta={d && <><Clock size={13} aria-hidden /> {t('common.today')}: {f.date(d.today)}</>}
        actions={<>
          <LinkButton to="/checks/calendar" icon={<CalendarDays size={16} />}>{t('checks.calendarTitle')}</LinkButton>
          <LinkButton to="/checks/reports" icon={<FileBarChart size={16} />}>{t('checks.reportsTitle')}</LinkButton>
          {can('checks.create') && <LinkButton to={`/checks/new?type=${can('checks.post') ? 'received' : 'issued'}`} variant="accent" icon={<Plus size={16} />}>{t('checks.newCheck')}</LinkButton>}
        </>} />
      {q.error && !d ? <Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel> : (
        <>
          <KpiGrid loading={!d} count={4}>
            {d && (
              <>
                <Kpi label={t('checks.dashboard.totalReceived')} value={f.money(d.received_live.amount)} sub={count(d.received_live.count)} tone="success" to="/checks/reports?tab=received&live=true" />
                <Kpi label={t('checks.dashboard.totalIssued')} value={f.money(d.issued_live.amount)} sub={count(d.issued_live.count)} tone="info" to="/checks/reports?tab=issued&live=true" />
                <Kpi label={t('checks.dashboard.inCustody')} value={f.money(d.in_custody.amount)} sub={count(d.in_custody.count)} icon={<Vault size={15} />} to="/checks/reports?tab=in-custody" />
                <Kpi label={t('checks.dashboard.awaitingCollection')} value={f.money(d.awaiting_collection.amount)} sub={count(d.awaiting_collection.count)} to="/checks/reports?tab=received&status=deposited" />
              </>
            )}
          </KpiGrid>
          {d && (
            <div className="dash-cards">
              <Panel title={<><Clock size={16} /> {t('checks.dashboard.dueToday')}</>}>
                <table className="mini-table"><tbody>
                  <tr><td>{t('checks.dashboard.receivable')}</td><td className="align-end"><Link to={`/checks/reports?tab=due&check_type=received&due_from=${d.today}&due_to=${d.today}`}><Money value={d.due_today_received.amount} /></Link> <span className="muted">({f.count(d.due_today_received.count)})</span></td></tr>
                  <tr><td>{t('checks.dashboard.payable')}</td><td className="align-end"><Link to={`/checks/reports?tab=due&due_from=${d.today}&due_to=${d.today}`}><Money value={d.due_today_issued.amount} /></Link> <span className="muted">({f.count(d.due_today_issued.count)})</span></td></tr>
                </tbody></table>
              </Panel>
              <Panel title={<><CalendarDays size={16} /> {t('checks.dashboard.dueWeek')}</>}>
                <table className="mini-table"><tbody>
                  <tr><td>{t('checks.dashboard.receivable')}</td><td className="align-end"><Money value={d.due_week_received.amount} /> <span className="muted">({f.count(d.due_week_received.count)})</span></td></tr>
                  <tr><td>{t('checks.dashboard.payable')}</td><td className="align-end"><Money value={d.due_week_issued.amount} /> <span className="muted">({f.count(d.due_week_issued.count)})</span></td></tr>
                </tbody></table>
              </Panel>
              <Panel title={<><AlertTriangle size={16} /> {t('checks.dashboard.overdue')}</>} className={d.overdue_received.count + d.overdue_issued.count > 0 ? 'panel-alert' : ''}>
                <table className="mini-table"><tbody>
                  <tr><td>{t('checks.dashboard.receivable')}</td><td className="align-end"><Link to="/checks/reports?tab=overdue&check_type=received"><Money value={d.overdue_received.amount} /></Link> <span className="muted">({f.count(d.overdue_received.count)})</span></td></tr>
                  <tr><td>{t('checks.dashboard.payable')}</td><td className="align-end"><Link to="/checks/reports?tab=overdue"><Money value={d.overdue_issued.amount} /></Link> <span className="muted">({f.count(d.overdue_issued.count)})</span></td></tr>
                </tbody></table>
              </Panel>
              <Panel title={<><Undo2 size={16} /> {t('checks.dashboard.returned')}</>}>
                <table className="mini-table"><tbody>
                  <tr><td>{t('checks.dashboard.receivable')}</td><td className="align-end"><Link to="/checks/reports?tab=returned"><Money value={d.returned_received.amount} /></Link> <span className="muted">({f.count(d.returned_received.count)})</span></td></tr>
                  <tr><td>{t('checks.dashboard.payable')}</td><td className="align-end"><Money value={d.returned_issued.amount} /> <span className="muted">({f.count(d.returned_issued.count)})</span></td></tr>
                </tbody></table>
              </Panel>
            </div>
          )}
          <div className="grid-2 section-gap">
            <Panel title={t('checks.dashboard.upcomingReceipts')} bodyless actions={<Link to="/checks/reports?tab=due&check_type=received">{t('common.viewAll')}</Link>}>
              {list(d?.upcoming_receipts)}
            </Panel>
            <Panel title={t('checks.dashboard.upcomingPayments')} bodyless actions={<Link to="/checks/reports?tab=due">{t('common.viewAll')}</Link>}>
              {list(d?.upcoming_payments)}
            </Panel>
          </div>
          <Panel title={t('checks.reports.names.cash-flow')} actions={<Link to="/checks/reports/cash-flow">{t('common.viewAll')}</Link>}>
            {flow.error ? <ErrorState compact error={flow.error} onRetry={() => void flow.refetch()} /> : flow.data && flow.data.rows.length > 0 ? (
              <BarChart format={(v) => f.money(v)} onSelect={(g) => navigate(`/checks/reports?tab=due&due_from=${g.key}&due_to=${addDaysIso(g.key, 6)}`)}
                series={[{ key: 'inflow', label: t('checks.inflow'), className: 'actual' }, { key: 'outflow', label: t('checks.outflow'), className: 'budget' }]}
                groups={flow.data.rows.map((r) => ({ key: r.from, label: f.date(r.from).slice(5), values: { inflow: r.inflow, outflow: r.outflow } }))} />
            ) : flow.data ? <EmptyState compact title={t('checks.none')} /> : null}
          </Panel>
        </>
      )}
    </div>
  );
}
