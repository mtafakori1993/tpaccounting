// Response shapes of the REST API. Money and quantities are decimal strings.
export type Money = string;
export type ISODate = string;

export interface Page<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface Me {
  id: number;
  email: string;
  full_name: string;
  role: string;
  language: 'fa' | 'en';
  theme: 'light' | 'dark';
  permissions: string[];
  company: { id: number; name: string; name_en: string; has_logo: boolean; base_currency: string; amount_decimals: number; calendar: 'jalali' | 'gregorian' };
  fiscal_year: { id: number; name: string; start_date: string; end_date: string; status: string } | null;
}

export type AccountType = 'asset' | 'liability' | 'equity' | 'revenue' | 'cost_of_sales' | 'expense';

export interface Account {
  id: number;
  parent_id: number | null;
  code: string;
  name: string;
  name_en: string;
  type: AccountType;
  level: number;
  is_active: boolean;
  description: string;
  has_children: boolean;
  postable: boolean;
  balance?: Money;
}

export interface Party {
  id: number;
  code: string;
  kind: 'person' | 'company';
  name: string;
  is_customer: boolean;
  is_supplier: boolean;
  is_other: boolean;
  national_id: string;
  economic_code: string;
  registration_number: string;
  phone: string;
  mobile: string;
  email: string;
  address: string;
  postal_code: string;
  bank_name: string;
  bank_account_number: string;
  iban: string;
  notes: string;
  receivable_account_id: number | null;
  payable_account_id: number | null;
  is_active: boolean;
  balance: Money;
}

export type CostCenterKind = 'cost_center' | 'department' | 'project';
export interface CostCenter { id: number; code: string; kind: CostCenterKind; name: string; name_en: string; description: string; is_active: boolean }

export interface JournalLine {
  id?: number;
  account_id: number;
  account_code?: string;
  account_name?: string;
  account_name_en?: string;
  party_id: number | null;
  party_name?: string | null;
  cost_center_id: number | null;
  cost_center_name?: string | null;
  description: string;
  debit: Money;
  credit: Money;
}

export interface JournalEntry {
  id: number;
  number: number;
  date: ISODate;
  description: string;
  reference: string;
  status: 'draft' | 'posted' | 'cancelled';
  source_type: string;
  source_id: number | null;
  reversal_of_id: number | null;
  reversal_of_number: number | null;
  reversed_by_id: number | null;
  reversed_by_number: number | null;
  total_debit: Money;
  total_credit: Money;
  posted_at: string | null;
  posted_by_name: string | null;
  created_by_name: string | null;
  created_at: string;
  lines?: JournalLine[];
}

export interface Product {
  id: number;
  kind: 'goods' | 'service';
  sku: string;
  name: string;
  name_en: string;
  unit: string;
  category_id: number | null;
  category_name: string | null;
  purchase_price: Money;
  sales_price: Money;
  tax_rate: string;
  track_inventory: boolean;
  is_active: boolean;
  description: string;
  on_hand: string;
  stock_value: Money;
}

export interface Category { id: number; name: string; description: string; product_count: number }
export interface Warehouse { id: number; code: string; name: string; address: string; is_active: boolean; is_default: boolean }

export type Side = 'sales' | 'purchase';
export type InvoiceKind = 'quotation' | 'invoice' | 'return';

export interface InvoiceItem {
  id?: number;
  product_id: number | null;
  product_name?: string | null;
  product_sku?: string | null;
  track_inventory?: boolean;
  description: string;
  quantity: string;
  unit: string;
  unit_price: Money;
  discount: Money;
  tax_rate: string;
  net_amount?: Money;
  tax_amount?: Money;
  total?: Money;
}

export interface Settlement {
  id: number;
  amount: Money;
  transaction_id: number | null;
  transaction_kind: string | null;
  transaction_number: number | null;
  credit_invoice_id: number | null;
  credit_invoice_number: number | null;
  check_id?: number | null;
  check_number?: string | null;
  date: ISODate;
}

export interface Invoice {
  id: number;
  side: Side;
  kind: InvoiceKind;
  number: number;
  date: ISODate;
  due_date: ISODate | null;
  party_id: number;
  party_name: string;
  party_code: string;
  warehouse_id: number | null;
  warehouse_name: string | null;
  cost_center_id: number | null;
  cost_center_name: string | null;
  related_invoice_id: number | null;
  related_number: number | null;
  related_kind: string | null;
  reference: string;
  notes: string;
  status: 'draft' | 'posted' | 'cancelled';
  subtotal: Money;
  discount_total: Money;
  tax_total: Money;
  total: Money;
  paid_amount: Money;
  outstanding: Money;
  payment_status: 'paid' | 'unpaid' | 'partial';
  journal_entry_id: number | null;
  journal_number: number | null;
  created_at: string;
  posted_at: string | null;
  items?: InvoiceItem[];
  settlements?: Settlement[];
}

