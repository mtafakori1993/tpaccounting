import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { AlertTriangle, CheckCircle2, Printer, Trash2, Upload, Wallet } from 'lucide-react';
import { api } from '@/api/client';
import type { AuditEntry, Page, PaymentBatch, Payslip, PayrollInput, PayrollLiability, PayrollPeriod } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button } from '@/components/ui/Button';
import { Field, MoneyInput, NumericInput, Select } from '@/components/ui/Field';
import { DateField } from '@/components/ui/DateField';
import { ActionMenu, Alert, Badge, KV, Money, PageHeader, Panel, StatusBadge, Tabs } from '@/components/ui/Misc';
import { ConfirmDialog, Dialog } from '@/components/ui/Dialog';
import { DataTable } from '@/components/ui/DataTable';
import { EmptyState, ErrorState, SkeletonPage } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { Stepper, Timeline } from '@/components/ui/Steps';
import { ExportMenu } from '@/components/ExportMenu';
import { ServerExport } from '@/components/ServerExport';
import { Attachments } from '@/components/Attachments';
import { TreasurySelect } from '@/components/pickers/Pickers';
import { useSubmit } from '@/lib/useSubmit';
import { usePdf } from '@/lib/pdf';
import { add } from '@/lib/decimal';
import { todayIso } from '@/lib/dates';
import { componentName, payslipDocument, useSalaryComponents } from './common';
import { periodLabel } from './PayrollPeriodsPage';
import { actionPermission, currentStep, stepAction, stepStates, type WorkflowStep } from './workflow';
import { AttendanceImportDialog } from './AttendanceImport';

const attendance = ['working_days', 'working_hours', 'absence_days', 'unpaid_leave_days', 'paid_leave_days', 'overtime_hours', 'mission_days', 'holiday_work_hours'] as const;
const attendanceLabel = {
  working_days: 'payroll.workingDays', working_hours: 'payroll.workingHours', absence_days: 'payroll.absenceDays', unpaid_leave_days: 'payroll.unpaidLeaveDays',
  paid_leave_days: 'payroll.paidLeaveDays', overtime_hours: 'payroll.overtimeHours', mission_days: 'payroll.missionDays', holiday_work_hours: 'payroll.holidayWorkHours',
} as const;
type Tab = 'inputs' | 'payslips' | 'warnings' | 'liabilities' | 'payments' | 'history';
const reverseActions = ['reopen', 'unpost', 'unpay'];

