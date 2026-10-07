package payroll

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/treasury"
)

const (
	StatusDraft      = "draft"
	StatusCalculated = "calculated"
	StatusApproved   = "approved"
	StatusPosted     = "posted"
	StatusPaid       = "paid"
	StatusClosed     = "closed"

	SourceAccrual = "payroll"
	SourcePayment = "payroll_payment"
)

// ---- Settings ----

type PayrollSettings struct {
	// PeriodDays is the day basis of daily wages and pro-rata pay:
	//   "actual" – the calendar days of the payroll month (29–31),
	//   "30"     – every month counts 30 days,
	//   "fixed"  – every month counts Divisor days.
	PeriodDays string          `json:"period_days"`
	Divisor    decimal.Decimal `json:"divisor"`
	// MonthlyHours is the divisor of hourly wages.
	MonthlyHours decimal.Decimal `json:"monthly_hours"`
}

func DefaultSettings() PayrollSettings {
	return PayrollSettings{PeriodDays: "30", Divisor: decimal.NewFromInt(30), MonthlyHours: decimal.NewFromInt(192)}
}

func LoadSettings(ctx context.Context, q db.DBTX, companyID int64) (PayrollSettings, error) {
	s := DefaultSettings()
	var raw []byte
	err := q.QueryRow(ctx, `SELECT value FROM app_settings WHERE company_id = $1 AND key = 'payroll'`, companyID).Scan(&raw)
	if db.IsNoRows(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return DefaultSettings(), nil
	}
	if !s.Divisor.IsPositive() {
		s.Divisor = decimal.NewFromInt(30)
	}
	return s, nil
}

func SaveSettings(ctx context.Context, q db.DBTX, companyID, userID int64, s PayrollSettings) error {
	v := apperr.NewValidator()
	v.Check(s.PeriodDays == "30" || s.PeriodDays == "actual" || s.PeriodDays == "fixed", "period_days", "invalid")
	if s.PeriodDays != "fixed" {
		s.Divisor = decimal.NewFromInt(30)
	}
	v.Check(s.Divisor.IsPositive() && s.Divisor.LessThanOrEqual(decimal.NewFromInt(31)), "divisor", "invalid")
	v.Check(s.MonthlyHours.IsPositive() && s.MonthlyHours.LessThanOrEqual(decimal.NewFromInt(744)), "monthly_hours", "invalid")
	if err := v.Err(); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	_, err := q.Exec(ctx, `INSERT INTO app_settings (company_id, key, value, updated_by) VALUES ($1, 'payroll', $2, $3)
		ON CONFLICT (company_id, key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`, companyID, b, userID)
	return err
}

// ---- Periods ----

type Period struct {
	ID                  int64           `json:"id"`
	FiscalYearID        int64           `json:"fiscal_year_id"`
	Year                int             `json:"year"`
	Month               int             `json:"month"`
	Name                string          `json:"name"`
	StartDate           civil.Date      `json:"start_date"`
	EndDate             civil.Date      `json:"end_date"`
	Status              string          `json:"status"`
	PaymentDate         *civil.Date     `json:"payment_date"`
	TreasuryAccountID   *int64          `json:"treasury_account_id"`
	TreasuryAccountName *string         `json:"treasury_account_name"`
	GrossTotal          decimal.Decimal `json:"gross_total"`
	DeductionTotal      decimal.Decimal `json:"deduction_total"`
	NetTotal            decimal.Decimal `json:"net_total"`
	EmployerTotal       decimal.Decimal `json:"employer_total"`
	EmployeeCount       int             `json:"employee_count"`
	JournalEntryID      *int64          `json:"journal_entry_id"`
	JournalNumber       *int64          `json:"journal_number"`
	PaymentEntryID      *int64          `json:"payment_entry_id"`
	PaymentNumber       *int64          `json:"payment_number"`
	Notes               string          `json:"notes"`
	CalculatedAt        *time.Time      `json:"calculated_at"`
	ApprovedAt          *time.Time      `json:"approved_at"`
	ApprovedByName      *string         `json:"approved_by_name"`
	PostedAt            *time.Time      `json:"posted_at"`
	PaidAt              *time.Time      `json:"paid_at"`
	ClosedAt            *time.Time      `json:"closed_at"`
	CreatedAt           time.Time       `json:"created_at"`
	CalcBasis           json.RawMessage `json:"calc_basis"`
	Warnings            json.RawMessage `json:"warnings"`
	AllowedActions      []string        `json:"allowed_actions"`
}

const periodCols = `p.id, p.fiscal_year_id, p.year, p.month, p.name, p.start_date, p.end_date, p.status, p.payment_date, p.treasury_account_id, ta.name,
	p.gross_total, p.deduction_total, p.net_total, p.employer_total, p.employee_count, p.journal_entry_id, je.number, p.payment_entry_id, pe.number,
	p.notes, p.calculated_at, p.approved_at, au.full_name, p.posted_at, p.paid_at, p.closed_at, p.created_at, p.calc_basis, p.warnings`

