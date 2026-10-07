import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { FileBarChart, IdCard, Plus } from 'lucide-react';
import { api } from '@/api/client';
import type { PayrollPeriod } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, LinkButton } from '@/components/ui/Button';
import { Field, Input, Select } from '@/components/ui/Field';
import { Alert, Amount, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { EmptyState } from '@/components/ui/Feedback';
import { currentStep } from './workflow';
import { Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { ExportMenu } from '@/components/ExportMenu';
import { useSubmit } from '@/lib/useSubmit';
import { calendarParts, monthNames, todayIso } from '@/lib/dates';

export { monthNames };

/** Localised payroll month name, e.g. «مهر ۱۴۰۵» or «Mehr 1405». */
export function periodLabel(p: { year: number; month: number }, calendar: string, lang: string, digits: (s: string) => string) {
  return digits(`${monthNames(calendar, lang)[p.month - 1]} ${p.year}`);
}

export function PayrollPeriodsPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const q = useQuery({ queryKey: ['payroll-periods'], queryFn: () => api.get<PayrollPeriod[]>('/payroll/periods') });
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('payroll.overviewTitle'), to: '/payroll' }]} title={t('payroll.periodsTitle')} subtitle={t('payroll.periodsSubtitle')} actions={<>
        <LinkButton to="/payroll/employees" icon={<IdCard size={16} />}>{t('payroll.employeesTitle')}</LinkButton>
        <LinkButton to="/payroll/reports" icon={<FileBarChart size={16} />}>{t('payroll.reports.title')}</LinkButton>
        <ExportMenu name="payroll" permission="payroll.export" rows={q.data} columns={[
          { header: t('payroll.period'), value: (p) => periodLabel(p, f.calendar, lang, f.digits) },
          { header: t('common.status'), value: (p) => t(`payroll.statuses.${p.status as 'draft'}`) },
          { header: t('payroll.employeeCount'), value: (p) => p.employee_count, numeric: true },
          { header: t('payroll.gross'), value: (p) => p.gross_total, numeric: true },
          { header: t('payroll.deductions'), value: (p) => p.deduction_total, numeric: true },
          { header: t('payroll.net'), value: (p) => p.net_total, numeric: true },
          { header: t('payroll.employer'), value: (p) => p.employer_total, numeric: true },
        ]} />
        {can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('payroll.newPeriod')}</Button>}
      </>} />
      <Panel bodyless>
        <DataTable rows={q.data} loading={q.isLoading} error={q.error} onRetry={() => void q.refetch()} rowKey={(p) => p.id} onRowClick={(p) => navigate(`/payroll/periods/${p.id}`)}
          caption={t('payroll.periodsTitle')}
          empty={<EmptyState title={t('payroll.emptyPeriods')} body={t('payroll.emptyPeriodsBody')}
            action={can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('payroll.newPeriod')}</Button>} />}
          columns={[
            { key: 'name', header: t('payroll.period'), cell: (p) => <strong>{periodLabel(p, f.calendar, lang, f.digits)}</strong> },
            { key: 'range', header: t('common.date'), cell: (p) => <span className="num">{f.date(p.start_date)} – {f.date(p.end_date)}</span> },
            { key: 'n', header: t('payroll.employeeCount'), align: 'end', cell: (p) => <span className="num">{f.count(p.employee_count)}</span> },
            { key: 'gross', header: t('payroll.gross'), align: 'end', cell: (p) => <Amount value={p.gross_total} /> },
            { key: 'ded', header: t('payroll.deductions'), align: 'end', cell: (p) => <Amount value={p.deduction_total} /> },
            { key: 'net', header: t('payroll.net'), align: 'end', cell: (p) => <Amount value={p.net_total} /> },
            { key: 'emp', header: t('payroll.employer'), align: 'end', cell: (p) => <Amount value={p.employer_total} /> },
            { key: 'status', header: t('common.status'), cell: (p) => <StatusBadge status={p.status} /> },
            { key: 'next', header: t('ui.nextStep'), cell: (p) => { const s = currentStep(p); return s ? <span className="muted">{t(`payroll.steps.${s}`)}</span> : <span className="success-text">✓</span>; } },
          ]} />
      </Panel>
      {creating && <NewPeriodDialog onClose={() => setCreating(false)} onCreated={(p) => navigate(`/payroll/periods/${p.id}`)} />}
    </div>
  );
}

export function NewPeriodDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (p: PayrollPeriod) => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const now = calendarParts(todayIso(), f.calendar);
  const [v, setV] = useState({ year: String(now.y), month: String(now.m) });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const save = async () => {
    const p = await run(() => api.post<PayrollPeriod>('/payroll/periods', { year: Number(v.year), month: Number(v.month) }), t('common.saved'));
    if (p) onCreated(p);
  };
  return (
    <Dialog title={t('payroll.newPeriod')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={save}>{t('common.create')}</Button></>}>
      <div className="form-grid">
        {error && <div className="span-all"><Alert>{error}</Alert></div>}
        <Field label={t('payroll.year')} error={errors.year}><Input value={v.year} onChange={(e) => setV({ ...v, year: e.target.value })} inputMode="numeric" /></Field>
        <Field label={t('payroll.month')} error={errors.month}>
          <Select value={v.month} onChange={(e) => setV({ ...v, month: e.target.value })}>
            {monthNames(f.calendar, lang).map((m, i) => <option key={m} value={i + 1}>{m}</option>)}
          </Select>
        </Field>
      </div>
    </Dialog>
  );
}
