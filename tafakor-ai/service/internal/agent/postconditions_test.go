package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

func TestPayrollTransitionIsTheApplicationsMachine(t *testing.T) {
	op, _ := catalog.Load().Get("payroll.periods.approve")
	if m := checkTransition(op, tools.OpArgs{}, map[string]any{"status": "calculated"}, map[string]any{"status": "approved"}); len(m) != 0 {
		t.Fatalf("valid approve flagged: %v", m)
	}
	if m := checkTransition(op, tools.OpArgs{}, map[string]any{"status": "calculated"}, map[string]any{"status": "calculated"}); len(m) == 0 {
		t.Fatal("an approve that changed nothing passed")
	}
	op2, _ := catalog.Load().Get("checks.transitions")
	if m := checkTransition(op2, tools.OpArgs{Body: json.RawMessage(`{"action":"deposit"}`)}, map[string]any{"status": "received", "check_type": "received"},
		map[string]any{"status": "deposited"}); len(m) != 0 {
		t.Fatalf("deposit: %v", m)
	}
	op3, _ := catalog.Load().Get("treasury.transactions.allocations")
	if w, _ := expectedStatus(op3, tools.OpArgs{}, nil); w != "" {
		t.Fatalf("allocations define no status, got %q", w)
	}
}

func TestCollateralCatchesOtherRecordsAndDuplicates(t *testing.T) {
	mk := func(rows map[string]string) listSnapshot { return listSnapshot{ok: true, rows: rows} }
	upd, _ := catalog.Load().Get("employees.update")
	if p, ok := collateral(upd, mk(map[string]string{"1": "a", "2": "b"}), mk(map[string]string{"1": "a2", "2": "b"}), "1"); !ok || len(p) != 0 {
		t.Fatalf("target change flagged: %v", p)
	}
	if p, _ := collateral(upd, mk(map[string]string{"1": "a", "2": "b"}), mk(map[string]string{"1": "a2", "2": "b2"}), "1"); len(p) != 1 || !strings.Contains(p[0], "record 2 changed") {
		t.Fatalf("collateral change missed: %v", p)
	}
	cr, _ := catalog.Load().Get("employees.create")
	if p, _ := collateral(cr, mk(map[string]string{"1": "a"}), mk(map[string]string{"1": "a", "5": "x", "6": "x"}), "5"); len(p) != 1 || !strings.Contains(p[0], "more than one") {
		t.Fatalf("duplicate missed: %v", p)
	}
	if _, ok := collateral(cr, listSnapshot{}, mk(nil), "5"); ok {
		t.Fatal("an unreadable list must report n/a, not ok")
	}
}

type fakePeriods struct {
	mu       sync.Mutex
	status   string
	advance  bool // the application really changes the status
	approves int
}

func withPeriod(t *testing.T, h *harness, advance bool) *fakePeriods {
	prev := adminPerms
	adminPerms = append(append([]string{}, adminPerms...), "payroll.approve")
	t.Cleanup(func() { adminPerms = prev })
	f := &fakePeriods{status: "calculated", advance: advance}
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	rec := func() map[string]any { return map[string]any{"id": 4, "title": "مهر ۱۴۰۵", "status": f.status} }
	h.acc.extra.HandleFunc("GET /api/v1/payroll/periods/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		js(w, rec())
	})
	h.acc.extra.HandleFunc("GET /api/v1/payroll/periods", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		js(w, map[string]any{"items": []any{rec(), map[string]any{"id": 3, "title": "شهریور", "status": "closed"}}})
	})
	h.acc.extra.HandleFunc("POST /api/v1/payroll/periods/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.approves++
		if f.advance {
			f.status = "approved"
		}
		js(w, rec())
	})
	me, _ := h.a.Acc.Me(context.Background(), h.user.Cred)
	h.user.Me = me
	return f
}

const periodRead = `{"action":"read","calls":[{"tool":"payroll.periods.list"}]}`
const periodPlan = `{"action":"plan","summary":"approve payroll","steps":[{"id":"s1","tool":"payroll.periods.approve","path":{"id":"4"},"purpose":"approve payroll"}]}`

func runApprove(t *testing.T, advance bool) (*harness, *fakePeriods, *Recorder, string) {
	h := newHarness(t, "admin")
	f := withPeriod(t, h, advance)
	h.ai.queue = []string{`{"intent":"operate","confidence":0.9,"event":{}}`}
	h.ai.planQueue = []string{periodRead, periodPlan}
	rec, id := startMode(t, h, "approve the payroll", "direct")
	ap, ok := rec.Last("approval").(map[string]any)
	if !ok {
		t.Fatalf("no approval: %+v\n%s", rec.Events, strings.Join(h.ai.planPrompts, "\n--\n"))
	}
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
	return h, f, r2, id
}

// Payroll period actions compare the status before and after with the application's machine.
func TestPayrollApproveVerifiesTheStatusTransition(t *testing.T) {
	h, f, r, id := runApprove(t, true)
	res, ok := r.Last("result").(map[string]any)
	if !ok || !res["result"].(*Result).Verified || f.approves != 1 {
		t.Fatalf("valid approve: %+v", r.Events)
	}
	_, st, _ := h.a.load(context.Background(), h.user, id)
	if c := st.Plan.Steps[0].Checks; c["transition"] != "ok" || c["collateral"] != "ok" || c["identity"] != "ok" {
		t.Fatalf("checks %v", c)
	}
	// the application answered 200 but the status did not move: never "verified"
	h2, _, r2, id2 := runApprove(t, false)
	res2, _ := r2.Last("result").(map[string]any)
	if res2 == nil || res2["result"].(*Result).Verified {
		t.Fatalf("an approve without a status change was verified: %+v", r2.Events)
	}
	_, st2, _ := h2.a.load(context.Background(), h2.user, id2)
	if st2.Plan.Steps[0].Checks["transition"] != "failed" || st2.Exec.Status != "VERIFICATION_FAILED" {
		t.Fatalf("checks %v exec %+v", st2.Plan.Steps[0].Checks, st2.Exec)
	}
}

