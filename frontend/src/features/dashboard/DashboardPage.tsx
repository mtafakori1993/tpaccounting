import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowLeftRight, BookOpen, FileText, Receipt, ShoppingCart } from 'lucide-react';
import { api } from '@/api/client';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { Amount, EmptyState, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { useErrorMessage } from '@/i18n/errors';
import { Alert } from '@/components/ui/Misc';
import { toBig } from '@/lib/decimal';
import { todayIso } from '@/lib/dates';

interface DashInvoice { id: number; side: string; number: number; date: string; due_date: string | null; party_name: string; total: string; outstanding: string }
interface Dash {
  month_start: string;
  year_start: string;
  today: string;
  cash_balance: string;
  bank_balance: string;
  receivables: string;
  payables: string;
  sales_month: string;
  expenses_month: string;
  net_profit_year: string;
  recent_entries: { id: number; number: number; date: string; description: string; source_type: string; amount: string }[];
  outstanding_invoices: DashInvoice[];
  upcoming_payments: DashInvoice[];
}

export function DashboardPage() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const { me, can } = useAuth();
  const navigate = useNavigate();
  const err = useErrorMessage();
  const q = useQuery({ queryKey: ['dashboard'], queryFn: () => api.get<Dash>('/dashboard') });
  const d = q.data;

  const kpi = (label: string, value: string, to: string, lead?: boolean) => (
    <Link to={to} className={`kpi ${lead ? 'lead' : ''}`}>
      <span className="label">{label}</span>
      <span className={`value num ${toBig(value).lt(0) ? 'neg' : ''}`}>{f.money(value)}</span>
    </Link>
  );

  const invoiceCols = (side: 'sales' | 'purchases') => [
    { key: 'number', header: t('common.number'), cell: (r: DashInvoice) => <span className="num">{f.int(r.number)}</span> },
    { key: 'party', header: side === 'sales' ? t('invoices.customer') : t('invoices.supplier'), cell: (r: DashInvoice) => r.party_name },
    {
      key: 'due', header: t('invoices.dueDate'), cell: (r: DashInvoice) => {
        const due = r.due_date ?? r.date;
        return <span className={`num ${due < todayIso() ? 'danger-text' : ''}`}>{f.date(due)}</span>;
      },
    },
    { key: 'out', header: t('dashboard.outstanding'), align: 'end' as const, cell: (r: DashInvoice) => <Amount value={r.outstanding} /> },
  ];

  return (
    <div className="page">
      <PageHeader title={t('dashboard.greeting', { name: me?.full_name ?? '' })} subtitle={d && `${t('common.today')}: ${f.date(d.today)}`} />
      <div className="quick no-print">
        {can('sales.create') && <LinkButton to="/sales/invoices/new" variant="accent" icon={<FileText size={16} />}>{t('invoices.newDoc', { doc: t('invoices.singular.sales_invoice') })}</LinkButton>}
        {can('purchases.create') && <LinkButton to="/purchases/invoices/new" icon={<ShoppingCart size={16} />}>{t('invoices.newDoc', { doc: t('invoices.singular.purchase_invoice') })}</LinkButton>}
        {can('treasury.create') && <LinkButton to="/treasury/transactions?new=receipt" icon={<ArrowLeftRight size={16} />}>{t('treasury.newReceipt')}</LinkButton>}
        {can('treasury.create') && <LinkButton to="/treasury/transactions?new=payment" icon={<ArrowLeftRight size={16} />}>{t('treasury.newPayment')}</LinkButton>}
        {can('expenses.create') && <LinkButton to="/expenses?new=1" icon={<Receipt size={16} />}>{t('expenses.newExpense')}</LinkButton>}
        {can('journal.create') && <LinkButton to="/journal/new" icon={<BookOpen size={16} />}>{t('journal.newEntry')}</LinkButton>}
      </div>
      {q.error && <Alert>{err(q.error)}</Alert>}
      {q.isLoading && <LoadingBlock />}
      {d && (
        <>
          <div className="kpis">
            {kpi(t('dashboard.netProfitYear'), d.net_profit_year, `/reports/profit-loss?from=${d.year_start}&to=${d.today}`, true)}
            {kpi(t('dashboard.cashBalance'), d.cash_balance, '/treasury')}
            {kpi(t('dashboard.bankBalance'), d.bank_balance, '/treasury')}
            {kpi(t('dashboard.receivables'), d.receivables, '/reports/receivables')}
            {kpi(t('dashboard.payables'), d.payables, '/reports/payables')}
            {kpi(t('dashboard.salesMonth'), d.sales_month, `/reports/sales?from=${d.month_start}&to=${d.today}`)}
            {kpi(t('dashboard.expensesMonth'), d.expenses_month, `/reports/expenses?from=${d.month_start}&to=${d.today}`)}
          </div>
          <div className="dash-grid">
            <Panel title={t('dashboard.outstandingInvoices')} bodyless actions={<Link to="/sales/invoices?payment_status=open">{t('common.viewAll')}</Link>}>
              <DataTable
                rows={d.outstanding_invoices}
                rowKey={(r) => r.id}
                columns={invoiceCols('sales')}
                onRowClick={(r) => navigate(`/sales/invoices/${r.id}`)}
                empty={<EmptyState title={t('dashboard.noOutstanding')} />}
              />
            </Panel>
            <Panel title={t('dashboard.upcomingPayments')} bodyless actions={<Link to="/purchases/invoices?payment_status=open">{t('common.viewAll')}</Link>}>
              <DataTable
                rows={d.upcoming_payments}
                rowKey={(r) => r.id}
                columns={invoiceCols('purchases')}
                onRowClick={(r) => navigate(`/purchases/invoices/${r.id}`)}
                empty={<EmptyState title={t('dashboard.noUpcoming')} />}
              />
            </Panel>
            <Panel title={t('dashboard.recentEntries')} bodyless className="full" actions={<Link to="/journal">{t('common.viewAll')}</Link>}>
              <DataTable
                rows={d.recent_entries}
                rowKey={(r) => r.id}
                onRowClick={(r) => navigate(`/journal/${r.id}`)}
                empty={<EmptyState title={t('dashboard.noRecent')} />}
                columns={[
                  { key: 'number', header: t('common.number'), cell: (r) => <span className="num">{f.int(r.number)}</span>, width: 80 },
                  { key: 'date', header: t('common.date'), cell: (r) => <span className="num">{f.date(r.date)}</span>, width: 120 },
                  { key: 'source', header: t('journal.source'), cell: (r) => tryT(`journal.sources.${r.source_type}`) ?? r.source_type, width: 160 },
                  { key: 'desc', header: t('common.description'), cell: (r) => r.description },
                  { key: 'amount', header: t('common.amount'), align: 'end', cell: (r) => <Amount value={r.amount} /> },
                ]}
              />
            </Panel>
          </div>
        </>
      )}
    </div>
  );
}
