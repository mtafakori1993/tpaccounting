import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Plus, Tags, Trash2 } from 'lucide-react';
import { api } from '@/api/client';
import { useProductCategories } from '@/api/queries';
import type { Category, Product } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { Checkbox, Field, Input, MoneyInput, PercentageInput, Select, TextArea } from '@/components/ui/Field';
import { Alert, Amount, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable, Pagination } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { useFormState } from '@/lib/hooks';
import { useList } from '@/lib/useList';
import { useSubmit } from '@/lib/useSubmit';

export function ProductsPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const cats = useProductCategories();
  const [editing, setEditing] = useState<Product | 'new' | null>(null);
  const [managing, setManaging] = useState(false);
  const list = useList<Product, { q: string; kind: string; category_id: string; active: string }>(
    'products', '/products', { q: '', kind: '', category_id: '', active: 'true' }, { key: 'name', desc: false },
  );
  const rows = list.query.data?.items;
  const label = (p: Product) => (lang === 'en' && p.name_en ? p.name_en : p.name);
  const refresh = () => void qc.invalidateQueries({ queryKey: ['products'] });

  return (
    <div className="page">
      <PageHeader
        title={t('products.title')}
        actions={
          <>
            <Button icon={<Tags size={16} />} onClick={() => setManaging(true)}>{t('products.categories')}</Button>
            <ExportMenu
              name="products"
              permission="products.export"
              rows={rows}
              columns={[
                { header: t('products.sku'), value: (p) => p.sku },
                { header: t('common.name'), value: (p) => p.name },
                { header: t('products.kind'), value: (p) => t(p.kind === 'goods' ? 'products.goods' : 'products.service') },
                { header: t('products.category'), value: (p) => p.category_name ?? '' },
                { header: t('products.unit'), value: (p) => p.unit },
                { header: t('products.salesPrice'), value: (p) => p.sales_price, numeric: true },
                { header: t('products.purchasePrice'), value: (p) => p.purchase_price, numeric: true },
                { header: t('products.taxRate'), value: (p) => p.tax_rate, numeric: true },
                { header: t('products.onHand'), value: (p) => p.on_hand, numeric: true },
              ]}
            />
            {can('products.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('products.newProduct')}</Button>}
          </>
        }
      />
      <Panel bodyless>
        <div className="toolbar">
          <Field label={t('common.search')} className="search"><Input value={list.filters.q} onChange={(e) => list.setFilter('q', e.target.value)} /></Field>
          <Field label={t('products.kind')}>
            <Select value={list.filters.kind} onChange={(e) => list.setFilter('kind', e.target.value)}>
              <option value="">{t('common.all')}</option>
              <option value="goods">{t('products.goods')}</option>
              <option value="service">{t('products.service')}</option>
            </Select>
          </Field>
          <Field label={t('products.category')}>
            <Select value={list.filters.category_id} onChange={(e) => list.setFilter('category_id', e.target.value)}>
              <option value="">{t('common.all')}</option>
              {cats.data?.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </Select>
          </Field>
          <Field label={t('common.status')}>
            <Select value={list.filters.active} onChange={(e) => list.setFilter('active', e.target.value)}>
              <option value="">{t('common.all')}</option>
              <option value="true">{t('common.active')}</option>
              <option value="false">{t('common.inactive')}</option>
            </Select>
          </Field>
        </div>
        <DataTable
          rows={rows}
          loading={list.query.isLoading}
          rowKey={(p) => p.id}
          sort={list.sort}
          onSort={list.setSort}
          onRowClick={can('products.edit') ? (p) => setEditing(p) : undefined}
          rowClassName={(p) => (p.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'sku', header: t('products.sku'), sortable: true, cell: (p) => <span className="num">{f.digits(p.sku)}</span>, width: 100 },
            { key: 'name', header: t('common.name'), sortable: true, cell: (p) => <>{label(p)} {!p.is_active && <Badge>{t('common.inactive')}</Badge>}</> },
            { key: 'kind', header: t('products.kind'), cell: (p) => t(p.kind === 'goods' ? 'products.goods' : 'products.service') },
            { key: 'cat', header: t('products.category'), cell: (p) => p.category_name ?? '' },
            { key: 'sales_price', header: t('products.salesPrice'), sortable: true, align: 'end', cell: (p) => <Amount value={p.sales_price} /> },
            { key: 'purchase_price', header: t('products.purchasePrice'), sortable: true, align: 'end', cell: (p) => <Amount value={p.purchase_price} /> },
            { key: 'on_hand', header: t('products.onHand'), sortable: true, align: 'end', cell: (p) => (p.track_inventory ? <span className="num">{f.qty(p.on_hand)} {p.unit}</span> : <span className="muted">—</span>) },
          ]}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.query.data?.total ?? 0} onPage={list.setPage} />
      </Panel>
      {editing && <ProductDialog product={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); refresh(); }} />}
      {managing && <CategoriesDialog onClose={() => setManaging(false)} />}
    </div>
  );
}