// The record was replaced after approval (the id now names someone else): nothing is sent.
func TestIdentityChangedAfterApprovalSendsNothing(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	rec, id := zahraStart(t, h, f, "")
	f.mu.Lock()
	f.rows[2]["full_name"] = "کس دیگری"
	f.mu.Unlock()
	ap := rec.Last("approval").(map[string]any)
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
	if len(f.deletes) != 0 || r2.Last("result") != nil {
		t.Fatalf("a changed record was deleted: deletes=%v %+v", f.deletes, r2.Events)
	}
}

// An update that also changed another record is not verified.
func TestUpdateThatTouchedAnotherRecordIsNotVerified(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	h.acc.extra.HandleFunc("PUT /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		var id int
		fmt.Sscan(r.PathValue("id"), &id)
		for k, v := range body {
			f.rows[id][k] = v
		}
		f.rows[3]["full_name"] = "علی رضایی (changed by a bug)" // collateral damage
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.rows[id])
	})
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{zahraRead, `{"action":"plan","summary":"edit","steps":[{"id":"s1","tool":"employees.update","path":{"id":"2"},"body":{"code":"E101","hire_date":"2025-03-21","first_name":"زهرا","last_name":"معتضد","mobile":"09120000000"},"purpose":"update mobile of زهرا معتضد"}]}`}
	rec, id := startMode(t, h, "موبایل زهرا معتضد رو 09120000000 کن", "direct")
	ap, ok := rec.Last("approval").(map[string]any)
	if !ok {
		if res, ok := rec.Last("result").(map[string]any); ok {
			if res["result"].(*Result).Verified {
				t.Fatal("collateral change verified (auto)")
			}
			return
		}
		t.Fatalf("no approval/result: %+v\n%s", rec.Events, strings.Join(h.ai.planPrompts, "\n--\n"))
	}
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
	res, _ := r2.Last("result").(map[string]any)
	if res == nil || res["result"].(*Result).Verified {
		t.Fatalf("collateral change verified: %+v", r2.Events)
	}
}

// Live finding (2026-10-09): «نرگس احمدی رو ویرایش کن احمدی سرتختی بشه» sent a partial
// body to a full-replacement PUT; her base salary became 0 and the step was "verified".
func withNarges(t *testing.T, h *harness, dropSalary bool) *fakeEmployees {
	f := withEmployees(t, h)
	f.rows[7] = map[string]any{"id": 7, "code": "E104", "first_name": "نرگس", "last_name": "احمدی", "full_name": "نرگس احمدی",
		"hire_date": "2026-09-23", "employment_type": "full_time", "base_salary": "95000000", "is_active": true}
	h.acc.extra.HandleFunc("PUT /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		var id int
		fmt.Sscan(r.PathValue("id"), &id)
		rec := map[string]any{"id": id} // full replacement, like the application
		for k, v := range body {
			rec[k] = v
		}
		if _, ok := rec["base_salary"]; !ok || dropSalary {
			rec["base_salary"] = "0"
		}
		rec["full_name"] = fmt.Sprint(rec["first_name"]) + " " + fmt.Sprint(rec["last_name"])
		f.rows[id] = rec
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rec)
	})
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{`{"action":"read","calls":[{"tool":"employees.list","query":{"q":"نرگس احمدی"}}]}`,
		// exactly what the real model sent: no base_salary
		`{"action":"plan","summary":"rename","steps":[{"id":"s1","tool":"employees.update","path":{"id":"7"},"body":{"code":"E104","employment_type":"full_time","first_name":"نرگس","full_name":"نرگس سرتختی","hire_date":"2026-09-23","is_active":true,"last_name":"سرتختی"},"purpose":"rename نرگس احمدی"}]}`}
	return f
}

func runNarges(t *testing.T, h *harness) *Recorder {
	rec, id := startMode(t, h, "نرگس احمدی رو ویرایش کن احمدی سرتختی بشه", "direct")
	if ap, ok := rec.Last("approval").(map[string]any); ok {
		r2 := &Recorder{}
		h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
		return r2
	}
	return rec
}

func TestPartialUpdateKeepsUntouchedFields(t *testing.T) {
	h := newHarness(t, "admin")
	f := withNarges(t, h, false)
	r := runNarges(t, h)
	res, ok := r.Last("result").(map[string]any)
	if !ok || !res["result"].(*Result).Verified {
		t.Fatalf("rename not verified: %+v\n%s", r.Events, strings.Join(h.ai.planPrompts, "\n--\n"))
	}
	if got := f.rows[7]; got["last_name"] != "سرتختی" || fmt.Sprint(got["base_salary"]) != "95000000" {
		t.Fatalf("the salary was wiped by the rename: %v", got)
	}
}

func TestUpdateThatWipesAnUntouchedFieldIsNotVerified(t *testing.T) {
	h := newHarness(t, "admin")
	withNarges(t, h, true) // the application drops the salary anyway
	r := runNarges(t, h)
	res, _ := r.Last("result").(map[string]any)
	if res == nil || res["result"].(*Result).Verified {
		t.Fatalf("a wiped salary was verified: %+v", r.Events)
	}
	if !strings.Contains(fmt.Sprint(r.Events), "base_salary changed") {
		t.Fatalf("the user is not told what changed: %+v", r.Events)
	}
}
