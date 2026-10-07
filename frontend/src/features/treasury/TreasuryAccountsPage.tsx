import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { Landmark, Wallet } from 'lucide-react';
import { api } from '@/api/client';
import { useTreasuryAccounts } from '@/api/queries';
import type { TreasuryAccount } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { ActionMenu, Alert, Amount, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useFormState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';
import { add } from '@/lib/decimal';
import { normalizeDigits, todayIso } from '@/lib/dates';

export function TreasuryAccountsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const q = useTreasuryAccounts();
  const [editing, setEditing] = useState<{ kind: 'cash' | 'bank'; account?: TreasuryAccount } | null>(null);
  const section = (kind: 'cash' | 'bank') => {
    const rows = q.data?.filter((a) => a.kind === kind);
    return (
      <Panel bodyless title={kind === 'cash' ? t('treasury.cash') : t('treasury.bank')}
        actions={can('treasury.create') && <Button size="sm" onClick={() => setEditing({ kind })}>{kind === 'cash' ? t('treasury.newCash') : t('treasury.newBank')}</Button>}>
        <DataTable
          rows={rows}
          loading={q.isLoading}
          rowKey={(a) => a.id}
          showFooter
          onRowClick={(a) => navigate(`/reports/ledger?account_id=${a.account_id}`)}
          rowClassName={(a) => (a.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'name', header: t('common.name'), cell: (a) => <>{a.name} {!a.is_active && <Badge>{t('common.inactive')}</Badge>}</> },
            ...(kind === 'bank' ? [
              { key: 'bank', header: t('treasury.bankName'), cell: (a: TreasuryAccount) => a.bank_name },
              { key: 'acc', header: t('treasury.accountNumber'), cell: (a: TreasuryAccount) => <span className="num" dir="ltr">{f.digits(a.account_number)}</span> },
            ] : []),
            { key: 'gl', header: t('treasury.ledgerAccount'), cell: (a) => <span className="num muted">{f.digits(a.account_code)}</span>, footer: t('common.total') },
            { key: 'balance', header: t('common.balance'), align: 'end', cell: (a) => <Amount value={a.balance} signed />, footer: <Amount value={add(...(rows ?? []).map((a) => a.balance))} /> },
            {
              key: 'x', header: '', width: 50, cell: (a) => (
                <span onClick={(e) => e.stopPropagation()}>
                  <ActionMenu items={[
                    { label: t('treasury.statement'), to: `/reports/ledger?account_id=${a.account_id}` },
                    { label: t('treasury.newReceipt'), to: `/treasury/transactions?new=receipt&account=${a.id}`, hidden: !can('treasury.create') },
                    { label: t('treasury.newPayment'), to: `/treasury/transactions?new=payment&account=${a.id}`, hidden: !can('treasury.create') },
                    { label: t('common.edit'), onClick: () => setEditing({ kind, account: a }), hidden: !can('treasury.edit') },
                  ]} />
                </span>
              ),
            },
          ]}
        />
      </Panel>
    );
  };
  return (
    <div className="page">
      <PageHeader title={t('treasury.accountsTitle')} actions={can('treasury.create') && <>
        <Button icon={<Wallet size={16} />} onClick={() => setEditing({ kind: 'cash' })}>{t('treasury.newCash')}</Button>
        <Button variant="accent" icon={<Landmark size={16} />} onClick={() => setEditing({ kind: 'bank' })}>{t('treasury.newBank')}</Button>
      </>} />
      <div className="stack">
        {section('bank')}
        {section('cash')}
      </div>
      {editing && <TreasuryDialog {...editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function TreasuryDialog({ kind, account, onClose }: { kind: 'cash' | 'bank'; account?: TreasuryAccount; onClose: () => void }) {
  const { t } = useI18n();
  const qc = useQueryClient();
  const { values: v, set, errors, setErrors } = useFormState({
    kind, name: account?.name ?? '', bank_name: account?.bank_name ?? '', branch: account?.branch ?? '', account_number: account?.account_number ?? '',
    iban: account?.iban ?? '', card_number: account?.card_number ?? '', account_holder: account?.account_holder ?? '',
    opening_balance: account?.opening_balance ?? '', opening_date: account?.opening_date ?? todayIso(), description: account?.description ?? '',
    is_active: account?.is_active ?? true,
  });
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const body = { ...v, opening_balance: v.opening_balance || '0', opening_date: v.opening_date || null };
    const ok = await run(() => (account ? api.put(`/treasury/accounts/${account.id}`, body) : api.post('/treasury/accounts', body)).then(() => true), t('common.saved'));
    if (ok) {
      void qc.invalidateQueries({ queryKey: ['treasury-accounts'] });
      void qc.invalidateQueries({ queryKey: ['accounts'] });
      onClose();
    }
  };
  const dg = (k: 'account_number' | 'card_number') => (e: React.ChangeEvent<HTMLInputElement>) => set(k, normalizeDigits(e.target.value));
  return (
    <Dialog wide title={account ? t('common.edit') : kind === 'cash' ? t('treasury.newCash') : t('treasury.newBank')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.name')} required error={errors.name} className="span-2"><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          {kind === 'bank' && <>
            <Field label={t('treasury.bankName')} required error={errors.bank_name}><Input value={v.bank_name} onChange={(e) => set('bank_name', e.target.value)} /></Field>
            <Field label={t('treasury.branch')}><Input value={v.branch} onChange={(e) => set('branch', e.target.value)} /></Field>
            <Field label={t('treasury.accountNumber')}><Input value={v.account_number} onChange={dg('account_number')} dir="ltr" /></Field>
            <Field label={t('treasury.iban')} error={errors.iban}><Input value={v.iban} onChange={(e) => set('iban', normalizeDigits(e.target.value).toUpperCase())} dir="ltr" placeholder="IR…" /></Field>
            <Field label={t('treasury.cardNumber')} error={errors.card_number}><Input value={v.card_number} onChange={dg('card_number')} dir="ltr" /></Field>
            <Field label={t('treasury.accountHolder')}><Input value={v.account_holder} onChange={(e) => set('account_holder', e.target.value)} /></Field>
          </>}
          {!account && <>
            <Field label={t('treasury.openingBalance')} error={errors.opening_balance} help={t('treasury.openingHelp')}><MoneyInput value={v.opening_balance} onChange={(x) => set('opening_balance', x)} /></Field>
            <Field label={t('treasury.openingDate')}><DateField value={v.opening_date} onChange={(x) => set('opening_date', x)} /></Field>
          </>}
          <Field label={t('common.description')} className="span-all"><Input value={v.description} onChange={(e) => set('description', e.target.value)} /></Field>
          {account && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} />}
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