/** /payroll/periods/:id — one payroll month, driven by its workflow. */
export function PayrollPeriodPage() {
  const { id } = useParams();
  const pid = Number(id);
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const printer = usePdf();
  const [sp, setSp] = useSearchParams();
  const [action, setAction] = useState<string | null>(null);
  const [slip, setSlip] = useState<number | null>(null);
  const q = useQuery({ queryKey: ['payroll-period', pid], queryFn: () => api.get<PayrollPeriod>(`/payroll/periods/${pid}`) });
  const slips = useQuery({ queryKey: ['payroll-period', pid, 'slips'], queryFn: () => api.get<Payslip[]>(`/payroll/periods/${pid}/payslips`) });
  const liabilities = useQuery({ queryKey: ['payroll-period', pid, 'liabilities'], queryFn: () => api.get<PayrollLiability[]>('/payroll/liabilities', { period_id: pid }) });
  const p = q.data;
  const defaultTab: Tab = !p ? 'inputs' : p.status === 'draft' ? 'inputs' : p.status === 'calculated' && p.warnings.length ? 'warnings' : p.status === 'posted' || p.status === 'paid' ? 'liabilities' : 'payslips';
  const tab = (sp.get('tab') as Tab) || defaultTab;
  const setTab = (k: Tab) => setSp({ tab: k }, { replace: true });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['payroll-period', pid] });
    void qc.invalidateQueries({ queryKey: ['payroll-periods'] });
    void qc.invalidateQueries({ queryKey: ['payroll-liabilities'] });
  };
  if (q.error) return <div className="page"><Panel bodyless><ErrorState error={q.error} onRetry={() => void q.refetch()} /></Panel></div>;
  if (!p) return <div className="page"><SkeletonPage kpis={4} /></div>;

  const name = periodLabel(p, f.calendar, lang, f.digits);
  const step = currentStep(p);
  const steps = stepStates(p, (s) => t(`payroll.steps.${s}`), {
    calculate: p.calculated_at ? f.date(p.calculated_at.slice(0, 10)) : undefined,
    approve: p.approved_at ? f.date(p.approved_at.slice(0, 10)) : undefined,
    post: p.posted_at ? f.date(p.posted_at.slice(0, 10)) : undefined,
    pay: p.paid_at ? f.date(p.paid_at.slice(0, 10)) : undefined,
    close: p.closed_at ? f.date(p.closed_at.slice(0, 10)) : undefined,
    warnings: p.status !== 'draft' && p.warnings.length ? t('payroll.warningCount', { count: f.count(p.warnings.length) }) : undefined,
  });
  const next = step ? stepAction[step] : undefined;
  const canNext = next && p.allowed_actions.includes(next) && can(actionPermission[next]);
  const reversals = p.allowed_actions.filter((a) => reverseActions.includes(a) && can(actionPermission[a]));
  const del = (p.status === 'draft' || p.status === 'calculated') && can('payroll.delete');
  const openLiab = (liabilities.data ?? []).filter((l) => l.status !== 'paid');
  const basis = p.calc_basis ?? {};
  return (
    <div className="page">
      <PageHeader
        crumbs={[{ label: t('payroll.overviewTitle'), to: '/payroll' }, { label: t('payroll.periodsTitle'), to: '/payroll/periods' }]}
        title={<span className="row" style={{ gap: 12 }}>{`${t('payroll.title')} — ${name}`} <StatusBadge status={p.status} /></span>}
        subtitle={<span className="num">{f.date(p.start_date)} – {f.date(p.end_date)}</span>}
        actions={<>
          {slips.data && slips.data.length > 0 && <Button icon={<Printer size={16} />} pending={printer.pending} onClick={() => printer.print(() => payslipDocument(slips.data!, t, f, lang))}>{t('payroll.allSlips')}</Button>}
          <ActionMenu items={[
            ...reversals.map((a) => ({ label: t(`payroll.actions.${a as 'post'}`), danger: true, onClick: () => setAction(a) })),
            ...(del ? [{ label: t('common.delete'), danger: true, icon: <Trash2 size={16} />, onClick: () => setAction('delete') }] : []),
          ]} />
        </>}
      />

      <KpiGrid count={4}>
        <Kpi label={t('payroll.net')} value={f.money(p.net_total)} sub={f.currencyLabel} tone="primary" />
        <Kpi label={t('payroll.gross')} value={f.money(p.gross_total)} sub={t('payroll.deductions') + ': ' + f.money(p.deduction_total)} />
        <Kpi label={t('payroll.employer')} value={f.money(p.employer_total)} sub={t('payroll.totalCost') + ': ' + f.money(add(p.gross_total, p.employer_total))} />
        <Kpi label={t('payroll.employeeCount')} value={f.count(p.employee_count)}
          sub={openLiab.length ? t('payroll.openLiabilities') + ': ' + f.money(openLiab.reduce((s, l) => add(s, l.outstanding), '0')) : undefined}
          tone={openLiab.length ? 'warning' : 'neutral'} />
      </KpiGrid>

      <Panel bodyless className="section-gap">
        <Stepper steps={steps} label={t('payroll.title')} />
        <div className="next-step">
          <div className="ns-text">
            <div className="ns-title">{step ? t('ui.nextStep') + ': ' + t(`payroll.steps.${step}`) : t('ui.allDone')}</div>
            <div className="ns-body">{t(`payroll.stepHelp.${(step ?? 'done') as WorkflowStep}`)}</div>
          </div>
          <div className="row">
            {step === 'inputs' && can('payroll.edit') && <Button icon={<Upload size={16} />} onClick={() => setAction('import')}>{t('payroll.importTitle')}</Button>}
            {step === 'warnings' && <Button icon={<AlertTriangle size={16} />} onClick={() => setTab('warnings')}>{t('payroll.tabs.warnings')}</Button>}
            {step === 'pay' && can('payroll.export') && <Button onClick={() => setTab('payments')}>{t('payroll.tabs.payments')}</Button>}
            {p.status === 'calculated' && can('payroll.calculate') && <Button onClick={() => setAction('calculate')}>{t('payroll.actions.calculate')}</Button>}
            {canNext && <Button variant="accent" onClick={() => setAction(next!)}>{t(`payroll.actions.${next as 'post'}`)}</Button>}
          </div>
        </div>
      </Panel>

      <div className="grid-main">
        <Panel bodyless>
          <Tabs<Tab> value={tab} onChange={setTab} label={t('payroll.title')} tabs={[
            { key: 'inputs', label: t('payroll.tabs.inputs') },
            { key: 'payslips', label: t('payroll.tabs.payslips'), count: slips.data?.length ? f.count(slips.data.length) : undefined },
            { key: 'warnings', label: t('payroll.tabs.warnings'), count: p.warnings.length ? f.count(p.warnings.length) : undefined, tone: 'warning' },
            { key: 'liabilities', label: t('payroll.tabs.liabilities'), count: openLiab.length ? f.count(openLiab.length) : undefined, tone: 'danger' },
            { key: 'payments', label: t('payroll.tabs.payments') },
            { key: 'history', label: t('payroll.tabs.history') },
          ]} />
          {tab === 'inputs' && <Inputs period={p} onSaved={refresh} onImport={() => setAction('import')} />}
          {tab === 'payslips' && (
            <>
              <div className="table-tools">
                <ExportMenu name={`payroll-${p.year}-${p.month}`} permission="payroll.export" title={`${t('payroll.payslipsTitle')} — ${p.name}`} rows={slips.data} columns={[
                  { header: t('payroll.code'), value: (s) => s.employee_code },
                  { header: t('payroll.employee'), value: (s) => s.employee_name },
                  { header: t('payroll.workingDays'), value: (s) => s.working_days, numeric: true },
                  { header: t('payroll.gross'), value: (s) => s.gross, numeric: true },
                  { header: t('payroll.deductions'), value: (s) => s.total_deductions, numeric: true },
                  { header: t('payroll.net'), value: (s) => s.net, numeric: true },
                  { header: t('payroll.employer'), value: (s) => s.employer_cost, numeric: true },
                  { header: t('payroll.iban'), value: (s) => s.iban },
                ]} />
              </div>
              <DataTable rows={slips.data} loading={slips.isLoading} error={slips.error} onRetry={() => void slips.refetch()} rowKey={(s) => s.id} onRowClick={(s) => setSlip(s.id)} showFooter
                caption={t('payroll.payslipsTitle')}
                empty={<EmptyState title={t('payroll.payslipsTitle')} body={t('payroll.stepHelp.calculate')} />}
                columns={[
                  { key: 'code', header: t('payroll.code'), cell: (s) => <span className="num">{f.digits(s.employee_code)}</span>, width: 90 },
                  { key: 'name', header: t('payroll.employee'), cell: (s) => s.employee_name, footer: t('common.total') },
                  { key: 'days', header: t('payroll.workingDays'), align: 'end', cell: (s) => <span className="num">{f.qty(s.working_days)}</span> },
                  { key: 'gross', header: t('payroll.gross'), align: 'end', cell: (s) => <Money value={s.gross} />, footer: <Money value={p.gross_total} /> },
                  { key: 'ded', header: t('payroll.deductions'), align: 'end', cell: (s) => <Money value={s.total_deductions} />, footer: <Money value={p.deduction_total} /> },
                  { key: 'net', header: t('payroll.net'), align: 'end', cell: (s) => <strong><Money value={s.net} /></strong>, footer: <Money value={p.net_total} /> },
                  { key: 'emp', header: t('payroll.employer'), align: 'end', cell: (s) => <Money value={s.employer_cost} />, footer: <Money value={p.employer_total} /> },
                ]} />
            </>
          )}
          {tab === 'warnings' && <Warnings period={p} />}
          {tab === 'liabilities' && <Liabilities period={p} rows={liabilities.data} loading={liabilities.isLoading} error={liabilities.error} onPaid={refresh} />}
          {tab === 'payments' && <Payments period={p} />}
          {tab === 'history' && <History period={p} />}
        </Panel>
        <div className="stack">
          <Panel title={t('payroll.calcBasis')}>
            {p.status === 'draft' && !basis.day_basis ? <span className="muted">{t('payroll.stepHelp.calculate')}</span> : (
              <dl className="dl-grid">
                <dt>{t('payroll.dayBasis')}</dt>
                <dd>{basis.day_basis ? t(`payroll.dayBases.${basis.day_basis}`, { days: f.number(basis.period_days ?? basis.calendar_days ?? '', 2) }) : '—'}</dd>
                <dt>{t('payroll.divisor')}</dt><dd className="num">{f.number(basis.period_days ?? '', 2)}</dd>
                <dt>{t('payroll.monthlyHoursUsed')}</dt><dd className="num">{f.number(basis.monthly_hours ?? '', 2)}</dd>
                <dt>{t('payroll.taxTableUsed')}</dt>
                <dd>{basis.tax_table_name ? <Link to="/payroll/settings">{basis.tax_table_name}</Link> : <span className="danger-text">{t('payroll.noneConfigured')}</span>}</dd>
                <dt>{t('payroll.insuranceRuleUsed')}</dt>
                <dd>{basis.insurance_rule_name ? <Link to="/payroll/settings">{basis.insurance_rule_name}</Link> : <span className="danger-text">{t('payroll.noneConfigured')}</span>}</dd>
              </dl>
            )}
          </Panel>
          <Panel title={t('common.details')}>
            <KV items={[
              { label: t('payroll.journal'), value: p.journal_entry_id && <Link to={`/journal/${p.journal_entry_id}`} className="num">{f.digits(String(p.journal_number ?? ''))}</Link> },
              { label: t('payroll.paymentEntry'), value: p.payment_entry_id && <Link to={`/journal/${p.payment_entry_id}`} className="num">{f.digits(String(p.payment_number ?? ''))}</Link> },
              { label: t('payroll.paymentAccount'), value: p.treasury_account_name },
              { label: t('payroll.paymentDate'), value: p.payment_date && <span className="num">{f.date(p.payment_date)}</span> },
              { label: t('status.approved'), value: p.approved_by_name && `${p.approved_by_name} — ${f.dateTime(p.approved_at)}` },
            ]} />
          </Panel>
          <Attachments entity="payroll_period" id={p.id} canEdit={can('payroll.edit')} />
        </div>
      </div>

      {action === 'delete' && (
        <ConfirmDialog title={t('common.delete')} body={t('common.confirmDelete')} confirmLabel={t('common.yesDelete')} negative onClose={() => setAction(null)}
          onConfirm={() => void api.del(`/payroll/periods/${p.id}`).then(() => { void qc.invalidateQueries({ queryKey: ['payroll-periods'] }); navigate('/payroll/periods'); })} />
      )}
      {action === 'import' && <AttendanceImportDialog period={p} onClose={() => setAction(null)} onImported={() => { refresh(); void qc.invalidateQueries({ queryKey: ['payroll-period', pid, 'inputs'] }); }} />}
      {action && !['delete', 'import'].includes(action) && <ActionDialog period={p} action={action} onClose={() => setAction(null)} onDone={() => { setAction(null); refresh(); setSp({}, { replace: true }); }} />}
      {slip && <PayslipDialog id={slip} onClose={() => setSlip(null)} />}
    </div>
  );
}

