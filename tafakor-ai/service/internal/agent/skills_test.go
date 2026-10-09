package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// payrollFake adds the payroll endpoints the skills use to the fake application,
// with failure injection for the reliability tests.
type payrollFake struct {
	mu         sync.Mutex
	comps      []map[string]any
	emps       []map[string]any
	writes     int
	failCreate string // "lost_response": commit, then answer 502 | "down": 502 without commit
	storeRate  string // when set, the application stores this rate instead (verification test)
}

func addPayroll(t *testing.T, h *harness) *payrollFake {
	f := &payrollFake{
		comps: []map[string]any{
			{"id": 1, "code": "base_salary", "name": "حقوق پایه", "name_en": "Base salary", "kind": "earning", "category": "salary", "method": "base_percent", "rate": "100", "seq": 10, "is_active": true},
			{"id": 2, "code": "housing", "name": "حق مسکن", "name_en": "Housing allowance", "kind": "earning", "category": "allowance", "method": "fixed", "rate": "9000000", "seq": 20, "is_active": true},
			{"id": 8, "code": "mission", "name": "فوق‌العاده مأموریت", "name_en": "Mission allowance", "kind": "earning", "category": "allowance", "method": "daily_base", "rate": "100", "seq": 80, "is_active": true},
		},
		emps: []map[string]any{
			{"id": 1, "code": "E001", "first_name": "زهرا", "last_name": "معتضد", "full_name": "زهرا معتضد", "is_active": true, "hire_date": "2025-03-21", "employment_type": "full_time", "marital_status": "single", "base_salary": "120000000", "components": []any{}},
			{"id": 2, "code": "E002", "first_name": "محمد", "last_name": "رضایی", "full_name": "محمد رضایی", "is_active": true, "hire_date": "2025-03-21", "employment_type": "full_time", "marital_status": "single", "base_salary": "120000000", "components": []any{}},
			{"id": 3, "code": "E003", "first_name": "علی", "last_name": "رضایی", "full_name": "علی رضایی", "is_active": true, "hire_date": "2025-03-21", "employment_type": "full_time", "marital_status": "single", "base_salary": "120000000", "components": []any{}},
		},
	}
	js := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	h.user.Me.Language = "fa" // the audited user works in Persian
	mux := h.acc.extra
	mux.HandleFunc("GET /api/v1/payroll/components", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		js(w, 200, f.comps)
	})
	mux.HandleFunc("POST /api/v1/payroll/components", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failCreate == "down" {
			f.failCreate = ""
			js(w, 502, map[string]any{"error": map[string]any{"code": "bad_gateway", "message": "bad gateway"}})
			return
		}
		in["id"] = 100 + len(f.comps)
		if f.storeRate != "" {
			in["rate"] = f.storeRate
		}
		f.comps = append(f.comps, in)
		f.writes++
		if f.failCreate == "lost_response" {
			f.failCreate = ""
			js(w, 502, map[string]any{"error": map[string]any{"code": "bad_gateway", "message": "bad gateway"}})
			return
		}
		js(w, 201, map[string]any{"id": in["id"]})
	})
	mux.HandleFunc("PUT /api/v1/payroll/components/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, c := range f.comps {
			if fmt.Sprint(c["id"]) == r.PathValue("id") {
				in["id"] = c["id"]
				f.comps[i] = in
				f.writes++
				js(w, 200, map[string]any{"id": c["id"]})
				return
			}
		}
		js(w, 404, map[string]any{"error": map[string]any{"code": "not_found"}})
	})
	mux.HandleFunc("GET /api/v1/employees", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		js(w, 200, map[string]any{"items": f.emps, "total": len(f.emps)})
	})
	mux.HandleFunc("GET /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, e := range f.emps {
			if fmt.Sprint(e["id"]) == r.PathValue("id") {
				js(w, 200, e)
				return
			}
		}
		js(w, 404, map[string]any{"error": map[string]any{"code": "not_found"}})
	})
	mux.HandleFunc("PUT /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, e := range f.emps {
			if fmt.Sprint(e["id"]) == r.PathValue("id") {
				in["id"] = e["id"]
				f.emps[i] = in
				f.writes++
				js(w, 200, in)
				return
			}
		}
		js(w, 404, map[string]any{"error": map[string]any{"code": "not_found"}})
	})
	return f
}

func (f *payrollFake) comp(name string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out map[string]any
	n := 0
	for _, c := range f.comps {
		if c["name"] == name {
			out = c
			n++
		}
	}
	if n > 1 {
		panic("duplicate component " + name)
	}
	return out
}

func (f *payrollFake) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.comps {
		if c["name"] == name {
			n++
		}
	}
	return n
}

func moneyHits(h *harness) []string {
	var out []string
	for _, x := range h.acc.hits {
		if strings.Contains(x, "/expenses") || strings.Contains(x, "/treasury/transactions") || strings.Contains(x, "/journal") {
			out = append(out, x)
		}
	}
	return out
}

