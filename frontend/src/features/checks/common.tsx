import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { api, type Query } from '@/api/client';
import type { Bank, Check, CheckType, Page } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, Select, TextArea } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Money, StatusBadge } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import type { Column } from '@/components/ui/DataTable';
import { AccountPicker, CostCenterSelect, PartyPicker, TreasurySelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { todayIso } from '@/lib/dates';

export const useBanks = () => useQuery({ queryKey: ['banks'], queryFn: () => api.get<Bank[]>('/checks/banks'), staleTime: Infinity });

export const receivedStatuses = ['received', 'in_custody', 'deposited', 'cleared', 'transferred', 'returned', 'cancelled', 'lost'];
export const issuedStatuses = ['draft', 'issued', 'delivered', 'cleared', 'returned', 'cancelled', 'replaced'];
export const negativeActions = ['return', 'lost', 'cancel', 'replace'];
export const checkPermission = (a: string) => (negativeActions.includes(a) ? 'checks.cancel' : 'checks.post');

/** Invalidates everything that shows check data. */
export const checkQueryKeys = [['checks'], ['check'], ['check-dashboard'], ['check-summary'], ['treasury-accounts']];

/** Due-date hint: overdue, today, or days to go. */
export function DueCell({ c }: { c: Check }) {
  const { t } = useI18n();
  const f = useFormat();
  return (
    <span className="num">
      {f.date(c.due_date)}
      {c.live && (
        <span className={`muted ${c.overdue ? 'danger-text' : ''}`} style={{ display: 'block', fontSize: 'var(--font-size-xs)' }}>
          {c.overdue ? t('checks.overdueBy', { days: f.count(-c.days_to_due) }) : c.days_to_due === 0 ? t('checks.dueToday') : t('checks.dueIn', { days: f.count(c.days_to_due) })}
        </span>
      )}
    </span>
  );
}

/** A check's bank name in the current language (catalogue banks have an English name). */
export function useBankName() {
  const { lang } = useI18n();
  const banks = useBanks();
  return (c: Pick<Check, 'bank_id' | 'bank_name'>) => {
    const b = c.bank_id ? banks.data?.find((x) => x.id === c.bank_id) : undefined;
    if (!b || (b.name !== c.bank_name && b.name_en !== c.bank_name)) return c.bank_name;
    return lang === 'en' && b.name_en ? b.name_en : b.name;
  };
}

/** Columns of every check table. Identifiers are shown as typed (never grouped). */
export function useCheckColumns(opts: { showType?: boolean } = {}): Column<Check>[] {
  const { t } = useI18n();
  const f = useFormat();
  const bankName = useBankName();
  return [
    { key: 'number', header: t('checks.registerNumber'), sortable: true, optional: true, cell: (c) => <span className="num muted">{f.digits(String(c.number))}</span>, width: 80 },
    ...(opts.showType === false ? [] : [{ key: 'type', header: t('checks.type'), optional: true, cell: (c: Check) => t(`checks.types.${c.check_type}`), width: 90 }]),
    { key: 'check_number', header: t('checks.checkNumber'), sortable: true, cell: (c) => <span className="num" dir="ltr">{f.digits(c.check_number)}</span> },
    { key: 'party', header: t('checks.party'), sortable: true, cell: (c) => <span>{c.party_name}{c.holder_party_name && <span className="muted" style={{ display: 'block', fontSize: 'var(--font-size-xs)' }}>← {c.holder_party_name}</span>}</span> },
    { key: 'bank', header: t('checks.bank'), sortable: true, cell: (c) => bankName(c) },
    { key: 'sayadi', header: t('checks.sayadiId'), optional: true, defaultHidden: true, cell: (c) => <span className="num" dir="ltr">{f.digits(c.sayadi_id)}</span> },
    { key: 'issue_date', header: t('checks.issueDate'), sortable: true, optional: true, defaultHidden: true, cell: (c) => <span className="num">{f.date(c.issue_date)}</span> },
    { key: 'due_date', header: t('checks.dueDate'), sortable: true, cell: (c) => <DueCell c={c} />, width: 140 },
    { key: 'amount', header: t('checks.amount'), sortable: true, align: 'end', cell: (c) => <Money value={c.amount} /> },
    { key: 'status', header: t('common.status'), sortable: true, cell: (c) => <StatusBadge status={c.status} />, width: 140 },
  ];
}

export interface CheckFilters {
  q: string; status: string; bank_id: string; party_id: string; due_from: string; due_to: string; min_amount: string; max_amount: string; live: string;
}
export const filterKeys: (keyof CheckFilters)[] = ['q', 'status', 'bank_id', 'party_id', 'due_from', 'due_to', 'min_amount', 'max_amount', 'live'];
export const advancedKeys: (keyof CheckFilters)[] = ['due_from', 'due_to', 'min_amount', 'max_amount', 'live'];

/** Filters kept in the URL, so a filtered view can be bookmarked or shared. */
export function useUrlFilters() {
  const [sp, setSp] = useSearchParams();
  const values = Object.fromEntries(filterKeys.map((k) => [k, sp.get(k) ?? ''])) as unknown as CheckFilters;
  const set = (patch: Record<string, string | null>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) {
      if (!v) n.delete(k);
      else n.set(k, v);
    }
    n.delete('page');
    setSp(n, { replace: true });
  };
  const active = filterKeys.filter((k) => values[k]).length;
  const advancedActive = advancedKeys.filter((k) => values[k]).length;
  const reset = () => set(Object.fromEntries(filterKeys.map((k) => [k, null])));
  const query: Query = Object.fromEntries(filterKeys.filter((k) => values[k]).map((k) => [k, values[k]]));
  return { sp, setSp, values, set, active, advancedActive, reset, query };
}

