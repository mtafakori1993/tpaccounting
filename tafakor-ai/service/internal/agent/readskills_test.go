package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"context"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

func withPayrollRules(h *harness, empty bool) {
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	h.acc.extra.HandleFunc("GET /api/v1/payroll/rules", func(w http.ResponseWriter, r *http.Request) {
		if empty {
			js(w, map[string]any{"tax_tables": []any{}, "insurance_rules": []any{}, "settings": map[string]string{}})
			return
		}
		js(w, map[string]any{
			"tax_tables": []any{map[string]any{"id": 1, "name": "جدول مالیات ۱۴۰۵", "effective_from": "2026-03-21", "effective_to": nil, "mode": "monthly", "exemption": "0", "is_active": true,
				"brackets": []any{map[string]any{"upto": "240000000", "rate": "0"}, map[string]any{"upto": nil, "rate": "30"}}}},
			"insurance_rules": []any{map[string]any{"id": 1, "name": "تأمین اجتماعی", "effective_from": "2026-03-21", "employee_rate": "7", "employer_rate": "20", "unemployment_rate": "3", "ceiling": "0", "is_active": true}},
			"settings":        map[string]string{"period_days": "30", "divisor": "30", "monthly_hours": "192"}})
	})
	h.acc.extra.HandleFunc("GET /api/v1/company/account-mappings", func(w http.ResponseWriter, r *http.Request) {
		js(w, []any{map[string]any{"key": "sales", "account_code": "6101", "account_name": "فروش"},
			map[string]any{"key": "payroll_salary_expense", "account_code": "8101", "account_name": "حقوق و دستمزد"},
			map[string]any{"key": "payroll_tax_payable", "account_code": "", "account_name": ""}})
	})
}

func messages(r *Recorder) string {
	var out []string
	for _, e := range r.Events {
		if e.Name == "message" {
			out = append(out, fmt.Sprint(e.Data.(map[string]any)["text"]))
		}
	}
	return strings.Join(out, "\n")
}

func hit(h *harness, s string) bool {
	h.acc.mu.Lock()
	defer h.acc.mu.Unlock()
	for _, x := range h.acc.hits {
		if x == s {
			return true
		}
	}
	return false
}

// «قواعد حقوق رو بگو» reads the configured rules — never "see Settings → …" (D61).
func TestPayrollRulesAreReadNotLocated(t *testing.T) {
	for _, text := range []string{"قواعد حقوق رو بگو", "قوانین حقوق و دستمزد رو نمایش بده"} {
		h := newHarness(t, "admin")
		withPayrollRules(h, false)
		rec, _ := startMode(t, h, text, "")
		m := messages(rec)
		if !hit(h, "GET /api/v1/payroll/rules") || !strings.Contains(m, "جدول مالیات ۱۴۰۵") || !strings.Contains(m, "تأمین اجتماعی") || !strings.Contains(m, "240,000,000") || !strings.Contains(m, "30%") && !strings.Contains(m, "30٪") {
			t.Fatalf("%q: %s\nevents %+v", text, m, rec.Events)
		}
		if h.ai.calls != 0 {
			t.Fatalf("%q: the model was consulted for a deterministic read", text)
		}
	}
}

func TestNoPayrollRulesIsSaidPlainly(t *testing.T) {
	h := newHarness(t, "admin")
	withPayrollRules(h, true)
	h.user.Me.Language = "fa"
	rec, _ := startMode(t, h, "قواعد حقوق رو بگو", "")
	if m := messages(rec); !strings.Contains(m, "قاعده‌ای برای حقوق ثبت نشده است") && !strings.Contains(m, "No payroll rules are configured") {
		t.Fatalf("empty rules: %s", m)
	}
}

func TestPayrollPostingRulesAreTheAccountMappings(t *testing.T) {
	prev := adminPerms
	adminPerms = append(append([]string{}, adminPerms...), "settings.view")
	t.Cleanup(func() { adminPerms = prev })
	h := newHarness(t, "admin")
	withPayrollRules(h, false)
	rec, _ := startMode(t, h, "قواعد ثبت حسابداری حقوق رو بگو", "")
	m := messages(rec)
	if !hit(h, "GET /api/v1/company/account-mappings") || !strings.Contains(m, "8101") || strings.Contains(m, "6101") || hit(h, "GET /api/v1/payroll/rules") {
		t.Fatalf("posting rules: %s %+v", m, rec.Events)
	}
}

// «کجاست» is navigation; «بساز» is a change: neither runs the read skill.
func TestWhereAndCreateAreNotReads(t *testing.T) {
	h := newHarness(t, "admin")
	withPayrollRules(h, false)
	h.ai.interp = `{"intent":"chat","confidence":0.9,"event":{}}`
	startMode(t, h, "قواعد حقوق کجاست؟", "")
	if hit(h, "GET /api/v1/payroll/rules") {
		t.Fatal("«کجاست» ran the read")
	}
	h2 := newHarness(t, "admin")
	withPayrollRules(h2, false)
	h2.ai.interp = `{"intent":"operate","confidence":0.9,"event":{}}`
	rec, _ := startMode(t, h2, "برای حقوق قانون جدید بساز", "")
	if hit(h2, "GET /api/v1/payroll/rules") && strings.Contains(messages(rec), "تأمین اجتماعی") {
		t.Fatal("«بساز» was answered as a read")
	}
	if len(h2.ai.planPrompts) == 0 {
		t.Fatal("«بساز» did not reach the planner (mutation flow)")
	}
}