func approve(t *testing.T, h *harness, r *Recorder) *Recorder {
	t.Helper()
	ap := approvalOf(t, r)
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	return rec
}

// THE regression of the audit (2026-10-08), exact user words. Before: an expense
// with «این هزینه را چه کسی پرداخت کرد؟», or 8 planner rounds rejected by a wrong
// schema and "I couldn't work out a safe way". After: payroll configuration,
// existing components inspected, one exact step, approval, verified read-back.
func TestSalaryComponentRequestIsPayrollConfiguration(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r, _ := talk(t, h, "", "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	if q := questions(r); len(q) > 0 {
		t.Fatalf("no question is needed (who paid? is wrong for a configuration): %v", q)
	}
	if len(h.ai.prompts) != 0 || len(h.ai.planPrompts) != 0 {
		t.Fatalf("the request is decided deterministically; the model was asked: %d/%d", len(h.ai.prompts), len(h.ai.planPrompts))
	}
	prev := r.Last("preview").(map[string]any)
	steps := prev["steps"].([]map[string]any)
	if len(steps) != 1 || steps[0]["tool"] != "payroll.components.create" {
		t.Fatalf("plan %+v", steps)
	}
	var body map[string]any
	var args tools.OpArgs
	_ = json.Unmarshal(steps[0]["args"].(json.RawMessage), &args)
	_ = json.Unmarshal(args.Body, &body)
	if body["name"] != "کمک هزینه سفر" || body["rate"] != "30000000" || body["method"] != "fixed" || body["kind"] != "earning" {
		t.Fatalf("payload %v (3,000,000 toman = 30,000,000 rial)", body)
	}
	if !strings.Contains(fmt.Sprint(prev["display"]), "۳,۰۰۰,۰۰۰ تومان") && !strings.Contains(fmt.Sprint(prev["display"]), "3,000,000 تومان") {
		t.Fatalf("the card states the amount in the user's unit: %v", prev["display"])
	}
	if pf.writes != 0 {
		t.Fatal("nothing is written before approval")
	}
	res := approve(t, h, r)
	out, ok := res.Last("result").(map[string]any)
	if !ok || !out["result"].(*Result).Verified {
		t.Fatalf("not verified: %+v", res.Events)
	}
	c := pf.comp("کمک هزینه سفر")
	if c == nil || c["rate"] != "30000000" || c["method"] != "fixed" {
		t.Fatalf("component not created as requested: %v", c)
	}
	if m := moneyHits(h); len(m) != 0 {
		t.Fatalf("a configuration request touched money endpoints: %v", m)
	}
}

func TestExistingSalaryComponentIsUpdatedNotDuplicated(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	pf.comps = append(pf.comps, map[string]any{"id": 9, "code": "travel", "name": "کمک هزینه سفر", "kind": "earning", "category": "allowance",
		"method": "fixed", "rate": "10000000", "seq": 90, "is_active": true, "quantity_field": "", "brackets": []any{}, "variability": "fixed"})
	r, _ := talk(t, h, "", "کمک‌هزینه سفر را به اجزای حقوق اضافه کن ۲۱,۰۰۰,۰۰۰ ریال")
	steps := r.Last("preview").(map[string]any)["steps"].([]map[string]any)
	if steps[0]["tool"] != "payroll.components.update" {
		t.Fatalf("an existing component must be updated: %v", steps[0]["tool"])
	}
	approve(t, h, r)
	if pf.count("کمک هزینه سفر") != 1 || pf.comp("کمک هزینه سفر")["rate"] != "21000000" {
		t.Fatalf("components %v", pf.comps)
	}
	// the same amount again: nothing to change, no approval card
	r2, _ := talk(t, h, "", "کمک هزینه سفر را به اجزای حقوق اضافه کن ۲۱۰۰۰۰۰۰ ریال")
	if r2.Last("approval") != nil || !strings.Contains(fmt.Sprint(r2.Last("message")), "تغییر نکرد") {
		t.Fatalf("no-op expected: %+v", r2.Events)
	}
}

func TestSalaryComponentAsksOnlyForWhatIsMissing(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r, conv := talk(t, h, "", "به اجزای حقوق بن کارگری اضافه کن")
	q := questions(r)
	if len(q) != 1 || !strings.Contains(q[0], "مبلغ") || strings.Contains(q[0], "پرداخت") {
		t.Fatalf("only the amount is missing: %v", q)
	}
	r2, _ := talk(t, h, conv, "۲ میلیون تومان")
	steps := r2.Last("preview").(map[string]any)["steps"].([]map[string]any)
	var args tools.OpArgs
	_ = json.Unmarshal(steps[0]["args"].(json.RawMessage), &args)
	if !strings.Contains(string(args.Body), `"name":"بن کارگری"`) || !strings.Contains(string(args.Body), `"rate":"20000000"`) {
		t.Fatalf("answer not merged into the same operation: %s", args.Body)
	}
	approve(t, h, r2)
	if pf.comp("بن کارگری") == nil {
		t.Fatal("not created")
	}
}

