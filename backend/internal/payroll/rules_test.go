package payroll

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
)

func TestFormulaEvaluation(t *testing.T) {
	vars := map[string]decimal.Decimal{"base_salary": d("300000000"), "children": d("2"), "rate": d("5"), "overtime_hours": d("50"), "hourly_wage": d("1000")}
	cases := map[string]string{
		"base_salary * rate / 100":                    "15000000",
		"min(overtime_hours, 40) * hourly_wage * 1.4": "56000",
		"max(1, 2, 3) + abs(-4)":                      "7",
		"-(2 + 3) * 2":                                "-10",
		"children * 7166184":                          "14332368",
		"۱۰ + 5":                                      "15", // Persian digits
		"10 / 0":                                      "0",
	}
	for f, want := range cases {
		got, err := EvalFormula(f, vars)
		if err != nil || !got.Equal(d(want)) {
			t.Errorf("%s = %s (%v), want %s", f, got, err, want)
		}
	}
	for _, bad := range []string{"", "1 +", "unknown_var * 2", "foo(1)", "(1 + 2", "1 2", "min(1)"} {
		if _, err := EvalFormula(bad, vars); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if CheckFormula("base_salary * 0.1 + gross - taxable") != nil || CheckFormula("salary * 2") == nil {
		t.Error("CheckFormula")
	}
}

func TestTaxTableModes(t *testing.T) {
	monthly := TaxTable{Mode: ModeMonthly, Exemption: d("100"), Brackets: []Bracket{{Upto: decp("100"), Rate: d("0")}, {Rate: d("10")}}}
	// base = 400 − 100 = 300 → 0% of the first 100, 10% of 200.
	if base, tax := monthly.Tax(d("400")); !base.Equal(d("300")) || !tax.Equal(d("20")) {
		t.Errorf("monthly: base %s tax %s", base, tax)
	}
	annual := TaxTable{Mode: ModeAnnual, Exemption: d("1200"), Brackets: []Bracket{{Rate: d("10")}}}
	// annual base = 400×12 − 1200 = 3600 → 360 a year → 30 a month.
	if _, tax := annual.Tax(d("400")); !tax.Equal(d("30")) {
		t.Errorf("annual tax %s", tax)
	}
	if _, tax := monthly.Tax(d("50")); !tax.IsZero() {
		t.Error("income under the exemption must not be taxed")
	}
}

func TestInsuranceRuleEligibleCodesAndCeiling(t *testing.T) {
	cs := comps()
	rule := InsuranceRule{EmployeeRate: d("7"), EmployerRate: d("20"), UnemploymentRate: d("3"), EligibleCodes: []string{"base_salary"}, Ceiling: d("200000000")}
	s := settings
	s.Insurance = &rule
	emp := EmployeeData{BaseSalary: d("300000000"), Overrides: map[int64]decimal.Decimal{}, Manual: map[int64]decimal.Decimal{}}
	r := Calculate(cs, emp, Attendance{WorkingDays: d("30")}, s)
	// Only base salary is insurable, capped at the ceiling.
	if !r.Insurable.Equal(d("300000000")) {
		t.Errorf("insurable %s", r.Insurable)
	}
	if got := lineAmount(r, "insurance_employee"); !got.Equal(d("14000000")) {
		t.Errorf("employee insurance %s want 7%% of the 200M ceiling", got)
	}
	if !r.Employer.Equal(d("46000000")) {
		t.Errorf("employer %s want 23%% of 200M", r.Employer)
	}
	// Without rules nothing statutory is computed (and the period warns instead).
	s.Tax, s.Insurance = nil, nil
	r = Calculate(cs, emp, Attendance{WorkingDays: d("30")}, s)
	if !lineAmount(r, "income_tax").IsZero() || !r.Employer.IsZero() {
		t.Error("no rule must mean no statutory amount")
	}
}

func TestFormulaComponent(t *testing.T) {
	cs := comps()
	cs = append(cs, Component{ID: 99, Code: "transport", Kind: KindEarning, Category: "allowance", Method: "formula", Seq: 45,
		Formula: "working_days * rate", Rate: d("100000"), Taxable: true, Insurable: false, IsActive: true})
	emp := EmployeeData{BaseSalary: d("300000000"), Overrides: map[int64]decimal.Decimal{99: d("200000")}, Manual: map[int64]decimal.Decimal{}}
	r := Calculate(cs, emp, Attendance{WorkingDays: d("22")}, settings)
	if got := lineAmount(r, "transport"); !got.Equal(d("4400000")) {
		t.Errorf("formula with override rate: %s", got)
	}
	cs[len(cs)-1].Formula = "nonsense +"
	if r := Calculate(cs, emp, Attendance{WorkingDays: d("22")}, settings); len(r.Errors) != 1 || r.Errors[0] != "transport" {
		t.Errorf("formula error not reported: %v", r.Errors)
	}
}

func TestPeriodDaysBasis(t *testing.T) {
	p := &Period{StartDate: civil.New(2026, 2, 1), EndDate: civil.New(2026, 2, 28)}
	cases := []struct {
		s    PayrollSettings
		want string
	}{
		{PayrollSettings{PeriodDays: "30"}, "30"},
		{PayrollSettings{PeriodDays: "actual"}, "28"},
		{PayrollSettings{PeriodDays: "fixed", Divisor: d("26")}, "26"},
	}
	for _, c := range cases {
		if got := periodDays(p, c.s); !got.Equal(d(c.want)) {
			t.Errorf("%s: %s want %s", c.s.PeriodDays, got, c.want)
		}
	}
}

func TestComponentEffectiveDates(t *testing.T) {
	from, to := civil.New(2026, 3, 1), civil.New(2026, 6, 30)
	c := Component{EffectiveFrom: &from, EffectiveTo: &to}
	if c.ActiveIn(civil.New(2026, 1, 1), civil.New(2026, 1, 31)) || !c.ActiveIn(civil.New(2026, 6, 1), civil.New(2026, 6, 30)) ||
		c.ActiveIn(civil.New(2026, 7, 1), civil.New(2026, 7, 31)) {
		t.Error("effective dates")
	}
}

func TestValidIBAN(t *testing.T) {
	if !ValidIBAN("IR820540102680020817909002") || ValidIBAN("IR820540102680020817909003") || ValidIBAN("IR12") {
		t.Error("IBAN check")
	}
}

func TestImportParsing(t *testing.T) {
	if d, ok := parseImportDate("۱۴۰۵/۰۷/۰۱"); !ok || d.String() != "2026-09-23" {
		t.Errorf("jalali date: %v %v", d, ok)
	}
	if d, ok := parseImportDate("2026-10-05"); !ok || d.String() != "2026-10-05" {
		t.Errorf("gregorian date: %v", d)
	}
	for _, bad := range []string{"1405/13/01", "1405/12/31", "2026-02-30", "x", "05/10/2026"} {
		if _, ok := parseImportDate(bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
	for in, want := range map[string]string{"8": "8", "7:30": "7.5", "بله": "1", "": "0", "۲٫۵": "2.5"} {
		if got, ok := parseQty(in); !ok || !got.Equal(d(want)) {
			t.Errorf("qty %q = %s %v", in, got, ok)
		}
	}
	if _, ok := parseQty("abc"); ok {
		t.Error("abc accepted")
	}
	if normHeader(" کد پرسنلی ") != normHeader("کدپرسنلی") || normHeader("Working Hours") != "workinghours" {
		t.Error("header normalisation")
	}
}