const periodJoins = ` FROM payroll_periods p LEFT JOIN treasury_accounts ta ON ta.id = p.treasury_account_id
	LEFT JOIN journal_entries je ON je.id = p.journal_entry_id LEFT JOIN journal_entries pe ON pe.id = p.payment_entry_id
	LEFT JOIN users au ON au.id = p.approved_by`

func scanPeriod(s rowScanner, x *Period) error {
	err := s.Scan(&x.ID, &x.FiscalYearID, &x.Year, &x.Month, &x.Name, &x.StartDate, &x.EndDate, &x.Status, &x.PaymentDate, &x.TreasuryAccountID,
		&x.TreasuryAccountName, &x.GrossTotal, &x.DeductionTotal, &x.NetTotal, &x.EmployerTotal, &x.EmployeeCount, &x.JournalEntryID,
		&x.JournalNumber, &x.PaymentEntryID, &x.PaymentNumber, &x.Notes, &x.CalculatedAt, &x.ApprovedAt, &x.ApprovedByName, &x.PostedAt,
		&x.PaidAt, &x.ClosedAt, &x.CreatedAt, &x.CalcBasis, &x.Warnings)
	x.AllowedActions = PeriodActions(x.Status)
	return err
}

// periodMachine: action → (from statuses, to status).
var periodMachine = map[string]struct {
	from []string
	to   string
	perm string
}{
	"calculate": {[]string{StatusDraft, StatusCalculated}, StatusCalculated, "payroll.calculate"},
	"approve":   {[]string{StatusCalculated}, StatusApproved, "payroll.approve"},
	"reopen":    {[]string{StatusCalculated, StatusApproved}, StatusDraft, "payroll.approve"},
	"post":      {[]string{StatusApproved}, StatusPosted, "payroll.post"},
	"unpost":    {[]string{StatusPosted}, StatusApproved, "payroll.post"},
	"pay":       {[]string{StatusPosted}, StatusPaid, "payroll.pay"},
	"unpay":     {[]string{StatusPaid}, StatusPosted, "payroll.pay"},
	"close":     {[]string{StatusPaid}, StatusClosed, "payroll.close"},
}

var periodOrder = []string{"calculate", "approve", "post", "pay", "close", "reopen", "unpost", "unpay"}

// PeriodActions lists the actions available in a status.
func PeriodActions(status string) []string {
	out := []string{}
	for _, a := range periodOrder {
		for _, f := range periodMachine[a].from {
			if f == status {
				out = append(out, a)
			}
		}
	}
	return out
}

// PeriodPermission returns the permission an action needs.
func PeriodPermission(action string) string {
	if m, ok := periodMachine[action]; ok {
		return m.perm
	}
	return "payroll.edit"
}

func checkTransition(status, action string) error {
	m, ok := periodMachine[action]
	if ok {
		for _, f := range m.from {
			if f == status {
				return nil
			}
		}
	}
	return apperr.Rule("invalid_transition", "this action is not available in the current status").WithParams("action", action, "status", status)
}

func GetPeriod(ctx context.Context, q db.DBTX, companyID, id int64) (*Period, error) {
	var x Period
	err := scanPeriod(q.QueryRow(ctx, `SELECT `+periodCols+periodJoins+` WHERE p.company_id = $1 AND p.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("payroll_period")
	}
	return &x, err
}

func ListPeriods(ctx context.Context, q db.DBTX, companyID int64, year int) ([]Period, error) {
	f := db.NewFilter("p.company_id = $1", companyID)
	if year > 0 {
		f.Add("p.year = $N", year)
	}
	rows, err := q.Query(ctx, `SELECT `+periodCols+periodJoins+` WHERE `+f.Where()+` ORDER BY p.year DESC, p.month DESC`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Period{}
	for rows.Next() {
		var x Period
		if err := scanPeriod(rows, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type PeriodInput struct {
	Year  int    `json:"year"`
	Month int    `json:"month"`
	Notes string `json:"notes"`
}

// MonthBounds returns the first and last day of a month in the company calendar.
func MonthBounds(year, month int, calendar string) (civil.Date, civil.Date, string, string) {
	if calendar == "jalali" {
		start := civil.Date{Time: jalali.ToGregorian(year, month, 1)}
		end := civil.Date{Time: jalali.ToGregorian(year, month, jalali.MonthLength(year, month))}
		return start, end, fmt.Sprintf("%s %d", jalali.MonthNames[month-1], year), fmt.Sprintf("%s %d", jalali.MonthNamesEn[month-1], year)
	}
	start := civil.New(year, time.Month(month), 1)
	end := civil.Date{Time: start.AddDate(0, 1, -1)}
	return start, end, start.Format("January 2006"), start.Format("January 2006")
}

func CreatePeriod(ctx context.Context, q db.DBTX, companyID, userID int64, in PeriodInput) (int64, error) {
	v := apperr.NewValidator()
	v.Check(in.Year >= 1300 && in.Year <= 2200, "year", "invalid")
	v.Check(in.Month >= 1 && in.Month <= 12, "month", "invalid")
	if err := v.Err(); err != nil {
		return 0, err
	}
	var calendar string
	if err := q.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1`, companyID).Scan(&calendar); err != nil {
		return 0, err
	}
	start, end, name, _ := MonthBounds(in.Year, in.Month, calendar)
	var fyID int64
	err := q.QueryRow(ctx, `SELECT id FROM fiscal_years WHERE company_id = $1 AND start_date <= $2 AND end_date >= $3`, companyID, start, end).Scan(&fyID)
	if db.IsNoRows(err) {
		return 0, apperr.Rule("no_fiscal_year", "no fiscal year covers this date").WithParams("date", start.String())
	}
	if err != nil {
		return 0, err
	}
	var id int64
	err = q.QueryRow(ctx, `INSERT INTO payroll_periods (company_id, fiscal_year_id, year, month, name, start_date, end_date, notes, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`, companyID, fyID, in.Year, in.Month, name, start, end, in.Notes, userID).Scan(&id)
	return id, err
}

func DeletePeriod(ctx context.Context, q db.DBTX, companyID, id int64) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if p.Status != StatusDraft && p.Status != StatusCalculated {
		return apperr.Rule("not_deletable", "only unapproved payrolls can be deleted")
	}
	_, err = q.Exec(ctx, `DELETE FROM payroll_periods WHERE id = $1`, id)
	return err
}

