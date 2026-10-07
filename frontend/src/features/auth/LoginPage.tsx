import { useState, type FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '@/api/client';
import { useI18n } from '@/i18n/I18nProvider';
import { Field, Input } from '@/components/ui/Field';
import { Button } from '@/components/ui/Button';
import { Alert } from '@/components/ui/Misc';
import { useSubmit } from '@/lib/useSubmit';
import { AuthLayout } from './AuthLayout';
import type { Me } from '@/api/types';

export function LoginPage() {
  const { t } = useI18n();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setFields);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await run(async () => {
      await api.post('/auth/login', { email, password });
      const me = await api.get<Me>('/auth/me');
      qc.setQueryData(['me'], me);
      return me;
    });
    if (ok) navigate('/', { replace: true });
  };

  return (
    <AuthLayout>
      <form onSubmit={submit} className="stack" noValidate>
        <div>
          <h1>{t('auth.welcome')}</h1>
          <p className="muted" style={{ marginTop: 6 }}>{t('auth.subtitle')}</p>
        </div>
        {error && <Alert>{error}</Alert>}
        <Field label={t('auth.email')} error={fields.email}>
          <Input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} autoFocus dir="ltr" />
        </Field>
        <Field label={t('auth.password')} error={fields.password}>
          <Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} dir="ltr" />
        </Field>
        <Button type="submit" variant="accent" pending={pending}>{pending ? t('auth.signingIn') : t('auth.login')}</Button>
      </form>
    </AuthLayout>
  );
}
