import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { api } from '@/api/client';
import { useWarehouses } from '@/api/queries';
import type { Warehouse } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input } from '@/components/ui/Field';
import { Alert, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useFormState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';

export function WarehousesPage() {
  const { t } = useI18n();
  const { can } = useAuth();
  const q = useWarehouses();
  const [editing, setEditing] = useState<Warehouse | 'new' | null>(null);
  return (
    <div className="page">
      <PageHeader title={t('inventory.warehousesTitle')} actions={can('inventory.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('inventory.newWarehouse')}</Button>} />
      <Panel bodyless>
        <DataTable
          rows={q.data}
          loading={q.isLoading}
          rowKey={(w) => w.id}
          onRowClick={can('inventory.edit') ? (w) => setEditing(w) : undefined}
          columns={[
            { key: 'code', header: t('common.code'), cell: (w) => <span className="num">{w.code}</span>, width: 100 },
            { key: 'name', header: t('common.name'), cell: (w) => <>{w.name} {w.is_default && <Badge tone="info">{t('inventory.isDefault')}</Badge>}</> },
            { key: 'addr', header: t('parties.address'), cell: (w) => w.address },
            { key: 'status', header: t('common.status'), cell: (w) => (w.is_active ? <Badge tone="success">{t('common.active')}</Badge> : <Badge>{t('common.inactive')}</Badge>) },
          ]}
        />
      </Panel>
      {editing && <WarehouseDialog warehouse={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function WarehouseDialog({ warehouse, onClose }: { warehouse?: Warehouse; onClose: () => void }) {
  const { t } = useI18n();
  const qc = useQueryClient();
  const { values: v, set, errors, setErrors } = useFormState({
    code: warehouse?.code ?? '', name: warehouse?.name ?? '', address: warehouse?.address ?? '',
    is_active: warehouse?.is_active ?? true, is_default: warehouse?.is_default ?? false,
  });
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const ok = await run(() => (warehouse ? api.put(`/inventory/warehouses/${warehouse.id}`, v) : api.post('/inventory/warehouses', v)).then(() => true), t('common.saved'));
    if (ok) {
      void qc.invalidateQueries({ queryKey: ['warehouses'] });
      onClose();
    }
  };
  return (
    <Dialog title={warehouse ? t('common.edit') : t('inventory.newWarehouse')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="stack" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <Alert>{error}</Alert>}
        <div className="form-grid">
          <Field label={t('common.code')} required error={errors.code}><Input value={v.code} onChange={(e) => set('code', e.target.value)} /></Field>
          <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
          <Field label={t('parties.address')} className="span-all"><Input value={v.address} onChange={(e) => set('address', e.target.value)} /></Field>
          <div className="span-all row" style={{ gap: 24 }}>
            <Checkbox label={t('inventory.isDefault')} checked={v.is_default} onChange={(x) => set('is_default', x)} />
            {warehouse && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} />}
          </div>
          {errors.is_active && <span className="field-error">{t('validation.default_inactive')}</span>}
        </div>
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
