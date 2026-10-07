// Package payroll manages employees, configurable salary components, monthly
// payroll runs and payslips. Calculation is pure decimal arithmetic (calc.go);
// accruals and payments are posted through the ledger.
package payroll

import (
	"sort"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
)

var hundred = decimal.NewFromInt(100)

const (
	KindEarning   = "earning"
	KindDeduction = "deduction"
	KindEmployer  = "employer"
)

// Bracket is one step of a progressive tax table. Upto is the upper bound of
// the taxable base for this rate; nil means "and above".
type Bracket struct {
	Upto *decimal.Decimal `json:"upto"`
	Rate decimal.Decimal  `json:"rate"`
}

type Component struct {
	ID            int64           `json:"id"`
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	NameEn        string          `json:"name_en"`
	Kind          string          `json:"kind"`
	Category      string          `json:"category"`
	Method        string          `json:"method"`
	Rate          decimal.Decimal `json:"rate"`
	QuantityField string          `json:"quantity_field"`
	Brackets      []Bracket       `json:"brackets"`
	Exemption     decimal.Decimal `json:"exemption"`
	Ceiling       decimal.Decimal `json:"ceiling"`
	Taxable       bool            `json:"taxable"`
	Insurable     bool            `json:"insurable"`
	AccountID     *int64          `json:"account_id"`
	Seq           int             `json:"seq"`
	IsActive      bool            `json:"is_active"`
	// Variability is informational: fixed (same every month) or variable.
	Variability string `json:"variability"`
	// Formula is the expression of method "formula" (see formula.go).
	Formula string `json:"formula"`
	// InsuranceShare selects the rate of the insurance rule for method "insurance_rule".
	InsuranceShare string      `json:"insurance_share"`
	EffectiveFrom  *civil.Date `json:"effective_from"`
	EffectiveTo    *civil.Date `json:"effective_to"`
}

// ActiveIn reports whether the component applies to a period.
func (c Component) ActiveIn(start, end civil.Date) bool {
	if c.EffectiveFrom != nil && !c.EffectiveFrom.IsZero() && c.EffectiveFrom.After(end) {
		return false
	}
	if c.EffectiveTo != nil && !c.EffectiveTo.IsZero() && c.EffectiveTo.Before(start) {
		return false
	}
	return true
}

// Attendance is the working data of one employee for one period.
type Attendance struct {
	WorkingDays      decimal.Decimal `json:"working_days"`
	WorkingHours     decimal.Decimal `json:"working_hours"`
	AbsenceDays      decimal.Decimal `json:"absence_days"`
	UnpaidLeaveDays  decimal.Decimal `json:"unpaid_leave_days"`
	PaidLeaveDays    decimal.Decimal `json:"paid_leave_days"`
	OvertimeHours    decimal.Decimal `json:"overtime_hours"`
	MissionDays      decimal.Decimal `json:"mission_days"`
	HolidayWorkHours decimal.Decimal `json:"holiday_work_hours"`
}

func (a Attendance) field(name string) decimal.Decimal {
	switch name {
	case "working_days":
		return a.WorkingDays
	case "working_hours":
		return a.WorkingHours
	case "absence_days":
		return a.AbsenceDays
	case "unpaid_leave_days":
		return a.UnpaidLeaveDays
	case "paid_leave_days":
		return a.PaidLeaveDays
	case "overtime_hours":
		return a.OvertimeHours
	case "mission_days":
		return a.MissionDays
	case "holiday_work_hours":
		return a.HolidayWorkHours
	}
	return decimal.Zero
}

// EmployeeData is what calculation needs to know about an employee.
type EmployeeData struct {
	BaseSalary      decimal.Decimal
	ChildrenCount   int
	InsuranceExempt bool
	TaxExempt       bool
	// Overrides replaces a component's rate (or fixed amount) for this employee.
	Overrides map[int64]decimal.Decimal
	// Manual holds amounts entered for manual components this period.
	Manual map[int64]decimal.Decimal
}

type Settings struct {
	PeriodDays   decimal.Decimal // days in the payroll month, the divisor of daily wages
	MonthlyHours decimal.Decimal // hours in a month, the divisor of hourly wages
	Places       int32
	// Statutory rules in force for the period (nil when none is configured).
	Tax       *TaxTable
	Insurance *InsuranceRule
}