/** Basic filters (always visible). */
export function BasicCheckFilters({ u, type }: { u: ReturnType<typeof useUrlFilters>; type?: string }) {
  const { t, lang } = useI18n();
  const banks = useBanks();
  const statuses = type === 'issued' ? issuedStatuses : type === 'received' ? receivedStatuses : Array.from(new Set([...receivedStatuses, ...issuedStatuses]));
  return (
    <>
      <Field label={t('common.search')} className="search">
        <Input type="search" value={u.values.q} placeholder={`${t('checks.checkNumber')}، ${t('checks.sayadiId')}، ${t('checks.party')}…`} onChange={(e) => u.set({ q: e.target.value })} />
      </Field>
      <Field label={t('common.status')}>
        <Select value={u.values.status} onChange={(e) => u.set({ status: e.target.value })}>
          <option value="">{t('common.all')}</option>
          {statuses.map((s) => <option key={s} value={s}>{t(`checks.statuses.${s as 'draft'}`)}</option>)}
        </Select>
      </Field>
      <Field label={t('checks.bank')}>
        <Select value={u.values.bank_id} onChange={(e) => u.set({ bank_id: e.target.value })}>
          <option value="">{t('common.all')}</option>
          {banks.data?.map((b) => <option key={b.id} value={b.id}>{lang === 'en' ? b.name_en : b.name}</option>)}
        </Select>
      </Field>
      <Field label={t('checks.party')}><PartyPicker value={Number(u.values.party_id) || null} onChange={(x) => u.set({ party_id: x ? String(x) : null })} /></Field>
    </>
  );
}

/** Advanced filters (behind "More filters"). */
export function AdvancedCheckFilters({ u, dates = true }: { u: ReturnType<typeof useUrlFilters>; dates?: boolean }) {
  const { t } = useI18n();
  return (
    <>
      {dates && (
        <>
          <Field label={t('checks.dueFrom')}><DateField value={u.values.due_from} onChange={(x) => u.set({ due_from: x })} /></Field>
          <Field label={t('checks.dueTo')}><DateField value={u.values.due_to} onChange={(x) => u.set({ due_to: x })} /></Field>
        </>
      )}
      <Field label={t('checks.minAmount')}><MoneyInput value={u.values.min_amount} onChange={(x) => u.set({ min_amount: x })} /></Field>
      <Field label={t('checks.maxAmount')}><MoneyInput value={u.values.max_amount} onChange={(x) => u.set({ max_amount: x })} /></Field>
      <Field label={t('checks.outstanding')}>
        <Select value={u.values.live} onChange={(e) => u.set({ live: e.target.value })}>
          <option value="">{t('common.all')}</option>
          <option value="true">{t('checks.liveOnly')}</option>
        </Select>
      </Field>
    </>
  );
}

