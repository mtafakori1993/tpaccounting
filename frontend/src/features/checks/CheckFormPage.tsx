import { useQueryClient } from '@tanstack/react-query';
import { Navigate, useNavigate, useSearchParams } from 'react-router-dom';
import { ArrowDownToLine, ArrowUpFromLine } from 'lucide-react';
import type { CheckType } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { Button } from '@/components/ui/Button';
import { Alert, PageHeader, Panel, Tabs } from '@/components/ui/Misc';
import { useToast } from '@/components/ui/Toast';
import { CheckFormBody, checkQueryKeys, useCheckForm } from './common';

/** /checks/new?type=received|issued — registering a check on its own page. */
export function CheckFormPage() {
  const [sp, setSp] = useSearchParams();
  const { can } = useAuth();
  const type: CheckType = sp.get('type') === 'issued' ? 'issued' : 'received';
  // Registering a received check posts it, so it needs checks.post too.
  if (!can('checks.create') || (type === 'received' && !can('checks.post'))) return <Navigate to="/checks" replace />;
  return <CheckForm key={type} type={type} onType={(x) => setSp({ type: x }, { replace: true })} />;
}

function CheckForm({ type, onType }: { type: CheckType; onType: (t: CheckType) => void }) {
  const { t } = useI18n();
  const { can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const form = useCheckForm(type);
  const save = async () => {
    const c = await form.save();
    if (c) {
      for (const k of checkQueryKeys) void qc.invalidateQueries({ queryKey: k });
      toast.success(t('common.saved'));
      navigate(`/checks/${c.id}`, { replace: true });
    }
  };
  return (
    <div className="page" style={{ maxWidth: 1080 }}>
      <PageHeader
        crumbs={[{ label: t('checks.listTitle'), to: '/checks' }]}
        title={type === 'received' ? t('checks.formTitleReceived') : t('checks.formTitleIssued')}
        subtitle={t('checks.formSubtitle')}
      />
      <Panel bodyless>
        <Tabs<CheckType> label={t('checks.typeChoice')} value={type} onChange={onType}
          tabs={[
            ...(can('checks.post') ? [{ key: 'received' as const, label: <><ArrowDownToLine size={15} /> {t('checks.types.received')}</> }] : []),
            { key: 'issued', label: <><ArrowUpFromLine size={15} /> {t('checks.types.issued')}</> },
          ]} />
        <form className="panel-body" onSubmit={(e) => { e.preventDefault(); void save(); }}>
          {form.error && <div className="section-gap"><Alert>{form.error}</Alert></div>}
          <CheckFormBody type={type} form={form} />
          <div className="form-actions">
            <Button onClick={() => navigate(-1)}>{t('common.cancel')}</Button>
            <Button type="submit" variant="accent" pending={form.pending}>{t('checks.saveAndView')}</Button>
          </div>
        </form>
      </Panel>
    </div>
  );
}
