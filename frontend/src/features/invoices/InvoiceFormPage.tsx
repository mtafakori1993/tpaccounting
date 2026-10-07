import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { Plus, Trash2, UserPlus } from 'lucide-react';
import { api } from '@/api/client';
import type { Invoice, Page, Product } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { AmountInput, Field, Input, MoneyInput, NumericInput, PercentageInput, Select, TextArea } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { CostCenterSelect, PartyPicker, ProductPicker, WarehouseSelect } from '@/components/pickers/Pickers';
import { PartyDialog } from '@/features/parties/PartyDialog';
import { useSubmit } from '@/lib/useSubmit';
import { calcLine, parseDiscount, sumLines } from '@/lib/decimal';
import { todayIso } from '@/lib/dates';
import { gridEnter } from '@/lib/gridNav';
import { docInfo, type DocInfo } from './docs';

interface Item {
  key: number;
  product_id: number | null;
  product_label: string;
  track: boolean;
  description: string;
  quantity: string;
  unit: string;
  unit_price: string;
  discount: string; // amount or "10%"
  tax_rate: string;
}

let seq = 0;
const newItem = (): Item => ({ key: ++seq, product_id: null, product_label: '', track: false, description: '', quantity: '1', unit: '', unit_price: '', discount: '', tax_rate: '0' });

function itemsFrom(inv: Invoice): Item[] {
  return (inv.items ?? []).map((it) => ({
    key: ++seq,
    product_id: it.product_id,
    product_label: it.product_name ?? '',
    track: !!it.track_inventory,
    description: it.description,
    quantity: it.quantity,
    unit: it.unit,
    unit_price: it.unit_price,
    discount: it.discount === '0' ? '' : it.discount,
    tax_rate: it.tax_rate,
  }));
}

export function InvoiceFormPage() {
  const { side, kind } = useParams();
  const info = docInfo(side, kind);
  if (!info) return <Navigate to="/" replace />;
  return <InvoiceForm key={info.key} info={info} />;
}

