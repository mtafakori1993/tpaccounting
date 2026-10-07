import { useQuery } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field } from '@/components/ui/Field';
import { Amount, EmptyState, PageHeader, Panel } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import { ProductPicker, WarehouseSelect } from '@/components/pickers/Pickers';

interface Movement { id: number; date: string; warehouse_name: string; source_type: string; source_id: number; description: string; qty_in: string; qty_out: string; unit_cost: string; value: string; balance_qty: string; balance_value: string }
interface Ledger { opening_qty: string; opening_value: string; movements: Movement[]; closing_qty: string; closing_value: string }

const sourceKey: Record<string, string> = {
  sales_invoice: 'journal.sources.sales_invoice', sales_return: 'journal.sources.sales_return', purchase_invoice: 'journal.sources.purchase_invoice',
  purchase_return: 'journal.sources.purchase_return', stock_document: 'journal.sources.stock_document',
};

export function StockLedgerPage() {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const [sp, setSp] = useSearchParams();
  const productId = Number(sp.get('product_id')) || null;
  const warehouseId = Number(sp.get('warehouse_id')) || null;
  const from = sp.get('from') ?? '';
  const to = sp.get('to') ?? '';
  const set = (k: string, v: string | number | null) => {
    const n = new URLSearchParams(sp);
    if (v) n.set(k, String(v));
    else n.delete(k);
    setSp(n, { replace: true });
  };
  const q = useQuery({
    queryKey: ['stock-ledger', productId, warehouseId, from, to],
    queryFn: () => api.get<Ledger>('/inventory/ledger', { product_id: productId, warehouse_id: warehouseId, from, to }),
    enabled: !!productId,
  });
  const src = (m: Movement) => {
    const base = m.source_type.replace(/_cancel$/, '');
    const label = tryT(sourceKey[base] ?? '') ?? base;
    return m.source_type.endsWith('_cancel') ? `${label} (${t('status.cancelled')})` : label;
  };
  return (
    <div className="page">
      <PageHeader title={t('inventory.ledgerTitle')} actions={
        <ExportMenu name="stock-ledger" permission="inventory.export" rows={q.data?.movements} columns={[
          { header: t('common.date'), value: (m) => f.date(m.date) },
          { header: t('inventory.document'), value: src },
          { header: t('inventory.warehouse'), value: (m) => m.warehouse_name },
          { header: t('inventory.in'), value: (m) => m.qty_in, numeric: true },
          { header: t('inventory.out'), value: (m) => m.qty_out, numeric: true },
          { header: t('inventory.unitCost'), value: (m) => m.unit_cost, numeric: true },
          { header: t('inventory.balanceQty'), value: (m) => m.balance_qty, numeric: true },
          { header: t('inventory.balanceValue'), value: (m) => m.balance_value, numeric: true },
        ]} />
      } />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('invoices.item')} className="search"><ProductPicker value={productId} onChange={(id) => set('product_id', id)} /></Field>
          <Field label={t('inventory.warehouse')}><WarehouseSelect allowEmpty value={warehouseId} onChange={(w) => set('warehouse_id', w)} /></Field>
          <DateRange from={from} to={to} onChange={(a, b) => { const n = new URLSearchParams(sp); a ? n.set('from', a) : n.delete('from'); b ? n.set('to', b) : n.delete('to'); setSp(n, { replace: true }); }} />
        </div>
        {!productId ? (
          <EmptyState title={t('inventory.selectProduct')} />
        ) : (
          <>
            <div className="summary-strip">
              <div className="summary-item"><div className="label">{t('inventory.opening')}</div><div className="value num">{f.qty(q.data?.opening_qty ?? 0)} · {f.money(q.data?.opening_value ?? 0)}</div></div>
              <div className="summary-item"><div className="label">{t('inventory.closing')}</div><div className="value num">{f.qty(q.data?.closing_qty ?? 0)} · {f.money(q.data?.closing_value ?? 0)}</div></div>
            </div>
            <DataTable
              rows={q.data?.movements}
              loading={q.isLoading}
              rowKey={(m) => m.id}
              columns={[
                { key: 'date', header: t('common.date'), cell: (m) => <span className="num">{f.date(m.date)}</span>, width: 110 },
                { key: 'doc', header: t('inventory.document'), cell: src },
                { key: 'wh', header: t('inventory.warehouse'), cell: (m) => m.warehouse_name },
                { key: 'in', header: t('inventory.in'), align: 'end', cell: (m) => (m.qty_in !== '0' ? <span className="num">{f.qty(m.qty_in)}</span> : '') },
                { key: 'out', header: t('inventory.out'), align: 'end', cell: (m) => (m.qty_out !== '0' ? <span className="num">{f.qty(m.qty_out)}</span> : '') },
                { key: 'cost', header: t('inventory.unitCost'), align: 'end', cell: (m) => <Amount value={m.unit_cost} /> },
                { key: 'bq', header: t('inventory.balanceQty'), align: 'end', cell: (m) => <span className="num">{f.qty(m.balance_qty)}</span> },
                { key: 'bv', header: t('inventory.balanceValue'), align: 'end', cell: (m) => <Amount value={m.balance_value} /> },
              ]}
            />
          </>
        )}
      </Panel>
    </div>
  );
}