type Line struct {
	Component Component       `json:"component"`
	Quantity  decimal.Decimal `json:"quantity"`
	Rate      decimal.Decimal `json:"rate"`
	Amount    decimal.Decimal `json:"amount"`
}

type Result struct {
	Lines      []Line
	Errors     []string        // formula errors, by component code
	Gross      decimal.Decimal // sum of earnings
	Deductions decimal.Decimal // sum of deductions
	Net        decimal.Decimal // gross − deductions
	Employer   decimal.Decimal // employer contributions (not paid to the employee)
	Taxable    decimal.Decimal
	Insurable  decimal.Decimal
}

// ProgressiveTax applies brackets to base.
func ProgressiveTax(base decimal.Decimal, brackets []Bracket) decimal.Decimal {
	tax := decimal.Zero
	lower := decimal.Zero
	for _, b := range brackets {
		if !base.GreaterThan(lower) {
			break
		}
		upper := base
		if b.Upto != nil && b.Upto.LessThan(base) {
			upper = *b.Upto
		}
		if upper.GreaterThan(lower) {
			tax = tax.Add(upper.Sub(lower).Mul(b.Rate).Div(hundred))
		}
		if b.Upto == nil {
			break
		}
		lower = *b.Upto
	}
	return tax
}

// Calculate computes one employee's payslip. Earnings come first and build the
// taxable and insurable bases; unpaid-leave deductions reduce those bases; then
// the remaining deductions and the employer contributions are computed.
func Calculate(comps []Component, emp EmployeeData, att Attendance, s Settings) Result {
	sorted := append([]Component(nil), comps...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Seq != sorted[j].Seq {
			return sorted[i].Seq < sorted[j].Seq
		}
		return sorted[i].ID < sorted[j].ID
	})
	var res Result
	daily := decimal.Zero
	if s.PeriodDays.IsPositive() {
		daily = emp.BaseSalary.Div(s.PeriodDays)
	}
	hourly := decimal.Zero
	if s.MonthlyHours.IsPositive() {
		hourly = emp.BaseSalary.Div(s.MonthlyHours)
	}
	earned := map[string]decimal.Decimal{} // earnings by component code
	unpaid := decimal.Zero                 // unpaid-leave deductions
	insurable := func() decimal.Decimal {
		if s.Insurance == nil || len(s.Insurance.EligibleCodes) == 0 {
			return res.Insurable
		}
		base := decimal.Zero
		for _, code := range s.Insurance.EligibleCodes {
			base = base.Add(earned[code])
		}
		return decimal.Max(decimal.Zero, base.Sub(unpaid))
	}
	vars := func(rate decimal.Decimal, c Component) map[string]decimal.Decimal {
		return map[string]decimal.Decimal{
			"base_salary": emp.BaseSalary, "daily_wage": daily, "hourly_wage": hourly, "period_days": s.PeriodDays, "monthly_hours": s.MonthlyHours,
			"working_days": att.WorkingDays, "working_hours": att.WorkingHours, "absence_days": att.AbsenceDays, "unpaid_leave_days": att.UnpaidLeaveDays,
			"paid_leave_days": att.PaidLeaveDays, "overtime_hours": att.OvertimeHours, "mission_days": att.MissionDays,
			"holiday_work_hours": att.HolidayWorkHours, "children": decimal.NewFromInt(int64(emp.ChildrenCount)), "rate": rate,
			"manual": emp.Manual[c.ID], "gross": res.Gross, "taxable": res.Taxable, "insurable": insurable(),
		}
	}
	amountOf := func(c Component) (qty, rate, amount decimal.Decimal) {
		rate = c.Rate
		if o, ok := emp.Overrides[c.ID]; ok {
			rate = o
		}
		if c.QuantityField != "" {
			qty = att.field(c.QuantityField)
		}
		switch c.Method {
		case "fixed":
			amount = rate
		case "base_percent":
			if c.QuantityField == "" {
				qty = s.PeriodDays
			}
			if s.PeriodDays.IsPositive() {
				amount = emp.BaseSalary.Mul(rate).Div(hundred).Mul(qty).Div(s.PeriodDays)
			}
		case "daily_base":
			amount = daily.Mul(rate).Div(hundred).Mul(qty)
		case "hourly_base":
			amount = hourly.Mul(rate).Div(hundred).Mul(qty)
		case "per_unit":
			amount = rate.Mul(qty)
		case "per_child":
			qty = decimal.NewFromInt(int64(emp.ChildrenCount))
			amount = rate.Mul(qty)
		case "percent_of_insurable":
			if emp.InsuranceExempt {
				return qty, rate, decimal.Zero
			}
			base := res.Insurable
			if c.Ceiling.IsPositive() && base.GreaterThan(c.Ceiling) {
				base = c.Ceiling
			}
			qty = base
			amount = base.Mul(rate).Div(hundred)
		case "percent_of_gross":
			qty = res.Gross
			amount = res.Gross.Mul(rate).Div(hundred)
		case "tax_brackets":
			if emp.TaxExempt {
				return qty, rate, decimal.Zero
			}
			base := res.Taxable.Sub(c.Exemption)
			if base.IsNegative() {
				base = decimal.Zero
			}
			qty = base
			amount = ProgressiveTax(base, c.Brackets)
		case "manual":
			amount = emp.Manual[c.ID]
		case "formula":
			v, err := EvalFormula(c.Formula, vars(rate, c))
			if err != nil {
				res.Errors = append(res.Errors, c.Code)
				return qty, rate, decimal.Zero
			}
			amount = v
		case "tax_table":
			if emp.TaxExempt || s.Tax == nil {
				return qty, rate, decimal.Zero
			}
			qty, amount = s.Tax.Tax(res.Taxable)
		case "insurance_rule":
			if emp.InsuranceExempt || s.Insurance == nil {
				return qty, rate, decimal.Zero
			}
			base := insurable()
			if s.Insurance.Ceiling.IsPositive() && base.GreaterThan(s.Insurance.Ceiling) {
				base = s.Insurance.Ceiling
			}
			rate = s.Insurance.Rate(c.InsuranceShare)
			qty = base
			amount = base.Mul(rate).Div(hundred)
		}
		if amount.IsNegative() {
			amount = decimal.Zero
		}
		return qty, rate, amount.Round(s.Places)
	}
	add := func(c Component) decimal.Decimal {
		qty, rate, amount := amountOf(c)
		if amount.IsZero() {
			return amount
		}
		res.Lines = append(res.Lines, Line{Component: c, Quantity: qty, Rate: rate, Amount: amount})
		return amount
	}
	for _, c := range sorted {
		if c.Kind == KindEarning {
			a := add(c)
			earned[c.Code] = earned[c.Code].Add(a)
			res.Gross = res.Gross.Add(a)
			if c.Taxable {
				res.Taxable = res.Taxable.Add(a)
			}
			if c.Insurable {
				res.Insurable = res.Insurable.Add(a)
			}
		}
	}
	// Unpaid leave and absence reduce the wage actually earned.
	for _, c := range sorted {
		if c.Kind == KindDeduction && c.Category == "unpaid_leave" {
			a := add(c)
			unpaid = unpaid.Add(a)
			res.Deductions = res.Deductions.Add(a)
			res.Taxable = decimal.Max(decimal.Zero, res.Taxable.Sub(a))
			res.Insurable = decimal.Max(decimal.Zero, res.Insurable.Sub(a))
		}
	}
	// An insurance rule listing eligible components defines the insurable base.
	res.Insurable = insurable()
	for _, c := range sorted {
		if c.Kind == KindDeduction && c.Category != "unpaid_leave" {
			res.Deductions = res.Deductions.Add(add(c))
		}
	}
	for _, c := range sorted {
		if c.Kind == KindEmployer {
			res.Employer = res.Employer.Add(add(c))
		}
	}
	res.Net = res.Gross.Sub(res.Deductions)
	return res
}

// Split divides amount by percentages (summing to 100). Rounding differences go
// to the last share so the parts always add up to amount exactly.
func Split(amount decimal.Decimal, percents []decimal.Decimal, places int32) []decimal.Decimal {
	out := make([]decimal.Decimal, len(percents))
	if len(percents) == 0 {
		return out
	}
	rest := amount
	for i, p := range percents {
		if i == len(percents)-1 {
			out[i] = rest
			break
		}
		out[i] = amount.Mul(p).Div(hundred).Round(places)
		rest = rest.Sub(out[i])
	}
	return out
}
