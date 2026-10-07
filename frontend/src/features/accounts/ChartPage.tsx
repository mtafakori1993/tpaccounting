import { useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ChevronDown, ChevronRight, Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { Account, AccountType } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select, TextArea } from '@/components/ui/Field';
import { Alert, Badge, BalanceAmount, KV, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { useAccountName } from '@/components/pickers/Pickers';
import { ExportMenu } from '@/components/ExportMenu';
import { useFormState, usePersistentState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';
import { normalizeDigits } from '@/lib/dates';
import { isZero } from '@/lib/decimal';

const TYPES: AccountType[] = ['asset', 'liability', 'equity', 'revenue', 'cost_of_sales', 'expense'];

export function ChartPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const name = useAccountName();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['accounts', 'tree'], queryFn: () => api.get<Account[]>('/accounts', { with_balance: true }) });
  const [filters, setFilters] = usePersistentState('filters.accounts', { q: '', type: '', inactive: false });
  const [expanded, setExpanded] = useState<Set<number> | 'all'>('all');
  const [selected, setSelected] = useState<number | null>(null);
  const [dialog, setDialog] = useState<{ mode: 'create' | 'edit'; parent?: Account; account?: Account } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const del = useSubmit();
  const toggleActive = useSubmit();

  const accounts = q.data ?? [];
  const byParent = useMemo(() => {
    const m = new Map<number | null, Account[]>();
    for (const a of accounts) m.set(a.parent_id, [...(m.get(a.parent_id) ?? []), a]);
    return m;
  }, [accounts]);

  // Visible = matches filters, plus all ancestors of matches.
  const visible = useMemo(() => {
    const s = normalizeDigits(filters.q).toLowerCase().trim();
    const match = (a: Account) =>
      (filters.inactive || a.is_active) &&
      (!filters.type || a.type === filters.type) &&
      (!s || a.code.startsWith(s) || a.name.toLowerCase().includes(s) || a.name_en.toLowerCase().includes(s));
    const ids = new Set<number>();
    const byId = new Map(accounts.map((a) => [a.id, a]));
    for (const a of accounts) {
      if (!match(a)) continue;
      let cur: Account | undefined = a;
      while (cur) {
        ids.add(cur.id);
        cur = cur.parent_id ? byId.get(cur.parent_id) : undefined;
      }
    }
    return ids;
  }, [accounts, filters]);

  const isOpen = (id: number) => expanded === 'all' || expanded.has(id) || !!filters.q;
  const toggle = (id: number) => {
    setExpanded((e) => {
      const next = new Set(e === 'all' ? accounts.filter((a) => a.has_children).map((a) => a.id) : e);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const renderRows = (parent: number | null): React.ReactNode[] =>
    (byParent.get(parent) ?? [])
      .filter((a) => visible.has(a.id))
      .flatMap((a) => [
        <div
          key={a.id}
          className={`tree-row level-${a.level} ${a.is_active ? '' : 'inactive'}`}
          style={{ paddingInlineStart: 8 + (a.level - 1) * 20 }}
          role="treeitem"
          aria-selected={selected === a.id}
          aria-expanded={a.has_children ? isOpen(a.id) : undefined}
          tabIndex={0}
          onClick={() => setSelected(a.id)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') setSelected(a.id);
            if ((e.key === 'ArrowRight' || e.key === 'ArrowLeft') && a.has_children) toggle(a.id);
          }}
        >
          <span className="tw" onClick={(e) => { e.stopPropagation(); if (a.has_children) toggle(a.id); }}>
            {a.has_children ? (isOpen(a.id) ? <ChevronDown size={14} /> : <ChevronRight size={14} className="icon-directional" />) : null}
          </span>
          <span className="code num">{f.digits(a.code)}</span>
          <span>{name(a)}</span>
          {a.balance && !isZero(a.balance) && <span className="bal"><BalanceAmount value={a.balance} /></span>}
        </div>,
        ...(a.has_children && isOpen(a.id) ? renderRows(a.id) : []),
      ]);

  const sel = accounts.find((a) => a.id === selected);
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['accounts'] });
  };

  return (
    <div className="page">
      <PageHeader
        title={t('accounts.title')}
        actions={
          <>
            <ExportMenu
              name="chart-of-accounts"
              permission="accounts.export"
              rows={accounts}
              columns={[
                { header: t('common.code'), value: (a) => a.code },
                { header: t('common.name'), value: (a) => a.name },
                { header: t('common.nameEn'), value: (a) => a.name_en },
                { header: t('common.type'), value: (a) => t(`accountTypes.${a.type}`) },
                { header: t('accounts.level'), value: (a) => t(`levels.${a.level as 1 | 2 | 3 | 4}`) },
                { header: t('common.balance'), value: (a) => a.balance ?? '0', numeric: true },
                { header: t('common.status'), value: (a) => (a.is_active ? t('common.active') : t('common.inactive')) },
              ]}
            />
            {can('accounts.create') && (
              <Button variant="accent" icon={<Plus size={16} />} onClick={() => setDialog({ mode: 'create' })}>{t('accounts.newAccount')}</Button>
            )}
          </>
        }
      />
      <div className="side-layout" style={{ gridTemplateColumns: 'minmax(0, 1fr) 380px' }}>
        <Panel bodyless>
          <div className="toolbar">
            <Field label={t('common.search')} className="search">
              <Input value={filters.q} onChange={(e) => setFilters({ ...filters, q: e.target.value })} placeholder={`${t('common.code')} / ${t('common.name')}`} />
            </Field>
            <Field label={t('common.type')}>
              <Select value={filters.type} onChange={(e) => setFilters({ ...filters, type: e.target.value })}>
                <option value="">{t('common.all')}</option>
                {TYPES.map((x) => <option key={x} value={x}>{t(`accountTypes.${x}`)}</option>)}
              </Select>
            </Field>
            <Checkbox label={t('accounts.showInactive')} checked={filters.inactive} onChange={(v) => setFilters({ ...filters, inactive: v })} />
            <span className="grow" />
            <Button variant="quiet" size="sm" onClick={() => setExpanded('all')}>{t('accounts.expandAll')}</Button>
            <Button variant="quiet" size="sm" onClick={() => setExpanded(new Set())}>{t('accounts.collapseAll')}</Button>
          </div>
          <div role="tree" style={{ padding: 8, maxHeight: 'calc(100vh - 260px)', overflow: 'auto' }}>
            {q.isLoading ? <LoadingBlock /> : renderRows(null)}
          </div>
        </Panel>
        <Panel title={sel ? `${f.digits(sel.code)} — ${name(sel)}` : t('common.details')}>
          {!sel ? (
            <p className="muted">{t('accounts.noSelection')}</p>
          ) : (
            <div className="stack">
              <KV
                items={[
                  { label: t('common.type'), value: t(`accountTypes.${sel.type}`) },
                  { label: t('accounts.level'), value: t(`levels.${sel.level as 1 | 2 | 3 | 4}`) },
                  { label: t('accounts.parent'), value: sel.parent_id ? (() => { const p = accounts.find((a) => a.id === sel.parent_id); return p ? `${p.code} — ${name(p)}` : ''; })() : '—' },
                  { label: t('accounts.balance'), value: <BalanceAmount value={sel.balance} /> },
                  { label: t('common.status'), value: sel.is_active ? <Badge tone="success">{t('common.active')}</Badge> : <Badge>{t('common.inactive')}</Badge> },
                  { label: t('accounts.postable'), value: sel.postable ? t('common.yes') : t('common.no') },
                  { label: t('common.nameEn'), value: sel.name_en },
                  { label: t('common.description'), value: sel.description },
                ]}
              />
              {toggleActive.error && <Alert>{toggleActive.error}</Alert>}
              <div className="row">
                <Link className="btn btn-secondary btn-sm" to={`/reports/ledger?account_id=${sel.id}`}>{t('accounts.ledger')}</Link>
                {can('accounts.edit') && <Button size="sm" onClick={() => setDialog({ mode: 'edit', account: sel })}>{t('common.edit')}</Button>}
                {can('accounts.create') && sel.level < 4 && sel.is_active && (
                  <Button size="sm" icon={<Plus size={14} />} onClick={() => setDialog({ mode: 'create', parent: sel })}>{t('accounts.newChild')}</Button>
                )}
                {can('accounts.edit') && (
                  <Button size="sm" variant="quiet" pending={toggleActive.pending} onClick={() =>
                    toggleActive.run(() => api.put(`/accounts/${sel.id}`, { ...sel, is_active: !sel.is_active }).then(refresh), t('common.saved'))
                  }>
                    {sel.is_active ? t('common.disable') : t('common.enable')}
                  </Button>
                )}
                {can('accounts.delete') && !sel.has_children && (
                  <Button size="sm" variant="quiet" className="danger-text" onClick={() => setConfirmDelete(true)}>{t('common.delete')}</Button>
                )}
              </div>
            </div>
          )}
        </Panel>
      </div>
      {dialog && (
        <AccountDialog
          {...dialog}
          onClose={() => setDialog(null)}
          onSaved={(a) => {
            setDialog(null);
            setSelected(a.id);
            refresh();
          }}
        />
      )}
      {confirmDelete && sel && (
        <ConfirmDialog
          title={t('common.delete')}
          body={t('common.confirmDelete')}
          confirmLabel={t('common.yesDelete')}
          negative
          pending={del.pending}
          error={del.error}
          onClose={() => setConfirmDelete(false)}
          onConfirm={async () => {
            const ok = await del.run(() => api.del(`/accounts/${sel.id}`).then(() => true), t('common.deleted'));
            if (ok) {
              setConfirmDelete(false);
              setSelected(null);
              refresh();
            }
          }}
        />
      )}
    </div>
  );
}