interface OpenItem { id: number; side: string; kind: string; number: number; date: string; outstanding: string }

/** State and submission of the check form (new page and edit dialog share it). */
export function useCheckForm(type: CheckType, check?: Check) {
  const [v, setV] = useState({
    check_number: check?.check_number ?? '', serial_number: check?.serial_number ?? '', sayadi_id: check?.sayadi_id ?? '',
    bank_id: check?.bank_id ?? (null as number | null), bank_name: check?.bank_name ?? '', branch: check?.branch ?? '',
    account_number: check?.account_number ?? '', account_holder: check?.account_holder ?? '', party_id: check?.party_id ?? (null as number | null),
    counter_account_id: check?.counter_account_id ?? (null as number | null), amount: check?.amount ?? '', issue_date: check?.issue_date ?? todayIso(),
    due_date: check?.due_date ?? todayIso(), received_date: check?.received_date ?? todayIso(), treasury_account_id: check?.treasury_account_id ?? (null as number | null),
    invoice_id: check?.invoice_id ?? (null as number | null), project_id: check?.project_id ?? (null as number | null),
    description: check?.description ?? '', notes: check?.notes ?? '', auto_allocate: false, issue: false,
  });
  const set = <K extends keyof typeof v>(k: K, x: (typeof v)[K]) => setV((s) => ({ ...s, [k]: x }));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const submit = useSubmit(setErrors);
  const save = () => {
    const body = { ...v, check_type: type, amount: v.amount || '0', party_id: v.party_id ?? 0, received_date: type === 'received' ? v.received_date : null };
    return submit.run(() => (check ? api.put<Check>(`/checks/${check.id}`, body) : api.post<Check>('/checks', body)));
  };
  return { v, setV, set, errors, save, pending: submit.pending, error: submit.error };
}

