import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { OpenItem, TreasuryTx } from '@/api/types';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, MoneyInput } from '@/components/ui/Field';
import { Alert, Amount } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useSubmit } from '@/lib/useSubmit';
import { add, sub, toBig } from '@/lib/decimal';

/** Settles open invoices with the unallocated part of an existing receipt or payment. */
export function AllocateDialog({ tx, onClose, onSaved }: { tx: TreasuryTx; onClose: () => void; onSaved: () => void }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const [auto, setAuto] = useState(false);
  const [alloc, setAlloc] = useState<Record<number, string>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const free = sub(tx.amount, tx.allocated || '0');
  const open = useQuery({
    queryKey: ['open-items', tx.party_id, tx.kind],
    queryFn: () => api.get<OpenItem[]>('/treasury/open-items', { party_id: tx.party_id, kind: tx.kind }),
  });
  const preview = useMemo(() => {
    if (!auto || !open.data) return alloc;
    let left = toBig(free);
    const out: Record<number, string> = {};
    for (const o of open.data) {
      if (left.lte(0)) break;
      const a = left.lt(o.outstanding) ? left : toBig(o.outstanding);
      out[o.id] = a.toString();
      left = left.minus(a);
    }
    return out;
  }, [auto, open.data, free, alloc]);
  const shown = auto ? preview : alloc;
  const remainder = sub(free, add(...Object.values(shown).map((x) => x || '0')));

  const save = async () => {
    const body = {
      auto_allocate: auto,
      allocations: auto ? [] : Object.entries(alloc).filter(([, a]) => a && !toBig(a).eq(0)).map(([id, a]) => ({ invoice_id: Number(id), amount: a })),
    };
    if (await run(() => api.post(`/treasury/transactions/${tx.id}/allocations`, body).then(() => true), t('common.saved'))) onSaved();
  };

  return (
    <Dialog wide title={t('treasury.allocateExisting')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <p className="muted num">{t('treasury.unallocated', { amount: f.money(free) })}</p>
        <Checkbox label={t('treasury.autoAllocate')} checked={auto} onChange={setAuto} />
        {errors.allocations && <Alert>{t(`validation.${errors.allocations}` as 'validation.invalid')}</Alert>}
        <DataTable<OpenItem>
          rows={open.data}
          loading={open.isLoading}
          rowKey={(o) => o.id}
          empty={<p className="muted" style={{ padding: 16 }}>{t('treasury.noOpenItems')}</p>}
          columns={[
            { key: 'doc', header: t('inventory.document'), cell: (o) => `${tryT(`invoices.singular.${o.side}_${o.kind}`)} ${f.int(o.number)}` },
            { key: 'date', header: t('common.date'), cell: (o) => <span className="num">{f.date(o.date)}</span> },
            { key: 'out', header: t('invoices.outstanding'), align: 'end', cell: (o) => <Amount value={o.outstanding} /> },
            {
              key: 'alloc', header: t('treasury.allocated'), align: 'end', width: 170, cell: (o, i) => (
                <MoneyInput value={shown[o.id] ?? ''} disabled={auto} aria-invalid={!!errors[`allocations.${i}.amount`]}
                  onChange={(x) => setAlloc({ ...alloc, [o.id]: x })} placeholder="0" />
              ),
            },
          ]}
        />
        {!toBig(remainder).eq(0) && (
          <p className={`num ${toBig(remainder).lt(0) ? 'danger-text' : 'muted'}`}>{t('treasury.unallocated', { amount: f.money(remainder) })}</p>
        )}
      </div>
    </Dialog>
  );
}