export interface TreasuryAccount {
  id: number;
  kind: 'cash' | 'bank';
  name: string;
  account_id: number;
  account_code: string;
  bank_name: string;
  branch: string;
  account_number: string;
  iban: string;
  card_number: string;
  account_holder: string;
  opening_balance: Money;
  opening_date: ISODate | null;
  description: string;
  is_active: boolean;
  balance: Money;
}

export interface TreasuryTx {
  id: number;
  kind: 'receipt' | 'payment' | 'transfer';
  number: number;
  date: ISODate;
  treasury_account_id: number;
  treasury_account_name: string;
  to_treasury_account_id: number | null;
  to_treasury_account_name: string | null;
  party_id: number | null;
  party_name: string | null;
  counter_account_id: number | null;
  counter_account_name: string | null;
  cost_center_id: number | null;
  amount: Money;
  allocated: Money;
  description: string;
  reference: string;
  status: 'posted' | 'cancelled';
  journal_entry_id: number | null;
  journal_number: number | null;
  created_at: string;
  allocations?: { invoice_id: number; side: Side; kind: InvoiceKind; number: number; amount: Money }[];
}

export interface OpenItem { id: number; side: Side; kind: InvoiceKind; number: number; date: ISODate; due_date: ISODate | null; total: Money; outstanding: Money }

export interface ExpenseCategory { id: number; name: string; name_en: string; account_id: number; account_code: string; account_name: string; is_active: boolean }

export interface Expense {
  id: number;
  number: number;
  date: ISODate;
  category_id: number;
  category_name: string;
  account_id: number;
  account_name: string;
  amount: Money;
  description: string;
  reference: string;
  cost_center_id: number | null;
  cost_center_name: string | null;
  payment_method: 'cash' | 'bank' | 'credit';
  treasury_account_id: number | null;
  treasury_account_name: string | null;
  party_id: number | null;
  party_name: string | null;
  generate_entry: boolean;
  status: 'draft' | 'posted' | 'cancelled';
  journal_entry_id: number | null;
  journal_number: number | null;
  created_at: string;
}

export interface StockDocLine { id?: number; product_id: number; product_name?: string; product_sku?: string; unit?: string; quantity: string; unit_cost: Money; description: string }

export interface StockDocument {
  id: number;
  kind: 'receipt' | 'issue' | 'transfer' | 'adjustment';
  number: number;
  date: ISODate;
  warehouse_id: number;
  warehouse_name: string;
  to_warehouse_id: number | null;
  to_warehouse_name: string | null;
  counter_account_id: number | null;
  counter_account_name: string | null;
  description: string;
  reference: string;
  status: 'draft' | 'posted' | 'cancelled';
  journal_entry_id: number | null;
  created_at: string;
  lines?: StockDocLine[];
}

export interface Attachment { id: number; entity_type: string; entity_id: number; file_name: string; content_type: string; size_bytes: number; uploaded_by: string | null; created_at: string }

export interface FiscalYear {
  id: number;
  name: string;
  start_date: ISODate;
  end_date: ISODate;
  status: 'open' | 'closed';
  closing_entry_id: number | null;
  periods?: { id: number; seq: number; name: string; start_date: ISODate; end_date: ISODate; is_locked: boolean }[];
}

export interface Company {
  id: number;
  name: string;
  name_en: string;
  legal_name: string;
  registration_number: string;
  national_id: string;
  economic_code: string;
  address: string;
  postal_code: string;
  phone: string;
  email: string;
  website: string;
  has_logo: boolean;
  base_currency: string;
  amount_decimals: number;
  calendar: 'jalali' | 'gregorian';
  allow_negative_stock: boolean;
}

export interface Mapping { key: string; account_id: number | null; account_code: string | null; account_name: string | null; account_name_en: string | null; allowed_types: AccountType[]; parent: boolean }

export interface User { id: number; email: string; full_name: string; role_id: number; role_name: string; role_name_en: string; is_active: boolean; last_login_at: string | null; created_at: string }
export interface Role { id: number; code: string; name: string; name_en: string; description: string; is_system: boolean; user_count: number; permissions: string[] }
export interface Permission { code: string; module: string; action: string }

export interface AuditEntry { id: number; user_id: number | null; user_name: string | null; action: string; entity: string; entity_id: number | null; ip: string; old_value: unknown; new_value: unknown; created_at: string }

