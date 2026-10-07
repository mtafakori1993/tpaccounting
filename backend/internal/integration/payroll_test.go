package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
)

func dec(v any) decimal.Decimal { return decimal.RequireFromString(fmt.Sprint(v)) }

func TestPayrollWorkflowAndAccounting(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)

	ccs := items(c.must("GET", "/cost-centers", nil))
	cc := map[string]int64{}
	for _, x := range ccs {
		cc[str(x, "code")] = id(x)
	}
	if len(ccs) < 5 || str(ccs[len(ccs)-1], "kind") == "" {
		t.Fatalf("seeded cost centers: %v", ccs)
	}
	comps := items(c.must("GET", "/payroll/components", nil))
	compID := map[string]int64{}
	for _, x := range comps {
		compID[str(x, "code")] = id(x)
	}
	if len(comps) != 20 || compID["income_tax"] == 0 {
		t.Fatalf("default salary components: %d", len(comps))
	}

	emp := map[string]any{"code": "E001", "first_name": "علی", "last_name": "رضایی", "national_id": "0499370899", "hire_date": "2026-01-01",
		"base_salary": "300000000", "children_count": 2, "department_id": cc["10"], "iban": "IR820540102680020817909002",
		"allocations": []any{map[string]any{"cost_center_id": cc["10"], "percent": "60"}, map[string]any{"cost_center_id": cc["P01"], "percent": "40"}}}
	e1 := c.must("POST", "/employees", emp)
	if len(e1["allocations"].([]any)) != 2 {
		t.Fatalf("allocations: %v", e1)
	}
	e2 := c.must("POST", "/employees", map[string]any{"code": "E002", "first_name": "Sara", "last_name": "Ahmadi", "hire_date": "2026-01-01",
		"base_salary": "200000000", "cost_center_id": cc["20"], "department_id": cc["20"]})

	// Validation: national id check digit, allocation total, duplicate code.
	bad := map[string]any{"code": "E009", "first_name": "x", "last_name": "y", "hire_date": today, "national_id": "1234567890"}
	code, out := c.do("POST", "/employees", bad)
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["national_id"] != "invalid_national_id" {
		t.Fatalf("national id: %d %v", code, out)
	}
	bad["national_id"] = ""
	bad["allocations"] = []any{map[string]any{"cost_center_id": cc["10"], "percent": "50"}}
	code, out = c.do("POST", "/employees", bad)
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["allocations"] != "allocation_total" {
		t.Fatalf("allocation total: %d %v", code, out)
	}
	c.expect(http.StatusConflict, "duplicate_code", "POST", "/employees", map[string]any{"code": "E001", "first_name": "a", "last_name": "b", "hire_date": today})

	// Salary changes are audited.
	e2["base_salary"] = "210000000"
	c.must("PUT", fmt.Sprintf("/employees/%d", id(e2)), e2)
	if a := items(c.must("GET", fmt.Sprintf("/audit?entity=employee&entity_id=%d&action=salary_change", id(e2)), nil)); len(a) != 1 {
		t.Fatalf("salary change audit: %v", a)
	}

	// Payroll for the current month of the company calendar.
	jy, jm, _ := jalali.FromGregorian(time.Now())
	period := c.must("POST", "/payroll/periods", map[string]any{"year": jy, "month": jm})
	c.expect(http.StatusConflict, "duplicate_payroll_period", "POST", "/payroll/periods", map[string]any{"year": jy, "month": jm})
	pid := id(period)
	base := fmt.Sprintf("/payroll/%d", pid)
	inputs := c.must("GET", fmt.Sprintf("/payroll/periods/%d/inputs", pid), nil)["items"].([]any)
	if len(inputs) != 2 || str(inputs[0].(map[string]any), "working_days") != "30" {
		t.Fatalf("default inputs: %v", inputs)
	}
	c.must("PUT", fmt.Sprintf("/payroll/periods/%d/inputs", pid), []any{
		map[string]any{"employee_id": id(e1), "working_days": "30", "overtime_hours": "10",
			"items": []any{map[string]any{"component_id": compID["bonus"], "amount": "10000000"}, map[string]any{"component_id": compID["loan"], "amount": "5000000"}}},
		map[string]any{"employee_id": id(e2), "working_days": "30", "unpaid_leave_days": "3"},
	})

	// The workflow is enforced and permission-checked.
	c.expect(http.StatusUnprocessableEntity, "invalid_transition", "POST", base+"/approve", nil)
	officer := loginAs(t, srv, c, "payroll_officer", "officer@example.com")
	period = officer.must("POST", base+"/calculate", nil)
	if str(period, "status") != "calculated" || num(period["employee_count"]) != 2 {
		t.Fatalf("calculate: %v", period)
	}
	officer.expect(http.StatusForbidden, "forbidden", "POST", base+"/approve", nil)
	accountant := loginAs(t, srv, c, "accountant", "acc@example.com")
	accountant.expect(http.StatusForbidden, "forbidden", "GET", "/employees", nil)
	accountant.expect(http.StatusForbidden, "forbidden", "GET", fmt.Sprintf("/payroll/periods/%d/payslips", pid), nil)

	gross, ded, net, employer := dec(period["gross_total"]), dec(period["deduction_total"]), dec(period["net_total"]), dec(period["employer_total"])
	if !net.Equal(gross.Sub(ded)) || !employer.IsPositive() {
		t.Fatalf("totals: %v", period)
	}
	slips := c.must("GET", fmt.Sprintf("/payroll/periods/%d/payslips", pid), nil)["items"].([]any)
	s1 := c.must("GET", fmt.Sprintf("/payroll/payslips/%d", id(slips[0].(map[string]any))), nil)
	lines := map[string]string{}
	for _, l := range s1["lines"].([]any) {
		m := l.(map[string]any)
		lines[str(m, "code")] = str(m, "amount")
	}
	if lines["base_salary"] != "300000000" || lines["bonus"] != "10000000" || lines["loan"] != "5000000" || lines["overtime"] != "21875000" ||
		lines["child"] != "14332368" || lines["insurance_employee"] == "" || lines["income_tax"] == "" || lines["insurance_employer"] == "" {
		t.Fatalf("payslip lines: %v", lines)
	}
	s2 := c.must("GET", fmt.Sprintf("/payroll/payslips/%d", id(slips[1].(map[string]any))), nil)
	for _, l := range s2["lines"].([]any) {
		m := l.(map[string]any)
		if str(m, "code") == "unpaid_leave" && str(m, "amount") != "21000000" {
			t.Fatalf("unpaid leave: %v", m)
		}
	}

	c.must("POST", base+"/approve", nil)
	// Approved payrolls are frozen.
	c.expect(http.StatusUnprocessableEntity, "payroll_locked", "PUT", fmt.Sprintf("/payroll/periods/%d/inputs", pid), []any{})
	c.expect(http.StatusUnprocessableEntity, "invalid_transition", "POST", base+"/pay", map[string]any{})

	period = c.must("POST", base+"/post", nil)
	je := entry(c, period["journal_entry_id"])
	if str(je, "source_type") != "payroll" || str(je, "total_debit") != str(je, "total_credit") {
		t.Fatalf("payroll entry: %v", je)
	}
	salaryExp, employerExp := accountByCode(c, "8101"), accountByCode(c, "8110")
	salPayable, insPayable, taxPayable, advances := accountByCode(c, "3301"), accountByCode(c, "3302"), accountByCode(c, "3203"), accountByCode(c, "1205")
	ccDebit := map[int64]decimal.Decimal{}
	for _, l := range je["lines"].([]any) {
		m := l.(map[string]any)
		if m["cost_center_id"] != nil {
			ccDebit[num(m["cost_center_id"])] = ccDebit[num(m["cost_center_id"])].Add(dec(m["debit"]))
		}
	}
	if !ccDebit[cc["10"]].IsPositive() || !ccDebit[cc["P01"]].IsPositive() || !ccDebit[cc["20"]].IsPositive() {
		t.Fatalf("expense must be split over cost centers and projects: %v", ccDebit)
	}
	if b := dec(balance(c, salPayable)); !b.Equal(net.Neg()) {
		t.Fatalf("salaries payable %s want -%s", b, net)
	}
	expense := dec(balance(c, salaryExp)).Add(dec(balance(c, employerExp)))
	if !expense.Equal(gross.Sub(decimal.RequireFromString("21000000")).Add(employer)) {
		t.Fatalf("salary cost %s (gross %s employer %s)", expense, gross, employer)
	}
	if !dec(balance(c, insPayable)).IsNegative() || !dec(balance(c, taxPayable)).IsNegative() || balance(c, advances) != "-5000000" {
		t.Fatalf("liabilities: ins %s tax %s loans %s", balance(c, insPayable), balance(c, taxPayable), balance(c, advances))
	}

	// Payment through the bank.
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Payroll bank", "bank_name": "Melli",
		"opening_balance": "2000000000", "opening_date": today})
	cash := c.must("POST", "/treasury/accounts", map[string]any{"kind": "cash", "name": "Petty cash"})
	c.expect(http.StatusUnprocessableEntity, "insufficient_funds", "POST", base+"/pay", map[string]any{"treasury_account_id": id(cash), "date": today})
	period = c.must("POST", base+"/pay", map[string]any{"treasury_account_id": id(bank), "date": today})
	if str(period, "status") != "paid" || balance(c, salPayable) != "0" {
		t.Fatalf("pay: %v payable %s", period, balance(c, salPayable))
	}
	if b := dec(str(c.must("GET", fmt.Sprintf("/treasury/accounts/%d", id(bank)), nil), "balance")); !b.Equal(decimal.RequireFromString("2000000000").Sub(net)) {
		t.Fatalf("bank after salaries: %s", b)
	}
	period = c.must("POST", base+"/close", nil)
	if str(period, "status") != "closed" {
		t.Fatalf("close: %v", period)
	}

	// Reports.
	sum := c.must("GET", "/payroll/reports/summary", nil)
	if !dec(sub(sum, "totals")["net"]).Equal(net) {
		t.Fatalf("summary: %v", sum)
	}
	byProject := c.must("GET", "/payroll/reports/by-project", nil)
	byCC := c.must("GET", "/payroll/reports/by-cost-center", nil)
	total := gross.Add(employer)
	if !dec(sub(byProject, "totals")["total_cost"]).Equal(total) || !dec(sub(byCC, "totals")["total_cost"]).Equal(total) {
		t.Fatalf("allocated cost: project %v cc %v", sub(byProject, "totals"), sub(byCC, "totals"))
	}
	if r := c.must("GET", "/payroll/reports/payments", nil); len(r["rows"].([]any)) != 2 || !dec(sub(r, "totals")["net"]).Equal(net) {
		t.Fatalf("payments: %v", r)
	}
	if r := c.must("GET", "/payroll/reports/net-payable", nil); len(r["rows"].([]any)) != 0 {
		t.Fatalf("nothing left to pay: %v", r)
	}
	for _, name := range []string{"employee", "by-department", "tax", "insurance", "employer-costs", "journal"} {
		if r := c.must("GET", "/payroll/reports/"+name, nil); len(r["rows"].([]any)) == 0 {
			t.Fatalf("report %s is empty", name)
		}
	}
	// Employees with payroll history cannot be deleted.
	c.expect(http.StatusConflict, "in_use", "DELETE", fmt.Sprintf("/employees/%d", id(e1)), nil)
	audit := items(c.must("GET", fmt.Sprintf("/audit?entity=payroll_period&entity_id=%d", pid), nil))
	if len(audit) < 6 {
		t.Fatalf("payroll audit trail: %d", len(audit))
	}
}

func TestPayrollUnpostReversesAccrual(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	c.must("POST", "/employees", map[string]any{"code": "E1", "first_name": "A", "last_name": "B", "hire_date": "2026-01-01", "base_salary": "100000000"})
	jy, jm, _ := jalali.FromGregorian(time.Now())
	p := c.must("POST", "/payroll/periods", map[string]any{"year": jy, "month": jm})
	base := fmt.Sprintf("/payroll/periods/%d", id(p))
	c.must("POST", base+"/calculate", nil)
	c.must("POST", base+"/approve", nil)
	p = c.must("POST", base+"/post", nil)
	salPayable := accountByCode(c, "3301")
	if balance(c, salPayable) == "0" {
		t.Fatal("accrual not posted")
	}
	p = c.must("POST", base+"/unpost", map[string]any{})
	if str(p, "status") != "approved" || balance(c, salPayable) != "0" {
		t.Fatalf("unpost: %v %s", p, balance(c, salPayable))
	}
	c.must("POST", base+"/reopen", nil)
	c.must("DELETE", base, nil)
}