function ProductDialog({ product, onClose, onSaved }: { product?: Product; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const cats = useProductCategories();
  const { values: v, set, errors, setErrors } = useFormState({
    kind: product?.kind ?? 'goods',
    sku: product?.sku ?? '',
    name: product?.name ?? '',
    name_en: product?.name_en ?? '',
    unit: product?.unit ?? '',
    category_id: product?.category_id ?? null,
    purchase_price: product?.purchase_price ?? '',
    sales_price: product?.sales_price ?? '',
    tax_rate: product?.tax_rate ?? '10',
    track_inventory: product?.track_inventory ?? true,
    is_active: product?.is_active ?? true,
    description: product?.description ?? '',
  });
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const body = { ...v, purchase_price: v.purchase_price || '0', sales_price: v.sales_price || '0', tax_rate: v.tax_rate || '0', track_inventory: v.kind === 'goods' && v.track_inventory };
    const ok = await run(() => (product ? api.put(`/products/${product.id}`, body) : api.post('/products', body)).then(() => true), t('common.saved'));
    if (ok) onSaved();
  };
  return (
    <Dialog wide title={product ? t('products.editProduct') : t('products.newProduct')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('products.kind')}>
            <Select value={v.kind} onChange={(e) => set('kind', e.target.value as 'goods' | 'service')}>
              <option value="goods">{t('products.goods')}</option>
              <option value="service">{t('products.service')}</option>
            </Select>
          </Field>
          <Field label={t('common.name')} required error={errors.name} className="span-2"><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label={t('products.sku')} error={errors.sku} help={!product ? t('products.skuHelp') : undefined}><Input value={v.sku} onChange={(e) => set('sku', e.target.value)} /></Field>
          <Field label={t('common.nameEn')} className="span-2"><Input value={v.name_en} onChange={(e) => set('name_en', e.target.value)} dir="ltr" /></Field>
          <Field label={t('products.unit')} error={errors.unit}><Input value={v.unit} onChange={(e) => set('unit', e.target.value)} /></Field>
          <Field label={t('products.category')} error={errors.category_id}>
            <Select value={v.category_id ?? ''} onChange={(e) => set('category_id', e.target.value ? Number(e.target.value) : null)}>
              <option value="">{t('products.noCategory')}</option>
              {cats.data?.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </Select>
          </Field>
          <Field label={t('products.salesPrice')} error={errors.sales_price}><MoneyInput value={v.sales_price} onChange={(x) => set('sales_price', x)} /></Field>
          <Field label={t('products.purchasePrice')} error={errors.purchase_price}><MoneyInput value={v.purchase_price} onChange={(x) => set('purchase_price', x)} /></Field>
          <Field label={t('products.taxRate')} error={errors.tax_rate}><PercentageInput value={v.tax_rate} onChange={(x) => set('tax_rate', x)} /></Field>
          <div className="span-all row" style={{ gap: 24 }}>
            {v.kind === 'goods' && <Checkbox label={t('products.trackInventory')} checked={v.track_inventory} onChange={(x) => set('track_inventory', x)} />}
            {product && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} />}
          </div>
          <Field label={t('common.description')} className="span-all"><TextArea value={v.description} onChange={(e) => set('description', e.target.value)} /></Field>
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function CategoriesDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const cats = useProductCategories();
  const [name, setName] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setFields);
  const refresh = () => void qc.invalidateQueries({ queryKey: ['product-categories'] });
  const addCat = async () => {
    if (await run(() => api.post('/products/categories', { name }).then(() => true), t('common.saved'))) {
      setName('');
      refresh();
    }
  };
  return (
    <Dialog title={t('products.categories')} onClose={onClose}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        {can('products.create') && (
          <form className="row" style={{ flexWrap: 'nowrap', alignItems: 'flex-start' }} onSubmit={(e) => { e.preventDefault(); void addCat(); }}>
            <Field error={fields.name} className="grow"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('products.newCategory')} /></Field>
            <Button type="submit" variant="accent" pending={pending} icon={<Plus size={16} />}>{t('common.add')}</Button>
          </form>
        )}
        <DataTable<Category>
          rows={cats.data}
          rowKey={(c) => c.id}
          columns={[
            { key: 'name', header: t('common.name'), cell: (c) => c.name },
            { key: 'count', header: t('products.productCount'), align: 'end', cell: (c) => <span className="num">{f.count(c.product_count)}</span> },
            {
              key: 'x', header: '', width: 40, cell: (c) => can('products.delete') && (
                <IconButton size="sm" label={t('common.delete')} icon={<Trash2 size={14} />} onClick={() => run(() => api.del(`/products/categories/${c.id}`)).then(refresh)} />
              ),
            },
          ]}
        />
      </div>
    </Dialog>
  );
}
