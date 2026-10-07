package payroll

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func comps() []Component {
	cs := DefaultComponents()
	for i := range cs {
		cs[i].ID = int64(i + 1)
		cs[i].IsActive = true
	}
	return cs
}

func byCode(cs []Component, code string) Component {
	for _, c := range cs {
		if c.Code == code {
			return c
		}
	}
	panic(code)
}

func lineAmount(r Result, code string) decimal.Decimal {
	for _, l := range r.Lines {
		if l.Component.Code == code {
			return l.Amount
		}
	}
	return decimal.Zero
}

var (
	taxTable = DefaultTaxTable(civil.New(2026, 1, 1))
	insRule  = DefaultInsuranceRule(civil.New(2026, 1, 1))
	settings = Settings{PeriodDays: d("30"), MonthlyHours: d("192"), Places: 0, Tax: &taxTable, Insurance: &insRule}
)

func TestProgressiveTax(t *testing.T) {
	br := taxTable.Brackets
	cases := map[string]string{
		"0":         "0",
		"240000000": "0",
		"300000000": "6000000",  // 10% of 60M
		"400000000": "22000000", // 6M + 15% of 80M (12M) + 20% of 20M (4M)
		"600000000": "72000000", // 6M + 12M + 20% of 120M (24M) + 30% of 100M (30M)
	}
	for base, want := range cases {
		if got := ProgressiveTax(d(base), br); !got.Equal(d(want)) {
			t.Errorf("tax(%s) = %s, want %s", base, got, want)
		}
	}
}

func TestCalculateFullMonth(t *testing.T) {
	cs := comps()
	emp := EmployeeData{BaseSalary: d("300000000"), ChildrenCount: 2, Overrides: map[int64]decimal.Decimal{}, Manual: map[int64]decimal.Decimal{}}
	emp.Manual[byCode(cs, "bonus").ID] = d("10000000")
	emp.Manual[byCode(cs, "loan").ID] = d("5000000")
	att := Attendance{WorkingDays: d("30"), OvertimeHours: d("10")}
	r := Calculate(cs, emp, att, settings)

	overtime := d("300000000").Div(d("192")).Mul(d("1.4")).Mul(d("10")).Round(0) // 21,875,000
	checks := map[string]decimal.Decimal{
		"base_salary": d("300000000"),
		"housing":     d("9000000"),
		"food":        d("22000000"),
		"child":       d("14332368"),
		"overtime":    overtime,
		"bonus":       d("10000000"),
		"loan":        d("5000000"),
	}
	for code, want := range checks {
		if got := lineAmount(r, code); !got.Equal(want) {
			t.Errorf("%s = %s, want %s", code, got, want)
		}
	}
	gross := d("300000000").Add(d("9000000")).Add(d("22000000")).Add(d("14332368")).Add(overtime).Add(d("10000000"))
	if !r.Gross.Equal(gross) {
		t.Fatalf("gross %s want %s", r.Gross, gross)
	}
	// Insurable: everything except child allowance and bonus.
	insurable := gross.Sub(d("14332368")).Sub(d("10000000"))
	if !r.Insurable.Equal(insurable) {
		t.Fatalf("insurable %s want %s", r.Insurable, insurable)
	}
	ins := insurable.Mul(d("0.07")).Round(0)
	if got := lineAmount(r, "insurance_employee"); !got.Equal(ins) {
		t.Errorf("employee insurance %s want %s", got, ins)
	}
	tax := ProgressiveTax(r.Taxable, taxTable.Brackets).Round(0)
	if got := lineAmount(r, "income_tax"); !got.Equal(tax) {
		t.Errorf("tax %s want %s", got, tax)
	}
	if !r.Deductions.Equal(ins.Add(tax).Add(d("5000000"))) {
		t.Errorf("deductions %s", r.Deductions)
	}
	if !r.Net.Equal(r.Gross.Sub(r.Deductions)) {
		t.Error("net must be gross − deductions")
	}
	employer := insurable.Mul(d("0.20")).Round(0).Add(insurable.Mul(d("0.03")).Round(0))
	if !r.Employer.Equal(employer) {
		t.Errorf("employer %s want %s", r.Employer, employer)
	}
}