func TestEmployeeCompensationCreatesComponentAndOverride(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r, _ := talk(t, h, "", "برای کارمند زهرا معتضد کمک هزینه سفر ۳ میلیون تومان ثبت کن")
	if q := questions(r); len(q) > 0 {
		t.Fatalf("nothing is missing: %v", q)
	}
	pv, _ := r.Last("preview").(map[string]any)
	if pv == nil {
		t.Fatalf("no plan: %+v", r.Events)
	}
	steps := pv["steps"].([]map[string]any)
	if len(steps) != 2 || steps[0]["tool"] != "payroll.components.create" || steps[1]["tool"] != "employees.update" {
		t.Fatalf("plan %+v", steps)
	}
	res := approve(t, h, r)
	if out, ok := res.Last("result").(map[string]any); !ok || !out["result"].(*Result).Verified {
		t.Fatalf("not verified: %+v", res.Events)
	}
	c := pf.comp("کمک هزینه سفر")
	if c == nil || c["rate"] != "0" {
		t.Fatalf("company-wide default must be 0: %v", c)
	}
	emp := pf.emps[0]
	ov := fmt.Sprint(emp["components"])
	if !strings.Contains(ov, fmt.Sprint(c["id"])) || !strings.Contains(ov, "30000000") || emp["full_name"] != "زهرا معتضد" {
		t.Fatalf("override not saved on the employee (and the rest kept): %v", emp)
	}
	if m := moneyHits(h); len(m) != 0 {
		t.Fatalf("no money moved: %v", m)
	}
}

func TestAmbiguousEmployeeIsAskedThenContinued(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r, conv := talk(t, h, "", "برای کارمند رضایی کمک هزینه سفر ۳ میلیون تومان ثبت کن")
	q := questions(r)
	if len(q) != 1 || !strings.Contains(q[0], "محمد رضایی") || !strings.Contains(q[0], "علی رضایی") {
		t.Fatalf("both Rezaeis must be offered: %v", q)
	}
	r2, _ := talk(t, h, conv, "علی رضایی")
	steps := r2.Last("preview").(map[string]any)["steps"].([]map[string]any)
	last := steps[len(steps)-1]
	var args tools.OpArgs
	_ = json.Unmarshal(last["args"].(json.RawMessage), &args)
	if last["tool"] != "employees.update" || args.Path["id"] != "3" {
		t.Fatalf("wrong employee: %+v", last)
	}
	approve(t, h, r2)
	if !strings.Contains(fmt.Sprint(pf.emps[2]["components"]), "30000000") || fmt.Sprint(pf.emps[1]["components"]) != "[]" {
		t.Fatalf("only Ali Rezaei changes: %v / %v", pf.emps[1], pf.emps[2])
	}
}

func TestMemoryInstructionIsSeparateAndScoped(t *testing.T) {
	h := newHarness(t, "admin")
	addPayroll(t, h)
	r, conv := talk(t, h, "", "کمک هزینه سفر را به اجزای حقوق اضافه کن ۳ میلیون تومان و برای دفعات بعد هم به خاطر بسپار")
	res := approve(t, h, r)
	var msgs []string
	for _, e := range res.Events {
		if e.Name == "message" {
			msgs = append(msgs, fmt.Sprint(e.Data))
		}
	}
	if !strings.Contains(strings.Join(msgs, " "), "تنظیمات حقوق شرکت") {
		t.Fatalf("the memory part must be answered (company configuration): %v", msgs)
	}
	rules, _ := h.st.ListRules(context.Background(), h.user.Scope)
	if len(rules) != 0 {
		t.Fatalf("business configuration must not become a personal rule: %v", rules)
	}
	// a memory-only message after it: no model call, explained honestly
	before := h.ai.calls
	r2, _ := talk(t, h, conv, "اینو برای دفعه بعد یادت باشه")
	if h.ai.calls != before || !strings.Contains(fmt.Sprint(r2.Last("message")), "تنظیمات شرکت") {
		t.Fatalf("memory-only: calls %d→%d %+v", before, h.ai.calls, r2.Events)
	}
}