// ---- Checks ----
export type CheckType = 'received' | 'issued';
export interface CheckTransition {
  id: number; action: string; from_status: string | null; to_status: string; date: ISODate; treasury_account_name: string | null;
  party_name: string | null; journal_entry_id: number | null; journal_number: number | null; reversal_entry_ids: number[]; reason: string;
  user_name: string | null; created_at: string;
}
export interface Check {
  id: number; check_type: CheckType; number: number; check_number: string; serial_number: string; sayadi_id: string;
  bank_id: number | null; bank_name: string; branch: string; account_number: string; account_holder: string;
  party_id: number; party_name: string; counter_account_id: number | null; amount: Money; currency: string;
  issue_date: ISODate; due_date: ISODate; received_date: ISODate | null; treasury_account_id: number | null; treasury_account_name: string | null;
  holder_party_id: number | null; holder_party_name: string | null; invoice_id: number | null; invoice_number: number | null;
  treasury_transaction_id: number | null; project_id: number | null; project_name: string | null; description: string; notes: string;
  status: string; status_date: ISODate | null; replaced_by_id: number | null; journal_entry_id: number | null; journal_number: number | null;
  allocated: Money; created_by_name: string | null; created_at: string; updated_at: string; cancelled_at: string | null;
  live: boolean; overdue: boolean; days_to_due: number; allowed_actions: string[]; transitions?: CheckTransition[];
}
export interface CheckPage extends Page<Check> { total_amount: Money }
export interface Bank { id: number; code: string; name: string; name_en: string }
export interface Bucket { count: number; amount: Money }
export interface CheckDashboard {
  today: ISODate; week_end: ISODate; received_live: Bucket; issued_live: Bucket; in_custody: Bucket; awaiting_collection: Bucket;
  due_today_received: Bucket; due_today_issued: Bucket; due_week_received: Bucket; due_week_issued: Bucket; overdue_received: Bucket;
  overdue_issued: Bucket; returned_received: Bucket; returned_issued: Bucket; amount_due: Bucket; upcoming_receipts: Check[]; upcoming_payments: Check[];
}

// ---- Payroll ----
export interface Allocation { cost_center_id: number; cost_center_name?: string; cost_center_kind?: string; percent: string }
export interface Employee {
  id: number; code: string; first_name: string; last_name: string; full_name: string; national_id: string; personnel_number: string;
  mobile: string; email: string; address: string; bank_name: string; bank_account_number: string; iban: string;
  department_id: number | null; department_name: string | null; cost_center_id: number | null; cost_center_name: string | null;
  position: string; employment_type: string; hire_date: ISODate; termination_date: ISODate | null; base_salary: Money;
  children_count: number; marital_status: string; insurance_number: string; insurance_exempt: boolean; tax_exempt: boolean;
  is_active: boolean; notes: string; allocations: Allocation[]; components: { component_id: number; rate: string }[];
}
export interface Bracket { upto: string | null; rate: string }
export interface SalaryComponent {
  id: number; code: string; name: string; name_en: string; kind: 'earning' | 'deduction' | 'employer'; category: string; method: string;
  rate: string; quantity_field: string; brackets: Bracket[]; exemption: Money; ceiling: Money; taxable: boolean; insurable: boolean;
  account_id: number | null; seq: number; is_active: boolean; variability: 'fixed' | 'variable'; formula: string;
  insurance_share: '' | 'employee' | 'employer' | 'unemployment'; effective_from: ISODate | null; effective_to: ISODate | null;
}
export interface PayrollPeriod {
  id: number; fiscal_year_id: number; year: number; month: number; name: string; start_date: ISODate; end_date: ISODate; status: string;
  payment_date: ISODate | null; treasury_account_id: number | null; treasury_account_name: string | null; gross_total: Money;
  deduction_total: Money; net_total: Money; employer_total: Money; employee_count: number; journal_entry_id: number | null;
  journal_number: number | null; payment_entry_id: number | null; payment_number: number | null; notes: string;
  calculated_at: string | null; approved_at: string | null; approved_by_name: string | null; posted_at: string | null; paid_at: string | null;
  closed_at: string | null; created_at: string; allowed_actions: string[]; calc_basis: PayrollCalcBasis; warnings: PayrollWarning[];
}
export interface PayrollInput {
  employee_id: number; employee_code: string; employee_name: string; saved: boolean; source: string;
  working_days: string; working_hours: string; absence_days: string; unpaid_leave_days: string; paid_leave_days: string; overtime_hours: string;
  mission_days: string; holiday_work_hours: string; items: { component_id: number; amount: string }[];
}
export interface PayslipLine { component_id: number; kind: string; category: string; code: string; name: string; name_en: string; quantity: string; rate: string; amount: Money }
export interface Payslip {
  id: number; period_id: number; period_name: string; period_status: string; start_date: ISODate; end_date: ISODate; payment_date: ISODate | null;
  employee_id: number; employee_code: string; employee_name: string; national_id: string; insurance_number: string; department_name: string | null;
  position: string; base_salary: Money; working_days: string; gross: Money; total_deductions: Money; net: Money; employer_cost: Money;
  taxable_income: Money; insurable_income: Money; iban: string; bank_account_number: string; allocations: Allocation[]; lines?: PayslipLine[];
}
export interface PayrollReportRow { id: number | null; key: string; label: string; extra?: Record<string, string>; count: number; amounts: Record<string, Money> }
export interface PayrollReport { name: string; columns: string[]; rows: PayrollReportRow[]; totals: Record<string, Money> }

