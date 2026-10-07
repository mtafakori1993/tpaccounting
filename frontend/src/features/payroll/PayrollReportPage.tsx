import { useQuery } from '@tanstack/react-query';
import { Link, Navigate, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import type { Employee, Page, PayrollReport, PayrollReportRow } from '@/api/types';
import { useAuth } from '@/auth/AuthProvider';
import { useI18n } from '@/i18n/I18nProvider';
import { useFormat } from '@/i18n/format';
import { Field, Select } from '@/components/ui/Field';
import { Amount, PageHeader, Panel } from '@/components/ui/Misc';
import { ErrorState } from '@/components/ui/Feedback';
import { FilterBar } from '@/components/ui/FilterBar';
import { Kpi, KpiGrid } from '@/components/ui/Kpi';
import { ChevronRight, Clock } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/DataTable';
import { DateRange } from '@/components/DateRange';
import { ExportMenu } from '@/components/ExportMenu';
import type { ExportColumn } from '@/components/export';
import { CostCenterSelect } from '@/components/pickers/Pickers';

const reports = ['summary', 'employee', 'by-department', 'by-cost-center', 'by-project', 'tax', 'insurance', 'employer-costs', 'net-payable', 'journal', 'payments'] as const;
type Report = (typeof reports)[number];

export function PayrollReportPage() {
  const { report } = useParams();
  const { t } = useI18n();
  if (!report) {
    return (
      <div className="page">
        <PageHeader crumbs={[{ label: t('payroll.overviewTitle'), to: '/payroll' }]} title={t('payroll.reports.title')} subtitle={t('payroll.reportsSubtitle')} />
        <div className="report-groups">
          {[reports.slice(0, 5), reports.slice(5)].map((group, gi) => (
            <Panel key={gi} bodyless>
              {group.map((r) => (
                <Link key={r} to={`/payroll/reports/${r}`} className="link-row">
                  <span>
                    <span className="lr-title" style={{ display: 'block' }}>{t(`payroll.reports.names.${r}`)}</span>
                    <span className="lr-desc">{t(`payroll.reports.descriptions.${r}`)}</span>
                  </span>
                  <ChevronRight size={16} className="icon-directional muted" aria-hidden />
                </Link>
              ))}
            </Panel>
          ))}
        </div>
      </div>
    );
  }
  if (!reports.includes(report as Report)) return <Navigate to="/payroll/reports" replace />;
  return <ReportView report={report as Report} />;
}

function ReportView({ report }: { report: Report }) {
  const { t, tryT } = useI18n();
  const f = useFormat();
  const { me } = useAuth();
  const navigate = useNavigate();
  const [sp, setSp] = useSearchParams();
  const fy = me?.fiscal_year;
  const from = sp.get('from') ?? fy?.start_date.slice(0, 10) ?? '';
  const to = sp.get('to') ?? fy?.end_date.slice(0, 10) ?? '';
  const employee = sp.get('employee_id') ?? '';
  const department = sp.get('department_id') ?? '';
  const set = (patch: Record<string, string>) => {
    const n = new URLSearchParams(sp);
    for (const [k, v] of Object.entries(patch)) (v ? n.set(k, v) : n.delete(k));
    setSp(n, { replace: true });
  };
  const emps = useQuery({ queryKey: ['employees', 'all'], queryFn: () => api.get<Page<Employee>>('/employees', { page_size: 500 }), enabled: report === 'employee' });
  const q = useQuery({
    queryKey: ['payroll-report', report, from, to, employee, department],
    queryFn: () => api.get<PayrollReport>(`/payroll/reports/${report}`, { from, to, employee_id: employee, department_id: department }),
  });
  const d = q.data;
  const title = t(`payroll.reports.names.${report}`);
  const colLabel = (c: string) => tryT(`payroll.reports.columns.${c}`) ?? c;
  const labelHeader = report === 'summary' || report === 'net-payable' || report === 'payments' ? t('payroll.period') : report === 'journal' ? t('common.description') : t('common.name');
  const extra: { key: string; label: string; fmt?: (v: string) => string }[] =
    report === 'net-payable' || report === 'payments'
      ? [{ key: 'period', label: t('payroll.period') }, { key: 'iban', label: t('payroll.iban') }, ...(report === 'payments' ? [{ key: 'payment_date', label: t('payroll.paymentDate'), fmt: (v: string) => f.date(v) }, { key: 'treasury_account', label: t('payroll.paymentAccount') }] : [])]
      : report === 'journal' ? [{ key: 'number', label: t('common.number'), fmt: (v: string) => f.int(v) }, { key: 'date', label: t('common.date'), fmt: (v: string) => f.date(v) }, { key: 'source_type', label: t('journal.source'), fmt: (v: string) => tryT(`journal.sources.${v}`) ?? v }]
        : report === 'summary' ? [{ key: 'status', label: t('common.status'), fmt: (v: string) => tryT(`payroll.statuses.${v}`) ?? v }] : [];
  const label = (r: PayrollReportRow) => (r.label ? f.digits(r.label) : t('payroll.unassigned'));
  const columns: Column<PayrollReportRow>[] = [
    ...extra.filter((e) => e.key !== 'period').map((e) => ({ key: `x-${e.key}`, header: e.label, cell: (r: PayrollReportRow) => <span className="num">{e.fmt ? e.fmt(r.extra?.[e.key] ?? '') : r.extra?.[e.key]}</span> })),
    { key: 'label', header: report === 'net-payable' || report === 'payments' ? t('payroll.employee') : labelHeader, cell: label, footer: t('common.total') },
    ...(report === 'net-payable' || report === 'payments' ? [{ key: 'period', header: t('payroll.period'), cell: (r: PayrollReportRow) => f.digits(r.extra?.period ?? '') }] : []),
    ...(['journal', 'net-payable', 'payments'].includes(report) ? [] : [{ key: 'count', header: t('payroll.employeeCount'), align: 'end' as const, cell: (r: PayrollReportRow) => <span className="num">{f.count(r.count)}</span> }]),
    ...(d?.columns ?? []).map((c) => ({ key: c, header: colLabel(c), align: 'end' as const, cell: (r: PayrollReportRow) => <Amount value={r.amounts[c]} />, footer: <Amount value={d?.totals[c]} /> })),
  ];
  const exportCols: ExportColumn<PayrollReportRow>[] = [
    ...extra.map((e) => ({ header: e.label, value: (r: PayrollReportRow) => (e.fmt ? e.fmt(r.extra?.[e.key] ?? '') : r.extra?.[e.key] ?? '') })),
    { header: labelHeader, value: (r) => r.label },
    { header: t('payroll.employeeCount'), value: (r) => r.count, numeric: true },
    ...(d?.columns ?? []).map((c) => ({ header: colLabel(c), value: (r: PayrollReportRow) => r.amounts[c], numeric: true })),
  ];
  const onRow = (r: PayrollReportRow) => {
    if (!r.id) return;
    if (report === 'summary') navigate(`/payroll/periods/${r.id}`);
    else if (report === 'journal') navigate(`/journal/${r.id}`);
    else if (report === 'employee' && !employee) set({ employee_id: String(r.id) });
  };
  const kpiCols = (d?.columns ?? []).slice(0, 4);
  return (
    <div className="page">
      <PageHeader crumbs={[{ label: t('payroll.reports.title'), to: '/payroll/reports' }]} title={title} subtitle={t(`payroll.reports.descriptions.${report}`)}
        meta={q.dataUpdatedAt ? <><Clock size={13} aria-hidden /> {t('ui.updatedAt', { time: f.dateTime(new Date(q.dataUpdatedAt).toISOString()) })}</> : null}
        actions={<ExportMenu name={`payroll-${report}`} permission="payroll.export" title={title} rows={d?.rows} columns={exportCols} subtitle={`${f.date(from)} – ${f.date(to)}`} />} />
      {q.error && !d ? <Panel bodyless><ErrorState error={q.error} title={t('ui.reportFailed')} onRetry={() => void q.refetch()} /></Panel> : (
        <>
          {kpiCols.length > 0 && (
            <KpiGrid loading={!d} count={kpiCols.length}>
              {kpiCols.map((c, i) => <Kpi key={c} label={colLabel(c)} value={f.money(d?.totals[c] ?? 0)} sub={i === 0 ? f.currencyLabel : undefined} tone={i === 0 ? 'primary' : 'neutral'} />)}
            </KpiGrid>
          )}
          <Panel bodyless>
            <FilterBar activeCount={[employee, department].filter(Boolean).length} onReset={() => set({ employee_id: '', department_id: '' })} basic={<>
              <DateRange from={from} to={to} onChange={(a, b) => set({ from: a, to: b })} />
              {report === 'employee' && (
                <Field label={t('payroll.employee')}>
                  <Select value={employee} onChange={(e) => set({ employee_id: e.target.value })}>
                    <option value="">{t('common.all')}</option>
                    {emps.data?.items.map((e) => <option key={e.id} value={e.id}>{`${f.digits(e.code)} — ${e.full_name}`}</option>)}
                  </Select>
                </Field>
              )}
              {!['journal', 'by-department'].includes(report) && (
                <Field label={t('payroll.department')}>
                  <CostCenterSelect kinds={['department']} emptyLabel={t('common.all')} value={Number(department) || null} onChange={(x) => set({ department_id: x ? String(x) : '' })} />
                </Field>
              )}
            </>} />
            <DataTable rows={d?.rows} loading={q.isFetching} error={q.error} onRetry={() => void q.refetch()} rowKey={(r, i) => `${r.id}-${i}`} showFooter onRowClick={onRow} columns={columns} caption={title} />
          </Panel>
        </>
      )}
    </div>
  );
}
