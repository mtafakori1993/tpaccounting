import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { Company } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Checkbox, Field, Input, Select } from '@/components/ui/Field';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { useSubmit } from '@/lib/useSubmit';
import { normalizeDigits } from '@/lib/dates';
import mark from '@/assets/logo-mark.svg';

export function CompanyPage() {
  const { t } = useI18n();
  const { can, refresh } = useAuth();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['company'], queryFn: () => api.get<Company>('/company') });
  const [v, setValues] = useState<Company | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = <K extends keyof Company>(k: K, val: Company[K]) => setValues((c) => (c ? { ...c, [k]: val } : c));
  const save = useSubmit(setErrors);
  const logo = useSubmit();
  const file = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (q.data) setValues(q.data);
  }, [q.data, setValues]);
  if (!v) return <LoadingBlock />;
  const editable = can('company.edit');
  const field = (k: keyof Company, digits = false) => ({
    value: String(v[k] ?? ''),
    disabled: !editable,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => set(k as never, (digits ? normalizeDigits(e.target.value) : e.target.value) as never),
  });
  const reload = () => {
    void qc.invalidateQueries({ queryKey: ['company'] });
    void refresh();
  };
  return (
    <div className="page">
      <PageHeader title={t('settings.companyTitle')} actions={editable && (
        <Button variant="accent" pending={save.pending} onClick={() => save.run(() => api.put('/company', { ...v, amount_decimals: Number(v.amount_decimals) }).then(reload), t('common.saved'))}>{t('common.save')}</Button>
      )} />
      <div className="stack">
        {save.error && <Alert>{save.error}</Alert>}
        <div className="side-layout">
          <Panel title={t('settings.companyInfo')}>
            <div className="form-grid">
              <Field label={t('common.name')} required error={errors.name}><Input {...field('name')} /></Field>
              <Field label={t('common.nameEn')}><Input {...field('name_en')} dir="ltr" /></Field>
              <Field label={t('settings.legalName')}><Input {...field('legal_name')} /></Field>
              <Field label={t('parties.registrationNumber')}><Input {...field('registration_number', true)} /></Field>
              <Field label={t('parties.nationalId')} error={errors.national_id}><Input {...field('national_id', true)} className="num" /></Field>
              <Field label={t('parties.economicCode')} error={errors.economic_code}><Input {...field('economic_code', true)} className="num" /></Field>
              <Field label={t('parties.phone')}><Input {...field('phone', true)} dir="ltr" /></Field>
              <Field label={t('parties.email')} error={errors.email}><Input {...field('email')} dir="ltr" /></Field>
              <Field label={t('parties.postalCode')}><Input {...field('postal_code', true)} /></Field>
              <Field label={t('settings.website')}><Input {...field('website')} dir="ltr" /></Field>
              <Field label={t('parties.address')} className="span-all"><Input {...field('address')} /></Field>
            </div>
          </Panel>
          <div className="stack">
            <Panel title={t('settings.logo')}>
              <div className="stack">
                <img src={v.has_logo ? `/api/v1/company/logo?v=${Date.now()}` : mark} alt="" style={{ width: 96, height: 96, objectFit: 'contain', padding: 8, borderRadius: 12, background: 'var(--brand-abyss)' }} />
                <p className="field-help">{t('settings.logoHelp')}</p>
                {logo.error && <Alert>{logo.error}</Alert>}
                {editable && (
                  <div className="row">
                    <input ref={file} type="file" accept="image/png,image/jpeg,image/webp" hidden onChange={(e) => {
                      const fl = e.target.files?.[0];
                      if (!fl) return;
                      const fd = new FormData();
                      fd.set('file', fl);
                      void logo.run(() => api.put('/company/logo', fd).then(reload), t('common.saved'));
                      e.target.value = '';
                    }} />
                    <Button size="sm" pending={logo.pending} onClick={() => file.current?.click()}>{t('settings.uploadLogo')}</Button>
                    {v.has_logo && <Button size="sm" variant="quiet" onClick={() => logo.run(() => api.del('/company/logo').then(reload))}>{t('settings.removeLogo')}</Button>}
                  </div>
                )}
              </div>
            </Panel>
            <Panel title={t('settings.accountingPrefs')}>
              <div className="stack">
                <Field label={t('settings.calendar')}>
                  <Select value={v.calendar} disabled={!editable} onChange={(e) => set('calendar', e.target.value as Company['calendar'])}>
                    <option value="jalali">{t('setup.jalali')}</option>
                    <option value="gregorian">{t('setup.gregorian')}</option>
                  </Select>
                </Field>
                <Field label={t('settings.baseCurrency')} error={errors.base_currency}><Input {...field('base_currency')} dir="ltr" maxLength={3} /></Field>
                <Field label={t('settings.amountDecimals')}>
                  <Select value={v.amount_decimals} disabled={!editable} onChange={(e) => set('amount_decimals', Number(e.target.value))}>
                    <option value={0}>0</option><option value={1}>1</option><option value={2}>2</option>
                  </Select>
                </Field>
                <Checkbox label={t('settings.allowNegativeStock')} checked={v.allow_negative_stock} disabled={!editable} onChange={(x) => set('allow_negative_stock', x)} />
              </div>
            </Panel>
          </div>
        </div>
      </div>
    </div>
  );
}
