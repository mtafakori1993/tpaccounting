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
	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/policy"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// CI invariant (D59): every operation the planner can see has a real executor and a
// verification path. A row with Planner=YES and Direct=NO and UI=NO fails the build.
func TestCapabilityInvariantPlannerImpliesExecutor(t *testing.T) {
	reg := tools.NewRegistry()
	planner := 0
	for _, c := range CapabilityMatrix() {
		op, _ := catalog.Load().Get(c.Tool)
		if c.Planner {
			planner++
			if !c.Direct && !c.UI {
				t.Errorf("%s is planner-exposed but has no executor", c.Tool)
			}
			if c.Verify == VerifyNone {
				t.Errorf("%s is planner-exposed but has no verification path", c.Tool)
			}
		}
		// the registry's Direct column is the real direct executor, not an assumption
		if _, ok := reg.Get(c.Tool); ok != c.Direct {
			t.Errorf("%s: registry says direct=%v, tool registry says %v", c.Tool, c.Direct, ok)
		}
		if op.Executable() && !c.Direct {
			t.Errorf("%s is executable but has no direct executor", c.Tool)
		}
	}
	// the planner's tool list is exactly the planner-exposed rows
	text := plannerTools(func(string) bool { return true })
	for _, c := range CapabilityMatrix() {
		listed := strings.Contains("\n"+text, "\n"+c.Tool+": ")
		if listed != c.Planner {
			t.Errorf("%s: listed to the planner=%v but planner capability=%v", c.Tool, listed, c.Planner)
		}
	}
	if planner < 150 {
		t.Fatalf("only %d planner tools: the registry lost operations", planner)
	}
}

// The UI column must be what the screen executor really does: for every write, the
// screen plan builds exactly when the registry says the screen can carry it out.
func TestUICapabilityMatchesScreenExecutor(t *testing.T) {
	st := &State{Lang: "en", Plan: &PlanState{}}
	for _, c := range CapabilityMatrix() {
		op, _ := catalog.Load().Get(c.Tool)
		if op.IsRead() || !op.Executable() {
			continue
		}
		path := tools.Scalars{}
		for _, p := range op.PathParams {
			path[p] = "7"
		}
		if containsStr(op.PathParams, "kind") {
			path["kind"] = "invoice"
		}
		_, why := buildStepPlan(st, PlanStep{ID: "s1", Tool: op.ID, Args: tools.OpArgs{Path: path, Body: json.RawMessage(`{}`)}}, true)
		if c.UI && why != "" && !strings.Contains(why, "invalid") && !strings.Contains(why, "required") {
			t.Errorf("%s: registry says UI, but the screen executor says %q", c.Tool, why)
		}
		if !c.UI && why == "" {
			t.Errorf("%s: registry says no UI, but the screen executor built a plan", c.Tool)
		}
	}
}

// Seed cases from production: each has a real executor; deletes run directly because the
// application has no delete control on those screens.
func TestSeedCasesHaveExecutors(t *testing.T) {
	for _, id := range []string{"employees.delete", "cost_centers.delete", "checks.create"} {
		c, ok := capabilityFor(id)
		if !ok || !c.Planner || !c.Direct || c.Verify == VerifyNone || c.Approval != "approval" {
			t.Errorf("%s: %+v", id, c)
		}
	}
	for id, verify := range map[string]string{"employees.delete": VerifyGone, "cost_centers.delete": VerifyListGone} {
		if c, _ := capabilityFor(id); c.UI || c.Verify != verify || !c.Destructive {
			t.Errorf("%s: %+v", id, c)
		}
	}
}