func lockPeriod(ctx context.Context, q db.DBTX, companyID, id int64) (*Period, error) {
	var dummy int64
	if err := q.QueryRow(ctx, `SELECT id FROM payroll_periods WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&dummy); err != nil {
		if db.IsNoRows(err) {
			return nil, apperr.NotFound("payroll_period")
		}
		return nil, err
	}
	return GetPeriod(ctx, q, companyID, id)
}

// ---- Inputs ----

type InputItem struct {
	ComponentID int64           `json:"component_id"`
	Amount      decimal.Decimal `json:"amount"`
}

type InputRow struct {
	EmployeeID   int64       `json:"employee_id"`
	EmployeeCode string      `json:"employee_code"`
	EmployeeName string      `json:"employee_name"`
	Saved        bool        `json:"saved"`
	Source       string      `json:"source"`
	Items        []InputItem `json:"items"`
	Attendance
}

type eligible struct {
	Employee
	defaultDays decimal.Decimal
}

// periodDays is the divisor of daily wages for a period under the settings.
func periodDays(p *Period, s PayrollSettings) decimal.Decimal {
	switch s.PeriodDays {
	case "actual":
		return decimal.NewFromInt(int64(p.EndDate.Sub(p.StartDate.Time).Hours()/24) + 1)
	case "fixed":
		if s.Divisor.IsPositive() {
			return s.Divisor
		}
	}
	return decimal.NewFromInt(30)
}

// eligibleEmployees returns employees employed during the period, with their
// default working days (pro rata for joiners and leavers).
func eligibleEmployees(ctx context.Context, q db.DBTX, companyID int64, p *Period, s PayrollSettings) ([]eligible, error) {
	rows, err := q.Query(ctx, `SELECT `+empCols+empJoins+` WHERE e.company_id = $1 AND e.hire_date <= $3
		AND (e.termination_date IS NULL OR e.termination_date >= $2) AND (e.is_active OR e.termination_date IS NOT NULL)
		ORDER BY e.code, e.id`, companyID, p.StartDate, p.EndDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	full := periodDays(p, s)
	actual := int64(p.EndDate.Sub(p.StartDate.Time).Hours()/24) + 1
	var out []eligible
	for rows.Next() {
		var e eligible
		if err := scanEmployee(rows, &e.Employee); err != nil {
			return nil, err
		}
		from, to := p.StartDate, p.EndDate
		if e.HireDate.After(from) {
			from = e.HireDate
		}
		if e.TerminationDate != nil && e.TerminationDate.Before(to) {
			to = *e.TerminationDate
		}
		days := int64(to.Sub(from.Time).Hours()/24) + 1
		if days >= actual {
			e.defaultDays = full
		} else {
			e.defaultDays = decimal.Min(full, decimal.NewFromInt(days))
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Inputs returns the working data of every eligible employee (saved or default).
func Inputs(ctx context.Context, q db.DBTX, companyID, periodID int64) ([]InputRow, error) {
	p, err := GetPeriod(ctx, q, companyID, periodID)
	if err != nil {
		return nil, err
	}
	s, err := LoadSettings(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	emps, err := eligibleEmployees(ctx, q, companyID, p, s)
	if err != nil {
		return nil, err
	}
	saved := map[int64]InputRow{}
	rows, err := q.Query(ctx, `SELECT employee_id, working_days, working_hours, absence_days, unpaid_leave_days, paid_leave_days, overtime_hours,
		mission_days, holiday_work_hours, source FROM payroll_inputs WHERE period_id = $1`, periodID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r InputRow
		if err := rows.Scan(&r.EmployeeID, &r.WorkingDays, &r.WorkingHours, &r.AbsenceDays, &r.UnpaidLeaveDays, &r.PaidLeaveDays, &r.OvertimeHours, &r.MissionDays,
			&r.HolidayWorkHours, &r.Source); err != nil {
			rows.Close()
			return nil, err
		}
		r.Saved = true
		saved[r.EmployeeID] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := map[int64][]InputItem{}
	irows, err := q.Query(ctx, `SELECT employee_id, component_id, amount FROM payroll_input_items WHERE period_id = $1 ORDER BY component_id`, periodID)
	if err != nil {
		return nil, err
	}
	for irows.Next() {
		var eid int64
		var it InputItem
		if err := irows.Scan(&eid, &it.ComponentID, &it.Amount); err != nil {
			irows.Close()
			return nil, err
		}
		items[eid] = append(items[eid], it)
	}
	irows.Close()
	if err := irows.Err(); err != nil {
		return nil, err
	}
	out := []InputRow{}
	for _, e := range emps {
		r, ok := saved[e.ID]
		if !ok {
			r = InputRow{EmployeeID: e.ID, Source: "default", Attendance: Attendance{WorkingDays: e.defaultDays}}
		}
		r.EmployeeCode, r.EmployeeName = e.Code, e.FullName
		r.Items = items[e.ID]
		if r.Items == nil {
			r.Items = []InputItem{}
		}
		out = append(out, r)
	}
	return out, nil
}

// SaveInputs stores working data; a calculated payroll goes back to draft.
func SaveInputs(ctx context.Context, q db.DBTX, companyID, periodID int64, in []InputRow) error {
	p, err := lockPeriod(ctx, q, companyID, periodID)
	if err != nil {
		return err
	}
	if p.Status != StatusDraft && p.Status != StatusCalculated {
		return apperr.Rule("payroll_locked", "the payroll is approved")
	}
	s, err := LoadSettings(ctx, q, companyID)
	if err != nil {
		return err
	}
	emps, err := eligibleEmployees(ctx, q, companyID, p, s)
	if err != nil {
		return err
	}
	ok := map[int64]bool{}
	for _, e := range emps {
		ok[e.ID] = true
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	v := apperr.NewValidator()
	for i, r := range in {
		f := fmt.Sprintf("%d.", i)
		v.Check(ok[r.EmployeeID], f+"employee_id", "invalid")
		for _, x := range []decimal.Decimal{r.WorkingDays, r.WorkingHours, r.AbsenceDays, r.UnpaidLeaveDays, r.PaidLeaveDays, r.OvertimeHours, r.MissionDays, r.HolidayWorkHours} {
			v.Check(!x.IsNegative() && x.LessThan(decimal.NewFromInt(1000)), f+"working_days", "invalid")
		}
		for j, it := range r.Items {
			v.Check(!it.Amount.IsNegative() && it.Amount.Equal(it.Amount.Round(places)), fmt.Sprintf("%d.items.%d.amount", i, j), "invalid")
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	for _, r := range in {
		src := r.Source
		if src == "" || src == "default" {
			src = "manual"
		}
		if _, err := q.Exec(ctx, `
			INSERT INTO payroll_inputs (period_id, employee_id, working_days, absence_days, unpaid_leave_days, paid_leave_days, overtime_hours,
			                            mission_days, holiday_work_hours, source, working_hours)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (period_id, employee_id) DO UPDATE SET working_days = EXCLUDED.working_days, absence_days = EXCLUDED.absence_days,
			       working_hours = EXCLUDED.working_hours,
			       unpaid_leave_days = EXCLUDED.unpaid_leave_days, paid_leave_days = EXCLUDED.paid_leave_days, overtime_hours = EXCLUDED.overtime_hours,
			       mission_days = EXCLUDED.mission_days, holiday_work_hours = EXCLUDED.holiday_work_hours, source = EXCLUDED.source`,
			periodID, r.EmployeeID, r.WorkingDays, r.AbsenceDays, r.UnpaidLeaveDays, r.PaidLeaveDays, r.OvertimeHours, r.MissionDays,
			r.HolidayWorkHours, src, r.WorkingHours); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM payroll_input_items WHERE period_id = $1 AND employee_id = $2`, periodID, r.EmployeeID); err != nil {
			return err
		}
		for _, it := range r.Items {
			if it.Amount.IsZero() {
				continue
			}
			if _, err := q.Exec(ctx, `INSERT INTO payroll_input_items (period_id, employee_id, component_id, amount)
				SELECT $1, $2, id, $4 FROM salary_components WHERE company_id = $5 AND id = $3`, periodID, r.EmployeeID, it.ComponentID, it.Amount, companyID); err != nil {
				return err
			}
		}
	}
	if p.Status == StatusCalculated {
		_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'draft', updated_at = now() WHERE id = $1`, periodID)
	}
	return err
}

// ---- Calculation ----

// CalculatePeriod (re)computes every payslip of a draft or calculated payroll.
func CalculatePeriod(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, "calculate"); err != nil {
		return err
	}
	s, err := LoadSettings(ctx, q, companyID)
	if err != nil {
		return err
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	all, err := ListComponents(ctx, q, companyID, true)
	if err != nil {
		return err
	}
	var comps []Component
	needTax, needIns := false, false
	for _, c := range all {
		if c.ActiveIn(p.StartDate, p.EndDate) {
			comps = append(comps, c)
			needTax = needTax || c.Method == "tax_table"
			needIns = needIns || c.Method == "insurance_rule"
		}
	}
	if len(comps) == 0 {
		return apperr.Rule("no_salary_components", "define salary components first")
	}
	// Statutory rules in force at the end of the payroll month.
	tax, err := TaxTableFor(ctx, q, companyID, p.EndDate)
	if err != nil {
		return err
	}
	ins, err := InsuranceRuleFor(ctx, q, companyID, p.EndDate)
	if err != nil {
		return err
	}
	var warnings []Warning
	if needTax && tax == nil {
		warnings = append(warnings, Warning{Code: "no_tax_table"})
	}
	if needIns && ins == nil {
		warnings = append(warnings, Warning{Code: "no_insurance_rule"})
	}
	inputs, err := Inputs(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		return apperr.Rule("no_employees", "no employee is employed in this period")
	}
	if _, err := q.Exec(ctx, `DELETE FROM payslips WHERE period_id = $1`, id); err != nil {
		return err
	}
	settings := Settings{PeriodDays: periodDays(p, s), MonthlyHours: s.MonthlyHours, Places: places, Tax: tax, Insurance: ins}
	basis := CalcBasis{DayBasis: s.PeriodDays, PeriodDays: settings.PeriodDays, MonthlyHours: s.MonthlyHours,
		CalendarDays: int(p.EndDate.Sub(p.StartDate.Time).Hours()/24) + 1}
	if tax != nil {
		basis.TaxTableID, basis.TaxTableName, basis.TaxMode = &tax.ID, tax.Name, tax.Mode
	}
	if ins != nil {
		basis.InsuranceRuleID, basis.InsuranceRuleName = &ins.ID, ins.Name
	}
	var gross, ded, net, employer decimal.Decimal
	for _, in := range inputs {
		emp, err := GetEmployee(ctx, q, companyID, in.EmployeeID)
		if err != nil {
			return err
		}
		data := EmployeeData{BaseSalary: emp.BaseSalary, ChildrenCount: emp.ChildrenCount, InsuranceExempt: emp.InsuranceExempt, TaxExempt: emp.TaxExempt,
			Overrides: map[int64]decimal.Decimal{}, Manual: map[int64]decimal.Decimal{}}
		for _, o := range emp.Components {
			data.Overrides[o.ComponentID] = o.Rate
		}
		for _, it := range in.Items {
			data.Manual[it.ComponentID] = it.Amount
		}
		res := Calculate(comps, data, in.Attendance, settings)
		if len(res.Errors) > 0 {
			return apperr.Rule("formula_error", "a salary component formula cannot be evaluated").WithParams("component", res.Errors[0], "employee", emp.FullName)
		}
		if res.Net.IsNegative() {
			return apperr.Rule("negative_net", "deductions exceed earnings").WithParams("employee", emp.FullName)
		}
		if res.Net.IsZero() {
			warnings = append(warnings, Warning{Code: "zero_net", EmployeeID: emp.ID, Employee: emp.FullName})
		} else if emp.IBAN == "" && emp.BankAccountNumber == "" {
			warnings = append(warnings, Warning{Code: "missing_bank_account", EmployeeID: emp.ID, Employee: emp.FullName})
		}
		if in.Source == "default" {
			warnings = append(warnings, Warning{Code: "default_inputs", EmployeeID: emp.ID, Employee: emp.FullName})
		}
		allocs := emp.Allocations
		if len(allocs) == 0 && emp.CostCenterID != nil {
			allocs = []Allocation{{CostCenterID: *emp.CostCenterID, Percent: hundred}}
		}
		allocJSON, _ := json.Marshal(allocs)
		var slipID int64
		if err := q.QueryRow(ctx, `
			INSERT INTO payslips (period_id, employee_id, employee_code, employee_name, department_id, position, base_salary, working_days, gross,
			                      total_deductions, net, employer_cost, taxable_income, insurable_income, iban, bank_account_number, allocations)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
			id, emp.ID, emp.Code, emp.FullName, emp.DepartmentID, emp.Position, emp.BaseSalary, in.WorkingDays, res.Gross, res.Deductions, res.Net,
			res.Employer, res.Taxable, res.Insurable, emp.IBAN, emp.BankAccountNumber, allocJSON).Scan(&slipID); err != nil {
			return err
		}
		for _, l := range res.Lines {
			c := l.Component
			if _, err := q.Exec(ctx, `INSERT INTO payslip_lines (payslip_id, component_id, kind, category, code, name, name_en, quantity, rate, amount, account_id)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, slipID, c.ID, c.Kind, c.Category, c.Code, c.Name, c.NameEn,
				l.Quantity.Round(2), l.Rate, l.Amount, c.AccountID); err != nil {
				return err
			}
		}
		gross, ded, net, employer = gross.Add(res.Gross), ded.Add(res.Deductions), net.Add(res.Net), employer.Add(res.Employer)
	}
	if warnings == nil {
		warnings = []Warning{}
	}
	basisJSON, _ := json.Marshal(basis)
	warnJSON, _ := json.Marshal(warnings)
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'calculated', gross_total = $2, deduction_total = $3, net_total = $4, employer_total = $5,
		employee_count = $6, calculated_at = now(), calculated_by = $7, updated_at = now(), calc_basis = $8, warnings = $9 WHERE id = $1`,
		id, gross, ded, net, employer, len(inputs), userID, basisJSON, warnJSON)
	return err
}

