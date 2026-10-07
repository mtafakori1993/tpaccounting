import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { Plus } from 'lucide-react';
import { api } from '@/api/client';
import { useCostCenters } from '@/api/queries';
import type { CostCenter, CostCenterKind } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select } from '@/components/ui/Field';
import { Alert, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useFormState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';

export function CostCentersPage() {
  const { t, lang } = useI18n();
  const { can } = useAuth();
  const q = useCostCenters();
  const [editing, setEditing] = useState<CostCenter | 'new' | null>(null);
  return (
    <div className="page">
      <PageHeader title={t('costCenters.title')} actions={<>
        <Link className="btn btn-secondary" to="/reports/cost-centers">{t('reports.names.cost-centers')}</Link>
        {can('cost_centers.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('costCenters.newCostCenter')}</Button>}
      </>} />
      <Panel bodyless>
        <DataTable rows={q.data} loading={q.isLoading} rowKey={(c) => c.id}
          onRowClick={can('cost_centers.edit') ? (c) => setEditing(c) : undefined}
          rowClassName={(c) => (c.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'code', header: t('common.code'), cell: (c) => <span className="num">{c.code}</span>, width: 100 },
            { key: 'name', header: t('common.name'), cell: (c) => (lang === 'en' && c.name_en ? c.name_en : c.name) },
            { key: 'kind', header: t('common.type'), cell: (c) => t(`costCenters.kinds.${c.kind ?? 'cost_center'}`), width: 140 },
            { key: 'desc', header: t('common.description'), cell: (c) => c.description },
            { key: 'status', header: t('common.status'), cell: (c) => (c.is_active ? <Badge tone="success">{t('common.active')}</Badge> : <Badge>{t('common.inactive')}</Badge>) },
          ]} />
      </Panel>
      {editing && <CostCenterDialog cc={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function CostCenterDialog({ cc, onClose }: { cc?: CostCenter; onClose: () => void }) {
  const { t } = useI18n();
  const qc = useQueryClient();
  const { values: v, set, errors, setErrors } = useFormState({ code: cc?.code ?? '', kind: (cc?.kind ?? 'cost_center') as CostCenterKind, name: cc?.name ?? '', name_en: cc?.name_en ?? '', description: cc?.description ?? '', is_active: cc?.is_active ?? true });
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const ok = await run(() => (cc ? api.put(`/cost-centers/${cc.id}`, v) : api.post('/cost-centers', v)).then(() => true), t('common.saved'));
    if (ok) {
      void qc.invalidateQueries({ queryKey: ['cost-centers'] });
      onClose();
    }
  };
  return (
    <Dialog title={cc ? t('costCenters.editCostCenter') : t('costCenters.newCostCenter')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="form-grid" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <div className="span-all"><Alert>{error}</Alert></div>}
        <Field label={t('common.code')} required error={errors.code}><Input value={v.code} onChange={(e) => set('code', e.target.value)} /></Field>
        <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => set('name', e.target.value)} /></Field>
        <Field label={t('common.type')}>
          <Select value={v.kind} onChange={(e) => set('kind', e.target.value as CostCenterKind)}>
            {(['cost_center', 'department', 'project'] as const).map((k) => <option key={k} value={k}>{t(`costCenters.kinds.${k}`)}</option>)}
          </Select>
        </Field>
        <Field label={t('common.nameEn')} className="span-all"><Input value={v.name_en} onChange={(e) => set('name_en', e.target.value)} dir="ltr" /></Field>
        <Field label={t('common.description')} className="span-all"><Input value={v.description} onChange={(e) => set('description', e.target.value)} /></Field>
        {cc && <Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} />}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
