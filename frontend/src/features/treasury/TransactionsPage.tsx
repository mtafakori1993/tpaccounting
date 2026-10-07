import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { ArrowDownLeft, ArrowLeftRight, ArrowUpRight } from 'lucide-react';
import { api } from '@/api/client';
import type { TreasuryTx } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { Field, Input, Select } from '@/components/ui/Field';
import { Alert, Amount, KV, LoadingBlock, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { Attachments } from '@/components/Attachments';
import { TreasurySelect } from '@/components/pickers/Pickers';
import { useList } from '@/lib/useList';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { treasuryDocument } from '@/lib/documents';
import { routeFor } from '@/features/invoices/docs';
import { TransactionDialog, type TxDefaults, type TxKind } from './TransactionDialog';

export function TransactionsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const [sp, setSp] = useSearchParams();
  const [creating, setCreating] = useState<{ kind: TxKind; defaults?: TxDefaults } | null>(null);
  const viewId = Number(sp.get('view')) || null;
  const list = useList<TreasuryTx, { q: string; kind: string; status: string; treasury_account_id: string; from: string; to: string }>(
    'treasury-tx', '/treasury/transactions', { q: '', kind: '', status: '', treasury_account_id: '', from: '', to: '' }, { key: 'date', desc: true },
  );

  // Deep links: ?new=receipt&party=..&invoice=..&amount=..&account=..
  useEffect(() => {
    const n = sp.get('new') as TxKind | null;
    if (n && ['receipt', 'payment', 'transfer'].includes(n) && can('treasury.create')) {
      const num = (k: string) => (sp.get(k) ? Number(sp.get(k)) : undefined);
      setCreating({ kind: n, defaults: { party: num('party'), invoice: num('invoice'), account: num('account'), amount: sp.get('amount') ?? undefined } });
      const next = new URLSearchParams(sp);
      ['new', 'party', 'invoice', 'amount', 'account'].forEach((k) => next.delete(k));
      setSp(next, { replace: true });
    }
  }, [sp, setSp, can]);

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['treasury-tx'] });
    void qc.invalidateQueries({ queryKey: ['treasury-accounts'] });
    void qc.invalidateQueries({ queryKey: ['dashboard'] });
    void qc.invalidateQueries({ queryKey: ['invoice'] });
  };
  const counterpart = (x: TreasuryTx) => x.party_name ?? x.counter_account_name ?? x.to_treasury_account_name ?? '';
  const rows = list.query.data?.items;

  return (
    <div className="page">
      <PageHeader
        title={t('treasury.transactionsTitle')}
        actions={<>
          <ExportMenu name="transactions" permission="treasury.export" rows={rows} columns={[
            { header: t('common.type'), value: (x) => t(`treasury.kinds.${x.kind}`) },
            { header: t('common.number'), value: (x) => x.number, numeric: true },
            { header: t('common.date'), value: (x) => f.date(x.date) },
            { header: t('treasury.treasuryAccount'), value: (x) => x.treasury_account_name },
            { header: t('treasury.counterpart'), value: counterpart },
            { header: t('common.amount'), value: (x) => x.amount, numeric: true },
            { header: t('common.description'), value: (x) => x.description },
            { header: t('common.status'), value: (x) => t(`status.${x.status}`) },
          ]} />
          {can('treasury.create') && <>
            <Button icon={<ArrowLeftRight size={16} />} onClick={() => setCreating({ kind: 'transfer' })}>{t('treasury.newTransfer')}</Button>
            <Button icon={<ArrowUpRight size={16} />} onClick={() => setCreating({ kind: 'payment' })}>{t('treasury.newPayment')}</Button>
            <Button variant="accent" icon={<ArrowDownLeft size={16} />} onClick={() => setCreating({ kind: 'receipt' })}>{t('treasury.newReceipt')}</Button>
          </>}
        </>}
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('common.type')}>
            <Select value={list.filters.kind} onChange={(e) => list.setFilter('kind', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {(['receipt', 'payment', 'transfer'] as const).map((k) => <option key={k} value={k}>{t(`treasury.kinds.${k}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('treasury.treasuryAccount')}>
            <TreasurySelect allowEmpty value={Number(list.filters.treasury_account_id) || null} onChange={(x) => list.setFilter('treasury_account_id', x ? String(x) : '')} />
          </Field>
          <DateRange from={list.filters.from} to={list.filters.to} onChange={(from, to) => list.patch({ from, to })} />
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(x) => x.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(x) => setSp({ view: String(x.id) })}
          columns={[
            { key: 'kind', header: t('common.type'), sortable: true, cell: (x) => t(`treasury.kinds.${x.kind}`), width: 100 },
            { key: 'number', header: t('common.number'), sortable: true, cell: (x) => <span className="num">{f.int(x.number)}</span>, width: 80 },
            { key: 'date', header: t('common.date'), sortable: true, cell: (x) => <span className="num">{f.date(x.date)}</span>, width: 110 },
            { key: 'acc', header: t('treasury.treasuryAccount'), cell: (x) => x.treasury_account_name },
            { key: 'cp', header: t('treasury.counterpart'), cell: counterpart },
            { key: 'amount', header: t('common.amount'), sortable: true, align: 'end', cell: (x) => <span className={x.kind === 'receipt' ? 'success-text' : undefined}><Amount value={x.amount} /></span> },
            { key: 'status', header: t('common.status'), cell: (x) => <StatusBadge status={x.status} />, width: 110 },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {creating && (
        <TransactionDialog kind={creating.kind} defaults={creating.defaults} onClose={() => setCreating(null)}
          onSaved={(tx) => { setCreating(null); refresh(); setSp({ view: String(tx.id) }); }} />
      )}
      {viewId && <TxView id={viewId} onClose={() => setSp({})} onChanged={refresh} />}
    </div>
  );
}

function TxView({ id, onClose, onChanged }: { id: number; onClose: () => void; onChanged: () => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { can } = useAuth();
  const [confirm, setConfirm] = useState(false);
  const action = useSubmit();
  const printer = usePdf();
  const q = useQuery({ queryKey: ['treasury-tx-view', id], queryFn: () => api.get<TreasuryTx>(`/treasury/transactions/${id}`) });
  const x = q.data;
  return (
    <Dialog wide title={x ? `${t(`treasury.kinds.${x.kind}`)} ${f.int(x.number)}` : t('common.loading')} onClose={onClose}
      footer={x && <>
        <Button pending={printer.pending} onClick={() => printer.view(() => treasuryDocument(x, t, tryT, f))}>{t('common.exportPdf')}</Button>
        <Button pending={printer.pending} onClick={() => printer.print(() => treasuryDocument(x, t, tryT, f))}>{t('common.printDoc')}</Button>
        {x.journal_entry_id && <Link className="btn btn-secondary" to={`/journal/${x.journal_entry_id}`}>{t('invoices.journalEntry')}</Link>}
        {x.status === 'posted' && can('treasury.cancel') && <Button variant="negative" onClick={() => setConfirm(true)}>{t('common.cancel')}</Button>}
        <Button onClick={onClose}>{t('common.close')}</Button>
      </>}>
      {q.error && <Alert>{err(q.error)}</Alert>}
      {!x ? <LoadingBlock /> : (
        <div className="stack">
          <KV items={[
            { label: t('common.status'), value: <StatusBadge status={x.status} /> },
            { label: t('common.date'), value: <span className="num">{f.date(x.date)}</span> },
            { label: t('common.amount'), value: <Amount value={x.amount} /> },
            { label: x.kind === 'transfer' ? t('treasury.fromAccount') : t('treasury.treasuryAccount'), value: x.treasury_account_name },
            { label: t('treasury.toAccount'), value: x.to_treasury_account_name, hidden: !x.to_treasury_account_name },
            { label: t('treasury.payee'), value: x.party_id && <Link to={`/parties/${x.party_id}`}>{x.party_name}</Link>, hidden: !x.party_id },
            { label: t('journal.account'), value: x.counter_account_name, hidden: !x.counter_account_name },
            { label: t('common.reference'), value: x.reference },
            { label: t('common.description'), value: x.description },
          ]} />
          {!!x.allocations?.length && (
            <DataTable rows={x.allocations} rowKey={(a) => a.invoice_id} columns={[
              { key: 'doc', header: t('treasury.allocations'), cell: (a) => <Link to={routeFor(a.side, a.kind, a.invoice_id)}>{tryT(`invoices.singular.${a.side}_${a.kind}`)} {f.int(a.number)}</Link> },
              { key: 'amt', header: t('treasury.allocated'), align: 'end', cell: (a) => <Amount value={a.amount} /> },
            ]} />
          )}
          <Attachments entity="treasury_transaction" id={x.id} canEdit={can('treasury.edit')} />
        </div>
      )}
      {confirm && x && (
        <ConfirmDialog title={t('treasury.cancelTitle')} body={t('treasury.cancelHelp')} confirmLabel={t('common.confirm')} negative
          pending={action.pending} error={action.error} onClose={() => setConfirm(false)}
          onConfirm={async () => {
            if (await action.run(() => api.post(`/treasury/transactions/${x.id}/cancel`, {}).then(() => true), t('common.cancelledToast'))) {
              setConfirm(false);
              void q.refetch();
              onChanged();
            }
          }} />
      )}
    </Dialog>
  );
}