// A write whose response is lost (the app committed, the proxy answered 502) is
// reconciled, not repeated: one record, step adopted and verified.
func TestLostResponseIsReconciledNotDuplicated(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	pf.failCreate = "lost_response"
	r, _ := talk(t, h, "", "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	res := approve(t, h, r)
	if out, ok := res.Last("result").(map[string]any); !ok || !out["result"].(*Result).Verified {
		t.Fatalf("expected recovered + verified: %+v", res.Events)
	}
	if pf.count("کمک هزینه سفر") != 1 || pf.writes != 1 {
		t.Fatalf("duplicate write: %d records, %d writes", pf.count("کمک هزینه سفر"), pf.writes)
	}
	if !auditHas(t, h, taskOf(r), "step_recovered") {
		t.Fatal("recovery not traced")
	}
}

// The app is down for the write: it was NOT saved (checked), the user is told so,
// and «دوباره امتحان کن» offers the same step again — once, after reconciling.
func TestFailedWriteIsReportedAndRetriedSafely(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	pf.failCreate = "down"
	r, conv := talk(t, h, "", "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	res := approve(t, h, r)
	errEv, _ := res.Last("error").(map[string]any)
	if errEv == nil || !strings.Contains(fmt.Sprint(errEv["message"]), "ثبت نشده") || pf.count("کمک هزینه سفر") != 0 {
		t.Fatalf("expected a verified 'not saved': %+v", res.Events)
	}
	r2, _ := talk(t, h, conv, "دوباره امتحان کن")
	if r2.Last("approval") == nil || len(h.ai.planPrompts) != 0 {
		t.Fatalf("retry must re-offer the same step without re-planning: %+v", r2.Events)
	}
	approve(t, h, r2)
	if pf.count("کمک هزینه سفر") != 1 {
		t.Fatalf("records after retry: %d", pf.count("کمک هزینه سفر"))
	}
}

// Verification compares values: the app storing something else is reported.
func TestReadBackMismatchIsNotReportedAsSuccess(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	pf.storeRate = "3000000"
	r, _ := talk(t, h, "", "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	res := approve(t, h, r)
	out := res.Last("result").(map[string]any)
	if out["result"].(*Result).Verified {
		t.Fatal("a different stored rate must not count as verified")
	}
	if !strings.Contains(fmt.Sprint(res.Events), "rate: 30000000 ≠ 3000000") {
		t.Fatalf("the mismatch must be shown: %+v", res.Events)
	}
}

// The pre-tool check: a payroll plan may not contain money movements, and the
// password placeholder may not fill other fields (an employee got code "$generate").
func TestPlanGuardsDomainAndPlaceholders(t *testing.T) {
	h := newHarness(t, "admin")
	p := &PlanState{Domain: "payroll"}
	_, problem := h.a.validatePlan(h.user, p, []plannerCall{{ID: "s1", Tool: "expenses.create",
		Body: json.RawMessage(`{"date":"2026-10-08","category_id":1,"amount":"30000000","payment_method":"cash","treasury_account_id":1}`)}})
	if !strings.Contains(problem, "payroll domain") {
		t.Fatalf("expense in a payroll plan must be rejected: %q", problem)
	}
	_, problem = h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "parties.create",
		Body: json.RawMessage(`{"code":"$generate","name":"شرکت پارس نوآور","kind":"company"}`)}})
	if !strings.Contains(problem, "only for password fields") {
		t.Fatalf("placeholder in a code must be rejected: %q", problem)
	}
}

// A plan that keeps failing the same check stops with that reason (traced),
// instead of 8 silent rounds and a generic message.
func TestRepeatedPlanRejectionIsTracedAndExplained(t *testing.T) {
	h := newHarness(t, "admin")
	bad := `{"action":"plan","summary":"x","steps":[{"id":"s1","tool":"no.such.tool","body":{}}]}`
	h.ai.queue = []string{`{"intent":"operate","confidence":0.9}`}
	h.ai.planQueue = []string{bad, bad, bad, bad}
	r, _ := talk(t, h, "", "یک کار عجیب انجام بده")
	if len(h.ai.planPrompts) != 3 {
		t.Fatalf("expected a stop after 3 identical rejections, got %d rounds", len(h.ai.planPrompts))
	}
	if !strings.Contains(fmt.Sprint(r.Last("message")), "unknown tool no.such.tool") || !auditHas(t, h, taskOf(r), "plan_rejected") {
		t.Fatalf("reason not given/traced: %+v", r.Events)
	}
}

func auditHas(t *testing.T, h *harness, task, kind string) bool {
	rows, err := h.st.ListAudit(context.Background(), h.user.Scope, task, 300)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Kind == kind {
			return true
		}
	}
	return false
}

