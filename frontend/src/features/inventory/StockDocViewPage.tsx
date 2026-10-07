import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api } from '@/api/client';
import type { StockDocument } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { ActionMenu, Alert, Amount, KV, LoadingBlock, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { Attachments } from '@/components/Attachments';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { stockDocument } from '@/lib/documents';

export function StockDocViewPage() {
  const { id } = useParams();
  const { t } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const action = useSubmit();
  const printer = usePdf();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | null>(null);
  const q = useQuery({ queryKey: ['stock-doc', Number(id)], queryFn: () => api.get<StockDocument>(`/inventory/documents/${id}`) });
  const d = q.data;
  if (q.error) return <div className="page"><Alert>{err(q.error)}</Alert></div>;
  if (!d) return <LoadingBlock />;
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['stock-doc'] });
    void qc.invalidateQueries({ queryKey: ['stock-docs'] });
    void qc.invalidateQueries({ queryKey: ['stock'] });
  };
  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('inventory.documentsTitle'), to: '/inventory/documents' }]}
        title={<>{t(`inventory.kinds.${d.kind}`)} {f.int(d.number)} <StatusBadge status={d.status} /></>}
        subtitle={f.date(d.date)}
        actions={<>
          {d.status === 'draft' && can('inventory.edit') && <Button onClick={() => navigate(`/inventory/documents/${d.id}/edit`)}>{t('common.edit')}</Button>}
          {d.status === 'draft' && can('inventory.post') && <Button variant="accent" pending={action.pending} onClick={() => action.run(() => api.post(`/inventory/documents/${d.id}/post`).then(refresh), t('common.postedToast'))}>{t('common.post')}</Button>}
          <Button pending={printer.pending} onClick={() => printer.print(() => stockDocument(d, t, f))}>{t('common.printDoc')}</Button>
          <ActionMenu items={[
            { label: t('common.exportPdf'), onClick: () => printer.view(() => stockDocument(d, t, f)) },
            { label: t('invoices.journalEntry'), to: `/journal/${d.journal_entry_id}`, hidden: !d.journal_entry_id },
            { label: t('common.cancel'), danger: true, onClick: () => setConfirm('cancel'), hidden: d.status === 'cancelled' || !can('inventory.cancel') },
            { label: t('common.delete'), danger: true, onClick: () => setConfirm('delete'), hidden: d.status !== 'draft' || !can('inventory.delete') },
          ]} />
        </>}
      />
      <div className="stack">
        {action.error && <Alert>{action.error}</Alert>}
        <div className="side-layout">
          <Panel bodyless title={t('invoices.items')}>
            <DataTable rows={d.lines} rowKey={(l) => l.id ?? 0} columns={[
              { key: 'p', header: t('invoices.item'), cell: (l) => <Link to={`/inventory/ledger?product_id=${l.product_id}`}>{l.product_name}</Link> },
              { key: 'q', header: t('inventory.quantity'), align: 'end', cell: (l) => <span className="num">{f.qty(l.quantity)} {l.unit}</span> },
              { key: 'c', header: t('inventory.unitCost'), align: 'end', cell: (l) => <Amount value={l.unit_cost} blankZero /> },
              { key: 'd', header: t('common.description'), cell: (l) => l.description },
            ]} />
          </Panel>
          <div className="stack">
            <Panel title={t('common.details')}>
              <KV items={[
                { label: t('inventory.warehouse'), value: d.warehouse_name },
                { label: t('inventory.toWarehouse'), value: d.to_warehouse_name, hidden: !d.to_warehouse_name },
                { label: t('inventory.counterAccount'), value: d.counter_account_name, hidden: !d.counter_account_name },
                { label: t('common.reference'), value: d.reference },
                { label: t('common.description'), value: d.description },
              ]} />
            </Panel>
            <Attachments entity="stock_document" id={d.id} canEdit={can('inventory.edit')} />
          </div>
        </div>
      </div>
      {confirm && (
        <ConfirmDialog
          title={confirm === 'cancel' ? t('common.cancel') : t('common.delete')}
          body={confirm === 'cancel' ? t('invoices.cancelHelp') : t('common.confirmDelete')}
          confirmLabel={confirm === 'cancel' ? t('common.confirm') : t('common.yesDelete')}
          negative
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            const ok = await action.run(() => (confirm === 'cancel' ? api.post(`/inventory/documents/${d.id}/cancel`, {}) : api.del(`/inventory/documents/${d.id}`)).then(() => true));
            if (ok) {
              setConfirm(null);
              refresh();
              if (confirm === 'delete') navigate('/inventory/documents');
            }
          }}
        />
      )}
    </div>
  );
}
