package budgets

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestEqualAllocationAddsUp(t *testing.T) {
	parts := Equal(d("100000000"), 12, 0)
	if len(parts) != 12 || !Sum(parts).Equal(d("100000000")) {
		t.Fatalf("equal: %v", parts)
	}
	if !parts[0].Equal(d("8333333")) || !parts[11].Equal(d("8333337")) {
		t.Errorf("rounding goes to the last period: %v", parts)
	}
}

func TestPercentageAllocation(t *testing.T) {
	pcts := make([]decimal.Decimal, 12)
	for i := range pcts {
		pcts[i] = d("5")
	}
	pcts[0], pcts[11] = d("25"), d("25")
	parts := Distribute(d("1000"), pcts, 12, 0)
	if !parts[0].Equal(d("250")) || !parts[1].Equal(d("50")) || !Sum(parts).Equal(d("1000")) {
		t.Fatalf("percentage: %v", parts)
	}
}

func TestHistoricalAllocationFallsBackToEqual(t *testing.T) {
	parts := Distribute(d("120"), make([]decimal.Decimal, 12), 12, 0)
	for _, p := range parts {
		if !p.Equal(d("10")) {
			t.Fatalf("no history must spread equally: %v", parts)
		}
	}
	hist := make([]decimal.Decimal, 12)
	hist[2], hist[5] = d("30"), d("10")
	parts = Distribute(d("100"), hist, 12, 0)
	if !parts[2].Equal(d("75")) || !parts[5].Equal(d("25")) || !parts[0].IsZero() {
		t.Fatalf("historical weights: %v", parts)
	}
}

func TestVarianceAndUtilization(t *testing.T) {
	// The example of the specification: budget 100M, actual 105M.
	if v := Variance(d("100000000"), d("105000000"), false); !v.Equal(d("-5000000")) {
		t.Errorf("expense variance %s", v)
	}
	if u := Utilization(d("100000000"), d("105000000")); u == nil || !u.Equal(d("105")) {
		t.Errorf("utilization %v", u)
	}
	if v := Variance(d("100"), d("120"), true); !v.Equal(d("20")) {
		t.Errorf("income variance %s", v)
	}
	if Utilization(decimal.Zero, d("1")) != nil {
		t.Error("no utilization without budget")
	}
	if f := Forecast(d("30"), 3, 12); !f.Equal(d("120")) {
		t.Errorf("forecast %s", f)
	}
}

func TestMatchRuleMostSpecificWins(t *testing.T) {
	b := int64(7)
	acc := int64(3)
	cc := int64(9)
	role := int64(2)
	rules := []Rule{
		{ID: 1, Policy: PolicyWarning, IsActive: true},
		{ID: 2, Policy: PolicySoft, IsActive: true, AccountID: &acc, accountPath: "/1/3/"},
		{ID: 3, Policy: PolicyHard, IsActive: true, AccountID: &acc, accountPath: "/1/3/", CostCenterID: &cc},
		{ID: 4, Policy: PolicyWarning, IsActive: true, RoleID: &role, roleCode: "accounting_manager", BudgetID: &b},
		{ID: 5, Policy: PolicyHard, IsActive: false},
	}
	if r := matchRule(rules, 7, "/1/3/12/", &cc, "accountant"); r.ID != 3 {
		t.Errorf("cost center rule should win, got %d", r.ID)
	}
	if r := matchRule(rules, 7, "/1/3/12/", nil, "accountant"); r.ID != 2 {
		t.Errorf("account rule should win, got %d", r.ID)
	}
	if r := matchRule(rules, 7, "/1/3/12/", &cc, "accounting_manager"); r.ID != 4 {
		t.Errorf("role rule should win, got %d", r.ID)
	}
	if r := matchRule(rules, 7, "/2/", nil, "x"); r.ID != 1 {
		t.Errorf("generic rule expected, got %d", r.ID)
	}
	if r := matchRule(nil, 7, "/2/", nil, "x"); r.Policy != PolicyWarning || r.ID != 0 {
		t.Errorf("default rule expected: %+v", r)
	}
}
