import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { Plus, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import type { StockDocument } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { Field, Input, MoneyInput, NumericInput } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { AccountPicker, ProductPicker, WarehouseSelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { todayIso } from '@/lib/dates';
import { gridEnter } from '@/lib/gridNav';
import { stockKinds } from './StockDocsPage';

interface Line { key: number; product_id: number | null; product_label: string; quantity: string; unit_cost: string; description: string }
let seq = 0;
const newLine = (): Line => ({ key: ++seq, product_id: null, product_label: '', quantity: '', unit_cost: '', description: '' });

export function StockDocFormPage() {
  const { id } = useParams();
  const [sp] = useSearchParams();
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const gridRef = useRef<HTMLDivElement>(null);
  const existing = useQuery({ queryKey: ['stock-doc', Number(id)], queryFn: () => api.get<StockDocument>(`/inventory/documents/${id}`), enabled: !!id });
  const kindParam = sp.get('kind');
  const [kind, setKind] = useState<StockDocument['kind']>(stockKinds.includes(kindParam as never) ? (kindParam as StockDocument['kind']) : 'receipt');
  const [h, setH] = useState({ date: todayIso(), warehouse_id: null as number | null, to_warehouse_id: null as number | null, counter_account_id: null as number | null, description: '', reference: '' });
  const [lines, setLines] = useState<Line[]>([newLine()]);
  const [loaded, setLoaded] = useState(!id);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);

  useEffect(() => {
    const d = existing.data;
    if (!d || loaded) return;
    setKind(d.kind);
    setH({ date: d.date, warehouse_id: d.warehouse_id, to_warehouse_id: d.to_warehouse_id, counter_account_id: d.counter_account_id, description: d.description, reference: d.reference });
    setLines((d.lines ?? []).map((l) => ({ key: ++seq, product_id: l.product_id, product_label: l.product_name ?? '', quantity: l.quantity, unit_cost: l.unit_cost === '0' ? '' : l.unit_cost, description: l.description })));
    setLoaded(true);
  }, [existing.data, loaded]);

  const update = (key: number, p: Partial<Line>) => setLines((ls) => ls.map((l) => (l.key === key ? { ...l, ...p } : l)));
  const addLine = () => {
    const n = newLine();
    setLines((ls) => [...ls, n]);
    setTimeout(() => gridRef.current?.querySelector<HTMLInputElement>(`[data-line="${n.key}"] input`)?.focus(), 30);
  };
  const save = async (post: boolean) => {
    const body = {
      kind, ...h, post,
      lines: lines.filter((l) => l.product_id).map((l) => ({ product_id: l.product_id, quantity: l.quantity || '0', unit_cost: l.unit_cost || '0', description: l.description })),
    };
    const d = await run(() => (id ? api.put<StockDocument>(`/inventory/documents/${id}`, body) : api.post<StockDocument>('/inventory/documents', body)), post ? t('common.postedToast') : t('common.saved'));
    if (d) {
      void qc.invalidateQueries({ queryKey: ['stock-docs'] });
      void qc.invalidateQueries({ queryKey: ['stock'] });
      navigate(`/inventory/documents/${d.id}`);
    }
  };
  if (!loaded) return <LoadingBlock />;
  const showCost = kind === 'receipt' || kind === 'adjustment';
  const showCounter = kind !== 'transfer';
  const title = t(`inventory.kinds.${kind}`);

  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('inventory.documentsTitle'), to: '/inventory/documents' }]}
        title={id ? `${t('common.edit')} — ${title}` : t('inventory.newDocument', { kind: title })}
        actions={<>
          <Button onClick={() => navigate(-1)}>{t('common.cancel')}</Button>
          <Button pending={pending} onClick={() => save(false)}>{t('common.saveDraft')}</Button>
          {can('inventory.post') && <Button variant="accent" pending={pending} onClick={() => save(true)}>{t('common.saveAndPost')}</Button>}
        </>}
      />
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <Panel>
          <div className="form-grid">
            <Field label={t('common.date')} required error={errors.date}><DateField value={h.date} onChange={(v) => setH({ ...h, date: v })} /></Field>
            <Field label={kind === 'transfer' ? t('treasury.fromAccount') : t('inventory.warehouse')} required error={errors.warehouse_id}><WarehouseSelect value={h.warehouse_id} onChange={(w) => setH({ ...h, warehouse_id: w })} /></Field>
            {kind === 'transfer' && <Field label={t('inventory.toWarehouse')} required error={errors.to_warehouse_id}><WarehouseSelect value={h.to_warehouse_id} onChange={(w) => setH({ ...h, to_warehouse_id: w })} /></Field>}
            {showCounter && <Field label={t('inventory.counterAccount')} help={t('inventory.counterAccountHelp')}><AccountPicker value={h.counter_account_id} onChange={(a) => setH({ ...h, counter_account_id: a })} /></Field>}
            <Field label={t('common.reference')}><Input value={h.reference} onChange={(e) => setH({ ...h, reference: e.target.value })} /></Field>
            <Field label={t('common.description')} className="span-all"><Input value={h.description} onChange={(e) => setH({ ...h, description: e.target.value })} /></Field>
          </div>
          {kind === 'adjustment' && <p className="field-help" style={{ marginTop: 12 }}>{t('inventory.adjustmentHelp')}</p>}
        </Panel>
        <section className="panel entry-grid">
          <div className="panel-head">
            <h2 className="panel-title">{t('invoices.items')}</h2>
            <Button size="sm" icon={<Plus size={14} />} onClick={addLine}>{t('invoices.addItem')}</Button>
          </div>
          {errors.lines && <div style={{ padding: '8px 20px' }}><Alert>{t('invoices.noItems')}</Alert></div>}
          <div className="table-wrap" ref={gridRef} onKeyDown={gridEnter(gridRef, addLine)}>
            <table className="table">
              <thead><tr>
                <th className="align-center">#</th>
                <th style={{ minWidth: 240 }}>{t('invoices.item')}</th>
                <th className="align-end" style={{ width: 130 }}>{t('inventory.quantity')}</th>
                {showCost && <th className="align-end" style={{ width: 160 }} title={t('inventory.costHelp')}>{t('inventory.unitCost')}</th>}
                <th>{t('common.description')}</th>
                <th style={{ width: 40 }} />
              </tr></thead>
              <tbody>
                {lines.map((l, i) => (
                  <tr key={l.key} data-line={l.key}>
                    <td className="line-no num">{f.int(i + 1)}</td>
                    <td data-invalid={!!errors[`lines.${i}.product_id`]}><ProductPicker value={l.product_id} valueLabel={l.product_label} onChange={(pid, p) => update(l.key, { product_id: pid, product_label: p?.name ?? '', unit_cost: l.unit_cost || (kind === 'receipt' ? p?.purchase_price ?? '' : '') })} /></td>
                    <td data-invalid={!!errors[`lines.${i}.quantity`]}><NumericInput fraction={3} allowNegative={kind === 'adjustment'} value={l.quantity} onChange={(v) => update(l.key, { quantity: v })} /></td>
                    {showCost && <td><MoneyInput value={l.unit_cost} onChange={(v) => update(l.key, { unit_cost: v })} placeholder="0" /></td>}
                    <td><Input value={l.description} onChange={(e) => update(l.key, { description: e.target.value })} /></td>
                    <td><IconButton size="sm" tabIndex={-1} label={t('common.remove')} icon={<Trash2 size={14} />} disabled={lines.length <= 1} onClick={() => setLines((ls) => ls.filter((x) => x.key !== l.key))} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  );
}