// CalcBasis records how a payroll was calculated, so the result can be explained later.
type CalcBasis struct {
	DayBasis          string          `json:"day_basis"` // actual | 30 | fixed
	PeriodDays        decimal.Decimal `json:"period_days"`
	CalendarDays      int             `json:"calendar_days"`
	MonthlyHours      decimal.Decimal `json:"monthly_hours"`
	TaxTableID        *int64          `json:"tax_table_id,omitempty"`
	TaxTableName      string          `json:"tax_table_name,omitempty"`
	TaxMode           string          `json:"tax_mode,omitempty"`
	InsuranceRuleID   *int64          `json:"insurance_rule_id,omitempty"`
	InsuranceRuleName string          `json:"insurance_rule_name,omitempty"`
}

// Warning is a non-blocking finding of a calculation, shown in the review step.
type Warning struct {
	Code       string `json:"code"`
	EmployeeID int64  `json:"employee_id,omitempty"`
	Employee   string `json:"employee,omitempty"`
}

// ---- Status transitions with accounting ----

type ActionInput struct {
	Date              civil.Date `json:"date"`
	TreasuryAccountID *int64     `json:"treasury_account_id"`
	Reason            string     `json:"reason"`
}

func simpleTransition(ctx context.Context, q db.DBTX, companyID, userID, id int64, action string, extra string) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, action); err != nil {
		return err
	}
	if action == "approve" && p.EmployeeCount == 0 {
		return apperr.Rule("no_employees", "the payroll has no payslips")
	}
	args := []any{id, periodMachine[action].to}
	if strings.Contains(extra, "$3") {
		args = append(args, userID)
	}
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = $2, updated_at = now()`+extra+` WHERE id = $1`, args...)
	return err
}

func Approve(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	return simpleTransition(ctx, q, companyID, userID, id, "approve", ", approved_at = now(), approved_by = $3")
}

func Reopen(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	return simpleTransition(ctx, q, companyID, userID, id, "reopen", ", approved_at = NULL, approved_by = NULL")
}

func Close(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	return simpleTransition(ctx, q, companyID, userID, id, "close", ", closed_at = now(), closed_by = $3")
}

type slipLine struct {
	kind, category string
	amount         decimal.Decimal
	accountID      *int64
	allocations    []Allocation
}

type accKey struct {
	account int64
	cc      int64
}

// AccrualLines builds the payroll accrual entry: salary and employer expense by
// cost center against salaries, insurance, tax and other deduction accounts.
func AccrualLines(lines []slipLine, net decimal.Decimal, m map[string]int64, places int32) ([]ledger.LineInput, error) {
	req := func(key string) (int64, error) { return ledger.Require(m, key) }
	salaries, err := req(ledger.MapPayrollSalariesPayable)
	if err != nil {
		return nil, err
	}
	expense := map[accKey]decimal.Decimal{}
	credit := map[int64]decimal.Decimal{}
	addExpense := func(acc int64, allocs []Allocation, amount decimal.Decimal) {
		if len(allocs) == 0 {
			expense[accKey{acc, 0}] = expense[accKey{acc, 0}].Add(amount)
			return
		}
		pcts := make([]decimal.Decimal, len(allocs))
		for i, a := range allocs {
			pcts[i] = a.Percent
		}
		for i, part := range Split(amount, pcts, places) {
			k := accKey{acc, allocs[i].CostCenterID}
			expense[k] = expense[k].Add(part)
		}
	}
	liability := func(category string, override *int64, kind string) (int64, error) {
		switch {
		case category == "tax":
			return req(ledger.MapPayrollTaxPayable)
		case category == "insurance":
			return req(ledger.MapPayrollInsurancePayable)
		case kind == KindEmployer:
			return req(ledger.MapPayrollInsurancePayable)
		case override != nil:
			return *override, nil
		default:
			return req(ledger.MapPayrollOtherDeductions)
		}
	}
	for _, l := range lines {
		switch {
		case l.kind == KindEarning || (l.kind == KindDeduction && l.category == "unpaid_leave"):
			acc := int64(0)
			if l.accountID != nil {
				acc = *l.accountID
			} else if acc, err = req(ledger.MapPayrollSalaryExpense); err != nil {
				return nil, err
			}
			amount := l.amount
			if l.kind == KindDeduction {
				amount = amount.Neg()
			}
			addExpense(acc, l.allocations, amount)
		case l.kind == KindDeduction:
			var override *int64
			if l.category != "tax" && l.category != "insurance" {
				override = l.accountID
			}
			acc, err := liability(l.category, override, l.kind)
			if err != nil {
				return nil, err
			}
			credit[acc] = credit[acc].Add(l.amount)
		case l.kind == KindEmployer:
			acc := int64(0)
			if l.accountID != nil {
				acc = *l.accountID
			} else if acc, err = req(ledger.MapPayrollEmployerExpense); err != nil {
				return nil, err
			}
			addExpense(acc, l.allocations, l.amount)
			lacc, err := liability(l.category, nil, l.kind)
			if err != nil {
				return nil, err
			}
			credit[lacc] = credit[lacc].Add(l.amount)
		}
	}
	credit[salaries] = credit[salaries].Add(net)
	keys := make([]accKey, 0, len(expense))
	for k := range expense {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].account != keys[j].account {
			return keys[i].account < keys[j].account
		}
		return keys[i].cc < keys[j].cc
	})
	var b ledger.Builder
	for _, k := range keys {
		var cc *int64
		if k.cc != 0 {
			c := k.cc
			cc = &c
		}
		b.Debit(k.account, nil, cc, "", expense[k])
	}
	caccs := make([]int64, 0, len(credit))
	for a := range credit {
		caccs = append(caccs, a)
	}
	sort.Slice(caccs, func(i, j int) bool { return caccs[i] < caccs[j] })
	for _, a := range caccs {
		b.Credit(a, nil, nil, "", credit[a])
	}
	return b.Lines(), nil
}

// Post books an approved payroll: the accrual entry dated at the period end.
func Post(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, "post"); err != nil {
		return err
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	m, err := ledger.Mappings(ctx, q, companyID)
	if err != nil {
		return err
	}
	rows, err := q.Query(ctx, `SELECT l.kind, l.category, l.amount, l.account_id, s.allocations FROM payslip_lines l JOIN payslips s ON s.id = l.payslip_id
		WHERE s.period_id = $1 ORDER BY s.id, l.id`, id)
	if err != nil {
		return err
	}
	var lines []slipLine
	for rows.Next() {
		var l slipLine
		var allocs []byte
		if err := rows.Scan(&l.kind, &l.category, &l.amount, &l.accountID, &allocs); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(allocs, &l.allocations); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	jl, err := AccrualLines(lines, p.NetTotal, m, places)
	if err != nil {
		return err
	}
	eid, err := ledger.PostAuto(ctx, q, companyID, userID, p.EndDate, "Payroll "+p.Name, fmt.Sprintf("PAYROLL-%d-%02d", p.Year, p.Month), jl,
		ledger.Source{Type: SourceAccrual, ID: id})
	if err != nil {
		return err
	}
	if err := createLiabilities(ctx, q, companyID, id, eid); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'posted', journal_entry_id = $2, posted_at = now(), posted_by = $3, updated_at = now()
		WHERE id = $1`, id, eid, userID)
	return err
}