function ActionDialog({ period, action, onClose, onDone }: { period: PayrollPeriod; action: string; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const [v, setV] = useState({ date: action === 'pay' ? todayIso() : '', treasury_account_id: null as number | null });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const go = async () => {
    const ok = await run(() => api.post(`/payroll/${period.id}/${action}`, { ...v, date: v.date || null }).then(() => true), t('common.saved'));
    if (ok) onDone();
  };
  const reverse = reverseActions.includes(action);
  return (
    <Dialog title={`${t(`payroll.actions.${action as 'post'}`)} — ${periodLabel(period, f.calendar, lang, f.digits)}`} description={t(`payroll.actionHelp.${action as 'post'}`)} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant={reverse ? 'negative' : 'accent'} pending={pending} onClick={go}>{t(`payroll.actions.${action as 'post'}`)}</Button></>}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        {(action === 'post' || action === 'pay') && (
          <dl className="dl-grid">
            <dt>{t('payroll.gross')}</dt><dd><Money value={period.gross_total} /></dd>
            <dt>{t('payroll.net')}</dt><dd><Money value={period.net_total} currency /></dd>
            <dt>{t('payroll.employer')}</dt><dd><Money value={period.employer_total} /></dd>
          </dl>
        )}
        {action === 'pay' && (
          <div className="form-grid">
            <Field label={t('payroll.paymentAccount')} required error={errors.treasury_account_id} className="span-2"><TreasurySelect value={v.treasury_account_id} onChange={(x) => setV({ ...v, treasury_account_id: x })} /></Field>
            <Field label={t('payroll.paymentDate')} error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          </div>
        )}
        {(action === 'unpost' || action === 'unpay') && (
          <Field label={t('common.date')} help={t('journal.cancelHelp')} error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
        )}
      </div>
    </Dialog>
  );
}

