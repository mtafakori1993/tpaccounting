import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Lock, LockOpen, Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { FiscalYear } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, IconButton } from '@/components/ui/Button';
import { Field, Input } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { Alert, Badge, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { useSubmit } from '@/lib/useSubmit';
import { addDaysIso, calendarParts, yearRange } from '@/lib/dates';

export function FiscalYearsPage() {
  const { t } = useI18n();
  const f = useFormat();
  const { can, refresh } = useAuth();
  const qc = useQueryClient();
  const years = useQuery({ queryKey: ['fiscal-years'], queryFn: () => api.get<FiscalYear[]>('/company/fiscal-years') });
  const [selected, setSelected] = useState<number | null>(null);
  const sel = selected ?? years.data?.[0]?.id ?? null;
  const detail = useQuery({ queryKey: ['fiscal-year', sel], queryFn: () => api.get<FiscalYear>(`/company/fiscal-years/${sel}`), enabled: !!sel });
  const [creating, setCreating] = useState(false);
  const [confirm, setConfirm] = useState<'close' | 'reopen' | null>(null);
  const action = useSubmit();
  const editable = can('company.edit');
  const reload = () => {
    void qc.invalidateQueries({ queryKey: ['fiscal-years'] });
    void qc.invalidateQueries({ queryKey: ['fiscal-year'] });
    void refresh();
  };
  const fy = detail.data;
  return (
    <div className="page">
      <PageHeader title={t('settings.fiscalYearsTitle')} actions={editable && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('settings.newFiscalYear')}</Button>} />
      <div className="side-layout" style={{ gridTemplateColumns: '360px minmax(0, 1fr)' }}>
        <Panel bodyless>
          <DataTable rows={years.data} loading={years.isLoading} rowKey={(y) => y.id} onRowClick={(y) => setSelected(y.id)}
            rowClassName={(y) => (y.id === sel ? 'selected-row' : undefined)}
            columns={[
              { key: 'name', header: t('common.name'), cell: (y) => <b className="num">{f.digits(y.name)}</b> },
              { key: 'range', header: `${t('common.from')} – ${t('common.to')}`, cell: (y) => <span className="num">{f.date(y.start_date)} – {f.date(y.end_date)}</span> },
              { key: 's', header: t('common.status'), cell: (y) => <StatusBadge status={y.status} /> },
            ]} />
        </Panel>
        {fy && (
          <Panel bodyless title={<>{t('common.fiscalYear')} {f.digits(fy.name)} <StatusBadge status={fy.status} /></>}
            actions={editable && (fy.status === 'open'
              ? <Button size="sm" onClick={() => setConfirm('close')}>{t('settings.closeYear')}</Button>
              : <Button size="sm" onClick={() => setConfirm('reopen')}>{t('settings.reopenYear')}</Button>)}>
            {action.error && <div style={{ padding: 16 }}><Alert>{action.error}</Alert></div>}
            <DataTable rows={fy.periods} rowKey={(p) => p.id} columns={[
              { key: 'seq', header: '#', cell: (p) => <span className="num">{f.int(p.seq)}</span>, width: 50 },
              { key: 'name', header: t('settings.periods'), cell: (p) => f.digits(p.name) },
              { key: 'range', header: `${t('common.from')} – ${t('common.to')}`, cell: (p) => <span className="num">{f.date(p.start_date)} – {f.date(p.end_date)}</span> },
              { key: 'lock', header: t('common.status'), cell: (p) => (p.is_locked ? <Badge tone="warning">{t('settings.locked')}</Badge> : <Badge tone="success">{t('status.open')}</Badge>) },
              {
                key: 'x', header: '', width: 50, cell: (p) => editable && fy.status === 'open' && (
                  <IconButton size="sm" label={p.is_locked ? t('settings.unlock') : t('settings.lock')} icon={p.is_locked ? <LockOpen size={14} /> : <Lock size={14} />}
                    onClick={() => action.run(() => api.put(`/company/periods/${p.id}`, { is_locked: !p.is_locked }).then(reload))} />
                ),
              },
            ]} />
          </Panel>
        )}
      </div>
      {creating && <NewYearDialog last={years.data?.[0]} onClose={() => setCreating(false)} onSaved={() => { setCreating(false); reload(); }} />}
      {confirm && fy && (
        <ConfirmDialog
          title={confirm === 'close' ? t('settings.closeYearTitle', { name: f.digits(fy.name) }) : t('settings.reopenYear')}
          body={confirm === 'close' ? t('settings.closeYearHelp') : ''}
          confirmLabel={confirm === 'close' ? t('settings.closeYear') : t('settings.reopenYear')}
          pending={action.pending} error={action.error} onClose={() => setConfirm(null)}
          onConfirm={async () => {
            if (await action.run(() => api.post(`/company/fiscal-years/${fy.id}/${confirm}`, {}).then(() => true), t('common.saved'))) {
              setConfirm(null);
              reload();
            }
          }} />
      )}
    </div>
  );
}

function NewYearDialog({ last, onClose, onSaved }: { last?: FiscalYear; onClose: () => void; onSaved: () => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const start = last ? addDaysIso(last.end_date.slice(0, 10), 1) : yearRange(new Date().toISOString().slice(0, 10), f.calendar).from;
  const range = yearRange(start, f.calendar);
  const [v, setV] = useState({ name: String(calendarParts(start, f.calendar).y), start_date: start, end_date: range.to });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  return (
    <Dialog title={t('settings.newFiscalYear')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={async () => { if (await run(() => api.post('/company/fiscal-years', v).then(() => true), t('common.saved'))) onSaved(); }}>{t('common.create')}</Button></>}>
      <div className="form-grid">
        {error && <div className="span-all"><Alert>{error}</Alert></div>}
        <Field label={t('common.name')} required error={errors.name}><Input value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
        <Field label={t('common.from')} required error={errors.start_date}><DateField value={v.start_date} onChange={(x) => setV({ ...v, start_date: x })} /></Field>
        <Field label={t('common.to')} required error={errors.end_date}><DateField value={v.end_date} onChange={(x) => setV({ ...v, end_date: x })} /></Field>
      </div>
    </Dialog>
  );
}