function AccountDialog({ mode, parent, account, onClose, onSaved }: { mode: 'create' | 'edit'; parent?: Account; account?: Account; onClose: () => void; onSaved: (a: Account) => void }) {
  const { t } = useI18n();
  const { values: v, set, errors, setErrors } = useFormState({
    code: account?.code ?? '',
    name: account?.name ?? '',
    name_en: account?.name_en ?? '',
    type: (account?.type ?? parent?.type ?? 'asset') as AccountType,
    description: account?.description ?? '',
  });
  const { run, pending, error } = useSubmit(setErrors);
  useQuery({
    queryKey: ['next-code', parent?.id ?? 0],
    queryFn: async () => {
      const r = await api.get<{ code: string }>('/accounts/next-code', { parent_id: parent?.id });
      if (!v.code) set('code', r.code);
      return r;
    },
    enabled: mode === 'create',
  });
  const isRoot = mode === 'create' ? !parent : account?.level === 1;
  const save = async () => {
    const body = { ...v, parent_id: parent?.id ?? null, is_active: account?.is_active ?? true };
    const a = await run(() => (mode === 'create' ? api.post<Account>('/accounts', body) : api.put<Account>(`/accounts/${account!.id}`, body)), t('common.saved'));
    if (a) onSaved(a);
  };
  return (
    <Dialog
      title={mode === 'create' ? (parent ? t('accounts.newChild') : t('accounts.newAccount')) : t('accounts.editAccount')}
      onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}
    >
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        {parent && <p className="muted">{t('accounts.parent')}: {parent.code} — {parent.name}</p>}
        <div className="form-grid">
          <Field label={t('common.code')} required error={errors.code} help={parent ? t('accounts.codeHelp', { code: parent.code }) : undefined}>
            <Input value={v.code} onChange={(e) => set('code', normalizeDigits(e.target.value))} inputMode="numeric" className="num" />
          </Field>
          <Field label={t('common.type')} error={errors.type} help={!isRoot ? t('accounts.typeInherited') : undefined}>
            <Select value={v.type} disabled={!isRoot} onChange={(e) => set('type', e.target.value as AccountType)}>
              {TYPES.map((x) => <option key={x} value={x}>{t(`accountTypes.${x}`)}</option>)}
            </Select>
          </Field>
          <Field label={t('common.name')} required error={errors.name} className="span-all"><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label={t('common.nameEn')} error={errors.name_en} className="span-all"><Input value={v.name_en} onChange={(e) => set('name_en', e.target.value)} dir="ltr" /></Field>
          <Field label={t('common.description')} className="span-all"><TextArea value={v.description} onChange={(e) => set('description', e.target.value)} /></Field>
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