// Negative: an operation without any executor is never offered, never accepted in a
// plan, and a plan that somehow carries it stops as NO_EXECUTOR before anything is shown.
func TestUnsupportedOperationIsNeverExecutable(t *testing.T) {
	fake := &catalog.Operation{ID: "fake.purge", Kind: "delete", Method: "DELETE", Path: "/fake/{id}", PathParams: []string{"id"}, Classification: catalog.NeedsApproval}
	c := capabilityOf(tools.NewRegistry(), fake)
	if c.Planner || c.Direct || c.UI || c.Blocked != FailNoExecutor {
		t.Fatalf("unsupported op exposed: %+v", c)
	}
	st := &State{Lang: "fa", ExecMode: "human_ui", Plan: &PlanState{Steps: []PlanStep{{ID: "s1", Tool: "fake.purge", Status: "pending", Args: tools.OpArgs{Path: tools.Scalars{"id": "1"}}}}}}
	if ok, _ := selectExecutor(st); ok || st.Exec.Status != "NO_EXECUTOR" || st.Exec.FailureCode != FailNoExecutor {
		t.Fatalf("selection must stop with NO_EXECUTOR: %+v", st.Exec)
	}
	h := newHarness(t, "admin")
	if _, problem := h.a.validatePlan(h.user, &PlanState{}, []plannerCall{{ID: "s1", Tool: "fake.purge"}}); problem == "" {
		t.Fatal("a plan with an unknown/unsupported tool was accepted")
	}
}

// ---------- employee delete against a fake application ----------

type fakeEmployees struct {
	mu      sync.Mutex
	rows    map[int]map[string]any
	deletes []string
	ignore  bool // the application accepts the DELETE but removes nothing
}

