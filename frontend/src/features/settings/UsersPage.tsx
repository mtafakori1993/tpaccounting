import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { Role, User } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select } from '@/components/ui/Field';
import { Alert, Badge, PageHeader, Panel } from '@/components/ui/Misc';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useFormState } from '@/lib/hooks';
import { useSubmit } from '@/lib/useSubmit';

export function UsersPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const q = useQuery({ queryKey: ['users'], queryFn: () => api.get<User[]>('/users') });
  const [editing, setEditing] = useState<User | 'new' | null>(null);
  return (
    <div className="page">
      <PageHeader title={t('settings.usersTitle')} actions={can('users.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setEditing('new')}>{t('settings.newUser')}</Button>} />
      <Panel bodyless>
        <DataTable rows={q.data} loading={q.isLoading} rowKey={(u) => u.id} onRowClick={can('users.edit') ? (u) => setEditing(u) : undefined}
          rowClassName={(u) => (u.is_active ? undefined : 'muted-row')}
          columns={[
            { key: 'name', header: t('common.name'), cell: (u) => u.full_name },
            { key: 'email', header: t('auth.email'), cell: (u) => <span dir="ltr">{u.email}</span> },
            { key: 'role', header: t('settings.role'), cell: (u) => (lang === 'en' ? u.role_name_en || u.role_name : u.role_name) },
            { key: 'last', header: t('settings.lastLogin'), cell: (u) => (u.last_login_at ? <span className="num">{f.dateTime(u.last_login_at)}</span> : t('settings.never')) },
            { key: 's', header: t('common.status'), cell: (u) => (u.is_active ? <Badge tone="success">{t('common.active')}</Badge> : <Badge>{t('common.inactive')}</Badge>) },
          ]} />
      </Panel>
      {editing && <UserDialog user={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
    </div>
  );
}

function UserDialog({ user, onClose }: { user?: User; onClose: () => void }) {
  const { t, lang } = useI18n();
  const qc = useQueryClient();
  const roles = useQuery({ queryKey: ['roles'], queryFn: () => api.get<Role[]>('/roles') });
  const { values: v, set, errors, setErrors } = useFormState({ email: user?.email ?? '', full_name: user?.full_name ?? '', password: '', role_id: user?.role_id ?? 0, is_active: user?.is_active ?? true });
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const ok = await run(() => (user ? api.put(`/users/${user.id}`, v) : api.post('/users', v)).then(() => true), t('common.saved'));
    if (ok) {
      void qc.invalidateQueries({ queryKey: ['users'] });
      onClose();
    }
  };
  return (
    <Dialog title={user ? t('settings.editUser') : t('settings.newUser')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button></>}>
      <form className="form-grid" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        {error && <div className="span-all"><Alert>{error}</Alert></div>}
        <Field label={t('setup.fullName')} required error={errors.full_name} className="span-all"><Input value={v.full_name} onChange={(e) => set('full_name', e.target.value)} /></Field>
        <Field label={t('auth.email')} required error={errors.email} className="span-all"><Input value={v.email} onChange={(e) => set('email', e.target.value)} dir="ltr" autoComplete="off" /></Field>
        <Field label={t('settings.role')} required error={errors.role_id}>
          <Select value={v.role_id || ''} onChange={(e) => set('role_id', Number(e.target.value))}>
            <option value="">{t('common.select')}</option>
            {roles.data?.map((r) => <option key={r.id} value={r.id}>{lang === 'en' ? r.name_en || r.name : r.name}</option>)}
          </Select>
        </Field>
        <Field label={t('auth.password')} required={!user} error={errors.password} help={user ? t('settings.passwordHelp') : t('auth.passwordHint')}>
          <Input type="password" value={v.password} onChange={(e) => set('password', e.target.value)} dir="ltr" autoComplete="new-password" />
        </Field>
        {user && <div className="span-all"><Checkbox label={t('common.isActive')} checked={v.is_active} onChange={(x) => set('is_active', x)} />{errors.is_active && <div className="field-error">{t(`validation.${errors.is_active}` as 'validation.invalid')}</div>}</div>}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}
