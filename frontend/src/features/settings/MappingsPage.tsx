import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { Mapping } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Field } from '@/components/ui/Field';
import { Alert, LoadingBlock, PageHeader, Panel } from '@/components/ui/Misc';
import { AccountPicker } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';

export function MappingsPage() {
  const { t, tryT } = useI18n();
  const { can } = useAuth();
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['mappings'], queryFn: () => api.get<Mapping[]>('/company/account-mappings') });
  const [values, setValues] = useState<Record<string, number | null>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  useEffect(() => {
    if (q.data) setValues(Object.fromEntries(q.data.map((m) => [m.key, m.account_id])));
  }, [q.data]);
  if (!q.data) return <LoadingBlock />;
  const editable = can('settings.edit');
  const save = () => {
    const body = Object.fromEntries(Object.entries(values).filter(([, v]) => v));
    return run(() => api.put('/company/account-mappings', body).then(() => qc.invalidateQueries({ queryKey: ['mappings'] })), t('common.saved'));
  };
  return (
    <div className="page">
      <PageHeader title={t('settings.mappingsTitle')} subtitle={t('settings.mappingsIntro')}
        actions={editable && <Button variant="accent" pending={pending} onClick={save}>{t('common.save')}</Button>} />
      <Panel>
        <div className="stack">
          {error && <Alert>{error}</Alert>}
          <div className="form-grid" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))' }}>
            {q.data.map((m) => (
              <Field key={m.key} label={tryT(`settings.mappingKeys.${m.key}`) ?? m.key} error={errors[m.key]}
                help={m.allowed_types.map((x) => tryT(`accountTypes.${x}`)).join(' / ')}>
                <AccountPicker
                  includeGroups={m.parent}
                  value={values[m.key] ?? null}
                  onChange={(id) => setValues({ ...values, [m.key]: id })}
                  filter={(a) => m.allowed_types.includes(a.type) && (m.parent ? a.level < 4 : a.postable)}
                />
              </Field>
            ))}
          </div>
        </div>
      </Panel>
    </div>
  );
}