// Unpost reverses the accrual of a posted, unpaid payroll.
func Unpost(ctx context.Context, q db.DBTX, companyID, userID, id int64, date civil.Date) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, "unpost"); err != nil {
		return err
	}
	if err := removeLiabilities(ctx, q, id); err != nil {
		return err
	}
	if _, err := ledger.Cancel(ctx, q, companyID, userID, *p.JournalEntryID, date, true); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'approved', journal_entry_id = NULL, posted_at = NULL, posted_by = NULL, updated_at = now()
		WHERE id = $1`, id)
	return err
}

// Pay books the payment of net salaries: Dr salaries payable / Cr cash or bank.
func Pay(ctx context.Context, q db.DBTX, companyID, userID, id int64, in ActionInput) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, "pay"); err != nil {
		return err
	}
	if in.Date.IsZero() {
		in.Date = civil.Today()
	}
	v := apperr.NewValidator()
	v.Check(in.TreasuryAccountID != nil, "treasury_account_id", "required")
	v.Check(!in.Date.Before(p.StartDate), "date", "before_date")
	if err := v.Err(); err != nil {
		return err
	}
	t, err := treasury.GetAccount(ctx, q, companyID, *in.TreasuryAccountID)
	if err != nil {
		return err
	}
	if !t.IsActive {
		return apperr.Rule("treasury_inactive", "the cash/bank account is disabled")
	}
	if t.Kind == "cash" && t.Balance.LessThan(p.NetTotal) {
		return apperr.Rule("insufficient_funds", "the cash balance is not sufficient").WithParams("balance", t.Balance.String())
	}
	salaries, err := ledger.Mapping(ctx, q, companyID, ledger.MapPayrollSalariesPayable)
	if err != nil {
		return err
	}
	var b ledger.Builder
	b.Debit(salaries, nil, nil, "", p.NetTotal)
	b.Credit(t.AccountID, nil, nil, "", p.NetTotal)
	eid, err := ledger.PostAuto(ctx, q, companyID, userID, in.Date, "Salary payment "+p.Name, fmt.Sprintf("PAYROLL-%d-%02d", p.Year, p.Month), b.Lines(),
		ledger.Source{Type: SourcePayment, ID: id})
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'paid', payment_entry_id = $2, payment_date = $3, treasury_account_id = $4,
		paid_at = now(), paid_by = $5, updated_at = now() WHERE id = $1`, id, eid, in.Date, t.ID, userID)
	return err
}