// ---- Budgets ----
export interface BudgetPeriod { seq: number; name: string; start_date: ISODate; end_date: ISODate }
export interface BudgetLine {
  id: number; line_no: number; account_id: number; account_code: string; account_name: string; account_name_en: string; account_type: string;
  cost_center_id: number | null; cost_center_name: string | null; cost_center_kind: string | null; allocation_method: string;
  annual_amount: Money; notes: string; amounts: Money[];
}
export interface Budget {
  id: number; fiscal_year_id: number; fiscal_year_name: string; root_id: number; previous_id: number | null; version: number; code: string;
  name: string; name_en: string; description: string; cost_center_id: number | null; cost_center_name: string | null; revision_reason: string;
  status: string; submitted_at: string | null; approved_at: string | null; approved_by_name: string | null; activated_at: string | null;
  closed_at: string | null; created_by_name: string | null; created_at: string; updated_at: string; expense_total: Money; income_total: Money;
  other_total: Money; line_count: number; latest_version: number; allowed_actions: string[]; periods?: BudgetPeriod[]; lines?: BudgetLine[];
  versions?: { id: number; version: number; status: string; revision_reason: string; created_at: string }[];
}
export interface BudgetCell { seq: number; budget: Money; actual: Money }
export interface BudgetRow {
  key: string; label: string; budget_id?: number; budget_name?: string; line_id?: number; account_id?: number; account_code?: string;
  account_name?: string; account_name_en?: string; cost_center_id?: number | null; cost_center_name?: string | null; cost_center_kind?: string | null;
  kind: 'expense' | 'income' | 'other'; budget: Money; actual: Money; variance: Money; utilization: string | null; annual_budget: Money;
  actual_ytd: Money; forecast: Money; forecast_variance: Money; over: boolean; months?: BudgetCell[];
  forecast_method?: string; forecast_basis?: string; forecast_periods?: number; forecast_coverage?: string; manual_forecast?: ManualForecast | null;
}
export interface KindTotals { budget: Money; actual: Money; variance: Money; annual: Money; remaining: Money; forecast: Money; utilization: string | null }
export interface BudgetAnalysis {
  fiscal_year_id: number; budgets: { id: number; code: string; name: string; version: number; status: string }[]; periods: BudgetPeriod[];
  from_seq: number; to_seq: number; elapsed_seq: number; group_by: string; rows: BudgetRow[];
  forecast_method: string; forecast_methods: string[]; has_prior_year: boolean; expense: KindTotals; income: KindTotals;
  monthly: { seq: number; name: string; expense_budget: Money; expense_actual: Money; income_budget: Money; income_actual: Money }[];
}
export interface BudgetDashboard extends BudgetAnalysis {
  over_count: number; under_count: number; top_variances: BudgetRow[]; over_items: BudgetRow[]; by_cost_center: BudgetRow[]; by_budget: BudgetRow[];
}
export interface BudgetRule {
  id: number; name: string; budget_id: number | null; budget_name: string | null; account_id: number | null; account_code: string | null;
  account_name: string | null; cost_center_id: number | null; cost_center_name: string | null; role_id: number | null; role_name: string | null;
  policy: 'none' | 'warning' | 'soft' | 'hard'; threshold_percent: string; basis: 'period' | 'ytd' | 'annual'; is_active: boolean;
}
export interface BudgetEvent {
  id: number; journal_entry_id: number | null; journal_number: number | null; budget_id: number | null; budget_name: string | null;
  account_code: string | null; account_name: string | null; cost_center_name: string | null; policy: string; outcome: string; amount: Money;
  budget_amount: Money; actual_amount: Money; reason: string; user_name: string | null; created_at: string;
}
export interface BudgetEvaluation {
  budget_id: number; budget_name: string; line_id: number; account_code: string; account_name: string; cost_center: string; policy: string;
  basis: string; threshold_percent: string; rule_id: number | null; amount: Money; budget: Money; actual: Money; remaining: Money; utilization: string; exceeded: boolean;
}
export interface DrillRow {
  entry_id: number; entry_number: number; date: ISODate; description: string; source_type: string; source_id: number | null; account_code: string;
  account_name: string; cost_center_name: string | null; party_name: string | null; line_description: string; amount: Money;
}
export interface BudgetWarning { policy: string; outcome: string; budget: string; account_code: string; account_name: string; cost_center?: string; budget_amount: Money; actual_amount: Money; excess: Money; utilization: string }

