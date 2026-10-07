import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowRight, CheckCircle2, Circle, FileBarChart, IdCard, Plus, Scale } from 'lucide-react';
import { api } from '@/api/client';
import type { Employee, Page, PayrollLiability, PayrollPeriod, PayrollRules } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Button, LinkButton } from '@/components/ui/Button';
import { Badge, Money, PageHeader, Panel, StatusBadge } from '@/components/ui/Misc';
import { DataTable } from '@/components/ui/DataTable';
import { EmptyState, ErrorState } from '@/components/ui/Feedback';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { Stepper } from '@/components/ui/Steps';
import { add } from '@/lib/decimal';
import { NewPeriodDialog, periodLabel } from './PayrollPeriodsPage';
import { useSalaryComponents } from './common';
import { currentStep, stepAction, stepStates } from './workflow';

/** /payroll — the current run, what needs doing next, unpaid statutory liabilities and recent runs. */
export function PayrollOverviewPage() {
  const { t, lang } = useI18n();
  const f = useFormat();
  const { can } = useAuth();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);
  const periods = useQuery({ queryKey: ['payroll-periods'], queryFn: () => api.get<PayrollPeriod[]>('/payroll/periods') });
  const liabilities = useQuery({ queryKey: ['payroll-liabilities', 'open'], queryFn: () => api.get<PayrollLiability[]>('/payroll/liabilities', { open: true }) });
  const employees = useQuery({ queryKey: ['employees', 'count'], queryFn: () => api.get<Page<Employee>>('/employees', { active: true, page_size: 1 }) });
  const rules = useQuery({ queryKey: ['payroll-rules'], queryFn: () => api.get<PayrollRules>('/payroll/rules') });
  const comps = useSalaryComponents();
  const list = periods.data ?? [];
  const current = list.find((p) => p.status !== 'closed') ?? list[0];
  const open = liabilities.data ?? [];
  const openTotal = open.reduce((s, l) => add(s, l.outstanding), '0');
  const step = current ? currentStep(current) : null;
  const checklist = [
    { done: (employees.data?.total ?? 0) > 0, label: t('payroll.checklist.employees'), to: '/payroll/employees' },
    { done: (comps.data?.length ?? 0) > 0, label: t('payroll.checklist.components'), to: '/payroll/components' },
    { done: (rules.data?.tax_tables.length ?? 0) > 0 && (rules.data?.insurance_rules.length ?? 0) > 0, label: t('payroll.checklist.rules'), to: '/payroll/settings' },
    { done: true, label: t('payroll.checklist.mappings'), to: '/settings/posting-rules' },
  ];
  return (
    <div className="page">
      <PageHeader title={t('payroll.overviewTitle')} subtitle={t('payroll.overviewSubtitle')} actions={<>
        <LinkButton to="/payroll/employees" icon={<IdCard size={16} />}>{t('payroll.employeesTitle')}</LinkButton>
        <LinkButton to="/payroll/reports" icon={<FileBarChart size={16} />}>{t('payroll.reports.title')}</LinkButton>
        <LinkButton to="/payroll/settings" icon={<Scale size={16} />}>{t('nav.payrollSettings')}</LinkButton>
        {can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('payroll.newPeriod')}</Button>}
      </>} />

      {periods.error ? <Panel bodyless><ErrorState error={periods.error} onRetry={() => void periods.refetch()} /></Panel> : (
        <>
          <KpiGrid loading={periods.isLoading} count={4}>
            <Kpi label={t('payroll.employeeCount')} value={f.count(employees.data?.total ?? 0)} to="/payroll/employees" />
            <Kpi label={`${t('payroll.net')}${current ? ` — ${periodLabel(current, f.calendar, lang, f.digits)}` : ''}`} value={f.money(current?.net_total ?? 0)} sub={f.currencyLabel} tone="primary" />
            <Kpi label={t('payroll.totalCost')} value={f.money(current ? add(current.gross_total, current.employer_total) : 0)} sub={current ? `${t('payroll.employer')}: ${f.money(current.employer_total)}` : undefined} />
            <Kpi label={t('payroll.openLiabilities')} value={f.money(openTotal)} sub={open.length ? `${t('payroll.liabilityStatus.open')}: ${f.count(open.length)}` : t('payroll.liabilityStatus.paid')}
              tone={open.length ? 'warning' : 'success'} />
          </KpiGrid>

          {!current && !periods.isLoading ? (
            <div className="grid-main">
              <Panel bodyless>
                <EmptyState title={t('payroll.noRun')} body={t('payroll.noRunBody')}
                  action={can('payroll.create') && <Button variant="accent" icon={<Plus size={16} />} onClick={() => setCreating(true)}>{t('payroll.newPeriod')}</Button>} />
              </Panel>
              <Panel title={t('payroll.setupChecklist')}>
                <ul className="checklist">
                  {checklist.map((c) => (
                    <li key={c.to}>{c.done ? <CheckCircle2 size={18} className="success-text" /> : <Circle size={18} className="muted" />} <Link to={c.to}>{c.label}</Link></li>
                  ))}
                </ul>
              </Panel>
            </div>
          ) : current && (
            <Panel bodyless className="section-gap" title={<span className="row">{t('payroll.currentRun')}: <Link to={`/payroll/periods/${current.id}`}>{periodLabel(current, f.calendar, lang, f.digits)}</Link> <StatusBadge status={current.status} /></span>}>
              <Stepper steps={stepStates(current, (s) => t(`payroll.steps.${s}`))} label={t('payroll.currentRun')} />
              <div className="next-step">
                <div className="ns-text">
                  <div className="ns-title">{step ? `${t('ui.nextStep')}: ${t(`payroll.steps.${step}`)}` : t('ui.allDone')}</div>
                  <div className="ns-body">{t(`payroll.stepHelp.${step ?? 'done'}`)}</div>
                </div>
                <Button variant="accent" onClick={() => navigate(`/payroll/periods/${current.id}`)}>
                  {step && stepAction[step] ? t('payroll.nextAction', { action: t(`payroll.actions.${stepAction[step] as 'post'}`) }) : t('ui.viewDetails')}
                  <ArrowRight size={16} className="icon-directional" />
                </Button>
              </div>
            </Panel>
          )}

          {list.length > 0 && (
            <div className="grid-2">
              <Panel bodyless title={t('payroll.openLiabilities')}>
                <DataTable rows={open} loading={liabilities.isLoading} error={liabilities.error} rowKey={(l) => l.id} dense onRowClick={(l) => navigate(`/payroll/periods/${l.period_id}?tab=liabilities`)}
                  empty={<EmptyState compact icon={<CheckCircle2 size={22} />} title={t('payroll.noOpenLiabilities')} />}
                  columns={[
                    { key: 'p', header: t('payroll.period'), cell: (l) => { const p = list.find((x) => x.id === l.period_id); return p ? periodLabel(p, f.calendar, lang, f.digits) : l.period_name; } },
                    { key: 'k', header: t('common.type'), cell: (l) => t(`payroll.liabilityKinds.${l.kind}`) },
                    { key: 'o', header: t('payroll.outstanding'), align: 'end', cell: (l) => <Money value={l.outstanding} /> },
                    { key: 's', header: '', cell: (l) => <Badge tone={l.status === 'partial' ? 'info' : 'warning'}>{t(`payroll.liabilityStatus.${l.status}`)}</Badge> },
                  ]} />
              </Panel>
              <Panel bodyless title={t('payroll.recentRuns')} actions={<Link to="/payroll/periods">{t('common.viewAll')}</Link>}>
                <DataTable rows={list.slice(0, 6)} rowKey={(p) => p.id} dense onRowClick={(p) => navigate(`/payroll/periods/${p.id}`)}
                  columns={[
                    { key: 'n', header: t('payroll.period'), cell: (p) => <strong>{periodLabel(p, f.calendar, lang, f.digits)}</strong> },
                    { key: 'net', header: t('payroll.net'), align: 'end', cell: (p) => <Money value={p.net_total} /> },
                    { key: 's', header: t('common.status'), cell: (p) => <StatusBadge status={p.status} /> },
                  ]} />
              </Panel>
            </div>
          )}
        </>
      )}
      {creating && <NewPeriodDialog onClose={() => setCreating(false)} onCreated={(p) => navigate(`/payroll/periods/${p.id}`)} />}
    </div>
  );
}