// Unpay reverses a salary payment.
func Unpay(ctx context.Context, q db.DBTX, companyID, userID, id int64, date civil.Date) error {
	p, err := lockPeriod(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if err := checkTransition(p.Status, "unpay"); err != nil {
		return err
	}
	if _, err := ledger.Cancel(ctx, q, companyID, userID, *p.PaymentEntryID, date, true); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE payroll_periods SET status = 'posted', payment_entry_id = NULL, payment_date = NULL, paid_at = NULL, paid_by = NULL,
		updated_at = now() WHERE id = $1`, id)
	return err
}

// ---- Payslips ----

type PayslipLine struct {
	ComponentID int64           `json:"component_id"`
	Kind        string          `json:"kind"`
	Category    string          `json:"category"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	NameEn      string          `json:"name_en"`
	Quantity    decimal.Decimal `json:"quantity"`
	Rate        decimal.Decimal `json:"rate"`
	Amount      decimal.Decimal `json:"amount"`
}

type Payslip struct {
	ID                int64           `json:"id"`
	PeriodID          int64           `json:"period_id"`
	PeriodName        string          `json:"period_name"`
	PeriodStatus      string          `json:"period_status"`
	StartDate         civil.Date      `json:"start_date"`
	EndDate           civil.Date      `json:"end_date"`
	PaymentDate       *civil.Date     `json:"payment_date"`
	EmployeeID        int64           `json:"employee_id"`
	EmployeeCode      string          `json:"employee_code"`
	EmployeeName      string          `json:"employee_name"`
	NationalID        string          `json:"national_id"`
	InsuranceNumber   string          `json:"insurance_number"`
	DepartmentName    *string         `json:"department_name"`
	Position          string          `json:"position"`
	BaseSalary        decimal.Decimal `json:"base_salary"`
	WorkingDays       decimal.Decimal `json:"working_days"`
	Gross             decimal.Decimal `json:"gross"`
	TotalDeductions   decimal.Decimal `json:"total_deductions"`
	Net               decimal.Decimal `json:"net"`
	EmployerCost      decimal.Decimal `json:"employer_cost"`
	TaxableIncome     decimal.Decimal `json:"taxable_income"`
	InsurableIncome   decimal.Decimal `json:"insurable_income"`
	IBAN              string          `json:"iban"`
	BankAccountNumber string          `json:"bank_account_number"`
	Allocations       json.RawMessage `json:"allocations"`
	Lines             []PayslipLine   `json:"lines,omitempty"`
}

const slipCols = `s.id, s.period_id, p.name, p.status, p.start_date, p.end_date, p.payment_date, s.employee_id, s.employee_code, s.employee_name,
	e.national_id, e.insurance_number, d.name, s.position, s.base_salary, s.working_days, s.gross, s.total_deductions, s.net, s.employer_cost,
	s.taxable_income, s.insurable_income, s.iban, s.bank_account_number, s.allocations`

const slipJoins = ` FROM payslips s JOIN payroll_periods p ON p.id = s.period_id JOIN employees e ON e.id = s.employee_id
	LEFT JOIN cost_centers d ON d.id = s.department_id`

func scanSlip(r rowScanner, s *Payslip) error {
	return r.Scan(&s.ID, &s.PeriodID, &s.PeriodName, &s.PeriodStatus, &s.StartDate, &s.EndDate, &s.PaymentDate, &s.EmployeeID, &s.EmployeeCode,
		&s.EmployeeName, &s.NationalID, &s.InsuranceNumber, &s.DepartmentName, &s.Position, &s.BaseSalary, &s.WorkingDays, &s.Gross,
		&s.TotalDeductions, &s.Net, &s.EmployerCost, &s.TaxableIncome, &s.InsurableIncome, &s.IBAN, &s.BankAccountNumber, &s.Allocations)
}

func ListPayslips(ctx context.Context, q db.DBTX, companyID, periodID int64) ([]Payslip, error) {
	rows, err := q.Query(ctx, `SELECT `+slipCols+slipJoins+` WHERE p.company_id = $1 AND s.period_id = $2 ORDER BY s.employee_code, s.id`, companyID, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Payslip{}
	for rows.Next() {
		var s Payslip
		if err := scanSlip(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetPayslip(ctx context.Context, q db.DBTX, companyID, id int64) (*Payslip, error) {
	var s Payslip
	err := scanSlip(q.QueryRow(ctx, `SELECT `+slipCols+slipJoins+` WHERE p.company_id = $1 AND s.id = $2`, companyID, id), &s)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("payslip")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT component_id, kind, category, code, name, name_en, quantity, rate, amount FROM payslip_lines
		WHERE payslip_id = $1 ORDER BY kind = 'employer', kind = 'deduction', id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s.Lines = []PayslipLine{}
	for rows.Next() {
		var l PayslipLine
		if err := rows.Scan(&l.ComponentID, &l.Kind, &l.Category, &l.Code, &l.Name, &l.NameEn, &l.Quantity, &l.Rate, &l.Amount); err != nil {
			return nil, err
		}
		s.Lines = append(s.Lines, l)
	}
	return &s, rows.Err()
}
