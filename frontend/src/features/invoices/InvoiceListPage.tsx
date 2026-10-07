import { Navigate, useNavigate, useParams } from 'react-router-dom';
import { Plus } from 'lucide-react';
import type { Invoice } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { LinkButton } from '@/components/ui/Button';
import { Field, Input, Select } from '@/components/ui/Field';
import { Amount, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { useList } from '@/lib/useList';
import { todayIso } from '@/lib/dates';
import { docInfo } from './docs';
import { useSearchParams } from 'react-router-dom';
import { useEffect } from 'react';

export function InvoiceListPage() {
  const { side, kind } = useParams();
  const info = docInfo(side, kind);
  if (!info) return <Navigate to="/" replace />;
  return <InvoiceList key={info.key} info={info} />;
}

function InvoiceList({ info }: { info: NonNullable<ReturnType<typeof docInfo>> }) {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const list = useList<Invoice, { q: string; status: string; payment_status: string; from: string; to: string }>(
    `invoices-${info.key}`, info.api, { q: '', status: '', payment_status: '', from: '', to: '' }, { key: 'date', desc: true },
  );
  useEffect(() => {
    const ps = search.get('payment_status');
    if (ps && ps !== list.filters.payment_status) list.setFilter('payment_status', ps);
  }, [search]);
  const rows = list.query.data?.items;
  const isQuote = info.kind === 'quotation';
  const doc = t(`invoices.singular.${info.key}`);

  return (
    <div className="page">
      <PageHeader
        title={t(`invoices.titles.${info.key}`)}
        actions={
          <>
            <ExportMenu
              name={info.key}
              permission={`${info.module}.export`}
              rows={rows}
              columns={[
                { header: t('common.number'), value: (r) => r.number, numeric: true },
                { header: t('common.date'), value: (r) => f.date(r.date) },
                { header: info.side === 'sales' ? t('invoices.customer') : t('invoices.supplier'), value: (r) => r.party_name },
                { header: t('invoices.subtotal'), value: (r) => r.subtotal, numeric: true },
                { header: t('invoices.discountTotal'), value: (r) => r.discount_total, numeric: true },
                { header: t('invoices.taxTotal'), value: (r) => r.tax_total, numeric: true },
                { header: t('invoices.grandTotal'), value: (r) => r.total, numeric: true },
                { header: t('invoices.paid'), value: (r) => r.paid_amount, numeric: true },
                { header: t('common.status'), value: (r) => t(`status.${r.status}`) },
              ]}
            />
            {can(`${info.module}.create`) && <LinkButton to={`${info.base}/new`} variant="accent" icon={<Plus size={16} />}>{t('invoices.newDoc', { doc })}</LinkButton>}
          </>
        }
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.status} onChange={(e) => list.setFilter('status', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {['draft', 'posted', 'cancelled'].map((s) => <option key={s} value={s}>{t(`status.${s as 'draft'}`)}</option>)}
            </Select>
          </Field>
          {!isQuote && (
            <Field label={t('invoices.paymentStatus')}>
              <Select value={list.filters.payment_status} onChange={(e) => list.setFilter('payment_status', e.target.value)}>
                <option value="">{t('common.all')}</option>
                <option value="open">{t('status.unpaid')} / {t('status.partial')}</option>
                <option value="overdue">{t('status.overdue')}</option>
                <option value="paid">{t('status.paid')}</option>
              </Select>
            </Field>
          )}
          <DateRange from={list.filters.from} to={list.filters.to} onChange={(from, to) => list.patch({ from, to })} />
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(r) => r.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(r) => navigate(`${info.base}/${r.id}`)}
          columns={[
            { key: 'number', header: t('common.number'), sortable: true, cell: (r) => <span className="num">{f.int(r.number)}</span>, width: 80 },
            { key: 'date', header: t('common.date'), sortable: true, cell: (r) => <span className="num">{f.date(r.date)}</span>, width: 110 },
            { key: 'party', header: info.side === 'sales' ? t('invoices.customer') : t('invoices.supplier'), sortable: true, cell: (r) => r.party_name },
            ...(!isQuote && info.kind === 'invoice' ? [{
              key: 'due_date', header: t('invoices.dueDate'), sortable: true,
              cell: (r: Invoice) => <span className={`num ${r.status === 'posted' && r.payment_status !== 'paid' && r.due_date && r.due_date < todayIso() ? 'danger-text' : ''}`}>{f.date(r.due_date)}</span>,
            }] : []),
            { key: 'total', header: t('invoices.grandTotal'), sortable: true, align: 'end' as const, cell: (r) => <Amount value={r.total} /> },
            ...(!isQuote ? [{ key: 'outstanding', header: t('invoices.outstanding'), align: 'end' as const, cell: (r: Invoice) => (r.status === 'posted' ? <Amount value={r.outstanding} blankZero /> : null) }] : []),
            {
              key: 'status', header: t('common.status'), sortable: true, width: 190,
              cell: (r) => <span className="row" style={{ gap: 4 }}><StatusBadge status={r.status} />{!isQuote && r.status === 'posted' && r.payment_status !== 'paid' && <StatusBadge status={r.payment_status} />}</span>,
            },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
    </div>
  );
}
