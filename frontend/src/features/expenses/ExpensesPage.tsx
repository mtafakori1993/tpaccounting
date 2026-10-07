import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { Plus, Tags } from 'lucide-react';
import { api } from '@/api/client';
import { useExpenseCategories } from '@/api/queries';
import type { Expense, ExpenseCategory } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { useErrorMessage } from '@/i18n/errors';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, Select } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Amount, KV, LoadingBlock, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { Attachments } from '@/components/Attachments';
import { AccountPicker, CostCenterSelect, PartyPicker, TreasurySelect } from '@/components/pickers/Pickers';
import { useList } from '@/lib/useList';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { expenseDocument } from '@/lib/documents';
import { todayIso } from '@/lib/dates';

export function ExpensesPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const cats = useExpenseCategories();
  const [sp, setSp] = useSearchParams();
  const [editing, setEditing] = useState<Expense | 'new' | null>(null);
  const [managing, setManaging] = useState(false);
  const viewId = Number(sp.get('view')) || null;
  const list = useList<Expense, { q: string; status: string; category_id: string; from: string; to: string }>(
    'expenses', '/expenses', { q: '', status: '', category_id: '', from: '', to: '' }, { key: 'date', desc: true },
  );
  useEffect(() => {
    if (sp.get('new') && can('expenses.create')) {
      setEditing('new');
      setSp({}, { replace: true });
    }
  }, [sp, setSp, can]);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['expenses'] });
    void qc.invalidateQueries({ queryKey: ['expense-view'] });
    void qc.invalidateQueries({ queryKey: ['treasury-accounts'] });
    void qc.invalidateQueries({ queryKey: ['dashboard'] });
  };
  const rows = list.query.data?.items;
  const catName = (c: ExpenseCategory) => (lang === 'en' && c.name_en ? c.name_en : c.name);
  return (
    <div className="page">
      <PageHeader title={t('expenses.title')} actions={<>
        <Button icon={<Tags size={16} />} onClick={() => setManaging(true)}>{t('expenses.categoriesTitle')}</Button>
        <ExportMenu name="expenses" permission="expenses.export" rows={rows} columns={[
          { header: t('common.number'), value: (x) => x.number, numeric: true },
          { header: t('common.date'), value: (x) => f.date(x.date) },
          { header: t('expenses.category'), value: (x) => x.category_name },
          { header: t('common.description'), value: (x) => x.description },
          { header: t('journal.costCenter'), value: (x) => x.cost_center_name ?? '' },
          { header: t('expenses.paymentMethod'), value: (x) => t(`expenses.methods.${x.payment_method}`) },
          { header: t('common.amount'), value: (x) => x.amount, numeric: true },
          { header: t('common.status'), value: (x) => t(`status.${x.status}`) },
        ]} />
        {can('expenses.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('expenses.newExpense')}</Button>}
      </>} />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('expenses.category')}>
            <Select value={list.filters.category_id} onChange={(e) => list.setFilter('category_id', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {cats.data?.map((c) => <option key={c.id} value={c.id}>{catName(c)}</option>)}
            </Select>
          </Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.status} onChange={(e) => list.setFilter('status', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {['draft', 'posted', 'cancelled'].map((s) => <option key={s} value={s}>{t(`status.${s as 'draft'}`)}</option>)}
            </Select>
          </Field>
          <DateRange from={list.filters.from} to={list.filters.to} onChange={(from, to) => list.patch({ from, to })} />
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(x) => x.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={(x) => (x.status === 'draft' && can('expenses.edit') ? setEditing(x) : setSp({ view: String(x.id) }))}
          columns={[
            { key: 'number', header: t('common.number'), sortable: true, cell: (x) => <span className="num">{f.int(x.number)}</span>, width: 80 },
            { key: 'date', header: t('common.date'), sortable: true, cell: (x) => <span className="num">{f.date(x.date)}</span>, width: 110 },
            { key: 'category', header: t('expenses.category'), sortable: true, cell: (x) => x.category_name },
            { key: 'desc', header: t('common.description'), cell: (x) => x.description },
            { key: 'cc', header: t('journal.costCenter'), cell: (x) => x.cost_center_name ?? '' },
            { key: 'method', header: t('expenses.paymentMethod'), cell: (x) => x.treasury_account_name ?? x.party_name ?? '' },
            { key: 'amount', header: t('common.amount'), sortable: true, align: 'end', cell: (x) => <Amount value={x.amount} /> },
            { key: 'status', header: t('common.status'), cell: (x) => <StatusBadge status={x.status} />, width: 110 },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {editing && <ExpenseDialog expense={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={(x) => { setEditing(null); refresh(); setSp({ view: String(x.id) }); }} />}
      {viewId && <ExpenseView id={viewId} onClose={() => setSp({})} onChanged={refresh} />}
      {managing && <CategoriesDialog onClose={() => setManaging(false)} />}
    </div>
  );
}

function ExpenseDialog({ expense, onClose, onSaved }: { expense?: Expense; onClose: () => void; onSaved: (x: Expense) => void }) {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const cats = useExpenseCategories();
  const [v, setV] = useState({
    date: expense?.date ?? todayIso(),
    category_id: expense?.category_id ?? (null as number | null),
    account_id: null as number | null,
    amount: expense?.amount ?? '',
    description: expense?.description ?? '',
    reference: expense?.reference ?? '',
    cost_center_id: expense?.cost_center_id ?? (null as number | null),
    payment_method: expense?.payment_method ?? ('cash' as Expense['payment_method']),
    treasury_account_id: expense?.treasury_account_id ?? (null as number | null),
    party_id: expense?.party_id ?? (null as number | null),
    generate_entry: expense?.generate_entry ?? true,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async (post: boolean) => {
    const body = { ...v, amount: v.amount || '0', category_id: v.category_id ?? 0, post };
    const x = await run(() => (expense ? api.put<Expense>(`/expenses/${expense.id}`, body) : api.post<Expense>('/expenses', body)), post ? t('common.postedToast') : t('common.saved'));
    if (x) onSaved(x);
  };
  return (
    <Dialog wide title={expense ? t('common.edit') : t('expenses.newExpense')} onClose={onClose}
      footer={<>
        <Button onClick={onClose}>{t('common.cancel')}</Button>
        <Button pending={pending} onClick={() => save(false)}>{t('common.saveDraft')}</Button>
        {can('expenses.post') && <Button variant="accent" pending={pending} onClick={() => save(true)}>{t('common.saveAndPost')}</Button>}
      </>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(can('expenses.post')); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.date')} required error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          <Field label={t('expenses.category')} required error={errors.category_id}>
            <Select value={v.category_id ?? ''} onChange={(e) => setV({ ...v, category_id: e.target.value ? Number(e.target.value) : null })} autoFocus>
              <option value="">{t('common.select')}</option>
              {cats.data?.filter((c) => c.is_active).map((c) => <option key={c.id} value={c.id}>{lang === 'en' && c.name_en ? c.name_en : c.name}</option>)}
            </Select>
          </Field>
          <Field label={t('common.amount')} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => setV({ ...v, amount: x })} /></Field>
          <Field label={t('expenses.paymentMethod')} error={errors.payment_method}>
            <Select value={v.payment_method} onChange={(e) => setV({ ...v, payment_method: e.target.value as Expense['payment_method'], treasury_account_id: null })}>
              {(['cash', 'bank', 'credit'] as const).map((m) => <option key={m} value={m}>{t(`expenses.methods.${m}`)}</option>)}
            </Select>
          </Field>
          {v.payment_method === 'credit' ? (
            <Field label={t('invoices.supplier')} required error={errors.party_id} className="span-2"><PartyPicker role="supplier" value={v.party_id} onChange={(x) => setV({ ...v, party_id: x })} /></Field>
          ) : (
            <Field label={t('treasury.treasuryAccount')} required error={errors.treasury_account_id} className="span-2">
              <TreasurySelect kind={v.payment_method} value={v.treasury_account_id} onChange={(x) => setV({ ...v, treasury_account_id: x })} />
            </Field>
          )}
          <Field label={t('journal.costCenter')}><CostCenterSelect value={v.cost_center_id} onChange={(x) => setV({ ...v, cost_center_id: x })} /></Field>
          <Field label={t('expenses.account')} help={t('expenses.accountHelp')} className="span-2">
            <AccountPicker value={v.account_id} onChange={(x) => setV({ ...v, account_id: x })} filter={(a) => a.type === 'expense' || a.type === 'cost_of_sales'} />
          </Field>
          <Field label={t('common.reference')}><Input value={v.reference} onChange={(e) => setV({ ...v, reference: e.target.value })} /></Field>
          <Field label={t('common.description')} className="span-2"><Input value={v.description} onChange={(e) => setV({ ...v, description: e.target.value })} /></Field>
          <div className="span-all"><Checkbox label={t('expenses.generateEntry')} checked={v.generate_entry} onChange={(x) => setV({ ...v, generate_entry: x })} /></div>
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function ExpenseView({ id, onClose, onChanged }: { id: number; onClose: () => void; onChanged: () => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const err = useErrorMessage();
  const { can } = useAuth();
  const [confirm, setConfirm] = useState<'cancel' | 'delete' | null>(null);
  const action = useSubmit();
  const printer = usePdf();
  const q = useQuery({ queryKey: ['expense-view', id], queryFn: () => api.get<Expense>(`/expenses/${id}`) });
  const x = q.data;
  return (
    <Dialog wide title={x ? `${t('expenses.title')} ${f.int(x.number)}` : t('common.loading')} onClose={onClose}
      footer={x && <>
        <Button pending={printer.pending} onClick={() => printer.print(() => expenseDocument(x, t, f))}>{t('common.printDoc')}</Button>
        {x.journal_entry_id && <Link className="btn btn-secondary" to={`/journal/${x.journal_entry_id}`}>{t('invoices.journalEntry')}</Link>}
        {x.status === 'draft' && can('expenses.post') && <Button variant="accent" pending={action.pending} onClick={() => action.run(() => api.post(`/expenses/${x.id}/post`).then(() => { void q.refetch(); onChanged(); }), t('common.postedToast'))}>{t('common.post')}</Button>}
        {x.status === 'draft' && can('expenses.delete') && <Button variant="negative" onClick={() => setConfirm('delete')}>{t('common.delete')}</Button>}
        {x.status === 'posted' && can('expenses.cancel') && <Button variant="negative" onClick={() => setConfirm('cancel')}>{t('common.cancel')}</Button>}
        <Button onClick={onClose}>{t('common.close')}</Button>
      </>}>
      {q.error && <Alert>{err(q.error)}</Alert>}
      {action.error && <Alert>{action.error}</Alert>}
      {!x ? <LoadingBlock /> : (
        <div className="stack">
          <KV items={[
            { label: t('common.status'), value: <StatusBadge status={x.status} /> },
            { label: t('common.date'), value: <span className="num">{f.date(x.date)}</span> },
            { label: t('common.amount'), value: <Amount value={x.amount} /> },
            { label: t('expenses.category'), value: x.category_name },
            { label: t('expenses.account'), value: x.account_name },
            { label: t('expenses.paymentMethod'), value: `${t(`expenses.methods.${x.payment_method}`)} — ${x.treasury_account_name ?? x.party_name ?? ''}` },
            { label: t('journal.costCenter'), value: x.cost_center_name },
            { label: t('common.reference'), value: x.reference },
            { label: t('common.description'), value: x.description },
          ]} />
          <Attachments entity="expense" id={x.id} canEdit={can('expenses.edit')} />
        </div>
      )}
      {confirm && x && (
        <ConfirmDialog title={confirm === 'cancel' ? t('expenses.cancelTitle') : t('common.delete')} body={confirm === 'cancel' ? t('journal.cancelHelp') : t('common.confirmDelete')}
          confirmLabel={confirm === 'cancel' ? t('common.confirm') : t('common.yesDelete')} negative pending={action.pending} error={action.error}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            const ok = await action.run(() => (confirm === 'cancel' ? api.post(`/expenses/${x.id}/cancel`, {}) : api.del(`/expenses/${x.id}`)).then(() => true));
            if (ok) {
              setConfirm(null);
              onChanged();
              if (confirm === 'delete') onClose();
              else void q.refetch();
            }
          }} />
      )}
    </Dialog>
  );
}

function CategoriesDialog({ onClose }: { onClose: () => void }) {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const qc = useQueryClient();
  const cats = useExpenseCategories();
  const [edit, setEdit] = useState<{ id?: number; name: string; name_en: string; account_id: number | null; is_active: boolean } | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    if (!edit) return;
    const body = { ...edit, account_id: edit.account_id ?? 0 };
    const ok = await run(() => (edit.id ? api.put(`/expenses/categories/${edit.id}`, body) : api.post('/expenses/categories', body)).then(() => true), t('common.saved'));
    if (ok) {
      setEdit(null);
      void qc.invalidateQueries({ queryKey: ['expense-categories'] });
    }
  };
  return (
    <Dialog wide title={t('expenses.categoriesTitle')} onClose={onClose}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        {edit ? (
          <form className="form-grid" onSubmit={(e) => { e.preventDefault(); void save(); }}>
            <Field label={t('common.name')} required error={errors.name}><Input value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value })} autoFocus /></Field>
            <Field label={t('common.nameEn')}><Input value={edit.name_en} onChange={(e) => setEdit({ ...edit, name_en: e.target.value })} dir="ltr" /></Field>
            <Field label={t('expenses.account')} required error={errors.account_id} className="span-2">
              <AccountPicker value={edit.account_id} onChange={(x) => setEdit({ ...edit, account_id: x })} filter={(a) => a.type === 'expense' || a.type === 'cost_of_sales'} />
            </Field>
            {edit.id && <Checkbox label={t('common.isActive')} checked={edit.is_active} onChange={(x) => setEdit({ ...edit, is_active: x })} />}
            <div className="span-all row">
              <Button type="submit" variant="accent" pending={pending}>{t('common.save')}</Button>
              <Button onClick={() => setEdit(null)}>{t('common.cancel')}</Button>
            </div>
          </form>
        ) : can('expenses.create') && (
          <div><Button icon={<Plus size={16} />} onClick={() => setEdit({ name: '', name_en: '', account_id: null, is_active: true })}>{t('expenses.newCategory')}</Button></div>
        )}
        <DataTable<ExpenseCategory>
          rows={cats.data}
          rowKey={(c) => c.id}
          onRowClick={can('expenses.edit') ? (c) => setEdit({ id: c.id, name: c.name, name_en: c.name_en, account_id: c.account_id, is_active: c.is_active }) : undefined}
          rowClassName={(c) => (c.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'name', header: t('common.name'), cell: (c) => (lang === 'en' && c.name_en ? c.name_en : c.name) },
            { key: 'acc', header: t('expenses.account'), cell: (c) => <><span className="num muted">{c.account_code}</span> {c.account_name}</> },
          ]}
        />
      </div>
    </Dialog>
  );
}
