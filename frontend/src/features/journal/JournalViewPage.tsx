import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { Copy, FileType2, Pencil, Printer, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { JournalEntry } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { ActionMenu, Alert, Amount, KV, LoadingBlock, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { Attachments } from '@/components/Attachments';
import { useAccountName } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { journalDocument } from '@/lib/documents';
import { sourceRoute } from './sourceLinks';

export function JournalViewPage() {
  const { id } = useParams();
  const { t, tryT } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const name = useAccountName();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | null>(null);
  const action = useSubmit();
  const printer = usePdf();
  const q = useQuery({ queryKey: ['journal', Number(id)], queryFn: () => api.get<JournalEntry>(`/journal-entries/${id}`) });
  const e = q.data;
  if (q.error) return <div className="page"><Alert>{err(q.error)}</Alert></div>;
  if (!e) return <LoadingBlock />;

  const manual = e.source_type === 'manual' || e.source_type === 'opening';
  const src = sourceRoute(e.source_type, e.source_id);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['journal'] });
    void qc.invalidateQueries({ queryKey: ['dashboard'] });
  };

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('journal.title'), to: '/journal' }]}
        title={<>{t('journal.entry', { number: f.int(e.number) })} <StatusBadge status={e.status} /></>}
        subtitle={f.date(e.date)}
        actions={
          <>
            {e.status === 'draft' && manual && can('journal.edit') && <Button icon={<Pencil size={16} />} onClick={() => navigate(`/journal/${e.id}/edit`)}>{t('common.edit')}</Button>}
            {e.status === 'draft' && can('journal.post') && (
              <Button variant="accent" pending={action.pending} onClick={() => action.run(() => api.post(`/journal-entries/${e.id}/post`).then(refresh), t('common.postedToast'))}>
                {t('common.post')}
              </Button>
            )}
            <ActionMenu
              items={[
                { label: t('common.duplicate'), icon: <Copy size={16} />, to: `/journal/new?copy=${e.id}`, hidden: !manual || !can('journal.create') },
                { label: t('common.exportPdf'), icon: <FileType2 size={16} />, onClick: () => printer.view(() => journalDocument(e, t, tryT, f, name)) },
                { label: t('common.printDoc'), icon: <Printer size={16} />, onClick: () => printer.print(() => journalDocument(e, t, tryT, f, name)) },
                { label: t('journal.viewSource'), to: src ?? '', hidden: !src || manual },
                { separator: true, label: '', hidden: !(manual && e.status !== 'cancelled' && !e.reversal_of_id) },
                { label: t('common.cancel'), danger: true, onClick: () => setConfirm('cancel'), hidden: !manual || e.status === 'cancelled' || !!e.reversal_of_id || !can('journal.cancel') },
                { label: t('common.delete'), icon: <Trash2 size={16} />, danger: true, onClick: () => setConfirm('delete'), hidden: e.status !== 'draft' || e.source_type !== 'manual' || !can('journal.delete') },
              ]}
            />
          </>
        }
      />
      <div className="stack">
        {action.error && <Alert>{action.error}</Alert>}
        {e.reversal_of_id && <Alert kind="info"><Link to={`/journal/${e.reversal_of_id}`}>{t('journal.reversalOf', { number: f.int(e.reversal_of_number ?? 0) })}</Link></Alert>}
        {e.reversed_by_id && <Alert kind="warning"><Link to={`/journal/${e.reversed_by_id}`}>{t('journal.reversedBy', { number: f.int(e.reversed_by_number ?? 0) })}</Link></Alert>}
        <div className="side-layout">
          <Panel bodyless title={t('journal.lines')}>
            <DataTable
              rows={e.lines}
              rowKey={(l) => l.id ?? 0}
              showFooter
              columns={[
                { key: 'account', header: t('journal.account'), cell: (l) => <Link to={`/reports/ledger?account_id=${l.account_id}`}><span className="num muted">{f.digits(l.account_code ?? '')}</span> {name({ name: l.account_name ?? '', name_en: l.account_name_en })}</Link> },
                { key: 'party', header: t('journal.party'), cell: (l) => (l.party_id ? <Link to={`/parties/${l.party_id}`}>{l.party_name}</Link> : '') },
                { key: 'cc', header: t('journal.costCenter'), cell: (l) => l.cost_center_name ?? '' },
                { key: 'desc', header: t('common.description'), cell: (l) => l.description, footer: t('common.total') },
                { key: 'debit', header: t('common.debit'), align: 'end', cell: (l) => <Amount value={l.debit} blankZero />, footer: <Amount value={e.total_debit} /> },
                { key: 'credit', header: t('common.credit'), align: 'end', cell: (l) => <Amount value={l.credit} blankZero />, footer: <Amount value={e.total_credit} /> },
              ]}
            />
          </Panel>
          <div className="stack">
            <Panel title={t('common.details')}>
              <KV
                items={[
                  { label: t('journal.source'), value: src && !manual ? <Link to={src}>{tryT(`journal.sources.${e.source_type}`)}</Link> : tryT(`journal.sources.${e.source_type}`) },
                  { label: t('common.reference'), value: e.reference },
                  { label: t('common.description'), value: e.description },
                  { label: t('common.createdAt'), value: <>{f.dateTime(e.created_at)} {e.created_by_name && <span className="muted">— {e.created_by_name}</span>}</> },
                  { label: t('status.posted'), value: e.posted_at ? <>{f.dateTime(e.posted_at)} <span className="muted">— {e.posted_by_name}</span></> : '—' },
                ]}
              />
            </Panel>
            <Attachments entity="journal_entry" id={e.id} canEdit={can('journal.edit')} />
          </div>
        </div>
      </div>
      {confirm === 'cancel' && (
        <ConfirmDialog
          title={t('journal.cancelTitle')}
          body={t('journal.cancelHelp')}
          confirmLabel={t('common.confirm')}
          negative
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            if (await action.run(() => api.post(`/journal-entries/${e.id}/cancel`, {}).then(() => true), t('common.cancelledToast'))) {
              setConfirm(null);
              refresh();
            }
          }}
        />
      )}
      {confirm === 'delete' && (
        <ConfirmDialog
          title={t('common.delete')}
          body={t('common.confirmDelete')}
          confirmLabel={t('common.yesDelete')}
          negative
          pending={action.pending}
          error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            if (await action.run(() => api.del(`/journal-entries/${e.id}`).then(() => true), t('common.deleted'))) {
              refresh();
              navigate('/journal');
            }
          }}
        />
      )}
    </div>
  );
}