func TestTraceShowsDecisionPlanStepsAndVerification(t *testing.T) {
	h := newHarness(t, "admin")
	addPayroll(t, h)
	r, _ := talk(t, h, "", "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	approve(t, h, r)
	tr, err := h.a.Trace(context.Background(), h.user, taskOf(r))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(tr)
	for _, want := range []string{`"domain":"payroll"`, `"skill":"salary_component"`, `"kind":"decision"`, `"kind":"skill_plan"`, `"kind":"record_change"`, `"verified":true`, `"t_ms"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("trace lacks %s: %s", want, b)
		}
	}
}

// The real-browser failure: «field payroll.exemption not found» — the screen plan
// typed a default 0 into a control the form shows only for tax methods.
func TestScreenPlanMarksDefaultValuesOptional(t *testing.T) {
	op, _ := catalogGet("payroll.components.create")
	body := newComponent(nil, "کمک هزینه سفر", mustDec("30000000"), false)
	steps, problem := formSteps("fa", op, tools.OpArgs{Body: mustJSON(body)}, &PlanState{}, true)
	if problem != "" {
		t.Fatal(problem)
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if s.Target == "payroll.exemption" || s.Target == "payroll.ceiling" {
			if !s.Optional {
				t.Fatalf("%s restates a default and must be optional", s.Target)
			}
		}
		if s.Op == "fill" && s.Value == "30000000" {
			seen["rate"] = true
			if s.Optional {
				t.Fatal("the amount is never optional")
			}
		}
	}
	if !seen["rate"] {
		t.Fatalf("rate not filled: %+v", steps)
	}
}

func catalogGet(id string) (*catalog.Operation, bool) { return catalog.Load().Get(id) }
func mustDec(s string) decimal.Decimal                { return decimal.RequireFromString(s) }

func startOnScreen(t *testing.T, h *harness, text string) string {
	t.Helper()
	yes := true
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: text, ExecMode: "ui", UICapable: &yes}, rec)
	res := approve(t, h, rec)
	if res.Last("ui_plan") == nil {
		t.Fatalf("expected the screen plan: %+v", res.Events)
	}
	return taskOf(rec)
}

// The tab was closed (or reloaded) right after the form was saved: resuming must
// complete the step with the saved record, never replay the form (a duplicate).
func TestResumeAfterInterruptionFindsTheSavedRecord(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	id := startOnScreen(t, h, "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	// the browser saved the form, then the page went away before reporting
	pf.mu.Lock()
	pf.comps = append(pf.comps, map[string]any{"id": 500, "code": "allowance_1", "name": "کمک هزینه سفر", "kind": "earning", "category": "allowance",
		"method": "fixed", "rate": "30000000", "is_active": true, "created_at": "2025-10-08T10:00:01Z"})
	pf.mu.Unlock()
	rec := &Recorder{}
	h.a.ResumeUI(context.Background(), h.user, id, rec)
	if rec.Last("ui_plan") != nil {
		t.Fatal("the form must not be replayed after it was saved")
	}
	out, ok := rec.Last("result").(map[string]any)
	if !ok || !out["result"].(*Result).Verified || pf.count("کمک هزینه سفر") != 1 {
		t.Fatalf("expected completion with the saved record: %+v", rec.Events)
	}
	if !auditHas(t, h, id, "step_recovered") {
		t.Fatal("recovery not traced")
	}
}

func TestResumeWithoutSaveReplaysTheScreenPlan(t *testing.T) {
	h := newHarness(t, "admin")
	addPayroll(t, h)
	id := startOnScreen(t, h, "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	rec := &Recorder{}
	h.a.ResumeUI(context.Background(), h.user, id, rec)
	if rec.Last("ui_plan") == nil {
		t.Fatalf("nothing was saved: the screen plan is offered again: %+v", rec.Events)
	}
}

// A late/duplicate screen report must not end in «مرحله را گم کردم».
func TestStaleScreenReportContinuesFromState(t *testing.T) {
	h := newHarness(t, "admin")
	addPayroll(t, h)
	id := startOnScreen(t, h, "به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان")
	_, st, _ := h.a.load(context.Background(), h.user, id)
	st.UIStep = "s9" // a report for a step that is not current
	tk, _ := h.st.GetTask(context.Background(), h.user.Scope, id)
	h.a.save(context.Background(), &tk, st)
	rec := &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: true}, rec)
	b, _ := json.Marshal(rec.Events)
	if strings.Contains(string(b), "گم کردم") || strings.Contains(string(b), "lost track") || rec.Last("ui_plan") == nil {
		t.Fatalf("expected the current step to continue: %s", b)
	}
}

// Live finding: «حق سرپرستی زرقم» was taken for the existing «حق سرپرستی» and
// answered "already set — nothing changed". A similar name is asked, never assumed.
func TestSimilarComponentNameIsAskedNotAssumed(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r, conv := talk(t, h, "", "به اجزای حقوق حق مسکن ویژه اضافه کن ۵ میلیون تومان")
	q := questions(r)
	if len(q) != 1 || !strings.Contains(q[0], "حق مسکن") || !strings.Contains(q[0], "جدید") {
		t.Fatalf("expected a question listing «حق مسکن» and a new component: %v", q)
	}
	r2, _ := talk(t, h, conv, "یک جزء جدید «حق مسکن ویژه»")
	approve(t, h, r2)
	if pf.comp("حق مسکن ویژه") == nil || pf.comp("حق مسکن")["rate"] != "9000000" {
		t.Fatalf("new component expected, existing untouched: %v", pf.comps)
	}
	// choosing the existing one updates it instead
	r3, conv3 := talk(t, h, "", "به اجزای حقوق مسکن اضافه کن ۱۲ میلیون تومان")
	if len(questions(r3)) != 1 {
		t.Fatalf("«مسکن» is similar to two components: %v", questions(r3))
	}
	r4, _ := talk(t, h, conv3, "حق مسکن")
	approve(t, h, r4)
	if pf.comp("حق مسکن")["rate"] != "120000000" {
		t.Fatalf("existing component not updated: %v", pf.comp("حق مسکن"))
	}
}

// Live finding: an employee's salary override ran directly (policy: routine
// employee edit). Pay changes always get a human approval.
func TestEmployeePayChangeAlwaysNeedsApproval(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	pf.comps = append(pf.comps, map[string]any{"id": 9, "code": "travel", "name": "کمک هزینه سفر", "kind": "earning", "method": "fixed", "rate": "0", "is_active": true})
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "برای کارمند زهرا معتضد کمک هزینه سفر ۱ میلیون تومان ثبت کن مستقیم ثبت کن", ExecMode: "api"}, rec)
	if rec.Last("approval") == nil || pf.writes != 0 {
		t.Fatalf("expected an approval card and no write: writes=%d %+v", pf.writes, rec.Events)
	}
}

// Live finding: a second complete request sent while the first waited for approval
// was merged into the first as a follow-up and silently lost.
func TestNewRequestDuringApprovalSupersedesExplicitly(t *testing.T) {
	h := newHarness(t, "admin")
	pf := addPayroll(t, h)
	r1, conv := talk(t, h, "", "به اجزای حقوق حق تخصص اضافه کن ۱ میلیون تومان")
	ap1 := approvalOf(t, r1)
	r2, _ := talk(t, h, conv, "به اجزای حقوق حق سختی کار اضافه کن ۲ میلیون تومان")
	if !strings.Contains(fmt.Sprint(r2.Events), "ثبت نشد و کنار گذاشته شد") {
		t.Fatalf("the user must be told the first request was set aside: %+v", r2.Events)
	}
	approve(t, h, r2)
	if pf.comp("حق سختی کار") == nil || pf.comp("حق تخصص") != nil {
		t.Fatalf("second created, first not: %v", pf.comps)
	}
	// the first card can no longer register anything
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap1["task_id"].(string), ApproveRequest{Token: ap1["token"].(string)}, rec)
	if pf.comp("حق تخصص") != nil {
		t.Fatal("a superseded approval must not execute")
	}
}

// The same hidden-control failure on an UPDATE (live run: «field payroll.exemption
// not found» when changing an existing component on screen).
func TestScreenUpdatePlanMarksDefaultValuesOptional(t *testing.T) {
	op, _ := catalogGet("payroll.components.update")
	body := newComponent(nil, "کمک هزینه سفر", mustDec("30000000"), false)
	steps, problem := formSteps("fa", op, tools.OpArgs{Path: tools.Scalars{"id": "21"}, Body: mustJSON(body)}, &PlanState{}, true)
	if problem != "" {
		t.Fatal(problem)
	}
	for _, s := range steps {
		if s.Target == "payroll.exemption" && !s.Optional {
			t.Fatal("exemption 0 must be optional on update too")
		}
	}
}

// Live finding (E1): «تاریخ استخدام ۱۴۰۵/۰۷/۰۱» was stored as the Gregorian year 1405.
func TestJalaliDatesInPlansAreConverted(t *testing.T) {
	h := newHarness(t, "admin")
	steps, problem := h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "employees.create",
		Body: json.RawMessage(`{"code":"E104","first_name":"نرگس","last_name":"احمدی","hire_date":"1405-07-01","base_salary":"95000000"}`)}})
	if problem != "" || !strings.Contains(string(steps[0].Args.Body), `"hire_date":"2026-09-23"`) {
		t.Fatalf("%q %s", problem, steps[0].Args.Body)
	}
	_, problem = h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "employees.create",
		Body: json.RawMessage(`{"code":"E105","first_name":"a","last_name":"b","hire_date":"1405/13/40"}`)}})
	if !strings.Contains(problem, "not a valid date") {
		t.Fatalf("invalid date must be refused: %q", problem)
	}
	steps, _ = h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "employees.create",
		Body: json.RawMessage(`{"code":"E106","first_name":"a","last_name":"b","hire_date":"2026-09-23"}`)}})
	if !strings.Contains(string(steps[0].Args.Body), `"hire_date":"2026-09-23"`) {
		t.Fatalf("a Gregorian date is kept: %s", steps[0].Args.Body)
	}
}

// Live finding (K2): a check without an amount was offered for approval with amount 0.
func TestZeroAmountIsAskedNotPlanned(t *testing.T) {
	h := newHarness(t, "admin")
	_, problem := h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "treasury.transactions.create",
		Body: json.RawMessage(`{"kind":"receipt","date":"2026-10-08","treasury_account_id":2,"party_id":101,"amount":0}`)}})
	if !strings.Contains(problem, "amount is 0 or missing") {
		t.Fatalf("zero amount must be refused: %q", problem)
	}
}

func TestChatActionClaimIsDetected(t *testing.T) {
	for _, s := range []string{"باشه. انتقال بین حساب‌ها را ثبت می‌کنم.", "ثبت شد", "I'll register it now", "It has been registered."} {
		if !actionClaim.MatchString(normalize.Text(s)) {
			t.Fatalf("not flagged: %q", s)
		}
	}
	if actionClaim.MatchString(normalize.Text("برای ثبت انتقال، مبلغ و حساب مبدأ را بگویید.")) {
		t.Fatal("an instruction is not a claim")
	}
}

// Live finding (F1): the same cash box was created twice without a warning.
func TestDuplicateCashBoxIsWarnedBeforeApproval(t *testing.T) {
	h := newHarness(t, "admin")
	h.user.Me.Language = "fa"
	h.acc.extra.HandleFunc("GET /api/v1/treasury/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 16, "kind": "cash", "name": "صندوق شعبه کرج", "code": "1105"}})
	})
	h.ai.queue = []string{`{"intent":"operate","confidence":0.9}`}
	h.ai.planQueue = []string{`{"action":"plan","summary":"صندوق جدید","steps":[{"id":"s1","tool":"treasury.accounts.create","body":{"kind":"cash","name":"صندوق شعبه کرج"},"purpose":"ایجاد صندوق"}]}`}
	r, _ := talk(t, h, "", "یک صندوق جدید به نام صندوق شعبه کرج بساز")
	ap := approvalOf(t, r)
	if ap["duplicate"] != true || !strings.Contains(fmt.Sprint(r.Events), "صندوق شعبه کرج") {
		t.Fatalf("duplicate cash box must be flagged on the card: %+v", r.Events)
	}
}

// Live finding (A4): «۵ میلیون تومان» was registered as 5,000,000 rial.
func TestTomanAmountMustBeConverted(t *testing.T) {
	st := &State{}
	p := &PlanState{UserTexts: []string{"۵ میلیون تومان از صندوق مرکزی به بانک ملت جاری منتقل کردیم"}}
	steps := []PlanStep{{ID: "s1", Tool: "treasury.transactions.create", Args: tools.OpArgs{Body: json.RawMessage(`{"kind":"transfer","amount":"5000000"}`)}}}
	if fb := checkAmounts(st, p, steps, true); fb == "" {
		t.Fatal("5,000,000 rial for «۵ میلیون تومان» must be refused")
	}
	steps[0].Args.Body = json.RawMessage(`{"kind":"transfer","amount":"50000000"}`)
	if fb := checkAmounts(st, p, steps, true); fb != "" {
		t.Fatalf("50,000,000 rial is right: %s", fb)
	}
	p.UserTexts = []string{"۵ میلیون از صندوق به بانک منتقل کردیم"} // no unit: base currency
	steps[0].Args.Body = json.RawMessage(`{"kind":"transfer","amount":"5000000"}`)
	if fb := checkAmounts(st, p, steps, true); fb != "" {
		t.Fatalf("no unit → rial: %s", fb)
	}
}

func realAccounts(h *harness) {
	h.acc.extra.HandleFunc("GET /api/v1/treasury/accounts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "kind": "cash", "name": "صندوق مرکزی", "is_active": true},
			{"id": 2, "kind": "bank", "name": "بانک ملت جاری", "is_active": true}, {"id": 4, "kind": "bank", "name": "پارسیان", "is_active": true}})
	})
}

// Live finding (A4): «۵ میلیون تومان از صندوق مرکزی به بانک ملت جاری منتقل کردیم» —
// the model planner looped re-creating existing accounts; earlier it stored 5,000,000 rial.
func TestTransferBetweenNamedAccountsIsDeterministic(t *testing.T) {
	h := newHarness(t, "admin")
	h.user.Me.Language = "fa"
	realAccounts(h)
	h.ai.queue = []string{`{"intent":"register","user_requested_registration":true,"event":{"type":"transfer","amount":"5000000","currency":"IRT","date":"1405-07-16"}}`}
	r, _ := talk(t, h, "", "۵ میلیون تومان از صندوق مرکزی به بانک ملت جاری منتقل کردیم")
	if len(h.ai.planPrompts) != 0 || len(questions(r)) != 0 {
		t.Fatalf("no planner, no question: %d %v", len(h.ai.planPrompts), questions(r))
	}
	args := string(r.Last("preview").(map[string]any)["steps"].([]map[string]any)[0]["args"].(json.RawMessage))
	for _, want := range []string{`"treasury_account_id":1`, `"to_treasury_account_id":2`, `"amount":"50000000"`, `"kind":"transfer"`, `"date":"2026-10-08"`} {
		if !strings.Contains(args, want) {
			t.Fatalf("%s missing: %s", want, args)
		}
	}
}

// Live finding (A5): two identical «ایرانیکارت» suppliers; the plan silently used one.
func TestTwinCounterpartiesInAPlanAreAsked(t *testing.T) {
	h := newHarness(t, "admin")
	h.user.Me.Language = "fa"
	h.acc.parties = append(h.acc.parties,
		map[string]any{"id": 7, "code": "0007", "name": "ایرانیکارت", "kind": "company", "is_supplier": true, "is_active": true},
		map[string]any{"id": 8, "code": "0008", "name": "ایرانیکارت", "kind": "company", "is_supplier": true, "is_active": true})
	plan := `{"action":"plan","summary":"پرداخت","steps":[{"id":"s1","tool":"treasury.transactions.create","body":{"kind":"payment","date":"2025-10-08","treasury_account_id":2,"party_id":7,"amount":"30000000","description":"پرداخت به ایرانیکارت"},"purpose":"پرداخت به ایرانیکارت"}]}`
	h.ai.queue = []string{`{"intent":"operate","confidence":0.9}`, `{"intent":"operate","confidence":0.9}`}
	h.ai.planQueue = []string{plan, plan}
	r, conv := talk(t, h, "", "۳ میلیون تومان به ایرانیکارت پرداخت کن")
	q := questions(r)
	if len(q) != 1 || !strings.Contains(q[0], "0007") || !strings.Contains(q[0], "0008") || r.Last("approval") != nil {
		t.Fatalf("must ask which Iranicard: %v", q)
	}
	r2, _ := talk(t, h, conv, "ایرانیکارت — کد 0007")
	if r2.Last("approval") == nil {
		t.Fatalf("after choosing, the plan proceeds: %+v", r2.Events)
	}
}

// Live finding (K2): an unknown field made every round fail until the task stopped.
func TestUnknownFieldIsDroppedAndShown(t *testing.T) {
	h := newHarness(t, "admin")
	steps, problem := h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "parties.create",
		Body: json.RawMessage(`{"name":"شرکت آرمان تجارت کیش","kind":"company","is_customer":true,"reference":"x"}`)}})
	if problem != "" || strings.Contains(string(steps[0].Args.Body), "reference") || len(steps[0].Notes) != 1 {
		t.Fatalf("%q %s %v", problem, steps[0].Args.Body, steps[0].Notes)
	}
}

// Live finding (Q1): «مانده … برابر با ۱,۲۰۰,۰۰۰,۰۰۰- ریال» for a balance of −120,000,000.
func TestAnswerNumbersMustComeFromTheData(t *testing.T) {
	p := &PlanState{Reads: []ReadRecord{{Tool: "parties.list", Result: `{"items":[{"id":24,"name":"شرکت داده‌پردازان سپهر","balance":"-120000000"}]}`}},
		UserTexts: []string{"مانده حساب شرکت داده‌پردازان سپهر چقدر است؟"}}
	if bad := ungroundedNumbers("مانده برابر با ۱,۲۰۰,۰۰۰,۰۰۰- ریال است.", p); len(bad) != 1 {
		t.Fatalf("10× wrong balance must be caught: %v", bad)
	}
	if bad := ungroundedNumbers("مانده برابر با ۱۲۰,۰۰۰,۰۰۰- ریال (بستانکار) است.", p); len(bad) != 0 {
		t.Fatalf("correct figure flagged: %v", bad)
	}
}

// Live finding (C2): «پارس نوآور» was created next to «شرکت پارس نوآور».
func TestPartyDuplicateIgnoresLegalForm(t *testing.T) {
	h := newHarness(t, "admin")
	h.acc.parties = append(h.acc.parties, map[string]any{"id": 21, "code": "0019", "name": "شرکت پارس نوآور", "kind": "company", "is_supplier": true, "is_active": true})
	dups := h.a.partyDuplicates(context.Background(), h.user, []PlanStep{{ID: "s1", Tool: "parties.create", Args: tools.OpArgs{Body: json.RawMessage(`{"name":"پارس نوآور","kind":"company","is_supplier":true}`)}}})
	if len(dups) != 1 || dups[0].Party.ID != 21 {
		t.Fatalf("expected the existing «شرکت پارس نوآور»: %+v", dups)
	}
	if !sameCore("شركت پارس‌نوآور", "پارس نوآور") && !sameCore("شرکت پارس نوآور", "پارس نوآور") {
		t.Fatal("core names")
	}
}
