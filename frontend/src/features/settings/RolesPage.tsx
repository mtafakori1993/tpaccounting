import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { Permission, Role } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Field, Input } from '@/components/ui/Field';
import { Alert, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useSubmit } from '@/lib/useSubmit';

// Standard actions first, then module-specific ones (calculate, approve, pay, …).
const actionOrder = ['view', 'create', 'edit', 'post', 'cancel', 'delete', 'export', 'submit', 'calculate', 'approve', 'activate', 'pay', 'close', 'override'];

export function RolesPage() {
  const { t, tryT, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ['roles'], queryFn: () => api.get<Role[]>('/roles') });
  const perms = useQuery({ queryKey: ['permissions'], queryFn: () => api.get<Permission[]>('/roles/permissions') });
  const [selected, setSelected] = useState<number | null>(null);
  const role = roles.data?.find((r) => r.id === (selected ?? roles.data?.[0]?.id));
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const save = useSubmit();
  useEffect(() => {
    if (role) setChecked(new Set(role.permissions));
  }, [role]);
  const modules = useMemo(() => Array.from(new Set(perms.data?.map((p) => p.module))), [perms.data]);
  const codes = useMemo(() => new Set(perms.data?.map((p) => p.code)), [perms.data]);
  const actions = useMemo(() => {
    const present = new Set(perms.data?.map((p) => p.action));
    return [...actionOrder.filter((a) => present.has(a)), ...Array.from(present).filter((a) => !actionOrder.includes(a))];
  }, [perms.data]);
  const locked = !role || role.code === 'admin' || !can('roles.edit');
  const toggle = (code: string) => setChecked((s) => {
    const n = new Set(s);
    if (n.has(code)) n.delete(code);
    else n.add(code);
    return n;
  });
  const reload = () => void qc.invalidateQueries({ queryKey: ['roles'] });
  const roleName = (r: Role) => (lang === 'en' ? r.name_en || r.name : r.name);
  return (
    <div className="page">
      <PageHeader title={t('settings.rolesTitle')} actions={can('roles.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('settings.newRole')}</Button>} />
      <div className="side-layout" style={{ gridTemplateColumns: '300px minmax(0, 1fr)' }}>
        <Panel bodyless>
          <DataTable rows={roles.data} rowKey={(r) => r.id} onRowClick={(r) => setSelected(r.id)} rowClassName={(r) => (r.id === role?.id ? 'selected-row' : undefined)}
            columns={[
              { key: 'name', header: t('settings.role'), cell: (r) => <>{roleName(r)} {r.is_system && <Badge>{t('settings.system')}</Badge>}</> },
              { key: 'n', header: t('settings.userCount'), align: 'end', cell: (r) => <span className="num">{f.count(r.user_count)}</span> },
            ]} />
        </Panel>
        {role && (
          <Panel bodyless title={`${t('settings.permissions')} — ${roleName(role)}`} actions={<>
            {!role.is_system && can('roles.delete') && <Button size="sm" variant="quiet" onClick={() => setDeleting(true)}>{t('common.delete')}</Button>}
            {!locked && <Button size="sm" variant="accent" pending={save.pending} onClick={() => save.run(() => api.put(`/roles/${role.id}`, { ...role, permissions: Array.from(checked) }).then(reload), t('common.saved'))}>{t('common.save')}</Button>}
          </>}>
            {save.error && <div style={{ padding: 16 }}><Alert>{save.error}</Alert></div>}
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th>{t('settings.entity')}</th>
                    {actions.map((a) => <th key={a} className="align-center">{tryT(`settings.actions.${a}`) ?? a}</th>)}
                  </tr>
                </thead>
                <tbody>
                  {modules.map((m) => (
                    <tr key={m}>
                      <td>{tryT(`settings.modules.${m}`) ?? m}</td>
                      {actions.map((a) => {
                        const code = `${m}.${a}`;
                        if (!codes.has(code)) return <td key={a} />;
                        return (
                          <td key={a} className="align-center">
                            <input type="checkbox" aria-label={`${m} ${a}`} checked={checked.has(code)} disabled={locked} onChange={() => toggle(code)} style={{ accentColor: 'var(--color-primary)', width: 16, height: 16 }} />
                          </td>
                        );
                      })}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </Panel>
        )}
      </div>
      {creating && <NewRoleDialog onClose={() => setCreating(false)} onSaved={(r) => { setCreating(false); reload(); setSelected(r.id); }} />}
      {deleting && role && (
        <ConfirmDialog title={t('common.delete')} body={t('common.confirmDelete')} confirmLabel={t('common.yesDelete')} negative pending={save.pending} error={save.error}
          onClose={() => setDeleting(false)}
          onConfirm={async () => { if (await save.run(() => api.del(`/roles/${role.id}`).then(() => true))) { setDeleting(false); setSelected(null); reload(); } }} />
      )}
    </div>
  );
}

function NewRoleDialog({ onClose, onSaved }: { onClose: () => void; onSaved: (r: Role) => void }) {
  const { t } = useI18n();
  const [v, setV] = useState({ code: '', name: '', name_en: '', description: '', permissions: [] as string[] });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  return (
    <Dialog title={t('settings.newRole')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={async () => { const r = await run(() => api.post<Role>('/roles', v), t('common.saved')); if (r) onSaved(r); }}>{t('common.create')}</Button></>}>
      <div className="form-grid">
        {error && <div className="span-all"><Alert>{error}</Alert></div>}
        <Field label={t('settings.roleCode')} required error={errors.code} help="a-z, 0-9, _"><Input value={v.code} onChange={(e) => setV({ ...v, code: e.target.value.toLowerCase() })} dir="ltr" /></Field>
        <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
        <Field label={t('common.nameEn')} className="span-all"><Input value={v.name_en} onChange={(e) => setV({ ...v, name_en: e.target.value })} dir="ltr" /></Field>
      </div>
    </Dialog>
  );
}
