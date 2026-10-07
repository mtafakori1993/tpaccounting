import { useState } from 'react';
import { api } from '@/api/client';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Field, Input } from '@/components/ui/Field';
import { Alert, KV, PageHeader, Panel } from '@/components/ui/Misc';
import { useSubmit } from '@/lib/useSubmit';
import { LanguageSwitch, ThemeToggle } from '@/layout/AppShell';

export function ProfilePage() {
  const { t } = useI18n();
  const { me } = useAuth();
  const [v, setV] = useState({ current_password: '', new_password: '' });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  return (
    <div className="page">
      <PageHeader title={t('common.profile')} />
      <div className="side-layout" style={{ gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 1fr)' }}>
        <Panel title={t('common.details')}>
          <div className="stack">
            <KV items={[{ label: t('setup.fullName'), value: me?.full_name }, { label: t('auth.email'), value: <span dir="ltr">{me?.email}</span> }, { label: t('settings.role'), value: me?.role }]} />
            <div className="row"><span className="field-label">{t('common.language')}</span><LanguageSwitch /><span className="field-label">{t('common.theme')}</span><ThemeToggle /></div>
          </div>
        </Panel>
        <Panel title={t('auth.changePassword')}>
          <form className="stack" onSubmit={async (e) => {
            e.preventDefault();
            if (await run(() => api.post('/auth/change-password', v).then(() => true), t('auth.passwordChanged'))) setV({ current_password: '', new_password: '' });
          }}>
            {error && <Alert>{error}</Alert>}
            <Field label={t('auth.currentPassword')} error={errors.current_password}><Input type="password" value={v.current_password} onChange={(e) => setV({ ...v, current_password: e.target.value })} dir="ltr" autoComplete="current-password" /></Field>
            <Field label={t('auth.newPassword')} error={errors.new_password} help={t('auth.passwordHint')}><Input type="password" value={v.new_password} onChange={(e) => setV({ ...v, new_password: e.target.value })} dir="ltr" autoComplete="new-password" /></Field>
            <div><Button type="submit" variant="accent" pending={pending}>{t('auth.changePassword')}</Button></div>
          </form>
        </Panel>
      </div>
    </div>
  );
}