function Inputs({ period, onSaved, onImport }: { period: PayrollPeriod; onSaved: () => void; onImport: () => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const comps = useSalaryComponents();
  const q = useQuery({ queryKey: ['payroll-period', period.id, 'inputs'], queryFn: () => api.get<PayrollInput[]>(`/payroll/periods/${period.id}/inputs`) });
  const [rows, setRows] = useState<PayrollInput[]>([]);
  const [dirty, setDirty] = useState(false);
  const { run, pending, error } = useSubmit();
  useEffect(() => { if (q.data) { setRows(q.data); setDirty(false); } }, [q.data]);
  const editable = (period.status === 'draft' || period.status === 'calculated') && can('payroll.edit');
  const manual = comps.data?.filter((c) => c.method === 'manual' && c.is_active) ?? [];
  const setField = (i: number, k: string, v: string) => { setDirty(true); setRows(rows.map((r, j) => (j === i ? { ...r, [k]: v } : r))); };
  const setItem = (i: number, cid: number, v: string) => { setDirty(true); setRows(rows.map((r, j) => (j === i ? { ...r, items: [...r.items.filter((x) => x.component_id !== cid), { component_id: cid, amount: v }] } : r))); };
  const save = () => run(() => api.put(`/payroll/periods/${period.id}/inputs`, rows.map((r) => ({
    ...r, ...Object.fromEntries(attendance.map((k) => [k, r[k] || '0'])), items: r.items.filter((x) => x.amount).map((x) => ({ component_id: x.component_id, amount: x.amount || '0' })),
  }))).then(() => { void q.refetch(); onSaved(); }), t('payroll.inputsSaved'));
  const source = (r: PayrollInput) => (r.source.startsWith('import') ? <Badge tone="info">{t('payroll.importHistory')}</Badge> : null);
  return (
    <div className="stack" style={{ padding: 16 }}>
      {editable && (
        <div className="row-between">
          <span className="muted">{t('payroll.stepHelp.inputs')}</span>
          <div className="row">
            <Button icon={<Upload size={16} />} onClick={onImport}>{t('payroll.importTitle')}</Button>
            <Button variant="accent" pending={pending} disabled={!dirty} onClick={save}>{t('payroll.saveInputs')}</Button>
          </div>
        </div>
      )}
      {error && <Alert>{error}</Alert>}
      <DataTable rows={rows} loading={q.isLoading} error={q.error} onRetry={() => void q.refetch()} rowKey={(r) => r.employee_id} caption={t('payroll.inputsTitle')}
        empty={<EmptyState title={t('payroll.emptyEmployees')} body={t('payroll.emptyEmployeesBody')} action={<Link className="btn btn-secondary" to="/payroll/employees">{t('payroll.employeesTitle')}</Link>} />}
        columns={[
          { key: 'emp', header: t('payroll.employee'), cell: (r) => <span>{r.employee_name} <span className="muted num">{f.digits(r.employee_code)}</span> {source(r)}</span> },
          ...attendance.map((k) => ({
            key: k, header: t(attendanceLabel[k]), align: 'end' as const,
            cell: (r: PayrollInput, i: number) => (editable ? <NumericInput className="grid-input" fraction={2} value={String(r[k] ?? '')} onChange={(x) => setField(i, k, x)} aria-label={`${t(attendanceLabel[k])} — ${r.employee_name}`} /> : <span className="num">{f.qty(r[k] ?? '0')}</span>),
          })),
          ...manual.map((c) => ({
            key: `c${c.id}`, header: componentName(c, lang), align: 'end' as const,
            cell: (r: PayrollInput, i: number) => {
              const v = r.items.find((x) => x.component_id === c.id)?.amount ?? '';
              return editable ? <MoneyInput className="grid-input" value={v} onChange={(x) => setItem(i, c.id, x)} aria-label={`${componentName(c, lang)} — ${r.employee_name}`} /> : <Money value={v || '0'} blankZero />;
            },
          })),
        ]} />
    </div>
  );
}

function Warnings({ period }: { period: PayrollPeriod }) {
  const { t, tryT } = useI18n();
  if (period.status === 'draft') return <EmptyState compact title={t('payroll.tabs.warnings')} body={t('payroll.stepHelp.calculate')} />;
  if (!period.warnings.length) return <EmptyState compact icon={<CheckCircle2 size={22} />} title={t('payroll.noWarnings')} />;
  const general = period.warnings.filter((w) => !w.employee_id);
  const perEmployee = period.warnings.filter((w) => w.employee_id);
  return (
    <div className="stack" style={{ padding: 16 }}>
      {general.map((w, i) => <Alert key={i} kind="warning">{tryT(`payroll.warnings.${w.code}`) ?? w.code} {w.code.startsWith('no_') && <Link to="/payroll/settings">{t('payroll.settingsTitle')}</Link>}</Alert>)}
      {perEmployee.length > 0 && (
        <DataTable rows={perEmployee} rowKey={(w, i) => `${w.code}-${i}`} dense caption={t('payroll.tabs.warnings')}
          columns={[
            { key: 'e', header: t('payroll.employee'), cell: (w) => w.employee },
            { key: 'm', header: t('payroll.problem'), cell: (w) => tryT(`payroll.warningProblems.${w.code}`) ?? w.code },
            { key: 'a', header: '', align: 'end', cell: (w) => (w.code === 'missing_bank_account' ? <Link to={`/payroll/employees?edit=${w.employee_id}`}>{t('common.edit')}</Link> : null) },
          ]} />
      )}
    </div>
  );
}

function Liabilities({ period, rows, loading, error, onPaid }: { period: PayrollPeriod; rows?: PayrollLiability[]; loading: boolean; error: unknown; onPaid: () => void }) {
  const { t } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const [paying, setPaying] = useState<PayrollLiability | null>(null);
  const canPay = can('payroll.pay') && can('treasury.post');
  if (!['posted', 'paid', 'closed'].includes(period.status)) return <EmptyState compact title={t('payroll.liabilitiesTitle')} body={t('payroll.noLiabilities')} />;
  const total = (k: 'amount' | 'paid' | 'outstanding') => (rows ?? []).reduce((s, l) => add(s, l[k]), '0');
  return (
    <>
      <div style={{ padding: '12px 20px 0' }} className="muted">{t('payroll.liabilitiesHelp')}</div>
      <DataTable rows={rows} loading={loading} error={error} rowKey={(l) => l.id} showFooter caption={t('payroll.liabilitiesTitle')}
        columns={[
          { key: 'kind', header: t('common.type'), cell: (l) => t(`payroll.liabilityKinds.${l.kind}`), footer: t('common.total') },
          { key: 'acc', header: t('payroll.account'), cell: (l) => <span><span className="muted num">{f.digits(l.account_code)}</span> {l.account_name}</span> },
          { key: 'amount', header: t('common.amount'), align: 'end', cell: (l) => <Money value={l.amount} />, footer: <Money value={total('amount')} /> },
          { key: 'paid', header: t('payroll.paid'), align: 'end', cell: (l) => <Money value={l.paid} blankZero />, footer: <Money value={total('paid')} /> },
          { key: 'out', header: t('payroll.outstanding'), align: 'end', cell: (l) => <strong><Money value={l.outstanding} blankZero /></strong>, footer: <Money value={total('outstanding')} /> },
          { key: 'st', header: t('common.status'), cell: (l) => <Badge tone={l.status === 'paid' ? 'success' : l.status === 'partial' ? 'info' : 'warning'}>{t(`payroll.liabilityStatus.${l.status}`)}</Badge> },
          { key: 'pay', header: '', align: 'end', cell: (l) => (l.status !== 'paid' && canPay ? <Button size="sm" icon={<Wallet size={14} />} onClick={() => setPaying(l)}>{t('payroll.payLiability')}</Button> : null) },
        ]} />
      {paying && <LiabilityPayDialog liability={paying} period={period} onClose={() => setPaying(null)} onDone={() => { setPaying(null); onPaid(); }} />}
    </>
  );
}

function LiabilityPayDialog({ liability, period, onClose, onDone }: { liability: PayrollLiability; period: PayrollPeriod; onClose: () => void; onDone: () => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const today = todayIso();
  const [v, setV] = useState({ treasury_account_id: null as number | null, amount: liability.outstanding, date: today < period.end_date ? period.end_date : today, reference: '' });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const { run, pending, error } = useSubmit(setErrors);
  const go = async () => {
    const ok = await run(() => api.post(`/payroll/liabilities/${liability.id}/pay`, v).then(() => true), t('payroll.liabilityPaid'));
    if (ok) onDone();
  };
  return (
    <Dialog title={t('payroll.payLiabilityTitle', { kind: t(`payroll.liabilityKinds.${liability.kind}`), period: periodLabel(period, f.calendar, lang, f.digits) })} description={t('payroll.liabilitiesHelp')} onClose={onClose}
      footer={<><Button onClick={onClose}>{t('common.cancel')}</Button><Button variant="accent" pending={pending} onClick={go}>{t('payroll.payLiability')}</Button></>}>
      <div className="stack">
        {error && <Alert>{error}</Alert>}
        <dl className="dl-grid">
          <dt>{t('common.amount')}</dt><dd><Money value={liability.amount} /></dd>
          <dt>{t('payroll.paid')}</dt><dd><Money value={liability.paid} /></dd>
          <dt>{t('payroll.outstanding')}</dt><dd><Money value={liability.outstanding} currency /></dd>
        </dl>
        <div className="form-grid">
          <Field label={t('payroll.payFrom')} required error={errors.treasury_account_id} className="span-2"><TreasurySelect value={v.treasury_account_id} onChange={(x) => setV({ ...v, treasury_account_id: x })} /></Field>
          <Field label={t('common.amount')} required error={errors.amount}><MoneyInput value={v.amount} onChange={(x) => setV({ ...v, amount: x })} /></Field>
          <Field label={t('common.date')} required error={errors.date}><DateField value={v.date} onChange={(x) => setV({ ...v, date: x })} /></Field>
          <Field label={t('common.reference')} error={errors.reference} className="span-2"><input className="input" value={v.reference} onChange={(e) => setV({ ...v, reference: e.target.value })} /></Field>
        </div>
      </div>
    </Dialog>
  );
}

function Payments({ period }: { period: PayrollPeriod }) {
  const { t } = useI18n();
  const f = useFormat();
  const [layout, setLayout] = useState('generic');
  const [date, setDate] = useState(period.payment_date ?? todayIso());
  const locked = period.status === 'draft' || period.status === 'calculated';
  const q = useQuery({ queryKey: ['payroll-period', period.id, 'payments', date], queryFn: () => api.get<PaymentBatch>(`/payroll/periods/${period.id}/payments`, { payment_date: date }), enabled: !locked });
  if (locked) return <EmptyState compact title={t('payroll.tabs.payments')} body={t('payroll.paymentsLocked')} />;
  const b = q.data;
  return (
    <>
      <div className="filter-bar">
        <div className="muted">{t('payroll.paymentsHelp')}</div>
        <div className="filter-row">
          <Field label={t('payroll.paymentDate')}><DateField value={date} onChange={setDate} /></Field>
          <Field label={t('payroll.layout')}>
            <Select value={layout} onChange={(e) => setLayout(e.target.value)}>
              {(b?.layouts ?? [{ code: 'generic' }, { code: 'report' }]).map((l) => <option key={l.code} value={l.code}>{t(`payroll.layouts.${l.code as 'generic'}`)}</option>)}
            </Select>
          </Field>
          <div className="filter-actions">
            <ServerExport path={`/payroll/periods/${period.id}/payments/export`} query={{ layout, payment_date: date }} name={`salary-payments-${period.year}-${period.month}`}
              permission="payroll.export" formats={layout === 'generic' ? ['xlsx', 'csv'] : ['xlsx', 'csv', 'pdf']} />
          </div>
        </div>
      </div>
      {b && b.issue_count > 0 && <div style={{ padding: '12px 20px 0' }}><Alert kind="warning">{t('payroll.issueCount', { count: f.count(b.issue_count) })}</Alert></div>}
      <DataTable rows={b?.rows} loading={q.isLoading} error={q.error} onRetry={() => void q.refetch()} rowKey={(r) => r.employee_id} showFooter caption={t('payroll.tabs.payments')}
        rowClassName={(r) => (r.issues.length ? 'danger-row' : undefined)}
        columns={[
          { key: 'code', header: t('payroll.code'), cell: (r) => <span className="num">{f.digits(r.employee_code)}</span>, width: 90 },
          { key: 'name', header: t('payroll.employee'), className: 'nowrap', cell: (r) => r.employee_name, footer: t('common.total') },
          { key: 'nid', header: t('payroll.nationalId'), cell: (r) => <span className="num" dir="ltr">{f.digits(r.national_id)}</span> },
          { key: 'bank', header: t('payroll.bankName'), className: 'nowrap', cell: (r) => r.bank_name },
          { key: 'iban', header: t('payroll.iban'), cell: (r) => <span className="num" dir="ltr">{r.iban || r.account_number}</span> },
          { key: 'amt', header: t('common.amount'), align: 'end', cell: (r) => <Money value={r.amount} />, footer: <Money value={b?.total} /> },
          { key: 'iss', header: t('payroll.problem'), cell: (r) => <span className="inline-list">{r.issues.map((i) => <Badge key={i} tone="danger">{t(`payroll.issues.${i as 'invalid_iban'}`)}</Badge>)}</span> },
        ]} />
    </>
  );
}

function History({ period }: { period: PayrollPeriod }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const q = useQuery({ queryKey: ['payroll-period', period.id, 'audit'], queryFn: () => api.get<Page<AuditEntry>>('/audit', { entity: 'payroll_period', entity_id: period.id, page_size: 100 }), enabled: can('audit.view') });
  const items = useMemo(() => q.data?.items ?? [], [q.data]);
  if (!can('audit.view')) return <EmptyState compact title={t('ui.auditTrail')} body={t('errors.forbidden')} />;
  if (q.error) return <ErrorState compact error={q.error} onRetry={() => void q.refetch()} />;
  if (!items.length) return <EmptyState compact title={q.isLoading ? t('common.loading') : t('ui.noAudit')} />;
  return (
    <div style={{ padding: 20 }}>
      <Timeline label={t('ui.auditTrail')} items={items.map((a) => ({
        key: a.id, title: tryT(`settings.auditActions.${a.action}`) ?? a.action, meta: <>{f.dateTime(a.created_at)} · {a.user_name ?? '—'}</>,
        tone: a.action.includes('unpost') || a.action.includes('unpay') || a.action.includes('reopen') ? 'warning' : a.action.includes('post') || a.action.includes('pay') || a.action.includes('approve') ? 'success' : 'neutral',
      }))} />
    </div>
  );
}

function PayslipDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const { t, lang } = useI18n();
  const f = useFormat();
  const printer = usePdf();
  const q = useQuery({ queryKey: ['payslip', id], queryFn: () => api.get<Payslip>(`/payroll/payslips/${id}`) });
  const s = q.data;
  const section = (kind: string) => s?.lines?.filter((l) => l.kind === kind) ?? [];
  const table = (kind: string, total: string, label: string) => (
    <DataTable rows={section(kind)} rowKey={(l) => l.component_id} showFooter dense
      columns={[
        { key: 'n', header: t(`payroll.kinds.${kind as 'earning'}`), cell: (l) => componentName(l, lang), footer: label },
        { key: 'q', header: t('payroll.quantity'), align: 'end', cell: (l) => (Number(l.quantity) ? <span className="num">{f.qty(l.quantity)}</span> : '') },
        { key: 'a', header: t('payroll.amount'), align: 'end', cell: (l) => <Money value={l.amount} />, footer: <Money value={total} /> },
      ]} />
  );
  return (
    <Dialog size="wide" title={s ? `${t('payroll.payslip')} — ${s.employee_name}` : t('common.loading')} description={s?.period_name} onClose={onClose}
      footer={s && <>
        <Button pending={printer.pending} onClick={() => printer.view(() => payslipDocument([s], t, f, lang))}>{t('common.exportPdf')}</Button>
        <Button variant="accent" icon={<Printer size={16} />} pending={printer.pending} onClick={() => printer.print(() => payslipDocument([s], t, f, lang))}>{t('payroll.printSlip')}</Button>
        <Button onClick={onClose}>{t('common.close')}</Button>
      </>}>
      {q.error ? <ErrorState compact error={q.error} onRetry={() => void q.refetch()} /> : !s ? <SkeletonPage kpis={0} /> : (
        <div className="stack">
          <KV items={[
            { label: t('payroll.code'), value: <span className="num">{f.digits(s.employee_code)}</span> },
            { label: t('payroll.nationalId'), value: s.national_id && <span className="num">{f.digits(s.national_id)}</span> },
            { label: t('payroll.department'), value: s.department_name },
            { label: t('payroll.position'), value: s.position },
            { label: t('payroll.baseSalary'), value: <Money value={s.base_salary} /> },
            { label: t('payroll.workingDays'), value: <span className="num">{f.qty(s.working_days)}</span> },
            { label: t('payroll.taxableIncome'), value: <Money value={s.taxable_income} /> },
            { label: t('payroll.insurableIncome'), value: <Money value={s.insurable_income} /> },
            { label: t('payroll.iban'), value: s.iban && <span className="num" dir="ltr">{s.iban}</span> },
          ]} />
          {table('earning', s.gross, t('payroll.totalEarnings'))}
          {table('deduction', s.total_deductions, t('payroll.totalDeductions'))}
          {table('employer', s.employer_cost, t('payroll.employer'))}
          <div className="totals-bar"><span>{t('payroll.netPay')}</span><strong className="num">{f.moneyWithCurrency(s.net)}</strong></div>
        </div>
      )}
    </Dialog>
  );
}