// ---- Hardening additions ----
export interface CheckSummary {
  count: number; amount: Money; received: Bucket; issued: Bucket; live: Bucket; due_soon: Bucket; overdue: Bucket; returned: Bucket; cleared: Bucket; today: ISODate;
}

export interface PayrollCalcBasis {
  day_basis?: '30' | 'actual' | 'fixed'; period_days?: string; calendar_days?: number; monthly_hours?: string;
  tax_table_id?: number; tax_table_name?: string; tax_mode?: string; insurance_rule_id?: number; insurance_rule_name?: string;
}
export interface PayrollWarning { code: string; employee_id?: number; employee?: string }
export interface TaxTable {
  id: number; name: string; effective_from: ISODate; effective_to: ISODate | null; mode: 'monthly' | 'annual'; exemption: Money; brackets: Bracket[];
  notes: string; is_active: boolean; updated_at: string;
}
export interface InsuranceRule {
  id: number; name: string; effective_from: ISODate; effective_to: ISODate | null; employee_rate: string; employer_rate: string; unemployment_rate: string;
  ceiling: Money; eligible_codes: string[]; notes: string; is_active: boolean; updated_at: string;
}
export interface PayrollSettings { period_days: '30' | 'actual' | 'fixed'; divisor: string; monthly_hours: string }
export interface PayrollRules { tax_tables: TaxTable[]; insurance_rules: InsuranceRule[]; settings: PayrollSettings; formula_variables: string[] }
export interface LiabilityPayment {
  transaction_id: number; transaction_number: number; date: ISODate; amount: Money; treasury_account: string; journal_entry_id: number | null; status: string;
  user_name: string | null; created_at: string;
}
export interface PayrollLiability {
  id: number; period_id: number; period_name: string; kind: 'employee_tax' | 'employee_insurance' | 'employer_insurance' | 'other'; account_id: number;
  account_code: string; account_name: string; amount: Money; paid: Money; outstanding: Money; status: 'open' | 'partial' | 'paid';
  journal_entry_id: number | null; created_at: string; payments?: LiabilityPayment[];
}
export interface PaymentRow {
  employee_id: number; employee_code: string; employee_name: string; national_id: string; bank_name: string; iban: string; account_number: string;
  amount: Money; description: string; issues: string[];
}
export interface PaymentBatch {
  period_id: number; period_name: string; status: string; payment_date: ISODate; rows: PaymentRow[]; total: Money; count: number; issue_count: number;
  layouts: { code: string; plain: boolean }[];
}
export interface ImportIssue { row: number; column?: string; code: string; value?: string; employee?: string }
export interface ImportEmployee {
  employee_id: number; employee_code: string; employee_name: string; days: number; working_days: string; working_hours: string; absence_days: string;
  unpaid_leave_days: string; paid_leave_days: string; overtime_hours: string; mission_days: string; holiday_work_hours: string;
}
export interface ImportResult {
  file_name: string; format: string; summary: { total_rows: number; valid_rows: number; invalid_rows: number; warnings: number; imported_rows: number; employees: number };
  errors: ImportIssue[]; warnings: ImportIssue[]; employees: ImportEmployee[]; can_import: boolean; imported: boolean; import_id?: number;
}
export interface ImportLog { id: number; file_name: string; file_format: string; total_rows: number; imported_rows: number; warning_count: number; employee_count: number; user_name: string | null; created_at: string }

export interface ManualForecast { id: number; line_id: number; amount: Money; reason: string; user_name: string | null; created_at: string }
export interface BudgetEventDetail extends BudgetEvent {
  source_type: string; source_id: number | null; entry_reference: string; entry_description: string; budget_line_id: number | null; basis: string;
  threshold_percent: string | null; limit_amount: Money | null; exceeded_amount: Money | null; role_code: string;
}
