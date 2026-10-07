import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { OpenItem, TreasuryTx } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Amount, Tabs } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { AccountPicker, CostCenterSelect, PartyPicker, TreasurySelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { add, cmp, sub, toBig } from '@/lib/decimal';
import { todayIso } from '@/lib/dates';

export type TxKind = 'receipt' | 'payment' | 'transfer';

export interface TxDefaults {
  party?: number;
  account?: number;
  invoice?: number;
  amount?: string;
}

export function TransactionDialog({ kind, defaults, onClose, onSaved }: { kind: TxKind; defaults?: TxDefaults; onClose: () => void; onSaved: (tx: TreasuryTx) => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const [mode, setMode] = useState<'party' | 'account'>('party');
  const [v, setV] = useState({
    date: todayIso(),
    treasury_account_id: defaults?.account ?? (null as number | null),
    to_treasury_account_id: null as number | null,
    party_id: defaults?.party ?? (null as number | null),
    counter_account_id: null as number | null,
    cost_center_id: null as number | null,
    amount: defaults?.amount ?? '',
    reference: '',
    description: '',
  });
  const [auto, setAuto] = useState(!defaults?.invoice);
  const [alloc, setAlloc] = useState<Record<number, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);

  const open = useQuery({
    queryKey: ['open-items', v.party_id, kind],
    queryFn: () => api.get<OpenItem[]>('/treasury/open-items', { party_id: v.party_id, kind }),
    enabled: kind !== 'transfer' && mode === 'party' && !!v.party_id,
  });

  // Pre-select the invoice that launched the dialog.
  useEffect(() => {
    if (!defaults?.invoice || !open.data) return;
    const it = open.data.find((o) => o.id === defaults.invoice);
    if (it) setAlloc({ [it.id]: cmp(it.outstanding, defaults.amount ?? it.outstanding) < 0 ? it.outstanding : defaults.amount ?? it.outstanding });
  }, [open.data, defaults?.invoice, defaults?.amount]);

  // With automatic allocation, preview the oldest-first distribution.
  const preview = useMemo(() => {
    if (!auto || !open.data) return alloc;
    let left = toBig(v.amount || '0');
    const out: Record<number, string> = {};
    for (const o of open.data) {
      if (left.lte(0)) break;
      const a = left.lt(o.outstanding) ? left : toBig(o.outstanding);
      out[o.id] = a.toString();
      left = left.minus(a);
    }
    return out;
  }, [auto, open.data, v.amount, alloc]);
  const shown = auto ? preview : alloc;
  const remainder = sub(v.amount || '0', add(...Object.values(shown).map((x) => x || '0')));

  const save = async () => {
    const body = {
      kind,
      ...v,
      party_id: kind !== 'transfer' && mode === 'party' ? v.party_id : null,
      counter_account_id: kind !== 'transfer' && mode === 'account' ? v.counter_account_id : null,
      to_treasury_account_id: kind === 'transfer' ? v.to_treasury_account_id : null,
      amount: v.amount || '0',
      auto_allocate: auto,
      allocations: auto ? [] : Object.entries(alloc).filter(([, a]) => a && !toBig(a).eq(0)).map(([id, a]) => ({ invoice_id: Number(id), amount: a })),
    };
    const tx = await run(() => api.post<TreasuryTx>('/treasury/transactions', body), t('common.saved'));
    if (tx) onSaved(tx);
  };

  const title = kind === 'receipt' ? t('treasury.newReceipt') : kind === 'payment' ? t('treasury.newPayment') : t('treasury.newTransfer');

  return (
    <Dialog wide title={title} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.date')} required error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          <Field label={kind === 'transfer' ? t('treasury.fromAccount') : t('treasury.treasuryAccount')} required error={errors.treasury_account_id}>
            <TreasurySelect value={v.treasury_account_id} onChange={(x) => setV({ ...v, treasury_account_id: x })} />
          </Field>
          {kind === 'transfer' && (
            <Field label={t('treasury.toAccount')} required error={errors.to_treasury_account_id}>
              <TreasurySelect value={v.to_treasury_account_id} onChange={(x) => setV({ ...v, to_treasury_account_id: x })} />
            </Field>
          )}
          <Field label={t('common.amount')} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => setV({ ...v, amount: x })} /></Field>
        </div>
        {kind !== 'transfer' && (
          <>
            <Tabs value={mode} onChange={setMode} tabs={[{ key: 'party', label: t('treasury.payee') }, { key: 'account', label: t('treasury.counterAccount') }]} />
            <div className="form-grid">
              {mode === 'party' ? (
                <Field label={t('treasury.payee')} required error={errors.party_id} className="span-2">
                  <PartyPicker value={v.party_id} onChange={(x) => { setV({ ...v, party_id: x }); setAlloc({}); }} autoFocus={!v.party_id} />
                </Field>
              ) : (
                <Field label={t('journal.account')} required error={errors.party_id || errors.counter_account_id} help={t('treasury.counterHelp')} className="span-2">
                  <AccountPicker value={v.counter_account_id} onChange={(x) => setV({ ...v, counter_account_id: x })} />
                </Field>
              )}
              <Field label={t('journal.costCenter')}><CostCenterSelect value={v.cost_center_id} onChange={(x) => setV({ ...v, cost_center_id: x })} /></Field>
            </div>
          </>
        )}
        <div className="form-grid">
          <Field label={t('common.reference')}><Input value={v.reference} onChange={(e) => setV({ ...v, reference: e.target.value })} /></Field>
          <Field label={t('common.description')} className="span-2"><Input value={v.description} onChange={(e) => setV({ ...v, description: e.target.value })} /></Field>
        </div>
        {kind !== 'transfer' && mode === 'party' && v.party_id && (
          <div className="panel">
            <div className="panel-head">
              <h3 className="panel-title">{t('treasury.allocations')}</h3>
              <Checkbox label={t('treasury.autoAllocate')} checked={auto} onChange={setAuto} />
            </div>
            {errors.allocations && <div style={{ padding: '8px 16px' }}><Alert>{t(`validation.${errors.allocations}` as 'validation.invalid')}</Alert></div>}
            <DataTable<OpenItem>
              rows={open.data}
              loading={open.isLoading}
              rowKey={(o) => o.id}
              empty={<p className="muted" style={{ padding: 16 }}>{t('treasury.noOpenItems')}</p>}
              columns={[
                { key: 'doc', header: t('inventory.document'), cell: (o) => `${tryT(`invoices.singular.${o.side}_${o.kind}`)} ${f.int(o.number)}` },
                { key: 'date', header: t('common.date'), cell: (o) => <span className="num">{f.date(o.date)}</span> },
                { key: 'due', header: t('invoices.dueDate'), cell: (o) => <span className="num">{f.date(o.due_date)}</span> },
                { key: 'out', header: t('invoices.outstanding'), align: 'end', cell: (o) => <Amount value={o.outstanding} /> },
                {
                  key: 'alloc', header: t('treasury.allocated'), align: 'end', width: 170, cell: (o, i) => (
                    <MoneyInput
                      value={shown[o.id] ?? ''}
                      disabled={auto}
                      aria-invalid={!!errors[`allocations.${i}.amount`]}
                      onChange={(x) => setAlloc({ ...alloc, [o.id]: x })}
                      placeholder="0"
                    />
                  ),
                },
              ]}
            />
            {!toBig(remainder).eq(0) && (
              <p className={`num ${toBig(remainder).lt(0) ? 'danger-text' : 'muted'}`} style={{ padding: '8px 16px' }}>
                {t('treasury.unallocated', { amount: f.money(remainder) })}
              </p>
            )}
          </div>
        )}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
