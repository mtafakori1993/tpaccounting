import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '@/api/client';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Checkbox, Field, Input } from '@/components/ui/Field';
import { Amount, PageHeader, Panel } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { WarehouseSelect } from '@/components/pickers/Pickers';
import { useDebounced, usePersistentState } from '@/lib/hooks';
import { add } from '@/lib/decimal';

interface StockRow { product_id: number; sku: string; product_name: string; unit: string; warehouse_id: number; warehouse_name: string; quantity: string; value: string; average_cost: string }

export function StockPage() {
  const { t } = useI18n();
  const f = useFormat();
  const navigate = useNavigate();
  const [flt, setFlt] = usePersistentState('filters.stock', { q: '', warehouse_id: 0, nonzero: true });
  const q = useDebounced(flt.q);
  const query = useQuery({
    queryKey: ['stock', flt.warehouse_id, q, flt.nonzero],
    queryFn: () => api.get<StockRow[]>('/inventory/stock', { q, warehouse_id: flt.warehouse_id || undefined, nonzero: flt.nonzero }),
  });
  const rows = query.data;
  const totalValue = add(...(rows ?? []).map((r) => r.value));
  return (
    <div className="page">
      <PageHeader
        title={t('inventory.stockTitle')}
        actions={
          <ExportMenu name="stock" permission="inventory.export" rows={rows} columns={[
            { header: t('products.sku'), value: (r) => r.sku },
            { header: t('common.name'), value: (r) => r.product_name },
            { header: t('inventory.warehouse'), value: (r) => r.warehouse_name },
            { header: t('inventory.quantity'), value: (r) => r.quantity, numeric: true },
            { header: t('products.unit'), value: (r) => r.unit },
            { header: t('inventory.averageCost'), value: (r) => r.average_cost, numeric: true },
            { header: t('inventory.value'), value: (r) => r.value, numeric: true },
          ]} />
        }
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={flt.q} onChange={(e) => setFlt({ ...flt, q: e.target.value })} /></Field>
          <Field label={t('inventory.warehouse')}><WarehouseSelect allowEmpty value={flt.warehouse_id || null} onChange={(w) => setFlt({ ...flt, warehouse_id: w ?? 0 })} /></Field>
          <Checkbox label={t('inventory.nonzeroOnly')} checked={flt.nonzero} onChange={(v) => setFlt({ ...flt, nonzero: v })} />
        </div>
        <DataTable
          rows={rows}
          loading={query.isLoading}
          rowKey={(r) => `${r.product_id}-${r.warehouse_id}`}
          onRowClick={(r) => navigate(`/inventory/ledger?product_id=${r.product_id}&warehouse_id=${r.warehouse_id}`)}
          showFooter
          columns={[
            { key: 'sku', header: t('products.sku'), cell: (r) => <span className="num">{f.digits(r.sku)}</span>, width: 100 },
            { key: 'name', header: t('common.name'), cell: (r) => r.product_name },
            { key: 'wh', header: t('inventory.warehouse'), cell: (r) => r.warehouse_name, footer: t('common.total') },
            { key: 'qty', header: t('inventory.quantity'), align: 'end', cell: (r) => <span className="num">{f.qty(r.quantity)} {r.unit}</span> },
            { key: 'avg', header: t('inventory.averageCost'), align: 'end', cell: (r) => <Amount value={r.average_cost} /> },
            { key: 'value', header: t('inventory.value'), align: 'end', cell: (r) => <Amount value={r.value} />, footer: <Amount value={totalValue} /> },
          ]}
        />
      </Panel>
    </div>
  );
}