func TestUnpaidLeaveAndProration(t *testing.T) {
	cs := comps()
	emp := EmployeeData{BaseSalary: d("300000000"), Overrides: map[int64]decimal.Decimal{}, Manual: map[int64]decimal.Decimal{}}
	// Joined mid-month: 15 days; 2 days unpaid leave.
	r := Calculate(cs, emp, Attendance{WorkingDays: d("15"), UnpaidLeaveDays: d("2")}, settings)
	if got := lineAmount(r, "base_salary"); !got.Equal(d("150000000")) {
		t.Errorf("prorated base %s", got)
	}
	if got := lineAmount(r, "unpaid_leave"); !got.Equal(d("20000000")) {
		t.Errorf("unpaid leave %s", got)
	}
	// Unpaid leave reduces the insurance base.
	insurable := d("150000000").Add(d("9000000")).Add(d("22000000")).Sub(d("20000000"))
	if !r.Insurable.Equal(insurable) {
		t.Errorf("insurable %s want %s", r.Insurable, insurable)
	}
}

func TestExemptionsAndOverrides(t *testing.T) {
	cs := comps()
	housing := byCode(cs, "housing").ID
	emp := EmployeeData{BaseSalary: d("100000000"), InsuranceExempt: true, TaxExempt: true,
		Overrides: map[int64]decimal.Decimal{housing: d("1000000")}, Manual: map[int64]decimal.Decimal{}}
	r := Calculate(cs, emp, Attendance{WorkingDays: d("30")}, settings)
	if got := lineAmount(r, "housing"); !got.Equal(d("1000000")) {
		t.Errorf("override not applied: %s", got)
	}
	if !lineAmount(r, "insurance_employee").IsZero() || !lineAmount(r, "income_tax").IsZero() || !r.Employer.IsZero() {
		t.Error("exempt employee must have no insurance or tax")
	}
}

func TestSplitAddsUp(t *testing.T) {
	parts := Split(d("1000001"), []decimal.Decimal{d("33.33"), d("33.33"), d("33.34")}, 0)
	if sum := parts[0].Add(parts[1]).Add(parts[2]); !sum.Equal(d("1000001")) {
		t.Fatalf("split %v sums to %s", parts, sum)
	}
}

func TestAccrualEntryBalancesWithCostCenters(t *testing.T) {
	m := map[string]int64{
		ledger.MapPayrollSalaryExpense: 1, ledger.MapPayrollEmployerExpense: 2, ledger.MapPayrollSalariesPayable: 3,
		ledger.MapPayrollInsurancePayable: 4, ledger.MapPayrollTaxPayable: 5, ledger.MapPayrollOtherDeductions: 6,
	}
	alloc := []Allocation{{CostCenterID: 10, Percent: d("60")}, {CostCenterID: 20, Percent: d("40")}}
	lines := []slipLine{
		{kind: KindEarning, category: "salary", amount: d("1000001"), allocations: alloc},
		{kind: KindDeduction, category: "unpaid_leave", amount: d("100000"), allocations: alloc},
		{kind: KindDeduction, category: "insurance", amount: d("63000")},
		{kind: KindDeduction, category: "tax", amount: d("20000")},
		{kind: KindDeduction, category: "loan", amount: d("5000")},
		{kind: KindEmployer, category: "insurance", amount: d("207000"), allocations: alloc},
	}
	net := d("1000001").Sub(d("100000")).Sub(d("63000")).Sub(d("20000")).Sub(d("5000"))
	jl, err := AccrualLines(lines, net, m, 0)
	if err != nil {
		t.Fatal(err)
	}
	dr, cr := ledger.Totals(jl)
	if !dr.Equal(cr) {
		t.Fatalf("unbalanced: %s / %s", dr, cr)
	}
	ccDebit := map[int64]decimal.Decimal{}
	for _, l := range jl {
		if l.CostCenterID != nil {
			ccDebit[*l.CostCenterID] = ccDebit[*l.CostCenterID].Add(l.Debit)
		}
	}
	if !ccDebit[10].Add(ccDebit[20]).Equal(d("900001").Add(d("207000"))) {
		t.Errorf("cost-center split %v", ccDebit)
	}
	if ccDebit[10].LessThanOrEqual(ccDebit[20]) {
		t.Errorf("60%% share must be larger: %v", ccDebit)
	}
}

func TestNationalID(t *testing.T) {
	if !ValidNationalID("0499370899") || ValidNationalID("0499370898") || ValidNationalID("1111111111") {
		t.Error("national id check digit")
	}
}