/** The check form, grouped in sections. Booked checks only allow non-financial changes. */
export function CheckFormBody({ type, check, form }: { type: CheckType; check?: Check; form: ReturnType<typeof useCheckForm> }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const banks = useBanks();
  const { v, set, setV, errors } = form;
  const locked = !!check && check.status !== 'draft';
  const received = type === 'received';
  const openItems = useQuery({
    queryKey: ['open-items', v.party_id, type],
    queryFn: () => api.get<OpenItem[]>('/treasury/open-items', { party_id: v.party_id, kind: received ? 'receipt' : 'payment' }),
    enabled: !!v.party_id && !locked,
  });
  return (
    <div className="stack">
      {locked && <Alert kind="info">{t('checks.checkClosedEdit')}</Alert>}
      <fieldset className="form-section">
        <legend>{t('checks.sections.party')}</legend>
        <div className="form-grid">
          <Field label={received ? t('checks.drawer') : t('checks.payee')} required error={errors.party_id} className="span-2">
            {locked ? <Input value={check.party_name} disabled /> : <PartyPicker role={received ? 'customer' : 'supplier'} value={v.party_id} onChange={(x) => setV((s) => ({ ...s, party_id: x, invoice_id: null }))} autoFocus />}
          </Field>
          <Field label={`${t('checks.amount')} (${f.currencyLabel})`} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => set('amount', x)} disabled={locked} /></Field>
        </div>
      </fieldset>
      <fieldset className="form-section">
        <legend>{t('checks.sections.instrument')}</legend>
        <div className="form-grid">
          <Field label={t('checks.checkNumber')} required error={errors.check_number}><Input value={v.check_number} onChange={(e) => set('check_number', e.target.value)} disabled={locked} inputMode="numeric" dir="ltr" /></Field>
          <Field label={t('checks.sayadiId')} help={t('checks.sayadiHelp')} error={errors.sayadi_id}><Input value={v.sayadi_id} onChange={(e) => set('sayadi_id', e.target.value)} inputMode="numeric" dir="ltr" maxLength={20} /></Field>
          <Field label={t('checks.serialNumber')} error={errors.serial_number}><Input value={v.serial_number} onChange={(e) => set('serial_number', e.target.value)} dir="ltr" /></Field>
          {received ? (
            <>
              <Field label={t('checks.bank')} required error={errors.bank_id}>
                <Select value={v.bank_id ?? ''} disabled={locked} onChange={(e) => set('bank_id', e.target.value ? Number(e.target.value) : null)}>
                  <option value="">{t('common.select')}</option>
                  {banks.data?.map((b) => <option key={b.id} value={b.id}>{lang === 'en' ? b.name_en : b.name}</option>)}
                </Select>
              </Field>
              <Field label={t('checks.accountNumber')} error={errors.account_number}><Input value={v.account_number} onChange={(e) => set('account_number', e.target.value)} disabled={locked} dir="ltr" /></Field>
            </>
          ) : (
            <Field label={t('checks.treasuryAccount')} required error={errors.treasury_account_id} className="span-2">
              {locked ? <Input value={check.treasury_account_name ?? ''} disabled /> : <TreasurySelect kind="bank" value={v.treasury_account_id} onChange={(x) => set('treasury_account_id', x)} />}
            </Field>
          )}
          <Field label={t('checks.branch')}><Input value={v.branch} onChange={(e) => set('branch', e.target.value)} /></Field>
          <Field label={t('checks.accountHolder')}><Input value={v.account_holder} onChange={(e) => set('account_holder', e.target.value)} /></Field>
        </div>
      </fieldset>
      <fieldset className="form-section">
        <legend>{t('checks.sections.dates')}</legend>
        <div className="form-grid">
          <Field label={t('checks.issueDate')} required error={errors.issue_date}><DateField value={v.issue_date} onChange={(x) => set('issue_date', x)} disabled={locked} /></Field>
          <Field label={t('checks.dueDate')} required error={errors.due_date}><DateField value={v.due_date} onChange={(x) => set('due_date', x)} /></Field>
          {received && <Field label={t('checks.receivedDate')} required error={errors.received_date}><DateField value={v.received_date} onChange={(x) => set('received_date', x)} disabled={locked} /></Field>}
        </div>
      </fieldset>
      {!locked && (
        <fieldset className="form-section">
          <legend>{t('checks.sections.settlement')}</legend>
          <div className="form-grid">
            <Field label={t('checks.invoice')} error={errors.invoice_id}>
              <Select value={v.invoice_id ?? ''} onChange={(e) => set('invoice_id', e.target.value ? Number(e.target.value) : null)} disabled={!v.party_id}>
                <option value="">{t('common.none')}</option>
                {openItems.data?.filter((o) => o.kind === 'invoice').map((o) => (
                  <option key={o.id} value={o.id}>{`${f.digits(String(o.number))} — ${f.date(o.date)} — ${f.money(o.outstanding)}`}</option>
                ))}
              </Select>
            </Field>
            <Field label={t('checks.project')} error={errors.project_id}><CostCenterSelect kinds={['project']} value={v.project_id} onChange={(x) => set('project_id', x)} /></Field>
            <Field label={t('checks.counterAccount')} help={t('checks.counterHelp')} className="span-2">
              <AccountPicker value={v.counter_account_id} onChange={(x) => set('counter_account_id', x)} filter={(a) => a.type === 'asset' || a.type === 'liability'} />
            </Field>
            {!v.invoice_id && <div className="span-all"><Checkbox label={t('checks.autoAllocate')} checked={v.auto_allocate} onChange={(x) => set('auto_allocate', x)} /></div>}
            {!check && !received && can('checks.post') && <div className="span-all"><Checkbox label={t('checks.issueNow')} checked={v.issue} onChange={(x) => set('issue', x)} /></div>}
          </div>
        </fieldset>
      )}
      <fieldset className="form-section">
        <legend>{t('checks.sections.notes')}</legend>
        <div className="form-grid">
          <Field label={t('common.description')} className="span-all"><Input value={v.description} onChange={(e) => set('description', e.target.value)} /></Field>
          <Field label={t('checks.notes')} className="span-all"><TextArea rows={2} value={v.notes} onChange={(e) => set('notes', e.target.value)} /></Field>
        </div>
      </fieldset>
    </div>
  );
}