function InvoiceForm({ info }: { info: DocInfo }) {
  const { id } = useParams();
  const [search] = useSearchParams();
  const copyId = search.get('copy');
  const returnOf = search.get('return_of');
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const gridRef = useRef<HTMLDivElement>(null);
  const doc = t(`invoices.singular.${info.key}`);

  const sourceId = id ?? copyId ?? returnOf;
  const sourceApi = returnOf ? `/${info.routeSide}/invoices/${returnOf}` : `${info.api}/${sourceId}`;
  const source = useQuery({ queryKey: ['invoice-source', sourceApi], queryFn: () => api.get<Invoice>(sourceApi), enabled: !!sourceId });

  const [h, setH] = useState({
    number: '',
    date: todayIso(),
    due_date: '',
    party_id: search.get('party') ? Number(search.get('party')) : (null as number | null),
    warehouse_id: null as number | null,
    cost_center_id: null as number | null,
    related_invoice_id: null as number | null,
    reference: '',
    notes: '',
  });
  const [items, setItems] = useState<Item[]>([newItem()]);
  const [loaded, setLoaded] = useState(!sourceId);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [newParty, setNewParty] = useState(false);
  const { run, pending, error } = useSubmit(setErrors);

  useEffect(() => {
    const s = source.data;
    if (!s || loaded) return;
    setItems(itemsFrom(s).length ? itemsFrom(s) : [newItem()]);
    setH({
      number: id ? String(s.number) : '',
      date: id ? s.date : todayIso(),
      due_date: id ? s.due_date ?? '' : '',
      party_id: s.party_id,
      warehouse_id: s.warehouse_id,
      cost_center_id: s.cost_center_id,
      related_invoice_id: returnOf ? s.id : id ? s.related_invoice_id : null,
      reference: id ? s.reference : '',
      notes: s.notes,
    });
    setLoaded(true);
  }, [source.data, loaded, id, returnOf]);

  // Posted invoices of the party, for linking a return to its original.
  const related = useQuery({
    queryKey: ['related-invoices', info.side, h.party_id],
    queryFn: () => api.get<Page<Invoice>>(`/${info.routeSide}/invoices`, { party_id: h.party_id, status: 'posted', page_size: 100, sort: 'date', order: 'desc' }),
    enabled: info.kind === 'return' && !!h.party_id,
  });

  const calcs = useMemo(
    () => items.map((it) => calcLine(it.quantity || '0', it.unit_price || '0', parseDiscount(it.discount, it.quantity || '0', it.unit_price || '0', f.decimals), it.tax_rate || '0', f.decimals)),
    [items, f.decimals],
  );
  const totals = sumLines(calcs);
  const hasStock = items.some((it) => it.track);

  const update = (key: number, patch: Partial<Item>) => setItems((xs) => xs.map((x) => (x.key === key ? { ...x, ...patch } : x)));
  const addItem = () => {
    const n = newItem();
    setItems((xs) => [...xs, n]);
    setTimeout(() => gridRef.current?.querySelector<HTMLInputElement>(`[data-item="${n.key}"] input`)?.focus(), 30);
  };
  const pickProduct = async (it: Item, pid: number | null, p?: Product) => {
    if (!pid) {
      update(it.key, { product_id: null, product_label: '', track: false });
      return;
    }
    const prod = p ?? (await api.get<Product>(`/products/${pid}`));
    update(it.key, {
      product_id: pid,
      product_label: f.locale.startsWith('en') && prod.name_en ? prod.name_en : prod.name,
      track: prod.track_inventory,
      unit: prod.unit,
      unit_price: info.side === 'sales' ? prod.sales_price : prod.purchase_price,
      tax_rate: prod.tax_rate,
    });
  };

  const save = async (post: boolean) => {
    const body = {
      number: h.number ? Number(h.number) : 0,
      date: h.date,
      due_date: h.due_date || null,
      party_id: h.party_id ?? 0,
      warehouse_id: h.warehouse_id,
      cost_center_id: h.cost_center_id,
      related_invoice_id: h.related_invoice_id,
      reference: h.reference,
      notes: h.notes,
      post,
      items: items
        .filter((it) => it.product_id || it.description.trim() || it.unit_price)
        .map((it) => ({
          product_id: it.product_id,
          description: it.description,
          quantity: it.quantity || '0',
          unit: it.unit,
          unit_price: it.unit_price || '0',
          discount: parseDiscount(it.discount, it.quantity || '0', it.unit_price || '0', f.decimals),
          tax_rate: it.tax_rate || '0',
        })),
    };
    const saved = await run(() => (id ? api.put<Invoice>(`${info.api}/${id}`, body) : api.post<Invoice>(info.api, body)), post ? t('common.postedToast') : t('common.saved'));
    if (saved) {
      void qc.invalidateQueries({ queryKey: [`invoices-${info.key}`] });
      void qc.invalidateQueries({ queryKey: ['dashboard'] });
      void qc.invalidateQueries({ queryKey: ['invoice'] });
      navigate(`${info.base}/${saved.id}`);
    }
  };

  const onPageKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      void save(can(`${info.module}.post`));
    } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      void save(false);
    } else if (e.altKey && e.key.toLowerCase() === 'n') {
      e.preventDefault();
      addItem();
    }
  };

  if (sourceId && !loaded) return source.error ? <div className="page"><Alert>{t('errors.not_found', { entity: t('entities.invoice') })}</Alert></div> : <LoadingBlock />;
  if (id && source.data && source.data.status !== 'draft') return <div className="page"><Alert>{t('errors.not_draft')}</Alert></div>;

  const err = (i: number, field: string) => errors[`items.${i}.${field}`];
  const isQuote = info.kind === 'quotation';

  return (
    <div className="page" onKeyDown={onPageKey}>
      <PageHeader
        crumbs={[{ label: t(`invoices.titles.${info.key}`), to: info.base }]}
        title={id ? t('invoices.editDoc', { doc }) : t('invoices.newDoc', { doc })}
        actions={
          <>
            <Button onClick={() => navigate(-1)}>{t('common.cancel')}</Button>
            <Button onClick={() => save(false)} pending={pending}>{t('common.saveDraft')}</Button>
            {can(`${info.module}.post`) && <Button variant="accent" onClick={() => save(true)} pending={pending}>{t('common.saveAndPost')}</Button>}
          </>
        }
      />
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <Panel>
          <div className="form-grid">
            <Field label={info.side === 'sales' ? t('invoices.customer') : t('invoices.supplier')} required error={errors.party_id} className="span-2">
              <div className="row" style={{ flexWrap: 'nowrap' }}>
                <div className="grow">
                  <PartyPicker value={h.party_id} role={info.partyRole} onChange={(pid) => setH({ ...h, party_id: pid, related_invoice_id: null })} autoFocus={!sourceId && !h.party_id} />
                </div>
                {can('parties.create') && <IconButton label={t('parties.newParty')} icon={<UserPlus size={16} />} onClick={() => setNewParty(true)} />}
              </div>
            </Field>
            <Field label={t('common.date')} required error={errors.date}><DateField value={h.date} onChange={(v) => setH({ ...h, date: v })} /></Field>
            {info.kind === 'invoice' && <Field label={t('invoices.dueDate')} error={errors.due_date}><DateField value={h.due_date} onChange={(v) => setH({ ...h, due_date: v })} /></Field>}
            <Field label={t('common.number')} error={errors.number} help={!id ? t('journal.autoNumber') : undefined}>
              <Input value={h.number} onChange={(e) => setH({ ...h, number: e.target.value.replace(/\D/g, '') })} className="num" inputMode="numeric" />
            </Field>
            {info.kind === 'return' && (
              <Field label={t('invoices.relatedInvoice')} error={errors.related_invoice_id}>
                <Select value={h.related_invoice_id ?? ''} onChange={(e) => setH({ ...h, related_invoice_id: e.target.value ? Number(e.target.value) : null })}>
                  <option value="">{t('common.none')}</option>
                  {related.data?.items.map((r) => <option key={r.id} value={r.id}>{f.int(r.number)} — {f.date(r.date)} — {f.money(r.total)}</option>)}
                </Select>
              </Field>
            )}
            {!isQuote && (
              <Field label={t('invoices.warehouse')} error={errors.warehouse_id} help={hasStock ? t('invoices.stockNote') : undefined}>
                <WarehouseSelect value={h.warehouse_id} onChange={(w) => setH({ ...h, warehouse_id: w })} />
              </Field>
            )}
            <Field label={t('journal.costCenter')}><CostCenterSelect value={h.cost_center_id} onChange={(c) => setH({ ...h, cost_center_id: c })} /></Field>
            <Field label={t('common.reference')} error={errors.reference}><Input value={h.reference} onChange={(e) => setH({ ...h, reference: e.target.value })} /></Field>
          </div>
        </Panel>

        <section className="panel entry-grid">
          <div className="panel-head">
            <h2 className="panel-title">{t('invoices.items')}</h2>
            <Button size="sm" icon={<Plus size={14} />} onClick={addItem}>{t('invoices.addItem')} <kbd>Alt+N</kbd></Button>
          </div>
          {errors.items && <div style={{ padding: '8px 20px' }}><Alert>{t('invoices.noItems')}</Alert></div>}
          <div className="table-wrap" ref={gridRef} onKeyDown={gridEnter(gridRef, addItem)}>
            <table className="table">
              <thead>
                <tr>
                  <th className="align-center">#</th>
                  <th style={{ minWidth: 220 }}>{t('invoices.item')}</th>
                  <th style={{ minWidth: 160 }}>{t('common.description')}</th>
                  <th className="align-end" style={{ width: 90 }}>{t('invoices.quantity')}</th>
                  <th style={{ width: 80 }}>{t('invoices.unit')}</th>
                  <th className="align-end" style={{ width: 140 }}>{t('invoices.unitPrice')}</th>
                  <th className="align-end" style={{ width: 120 }} title={t('invoices.discountHint')}>{t('invoices.discount')}</th>
                  <th className="align-end" style={{ width: 70 }}>{t('invoices.taxRate')}</th>
                  <th className="align-end" style={{ width: 140 }}>{t('invoices.lineTotal')}</th>
                  <th style={{ width: 40 }} />
                </tr>
              </thead>
              <tbody>
                {items.map((it, i) => (
                  <tr key={it.key} data-item={it.key}>
                    <td className="line-no num">{f.int(i + 1)}</td>
                    <td data-invalid={!!err(i, 'product_id')}>
                      <ProductPicker value={it.product_id} valueLabel={it.product_label} onChange={(pid, p) => void pickProduct(it, pid, p)} aria-invalid={!!err(i, 'product_id')} />
                    </td>
                    <td><Input value={it.description} onChange={(e) => update(it.key, { description: e.target.value })} placeholder={it.product_id ? '' : t('invoices.freeText')} /></td>
                    <td data-invalid={!!err(i, 'quantity')}><NumericInput fraction={3} value={it.quantity} onChange={(v) => update(it.key, { quantity: v })} /></td>
                    <td><Input value={it.unit} onChange={(e) => update(it.key, { unit: e.target.value })} /></td>
                    <td data-invalid={!!err(i, 'unit_price')}><MoneyInput value={it.unit_price} onChange={(v) => update(it.key, { unit_price: v })} /></td>
                    <td data-invalid={!!err(i, 'discount')}><AmountInput value={it.discount} onChange={(v) => update(it.key, { discount: v })} placeholder="0" /></td>
                    <td data-invalid={!!err(i, 'tax_rate')}><PercentageInput value={it.tax_rate} onChange={(v) => update(it.key, { tax_rate: v })} /></td>
                    <td className="align-end num" style={{ paddingTop: 10 }}>{f.money(calcs[i].total)}</td>
                    <td>
                      <IconButton size="sm" tabIndex={-1} label={t('common.remove')} icon={<Trash2 size={14} />} disabled={items.length <= 1}
                        onClick={() => setItems((xs) => xs.filter((x) => x.key !== it.key))} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="doc-totals">
            <span className="muted">{t('invoices.subtotal')}</span><span className="num align-end">{f.money(totals.gross)}</span>
            <span className="muted">{t('invoices.discountTotal')}</span><span className="num">{f.money(totals.discount)}</span>
            <span className="muted">{t('invoices.taxTotal')}</span><span className="num">{f.money(totals.tax)}</span>
            <span className="grand">{t('invoices.grandTotal')}</span><span className="grand num">{f.money(totals.total)}</span>
          </div>
        </section>
        <Panel>
          <Field label={t('common.notes')}><TextArea value={h.notes} onChange={(e) => setH({ ...h, notes: e.target.value })} /></Field>
        </Panel>
      </div>
      {newParty && (
        <PartyDialog role={info.partyRole} onClose={() => setNewParty(false)} onSaved={(p) => { setNewParty(false); setH({ ...h, party_id: p.id }); }} />
      )}
    </div>
  );
}
