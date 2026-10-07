import type { FormEvent } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '@/api/client';
import { useI18n } from '@/i18n/I18nProvider';
import { Checkbox, Field, Input, Select } from '@/components/ui/Field';
import { Button } from '@/components/ui/Button';
import { Alert } from '@/components/ui/Misc';
import { useSubmit } from '@/lib/useSubmit';
import { useFormState } from '@/lib/hooks';
import { AuthLayout } from './AuthLayout';
import type { Me } from '@/api/types';

export function SetupPage() {
  const { t, lang } = useI18n();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { values: v, set, errors, setErrors } = useFormState({
    name: '', name_en: '', national_id: '', economic_code: '', phone: '', address: '',
    calendar: 'jalali', base_currency: 'IRR', amount_decimals: 0,
    admin_name: '', admin_email: '', admin_password: '', seed: true,
  });
  const { run, pending, error } = useSubmit(setErrors);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await run(async () => {
      await api.post('/setup', {
        company: { name: v.name, name_en: v.name_en, national_id: v.national_id, economic_code: v.economic_code, phone: v.phone,
          address: v.address, calendar: v.calendar, base_currency: v.base_currency, amount_decimals: Number(v.amount_decimals) },
        admin: { full_name: v.admin_name, email: v.admin_email, password: v.admin_password },
        seed_chart: v.seed,
        language: lang,
      });
      const me = await api.get<Me>('/auth/me');
      qc.setQueryData(['me'], me);
      qc.setQueryData(['setup-status'], { needs_setup: false });
      return true;
    });
    if (ok) navigate('/', { replace: true });
  };

  return (
    <AuthLayout wide>
      <form onSubmit={submit} className="stack" noValidate>
        <div>
          <h1>{t('setup.title')}</h1>
          <p className="muted" style={{ marginTop: 6 }}>{t('setup.intro')}</p>
        </div>
        {error && <Alert>{error}</Alert>}
        <h2 className="form-section-title">{t('setup.companySection')}</h2>
        <div className="form-grid">
          <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => set('name', e.target.value)} autoFocus /></Field>
          <Field label={t('common.nameEn')} error={errors.name_en}><Input value={v.name_en} onChange={(e) => set('name_en', e.target.value)} dir="ltr" /></Field>
          <Field label={t('parties.nationalId')} error={errors.national_id}><Input value={v.national_id} onChange={(e) => set('national_id', e.target.value)} inputMode="numeric" /></Field>
          <Field label={t('parties.economicCode')} error={errors.economic_code}><Input value={v.economic_code} onChange={(e) => set('economic_code', e.target.value)} inputMode="numeric" /></Field>
          <Field label={t('parties.phone')}><Input value={v.phone} onChange={(e) => set('phone', e.target.value)} /></Field>
          <Field label={t('parties.address')} className="span-2"><Input value={v.address} onChange={(e) => set('address', e.target.value)} /></Field>
        </div>
        <h2 className="form-section-title">{t('setup.basicsSection')}</h2>
        <div className="form-grid">
          <Field label={t('setup.calendar')}>
            <Select value={v.calendar} onChange={(e) => set('calendar', e.target.value)}>
              <option value="jalali">{t('setup.jalali')}</option>
              <option value="gregorian">{t('setup.gregorian')}</option>
            </Select>
          </Field>
          <Field label={t('setup.currency')} error={errors.base_currency}><Input value={v.base_currency} maxLength={3} onChange={(e) => set('base_currency', e.target.value.toUpperCase())} dir="ltr" /></Field>
          <Field label={t('setup.decimals')}>
            <Select value={v.amount_decimals} onChange={(e) => set('amount_decimals', Number(e.target.value))}>
              <option value={0}>0</option><option value={1}>1</option><option value={2}>2</option>
            </Select>
          </Field>
          <div className="span-all">
            <Checkbox label={t('setup.seedChart')} checked={v.seed} onChange={(x) => set('seed', x)} />
            <p className="field-help" style={{ marginInlineStart: 24 }}>{t('setup.seedChartHelp')}</p>
          </div>
        </div>
        <h2 className="form-section-title">{t('setup.adminSection')}</h2>
        <div className="form-grid">
          <Field label={t('setup.fullName')} required error={errors['admin.full_name']}><Input value={v.admin_name} onChange={(e) => set('admin_name', e.target.value)} /></Field>
          <Field label={t('auth.email')} required error={errors['admin.email']}><Input type="email" value={v.admin_email} onChange={(e) => set('admin_email', e.target.value)} dir="ltr" autoComplete="username" /></Field>
          <Field label={t('auth.password')} required help={t('auth.passwordHint')} error={errors['admin.password']}><Input type="password" value={v.admin_password} onChange={(e) => set('admin_password', e.target.value)} dir="ltr" autoComplete="new-password" /></Field>
        </div>
        <div className="row">
          <Button type="submit" variant="accent" pending={pending}>{pending ? t('setup.creating') : t('setup.finish')}</Button>
        </div>
      </form>
    </AuthLayout>
  );
}