func withEmployees(t *testing.T, h *harness) *fakeEmployees {
	prev := adminPerms
	adminPerms = append(append([]string{}, adminPerms...), "payroll.delete", "cost_centers.delete")
	t.Cleanup(func() { adminPerms = prev })
	f := &fakeEmployees{rows: map[int]map[string]any{
		1: {"id": 1, "code": "E001", "first_name": "قاسم", "last_name": "مرادی", "full_name": "قاسم مرادی", "is_active": true},
		2: {"id": 2, "code": "E101", "first_name": "زهرا", "last_name": "معتضد", "full_name": "زهرا معتضد", "is_active": true},
		3: {"id": 3, "code": "E102", "first_name": "علی", "last_name": "رضایی", "full_name": "علی رضایی", "is_active": true},
	}}
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	h.acc.extra.HandleFunc("GET /api/v1/employees", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query().Get("q")
		var items []map[string]any
		for i := 1; i <= 10; i++ {
			// like the application: single columns (first OR last name OR code), never the full name
			if e, ok := f.rows[i]; ok && (q == "" || strings.Contains(e["first_name"].(string), q) || strings.Contains(e["last_name"].(string), q) || strings.Contains(e["code"].(string), q)) {
				items = append(items, e)
			}
		}
		js(w, map[string]any{"items": items, "total": len(items)})
	})
	h.acc.extra.HandleFunc("GET /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var id int
		fmt.Sscan(r.PathValue("id"), &id)
		if e, ok := f.rows[id]; ok {
			js(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"not found"}}`))
	})
	h.acc.extra.HandleFunc("DELETE /api/v1/employees/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var id int
		fmt.Sscan(r.PathValue("id"), &id)
		f.deletes = append(f.deletes, r.PathValue("id"))
		if !f.ignore {
			delete(f.rows, id)
		}
		w.WriteHeader(204)
	})
	// re-read the user's permissions with payroll.delete
	me, err := h.a.Acc.Me(context.Background(), h.user.Cred)
	if err != nil {
		t.Fatal(err)
	}
	h.user.Me = me
	return f
}

const zahraText = "کارمند زهرا معتضد رو حذف کن"
const zahraInterp = `{"intent":"operate","user_requested_registration":false,"confidence":0.9,"event":{}}`

// the production failure: the model guessed id 1 without any lookup
const zahraGuess = `{"action":"plan","summary":"حذف کارمند زهرا معتضد","steps":[{"id":"s1","tool":"employees.delete","path":{"id":"1"},"purpose":"حذف کارمند زهرا معتضد"}]}`
const zahraRead = `{"action":"read","calls":[{"tool":"employees.list","query":{"q":"زهرا معتضد"}}]}`
const zahraPlan = `{"action":"plan","summary":"حذف کارمند زهرا معتضد","steps":[{"id":"s1","tool":"employees.delete","path":{"id":"2"},"purpose":"حذف کارمند زهرا معتضد"}]}`

func zahraStart(t *testing.T, h *harness, f *fakeEmployees, mode string) (*Recorder, string) {
	t.Helper()
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{zahraGuess, zahraRead, zahraPlan}
	rec, id := startMode(t, h, zahraText, mode)
	if rec.Last("approval") == nil {
		t.Fatalf("expected approval: %+v\nPROMPTS:%s", rec.Events, strings.Join(h.ai.planPrompts, "\n---\n"))
	}
	if len(f.deletes) != 0 {
		t.Fatal("nothing may be deleted before approval")
	}
	return rec, id
}

// E2E regression (fake app): resolve → reject the guessed id → look up → approval →
// the executor that exists (direct) → deleted → verified by reading it back.
func TestEmployeeDeleteResolvesApprovesExecutesVerifies(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	rec, id := zahraStart(t, h, f, "")
	// the guessed id was refused with the record's real name, then looked up
	if !strings.Contains(strings.Join(h.ai.planPrompts, "\n"), "never read") {
		t.Fatalf("the guessed id was not refused:\n%s", strings.Join(h.ai.planPrompts, "\n---\n"))
	}
	prev := rec.Last("preview").(map[string]any)
	step := prev["steps"].([]map[string]any)[0]
	if tg, _ := step["target"].(*StepTarget); tg == nil || tg.Name != "زهرا معتضد" || tg.ID != "2" {
		t.Fatalf("card target %+v", step["target"])
	}
	ap := rec.Last("approval").(map[string]any)
	// executor selected BEFORE the card: the screen has no delete control → direct, said so
	if ap["exec_mode"] != "direct" || ap["can_direct"] != true || ap["can_ui"] != false || prev["exec_note"] == nil {
		t.Fatalf("approval %+v preview note %v", ap, prev["exec_note"])
	}
	if d := prev["decision"]; !strings.Contains(fmt.Sprint(d), "APPROVAL_REQUIRED") {
		t.Fatalf("a delete must require approval: %v", d)
	}
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
	res, ok := r2.Last("result").(map[string]any)
	if !ok || !res["result"].(*Result).Verified {
		t.Fatalf("not verified: %+v", r2.Events)
	}
	if strings.Join(f.deletes, ",") != "2" || f.rows[2] != nil || f.rows[1] == nil || f.rows[3] == nil {
		t.Fatalf("deletes=%v rows=%v", f.deletes, f.rows)
	}
	_, st, _ := h.a.load(context.Background(), h.user, id)
	if st.Exec == nil || st.Exec.Status != "COMPLETED" || st.Exec.Executor != "direct_api" || st.Exec.Attempts != 1 {
		t.Fatalf("exec record %+v", st.Exec)
	}
}

// «مستقیم اجرا کن» on the approval card's task: same task, approval still required,
// executed exactly once after approval.
func TestTypedDirectKeepsTaskAndApproval(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	rec, id := zahraStart(t, h, f, "")
	conv := rec.Last("conversation").(map[string]any)["conversation_id"].(string)
	plannerCalls := len(h.ai.planPrompts)
	r2, _ := talk(t, h, conv, "مستقیم اجرا کن")
	id2 := taskOf(r2)
	if id2 != id || len(h.ai.planPrompts) != plannerCalls {
		t.Fatalf("a bare mode instruction re-planned or opened a task: %s vs %s, planner %d→%d", id2, id, plannerCalls, len(h.ai.planPrompts))
	}
	if len(f.deletes) != 0 || r2.Last("result") != nil || r2.Last("approval") == nil {
		t.Logf("events %+v", r2.Events)
		t.Fatal("«مستقیم اجرا کن» must not bypass approval")
	}
	ap := r2.Last("approval").(map[string]any)
	r3 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r3)
	if res, ok := r3.Last("result").(map[string]any); !ok || !res["result"].(*Result).Verified || len(f.deletes) != 1 {
		t.Fatalf("result %+v deletes %v", r3.Events, f.deletes)
	}
	// repeating it after completion reports the real state; no new plan, no new delete
	r4, _ := talk(t, h, conv, "مستقیم اجرا کن")
	if len(f.deletes) != 1 || len(h.ai.planPrompts) != plannerCalls || !strings.Contains(fmt.Sprint(r4.Events), "already done and verified") {
		t.Fatalf("repeat: deletes=%v events=%+v", f.deletes, r4.Events)
	}
}

// The screen was explicitly requested for a delete the screen cannot do: the card says
// so (and needs approval anyway); choosing the screen on the card is BLOCKED with
// NO_UI_EXECUTOR, and «به‌جای آن مستقیم اجرا کن» then runs it directly — no loop.
func TestNoUIExecutorThenFallbackRunsDirectlyOnce(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	_, id := zahraStart(t, h, f, "human_ui")
	_, st, _ := h.a.load(context.Background(), h.user, id)
	ap := map[string]any{}
	r := &Recorder{}
	h.a.Reissue(context.Background(), h.user, id, r)
	ap = r.Last("approval").(map[string]any)
	if st.ExecMode != "direct" || ap["can_ui"] != false {
		t.Fatalf("mode %s card %+v", st.ExecMode, ap)
	}
	// the user insists on the screen via the card button
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string), ExecMode: "human_ui"}, r2)
	if r2.Last("ui_plan") != nil || len(f.deletes) != 0 {
		t.Fatalf("an impossible screen plan was started: %+v", r2.Events)
	}
	_, st, _ = h.a.load(context.Background(), h.user, id)
	if st.Exec.FailureCode != FailNoUIExecutor || st.Stage != StageUIFailed {
		t.Fatalf("state %+v %s", st.Exec, st.Stage)
	}
	for i := 0; i < 2; i++ { // pressed twice: executed once
		r3 := &Recorder{}
		h.a.UIResult(context.Background(), h.user, id, UIResult{FallbackAPI: true}, r3)
	}
	if strings.Join(f.deletes, ",") != "2" || f.rows[2] != nil {
		t.Fatalf("deletes %v", f.deletes)
	}
}

// A DELETE the application accepted but that removed nothing is not "verified".
func TestDeleteThatRemovedNothingIsNotVerified(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	f.ignore = true
	rec, id := zahraStart(t, h, f, "")
	ap := rec.Last("approval").(map[string]any)
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string)}, r2)
	res, _ := r2.Last("result").(map[string]any)
	if res == nil || res["result"].(*Result).Verified {
		t.Fatalf("an undeleted record was reported verified: %+v", r2.Events)
	}
	_, st, _ := h.a.load(context.Background(), h.user, id)
	if st.Exec.Status != "VERIFICATION_FAILED" {
		t.Fatalf("exec %+v", st.Exec)
	}
}

var _ = execmode.Direct

// The model put the NAME where the id belongs (production: cost_centers.delete {id:"عیاشی"}
// three rounds in a row): the name is resolved through the application, the card names
// the record, and the delete is verified from the list (cost centers have no read-by-id).
func TestNamedTargetIsResolvedThroughTheApplication(t *testing.T) {
	h := newHarness(t, "admin")
	withEmployees(t, h) // grants cost_centers.delete
	var mu sync.Mutex
	ccs := []map[string]any{{"id": 4, "code": "10", "name": "توسعه"}, {"id": 9, "code": "CC9", "name": "عیاشی"}}
	var deleted []string
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	h.acc.extra.HandleFunc("GET /api/v1/cost-centers", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		js(w, map[string]any{"items": ccs})
	})
	h.acc.extra.HandleFunc("DELETE /api/v1/cost-centers/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		deleted = append(deleted, r.PathValue("id"))
		var keep []map[string]any
		for _, c := range ccs {
			if fmt.Sprint(c["id"]) != r.PathValue("id") {
				keep = append(keep, c)
			}
		}
		ccs = keep
		w.WriteHeader(204)
	})
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{`{"action":"plan","summary":"Delete cost center عیاشی","steps":[{"id":"s1","tool":"cost_centers.delete","path":{"id":"عیاشی"},"purpose":"Delete cost center عیاشی"}]}`}
	rec, id := startMode(t, h, "عیاشی رو حذف کن", "")
	ap, ok := rec.Last("approval").(map[string]any)
	if !ok {
		t.Fatalf("no approval: %+v\n%s", rec.Events, strings.Join(h.ai.planPrompts, "\n---\n"))
	}
	step := rec.Last("preview").(map[string]any)["steps"].([]map[string]any)[0]
	if tg, _ := step["target"].(*StepTarget); tg == nil || tg.ID != "9" || tg.Name != "عیاشی" {
		t.Fatalf("target %+v", step["target"])
	}
	r2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, id, ApproveRequest{Token: ap["token"].(string), ExecMode: "direct"}, r2)
	if res, ok := r2.Last("result").(map[string]any); !ok || !res["result"].(*Result).Verified || strings.Join(deleted, ",") != "9" || len(ccs) != 1 {
		t.Fatalf("events %+v deleted %v", r2.Events, deleted)
	}
}

// A step id or reference the executor cannot resolve is refused before approval
// (production: "party_id": "$c1.id" reached the application literally).
func TestUnresolvableReferenceIsRefused(t *testing.T) {
	h := newHarness(t, "admin")
	calls := []plannerCall{
		{ID: "c1", Tool: "parties.create", Body: json.RawMessage(`{"name":"X","kind":"company"}`)},
	}
	if _, problem := h.a.validatePlan(h.user, &PlanState{}, calls); !strings.Contains(problem, "s1, s2") {
		t.Fatalf("step id c1 accepted: %q", problem)
	}
	calls = []plannerCall{
		{ID: "s1", Tool: "parties.create", Body: json.RawMessage(`{"name":"X","kind":"company"}`)},
		{ID: "s2", Tool: "parties.update", Path: tools.Scalars{"id": "$s1.id"}, Body: json.RawMessage(`{"name":"$c1.name"}`)},
	}
	if _, problem := h.a.validatePlan(h.user, &PlanState{}, calls); !strings.Contains(problem, "$c1.name") {
		t.Fatalf("reference $c1.name accepted: %q", problem)
	}
}

// The model put only the surname in the id slot: the record whose full name the user
// wrote is used — and only that one.
func TestPartialNameInIDSlotResolvesToTheNamedRecord(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{`{"action":"plan","summary":"حذف","steps":[{"id":"s1","tool":"employees.delete","path":{"id":"معتضد"},"purpose":"حذف کارمند زهرا معتضد"}]}`}
	rec, _ := startMode(t, h, zahraText, "")
	step := rec.Last("preview").(map[string]any)["steps"].([]map[string]any)[0]
	if tg, _ := step["target"].(*StepTarget); tg == nil || tg.ID != "2" || len(f.deletes) != 0 {
		t.Fatalf("target %+v", step["target"])
	}
}

// «کارمند زهرا معتضد» as a search: the kind word is not part of the name.
func TestSearchIgnoresTheKindWord(t *testing.T) {
	h := newHarness(t, "admin")
	withEmployees(t, h)
	rec := h.a.planRead(context.Background(), h.user, nil, plannerCall{Tool: "employees.list", Query: tools.Scalars{"q": "کارمند زهرا معتضد"}}, nopEmitter{})
	if !strings.Contains(rec.Result, `"id":2`) || strings.Contains(rec.Result, `"id":1,`) {
		t.Fatalf("search %s", rec.Result)
	}
}

func TestSearchTermFollowsTheUsersSpelling(t *testing.T) {
	p := &PlanState{UserTexts: []string{"کارمند آزمایش خطاالفشمیز رو حذف کن"}}
	c := userSpelling(plannerCall{Tool: "employees.list", Query: tools.Scalars{"q": "آزمایش خطاالفمیز"}}, p)
	if c.Query["q"] != "آزمایش خطاالفشمیز" {
		t.Fatalf("q=%q", c.Query["q"])
	}
	if c := userSpelling(plannerCall{Tool: "parties.list", Query: tools.Scalars{"q": "ایرانسل"}}, &PlanState{UserTexts: []string{"به ایرانیکارت پرداخت کن"}}); c.Query["q"] != "ایرانسل" {
		t.Fatalf("a different name must not be rewritten: %q", c.Query["q"])
	}
}

// Completing an update from the current record must not ground a guessed id.
func TestCompletingAnUpdateDoesNotGroundAGuessedID(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	f.rows[1]["hire_date"] = "2024-01-01" // a complete record: only grounding can stop the guess
	h.ai.queue = []string{zahraInterp}
	guess := `{"action":"plan","summary":"edit","steps":[{"id":"s1","tool":"employees.update","path":{"id":"1"},"body":{"mobile":"09120000000"},"purpose":"update mobile of زهرا معتضد"}]}`
	h.ai.planQueue = []string{guess, guess, guess}
	rec, _ := startMode(t, h, "موبایل زهرا معتضد رو 09120000000 کن", "direct")
	if rec.Last("approval") != nil || rec.Last("result") != nil {
		t.Fatalf("a guessed id (another employee) reached the card: %+v", rec.Events)
	}
	if !strings.Contains(strings.Join(h.ai.planPrompts, "\n"), "never read") {
		t.Fatalf("the guess was not refused:\n%s", strings.Join(h.ai.planPrompts, "\n--\n"))
	}
}

// The user's one change, the rest from the current record.
func TestUpdateIsCompletedFromTheCurrentRecord(t *testing.T) {
	h := newHarness(t, "admin")
	f := withEmployees(t, h)
	f.rows[2]["hire_date"] = "2025-03-21"
	h.ai.queue = []string{zahraInterp}
	h.ai.planQueue = []string{zahraRead, `{"action":"plan","summary":"edit","steps":[{"id":"s1","tool":"employees.update","path":{"id":"2"},"body":{"mobile":"09120000000"},"purpose":"update mobile of زهرا معتضد"}]}`}
	rec, _ := startMode(t, h, "موبایل زهرا معتضد رو 09120000000 کن", "direct")
	pv, ok := rec.Last("preview").(map[string]any)
	if !ok {
		t.Fatalf("no plan: %+v\n%s", rec.Events, strings.Join(h.ai.planPrompts, "\n--\n"))
	}
	args := string(pv["steps"].([]map[string]any)[0]["args"].(json.RawMessage))
	for _, want := range []string{`"mobile":"09120000000"`, `"code":"E101"`, `"last_name":"معتضد"`, `"hire_date":"2025-03-21"`} {
		if !strings.Contains(args, want) {
			t.Errorf("payload lacks %s: %s", want, args)
		}
	}
}

// Every planner-visible mutation has postconditions and a defined retry rule (D63, D65).
func TestEveryMutationHasPostconditionsAndARetryRule(t *testing.T) {
	for _, c := range CapabilityMatrix() {
		op, _ := catalog.Load().Get(c.Tool)
		if !c.Planner || op.IsRead() {
			continue
		}
		if len(c.Postconditions) == 0 || c.Retry == "" {
			t.Errorf("%s: postconditions %v retry %q", c.Tool, c.Postconditions, c.Retry)
		}
	}
}

// Approval is enforced for every approval-class operation, whatever the user says
// («مستقیم», routine change) — capability never weakens policy (D59).
func TestApprovalClassNeverRunsWithoutApproval(t *testing.T) {
	n := 0
	for _, c := range CapabilityMatrix() {
		if !c.Planner || c.Approval != "approval" {
			continue
		}
		op, _ := catalog.Load().Get(c.Tool)
		d := policy.DecidePlan(policy.PlanFacts{HasPermission: true, Requested: true, ExplicitDirect: true,
			Steps: []policy.StepFacts{{Tool: c.Tool, Classification: op.Classification, NeverAuto: policy.NeverAuto[c.Tool] || policy.NeverAutoTools[c.Tool], Routine: true}}})
		if d.Action != policy.ActionApproval {
			t.Errorf("%s: %s without approval", c.Tool, d.Action)
		}
		n++
	}
	if n < 60 {
		t.Fatalf("only %d approval-class operations checked", n)
	}
}
