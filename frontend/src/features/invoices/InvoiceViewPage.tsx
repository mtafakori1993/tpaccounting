import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { Copy, FileOutput, FileType2, Pencil, Printer, Trash2, Undo2, Wallet } from 'lucide-react';
import { api } from '@/api/client';
import type { Invoice } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button, LinkButton } from '@/components/ui/Button';
import { ActionMenu, Alert, Amount, KV, LoadingBlock, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { Attachments } from '@/components/Attachments';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { invoiceDocument } from '@/lib/documents';
import { docInfo, routeFor, type DocInfo } from './docs';

export function InvoiceViewPage() {
  const { side, kind } = useParams();
  const info = docInfo(side, kind);
  if (!info) return <Navigate to="/" replace />;
  return <InvoiceView info={info} />;
}

function InvoiceView({ info }: { info: DocInfo }) {
  const { id } = useParams();
  const { t, tryT, lang } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const action = useSubmit();
  const printer = usePdf();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | null>(null);
  const q = useQuery({ queryKey: ['invoice', info.key, Number(id)], queryFn: () => api.get<Invoice>(`${info.api}/${id}`) });
  const inv = q.data;
  if (q.error) return <div className="page"><Alert>{err(q.error)}</Alert></div>;
  if (!inv) return <LoadingBlock />;

  const doc = t(`invoices.singular.${info.key}`);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['invoice'] });
    void qc.invalidateQueries({ queryKey: [`invoices-${info.key}`] });
    void qc.invalidateQueries({ queryKey: ['dashboard'] });
  };
  const posted = inv.status === 'posted';
  const isQuote = info.kind === 'quotation';
  const payKind = (info.side === 'sales') === (info.kind === 'invoice') ? 'receipt' : 'payment';

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t(`invoices.titles.${info.key}`), to: info.base }]}
        title={<>{t('invoices.docNumber', { doc, number: f.int(inv.number) })} <StatusBadge status={inv.status} /> {posted && !isQuote && <StatusBadge status={inv.payment_status} />}</>}
        subtitle={<><Link to={`/parties/${inv.party_id}`}>{inv.party_name}</Link> — {f.date(inv.date)}</>}
        actions={
          <>
            {inv.status === 'draft' && can(`${info.module}.edit`) && <Button icon={<Pencil size={16} />} onClick={() => navigate(`${info.base}/${inv.id}/edit`)}>{t('common.edit')}</Button>}
            {inv.status === 'draft' && can(`${info.module}.post`) && (
              <Button variant="accent" pending={action.pending} onClick={() => action.run(() => api.post(`${info.api}/${inv.id}/post`).then(refresh), t('common.postedToast'))}>{t('common.post')}</Button>
            )}
            {posted && !isQuote && inv.payment_status !== 'paid' && can('treasury.create') && (
              <LinkButton variant="accent" icon={<Wallet size={16} />} to={`/treasury/transactions?new=${payKind}&party=${inv.party_id}&invoice=${inv.id}&amount=${inv.outstanding}`}>
                {payKind === 'receipt' ? t('invoices.receivePayment') : t('invoices.makePayment')}
              </LinkButton>
            )}
            {isQuote && inv.status !== 'cancelled' && can('sales.create') && (
              <Button variant="accent" icon={<FileOutput size={16} />} pending={action.pending} onClick={async () => {
                const created = await action.run(() => api.post<Invoice>(`${info.api}/${inv.id}/convert`));
                if (created) navigate(routeFor('sales', 'invoice', created.id));
              }}>{t('invoices.convert')}</Button>
            )}
            <Button icon={<Printer size={16} />} pending={printer.pending} onClick={() => printer.print(() => invoiceDocument(inv, t, f, lang))}>{t('common.printDoc')}</Button>
            <ActionMenu
              items={[
                { label: t('common.exportPdf'), icon: <FileType2 size={16} />, onClick: () => printer.view(() => invoiceDocument(inv, t, f, lang)) },
                { label: t('common.duplicate'), icon: <Copy size={16} />, to: `${info.base}/new?copy=${inv.id}`, hidden: !can(`${info.module}.create`) },
                { label: t('invoices.createReturn'), icon: <Undo2 size={16} />, to: `/${info.routeSide}/returns/new?return_of=${inv.id}`, hidden: !(posted && info.kind === 'invoice' && can(`${info.module}.create`)) },
                { label: t('invoices.journalEntry'), to: `/journal/${inv.journal_entry_id}`, hidden: !inv.journal_entry_id },
                { separator: true, label: '' },
                { label: t('common.cancel'), danger: true, onClick: () => setConfirm('cancel'), hidden: inv.status === 'cancelled' || !can(`${info.module}.cancel`) },
                { label: t('common.delete'), icon: <Trash2 size={16} />, danger: true, onClick: () => setConfirm('delete'), hidden: inv.status !== 'draft' || !can(`${info.module}.delete`) },
              ]}
            />
          </>
        }
      />
      <div className="stack">
        {action.error && <Alert>{action.error}</Alert>}
        <div className="side-layout">
          <div className="stack">
            <Panel bodyless title={t('invoices.items')}>
              <DataTable
                rows={inv.items}
                rowKey={(it) => it.id ?? 0}
                columns={[
                  { key: 'item', header: t('invoices.item'), cell: (it) => <>{it.product_name ?? it.description}{it.product_name && it.description && <div className="muted" style={{ fontSize: 12 }}>{it.description}</div>}</> },
                  { key: 'qty', header: t('invoices.quantity'), align: 'end', cell: (it) => <span className="num">{f.qty(it.quantity)} {it.unit}</span> },
                  { key: 'price', header: t('invoices.unitPrice'), align: 'end', cell: (it) => <Amount value={it.unit_price} /> },
                  { key: 'disc', header: t('invoices.discount'), align: 'end', cell: (it) => <Amount value={it.discount} blankZero /> },
                  { key: 'tax', header: t('invoices.tax'), align: 'end', cell: (it) => <Amount value={it.tax_amount} blankZero /> },
                  { key: 'total', header: t('invoices.lineTotal'), align: 'end', cell: (it) => <Amount value={it.total} /> },
                ]}
              />
              <div className="doc-totals">
                <span className="muted">{t('invoices.subtotal')}</span><span className="num">{f.money(inv.subtotal)}</span>
                <span className="muted">{t('invoices.discountTotal')}</span><span className="num">{f.money(inv.discount_total)}</span>
                <span className="muted">{t('invoices.taxTotal')}</span><span className="num">{f.money(inv.tax_total)}</span>
                <span className="grand">{t('invoices.grandTotal')}</span><span className="grand num">{f.money(inv.total)}</span>
                {posted && !isQuote && (
                  <>
                    <span className="muted">{t('invoices.paid')}</span><span className="num">{f.money(inv.paid_amount)}</span>
                    <span className="grand">{t('invoices.outstanding')}</span><span className="grand num">{f.money(inv.outstanding)}</span>
                  </>
                )}
              </div>
            </Panel>
            {!!inv.settlements?.length && (
              <Panel bodyless title={t('invoices.settlements')}>
                <DataTable
                  rows={inv.settlements}
                  rowKey={(s) => s.id}
                  onRowClick={(s) => { if (s.transaction_id) navigate(`/treasury/transactions?view=${s.transaction_id}`); else if (s.check_id) navigate(`/checks/${s.check_id}`); }}
                  columns={[
                    { key: 'date', header: t('common.date'), cell: (s) => <span className="num">{f.date(s.date)}</span> },
                    { key: 'doc', header: t('common.reference'), cell: (s) => (s.transaction_id ? `${tryT(`treasury.kinds.${s.transaction_kind}`)} ${f.int(s.transaction_number ?? 0)}` : s.check_id ? `${t('checks.checkNumber')} ${f.digits(s.check_number ?? '')}` : `${t(`invoices.singular.${info.side}_return` as 'invoices.singular.sales_return')} ${f.int(s.credit_invoice_number ?? 0)}`) },
                    { key: 'amount', header: t('common.amount'), align: 'end', cell: (s) => <Amount value={s.amount} /> },
                  ]}
                />
              </Panel>
            )}
          </div>
          <div className="stack">
            <Panel title={t('common.details')}>
              <KV
                items={[
                  { label: info.side === 'sales' ? t('invoices.customer') : t('invoices.supplier'), value: <Link to={`/parties/${inv.party_id}`}>{inv.party_name}</Link> },
                  { label: t('common.date'), value: <span className="num">{f.date(inv.date)}</span> },
                  { label: t('invoices.dueDate'), value: <span className="num">{f.date(inv.due_date)}</span>, hidden: !inv.due_date },
                  { label: t('invoices.warehouse'), value: inv.warehouse_name, hidden: !inv.warehouse_name },
                  { label: t('journal.costCenter'), value: inv.cost_center_name, hidden: !inv.cost_center_name },
                  { label: t('common.reference'), value: inv.reference, hidden: !inv.reference },
                  {
                    label: t('invoices.relatedInvoice'),
                    hidden: !inv.related_invoice_id,
                    value: inv.related_invoice_id && (
                      <Link to={routeFor(info.side, inv.related_kind as 'invoice', inv.related_invoice_id)}>
                        {inv.related_kind === 'quotation' ? t('invoices.convertedFrom', { number: f.int(inv.related_number ?? 0) }) : t('invoices.returnOf', { number: f.int(inv.related_number ?? 0) })}
                      </Link>
                    ),
                  },
                  { label: t('invoices.journalEntry'), value: inv.journal_entry_id && <Link to={`/journal/${inv.journal_entry_id}`}>{f.int(inv.journal_number ?? 0)}</Link>, hidden: !inv.journal_entry_id },
                  { label: t('common.notes'), value: inv.notes, hidden: !inv.notes },
                ]}
              />
            </Panel>
            <Attachments entity="invoice" id={inv.id} canEdit={can(`${info.module}.edit`)} />
          </div>
        </div>
      </div>
      {confirm && (
        <ConfirmDialog
          title={confirm === 'cancel' ? t('invoices.cancelTitle') : t('common.delete')}
          body={confirm === 'cancel' ? t('invoices.cancelHelp') : t('common.confirmDelete')}
          confirmLabel={confirm === 'cancel' ? t('common.confirm') : t('common.yesDelete')}
          negative
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            const ok = await action.run(
              () => (confirm === 'cancel' ? api.post(`${info.api}/${inv.id}/cancel`, {}) : api.del(`${info.api}/${inv.id}`)).then(() => true),
              confirm === 'cancel' ? t('common.cancelledToast') : t('common.deleted'),
            );
            if (ok) {
              setConfirm(null);
              refresh();
              if (confirm === 'delete') navigate(info.base);
            }
          }}
        />
      )}
    </div>
  );
}
