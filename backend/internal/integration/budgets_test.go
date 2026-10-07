package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBudgetLifecycleActualsAndControl(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)

	// The setup seeds a sample draft budget.
	seeded := items(c.must("GET", "/budgets", nil))
	if len(seeded) != 1 || str(seeded[0], "status") != "draft" || str(seeded[0], "expense_total") != "16560000000" {
		t.Fatalf("seeded budget: %v", seeded)
	}
	fy := num(seeded[0]["fiscal_year_id"])
	cc := map[string]int64{}
	for _, x := range items(c.must("GET", "/cost-centers", nil)) {
		cc[str(x, "code")] = id(x)
	}
	adv, sales := accountByCode(c, "8201"), accountByCode(c, "6101")
	pcts := []any{}
	for i := 0; i < 12; i++ {
		pcts = append(pcts, "8.25")
	}
	pcts[11] = "9.25"
	in := map[string]any{"fiscal_year_id": fy, "code": "MKT", "name": "Marketing", "cost_center_id": cc["30"], "lines": []any{
		map[string]any{"account_id": adv, "cost_center_id": cc["30"], "allocation_method": "equal", "annual_amount": "120000000"},
		map[string]any{"account_id": sales, "allocation_method": "percentage", "annual_amount": "1200000000", "percents": pcts},
	}}
	b := c.must("POST", "/budgets", in)
	bid := id(b)
	lines := b["lines"].([]any)
	if len(lines) != 2 || fmt.Sprint(lines[0].(map[string]any)["amounts"].([]any)[0]) != "10000000" {
		t.Fatalf("budget lines: %v", lines)
	}
	if a := lines[1].(map[string]any)["amounts"].([]any); fmt.Sprint(a[0]) != "99000000" || fmt.Sprint(a[11]) != "111000000" {
		t.Fatalf("percentage allocation: %v", a)
	}
	// Duplicate line for the same account and cost center.
	dup := map[string]any{"fiscal_year_id": fy, "code": "DUP", "name": "dup", "lines": []any{
		map[string]any{"account_id": adv, "annual_amount": "1"}, map[string]any{"account_id": adv, "annual_amount": "2"}}}
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", "/budgets", dup)

	// Workflow: draft → submitted → approved → active, permission-checked.
	path := fmt.Sprintf("/budgets/%d", bid)
	c.must("POST", path+"/submit", nil)
	in["name"] = "changed"
	c.expect(http.StatusUnprocessableEntity, "budget_locked", "PUT", path, in)
	accountant := loginAs(t, srv, c, "accountant", "acc@example.com")
	accountant.expect(http.StatusForbidden, "forbidden", "POST", path+"/approve", nil)
	c.must("POST", path+"/approve", nil)
	b = c.must("POST", path+"/activate", nil)
	if str(b, "status") != "active" {
		t.Fatalf("activate: %v", b)
	}

	// Actual spending comes from the ledger.
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Bank", "bank_name": "Mellat", "opening_balance": "1000000000", "opening_date": today})
	var advCat int64
	for _, x := range items(c.must("GET", "/expenses/categories", nil)) {
		if str(x, "name_en") == "Advertising" {
			advCat = id(x)
		}
	}
	expense := func(cl *client, amount string, headers map[string]string) (int, map[string]any, http.Header) {
		return cl.doH("POST", "/expenses", map[string]any{"date": today, "category_id": advCat, "amount": amount, "payment_method": "bank",
			"treasury_account_id": id(bank), "cost_center_id": cc["30"], "post": true}, headers)
	}
	if code, out, _ := expense(c, "50000000", nil); code != http.StatusCreated {
		t.Fatalf("expense: %d %v", code, out)
	}
	an := c.must("GET", path+"/analysis?group_by=line", nil)
	row := an["rows"].([]any)[0].(map[string]any)
	if str(row, "actual") != "50000000" || str(row, "budget") != "120000000" || str(row, "variance") != "70000000" || str(row, "utilization") != "41.67" {
		t.Fatalf("budget vs actual: %v", row)
	}

	// Hard limit on the annual advertising budget.
	c.must("POST", "/budgets/control/rules", map[string]any{"name": "ads hard", "account_id": adv, "policy": "hard", "basis": "annual"})
	code, out, _ := expense(c, "80000000", nil)
	if code != http.StatusUnprocessableEntity || sub(out, "error")["code"] != "budget_exceeded" || sub(sub(out, "error"), "params")["can_override"] != true {
		t.Fatalf("hard limit: %d %v", code, out)
	}
	// Nothing was posted by the rejected request.
	if got := c.must("GET", "/expenses", nil); num(got["total"]) != 1 {
		t.Fatalf("rejected expense must roll back: %v", got)
	}
	// Users without budgets.override cannot override, even with a reason.
	if code, out, _ := expense(accountant, "80000000", map[string]string{"X-Budget-Override": url.QueryEscape("campaign")}); code != http.StatusUnprocessableEntity ||
		sub(sub(out, "error"), "params")["can_override"] != false {
		t.Fatalf("override without permission: %d %v", code, out)
	}
	code, out, hdr := expense(c, "80000000", map[string]string{"X-Budget-Override": url.QueryEscape("کمپین نوروز")})
	if code != http.StatusCreated || !strings.Contains(hdr.Get("X-Budget-Warnings"), "overridden") {
		t.Fatalf("override: %d %v %v", code, out, hdr)
	}
	ctl := c.must("GET", "/budgets/control", nil)
	events := ctl["events"].([]any)
	// The refused attempt (no permission) is logged as blocked even though its posting rolled back.
	if len(events) != 2 {
		t.Fatalf("override events: %v", events)
	}
	ov, blocked := events[0].(map[string]any), events[1].(map[string]any)
	if str(ov, "outcome") != "overridden" || str(ov, "reason") != "کمپین نوروز" || str(ov, "journal_entry_id") == "" || str(ov, "source_type") != "expense" ||
		str(ov, "limit_amount") != "120000000" || str(ov, "exceeded_amount") != "10000000" || str(ov, "amount") != "80000000" || str(ov, "user_name") != "Admin" {
		t.Fatalf("override event: %v", ov)
	}
	if str(blocked, "outcome") != "blocked" || blocked["journal_entry_id"] != nil || str(blocked, "reason") != "campaign" || str(blocked, "role_code") != "accountant" {
		t.Fatalf("blocked event: %v", blocked)
	}
	if detail := c.must("GET", fmt.Sprintf("/budgets/control/events/%d", id(ov)), nil); str(detail, "budget_name") == "" || str(detail, "basis") == "" {
		t.Fatalf("event detail: %v", detail)
	}
	if a := items(c.must("GET", "/audit?action=budget_override", nil)); len(a) != 1 {
		t.Fatalf("override audit: %v", a)
	}

	// Soft limit: needs confirmation.
	rules := ctl["rules"].([]any)
	rule := rules[0].(map[string]any)
	rule["policy"] = "soft"
	c.must("PUT", fmt.Sprintf("/budgets/control/rules/%d", id(rule)), rule)
	code, out, _ = expense(c, "1000000", nil)
	if code != http.StatusConflict || sub(out, "error")["code"] != "budget_confirmation_required" {
		t.Fatalf("soft limit: %d %v", code, out)
	}
	if code, out, hdr = expense(c, "1000000", map[string]string{"X-Budget-Confirm": "true"}); code != http.StatusCreated || hdr.Get("X-Budget-Warnings") == "" {
		t.Fatalf("confirmed soft limit: %d %v", code, out)
	}

	// Warning: posting allowed, the user is told.
	rule["policy"] = "warning"
	c.must("PUT", fmt.Sprintf("/budgets/control/rules/%d", id(rule)), rule)
	if code, _, hdr = expense(c, "1000000", nil); code != http.StatusCreated || !strings.Contains(hdr.Get("X-Budget-Warnings"), "warned") {
		t.Fatalf("warning: %d %v", code, hdr)
	}

	// Simulation without posting.
	sim := c.must("POST", "/budgets/control/check", map[string]any{"account_id": adv, "cost_center_id": cc["30"], "amount": "1000", "date": today})
	if evs := sim["items"].([]any); len(evs) != 1 || sub(map[string]any{"x": evs[0]}, "x")["exceeded"] != true {
		t.Fatalf("check: %v", sim)
	}

	// Reports. Actual 132M (50 + 80 + 1 + 1) against 120M.
	vs := c.must("GET", fmt.Sprintf("/budgets/reports/over-budget?budget_id=%d", bid), nil)
	over := vs["rows"].([]any)
	if len(over) != 1 || str(over[0].(map[string]any), "actual") != "132000000" || str(over[0].(map[string]any), "variance") != "-12000000" {
		t.Fatalf("over budget: %v", vs)
	}
	for _, r := range []string{"actual-vs-budget", "variance", "expense", "income", "cost-center", "monthly", "annual", "forecast"} {
		c.must("GET", fmt.Sprintf("/budgets/reports/%s?budget_id=%d", r, bid), nil)
	}
	if p := c.must("GET", fmt.Sprintf("/budgets/reports/expense?budget_id=%d", bid), nil); len(p["rows"].([]any)) != 1 {
		t.Fatalf("expense report: %v", p)
	}
	dash := c.must("GET", "/budgets/dashboard", nil)
	if num(dash["over_count"]) != 1 || str(sub(dash, "expense"), "actual") != "132000000" {
		t.Fatalf("dashboard: %v", dash)
	}
	drill := c.must("GET", fmt.Sprintf("/budgets/drilldown?line_id=%d", num(row["line_id"])), nil)
	if d := drill["items"].([]any); len(d) != 4 || str(d[0].(map[string]any), "source_type") != "expense" {
		t.Fatalf("drilldown: %v", drill)
	}

	// Revision: v2 is a draft copy; v1 stays active until v2 is activated.
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", path+"/revise", map[string]any{"reason": ""})
	v2 := c.must("POST", path+"/revise", map[string]any{"reason": "extra campaign"})
	if num(v2["version"]) != 2 || str(v2, "status") != "draft" || len(v2["lines"].([]any)) != 2 {
		t.Fatalf("revision: %v", v2)
	}
	c.expect(http.StatusUnprocessableEntity, "revision_pending", "POST", path+"/revise", map[string]any{"reason": "again"})
	v2in := map[string]any{"fiscal_year_id": fy, "code": "MKT", "name": "Marketing v2", "lines": []any{
		map[string]any{"account_id": adv, "cost_center_id": cc["30"], "allocation_method": "equal", "annual_amount": "200000000"}}}
	p2 := fmt.Sprintf("/budgets/%d", id(v2))
	c.must("PUT", p2, v2in)
	c.must("POST", p2+"/submit", nil)
	c.must("POST", p2+"/approve", nil)
	c.must("POST", p2+"/activate", nil)
	if v1 := c.must("GET", path, nil); str(v1, "status") != "archived" || len(v1["versions"].([]any)) != 2 {
		t.Fatalf("previous version must be archived: %v", v1)
	}
	if v1 := c.must("GET", path, nil); str(v1["lines"].([]any)[0].(map[string]any), "annual_amount") != "120000000" {
		t.Fatal("approved versions must keep their figures")
	}
	c.must("POST", p2+"/close", nil)
	c.expect(http.StatusUnprocessableEntity, "not_deletable", "DELETE", p2, nil)
}