/** Edit a check in a dialog. */
export function CheckDialog({ check, onClose, onSaved }: { check: Check; onClose: () => void; onSaved: (c: Check) => void }) {
  const { t } = useI18n();
  const form = useCheckForm(check.check_type, check);
  const save = async () => {
    const c = await form.save();
    if (c) onSaved(c);
  };
  return (
    <Dialog size="wide" title={t('checks.editCheck')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={form.pending} onClick={save}>{t('common.save')}</Button></>}>
      <form onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {form.error && <div className="section-gap"><Alert>{form.error}</Alert></div>}
        <CheckFormBody type={check.check_type} check={check} form={form} />
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

/** Performs a status transition with the inputs the action needs. */
export function TransitionDialog({ check, action, onClose, onDone }: { check: Check; action: string; onClose: () => void; onDone: (c: Check) => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const [v, setV] = useState({ date: todayIso(), treasury_account_id: null as number | null, party_id: null as number | null, replacement_check_id: null as number | null, reason: '' });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const replacements = useQuery({
    queryKey: ['check-replacements', check.party_id],
    queryFn: () => api.get<Page<Check>>('/checks', { check_type: 'issued', party_id: check.party_id, status: 'draft,issued,delivered', page_size: 100 }),
    enabled: action === 'replace',
  });
  const needsReason = ['return', 'lost', 'replace'].includes(action);
  const submit = async () => {
    const c = await run(() => api.post<Check>(`/checks/${check.id}/transitions`, { action, ...v }), t('checks.updated'));
    if (c) onDone(c);
  };
  const negative = negativeActions.includes(action);
  return (
    <Dialog title={`${tryT(`checks.actions.${action}`) ?? action} — ${f.digits(check.check_number)}`} description={tryT(`checks.actionHelp.${action}`)} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant={negative ? 'negative' : 'accent'} pending={pending} onClick={submit}>{tryT(`checks.actions.${action}`) ?? t('common.confirm')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void submit(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="dl-grid">
          <dt>{t('checks.amount')}</dt><dd><Money value={check.amount} currency /></dd>
          <dt>{t('checks.party')}</dt><dd>{check.party_name}</dd>
          <dt>{t('checks.currentStatus')}</dt><dd><StatusBadge status={check.status} /></dd>
        </div>
        <div className="form-grid">
          <Field label={t('checks.date')} required error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          {(action === 'deposit' || action === 'cash') && (
            <Field label={action === 'deposit' ? t('checks.depositAccount') : t('checks.cashAccount')} required error={errors.treasury_account_id} className="span-2">
              <TreasurySelect kind={action === 'deposit' ? 'bank' : undefined} value={v.treasury_account_id} onChange={(x) => setV({ ...v, treasury_account_id: x })} />
            </Field>
          )}
          {action === 'transfer' && (
            <Field label={t('checks.holder')} required error={errors.party_id} className="span-2">
              <PartyPicker value={v.party_id} onChange={(x) => setV({ ...v, party_id: x })} />
            </Field>
          )}
          {action === 'replace' && (
            <Field label={t('checks.replacement')} required error={errors.replacement_check_id} className="span-2">
              <Select value={v.replacement_check_id ?? ''} onChange={(e) => setV({ ...v, replacement_check_id: e.target.value ? Number(e.target.value) : null })}>
                <option value="">{t('common.select')}</option>
                {replacements.data?.items.filter((c) => c.id !== check.id).map((c) => (
                  <option key={c.id} value={c.id}>{`${f.digits(c.check_number)} — ${f.date(c.due_date)} — ${f.money(c.amount)}`}</option>
                ))}
              </Select>
            </Field>
          )}
          <Field label={t('checks.reason')} required={needsReason} error={errors.reason} className="span-all">
            <TextArea rows={2} value={v.reason} onChange={(e) => setV({ ...v, reason: e.target.value })} />
          </Field>
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
