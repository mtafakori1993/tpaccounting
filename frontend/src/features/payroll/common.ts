import { useQuery } from '@tanstack/react-query';
import { api } from '@/api/client';
import type { Company, Payslip, SalaryComponent } from '@/api/types';
import type { Params, TKey } from '@/i18n/I18nProvider';
import type { Formatter } from '@/i18n/format';
import type { PdfDocument, PdfTable } from '@/lib/pdf';
import { add } from '@/lib/decimal';

type T = (key: TKey, params?: Params) => string;

export const useSalaryComponents = () =>
  useQuery({ queryKey: ['salary-components'], queryFn: () => api.get<SalaryComponent[]>('/payroll/components'), staleTime: 60_000 });

export const componentName = (c: { name: string; name_en: string }, lang: string) => (lang === 'en' && c.name_en ? c.name_en : c.name);

/** Salary slip(s) as a letterhead document: company, employee, period, earnings, deductions, employer cost and payment. */
export async function payslipDocument(slips: Payslip[], t: T, f: Formatter, lang: string): Promise<PdfDocument> {
  let company: Company | null = null;
  try {
    company = await api.get<Company>('/company');
  } catch {
    company = null;
  }
  const full = await Promise.all(slips.map((s) => (s.lines ? Promise.resolve(s) : api.get<Payslip>(`/payroll/payslips/${s.id}`))));
  const companyName = company ? (lang === 'en' && company.name_en ? company.name_en : company.name) : '';
  const tables: PdfTable[] = [];
  for (const s of full) {
    const lines = s.lines ?? [];
    const section = (kind: string) => lines.filter((l) => l.kind === kind);
    const row = (l: (typeof lines)[number]) => ({ cells: [componentName(l, lang), Number(l.quantity) ? f.qty(l.quantity) : '', f.money(l.amount)] });
    const cols = [{ label: t('payroll.employee'), align: 'start' as const }, { label: t('payroll.quantity'), align: 'end' as const }, { label: t('payroll.amount'), align: 'end' as const }];
    const header = `${s.employee_name} — ${f.digits(s.employee_code)}${s.national_id ? ` — ${t('payroll.nationalId')}: ${f.digits(s.national_id)}` : ''}${s.department_name ? ` — ${s.department_name}` : ''}`;
    tables.push(
      { title: `${header} — ${t('payroll.earnings')}`, columns: cols, rows: section('earning').map(row), footer: [t('payroll.totalEarnings'), '', f.money(s.gross)] },
      { title: t('payroll.deductions'), columns: cols, rows: section('deduction').map(row), footer: [t('payroll.totalDeductions'), '', f.money(s.total_deductions)] },
      {
        title: t('payroll.employer'), columns: cols, rows: section('employer').map(row),
        footer: [t('payroll.employerCost'), '', f.money(add(s.gross, s.employer_cost))],
      },
      {
        title: t('payroll.paymentInfo'),
        columns: [{ label: t('payroll.netPay'), align: 'end' }, { label: t('payroll.iban'), align: 'start' }, { label: t('payroll.bankAccountNumber'), align: 'start' }, { label: t('payroll.paymentDate'), align: 'start' }],
        rows: [{ style: 'total', cells: [f.money(s.net), s.iban, f.digits(s.bank_account_number), f.date(s.payment_date)] }],
      },
    );
  }
  const one = full.length === 1 ? full[0] : null;
  return {
    title: t('payroll.payslip'),
    subtitle: full[0]?.period_name,
    number: one ? f.digits(one.employee_code) : undefined,
    date: full[0] ? f.date(full[0].end_date) : undefined,
    meta: [
      { label: t('setup.companySection'), value: companyName },
      ...(one ? [
        { label: t('payroll.workingDays'), value: f.qty(one.working_days) },
        { label: t('payroll.baseSalary'), value: f.money(one.base_salary) },
        { label: t('payroll.taxableIncome'), value: f.money(one.taxable_income) },
        { label: t('payroll.insurableIncome'), value: f.money(one.insurable_income) },
        { label: t('payroll.netPay'), value: f.money(one.net) },
      ] : []),
    ].filter((m) => m.value),
    tables,
    signatures: [t('journal.preparedBy'), t('journal.approvedBy'), t('payroll.employee')],
  };
}