// Any tell/show/list request the model labels "chat" reads the application; a location
// answer with no read is refused (the planner is told to read).
func TestShowRequestIsNeverAnsweredWithALocation(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"chat","confidence":0.9,"event":{}}`
	h.ai.planQueue = []string{
		`{"action":"answer","text":"They are under Settings → Cost centers."}`,
		`{"action":"read","calls":[{"tool":"cost_centers.list"}]}`,
		`{"action":"answer","text":"Cost centers: پروژه وب‌سایت (PRJ-WEB)."}`,
	}
	rec, _ := startMode(t, h, "مراکز هزینه رو نشون بده", "")
	m := messages(rec)
	if !strings.Contains(m, "PRJ-WEB") || strings.Contains(m, "Settings →") || !hit(h, "GET /api/v1/cost-centers") {
		t.Fatalf("answer %q\nprompts %s", m, strings.Join(h.ai.planPrompts, "\n---\n"))
	}
}

// «برای حقوق قانون جدید بساز» with no values: the model's invented tax table is refused
// and the user is asked for the rule's values.
func TestRuleValuesAreAskedNotInvented(t *testing.T) {
	prev := adminPerms
	adminPerms = append(append([]string{}, adminPerms...), "payroll.settings")
	t.Cleanup(func() { adminPerms = prev })
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","confidence":0.9,"event":{}}`
	inv := `{"action":"plan","summary":"New tax table","steps":[{"id":"s1","tool":"payroll.tax_tables.create","body":{"name":"tax","effective_from":"2026-10-09","mode":"monthly","brackets":[{"upto":10000000,"rate":0},{"upto":null,"rate":35}]},"purpose":"create tax table"}]}`
	h.ai.planQueue = []string{inv, inv}
	rec, _ := startMode(t, h, "برای حقوق قانون جدید بساز", "")
	if rec.Last("approval") != nil || rec.Last("question") == nil {
		t.Fatalf("invented values reached the card: %+v", rec.Events)
	}
}

// A filter the model invented («kind=bank,cash») matched nothing: the list is read again
// without it, so existing records are never reported as "none".
func TestInventedFilterDoesNotTurnRecordsIntoNone(t *testing.T) {
	h := newHarness(t, "admin")
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	h.acc.extra.HandleFunc("GET /api/v1/treasury/accounts", func(w http.ResponseWriter, r *http.Request) {
		if k := r.URL.Query().Get("kind"); k != "" && k != "bank" && k != "cash" {
			js(w, []any{})
			return
		}
		js(w, []any{map[string]any{"id": 2, "kind": "bank", "name": "بانک ملت جاری"}})
	})
	rec := h.a.planRead(context.Background(), h.user, nil, plannerCall{Tool: "treasury.accounts.list", Query: tools.Scalars{"kind": "bank,cash"}}, nopEmitter{})
	if !strings.Contains(rec.Result, "بانک ملت جاری") || !strings.Contains(rec.Result, "filter_without_match") {
		t.Fatalf("read %s", rec.Result)
	}
}

// A Jalali year of a date the software returned is grounded (not an invented number).
func TestJalaliYearOfAReadDateIsGrounded(t *testing.T) {
	p := &PlanState{Reads: []ReadRecord{{Result: `{"effective_from":"2026-03-21","amount":"5000000"}`}}}
	if bad := ungroundedNumbers("از ۱۴۰۵/۰۱/۰۱، مبلغ ۵,۰۰۰,۰۰۰", p); len(bad) != 0 {
		t.Fatalf("flagged %v", bad)
	}
	if bad := ungroundedNumbers("مبلغ ۷,۰۰۰,۰۰۰", p); len(bad) != 1 {
		t.Fatalf("an invented amount must still be flagged: %v", bad)
	}
}

// A total the application's data supports is grounded; a wrong one is not.
func TestTotalsOfReadRecordsAreGrounded(t *testing.T) {
	p := &PlanState{Reads: []ReadRecord{{Raw: json.RawMessage(`[{"id":2,"kind":"bank","balance":"310870000"},{"id":3,"kind":"bank","balance":"279719500"},{"id":9,"kind":"cash","balance":"30875000"}]`)}}}
	if bad := ungroundedNumbers("جمع موجودی بانک‌ها ۵۹۰,۵۸۹,۵۰۰ ریال و کل ۶۲۱,۴۶۴,۵۰۰", p); len(bad) != 0 {
		t.Fatalf("correct totals flagged: %v", bad)
	}
	if bad := ungroundedNumbers("جمع موجودی بانک‌ها ۶۹۰,۵۸۹,۵۰۰ ریال", p); len(bad) != 1 {
		t.Fatalf("a wrong total passed: %v", bad)
	}
}
