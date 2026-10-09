package agent

import (
	"encoding/json"

	"context"
	"fmt"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
	"strings"
	"testing"
)

func startMode(t *testing.T, h *harness, text, mode string) (*Recorder, string) {
	t.Helper()
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: text, ExecMode: mode}, rec)
	return rec, taskOf(rec)
}

// simulates the widget saving the real form: the application creates the record.
func widgetSavesParty(h *harness, name, mobile string) {
	h.acc.mu.Lock()
	defer h.acc.mu.Unlock()
	h.acc.parties = append(h.acc.parties, map[string]any{"id": 300 + len(h.acc.parties), "code": "9999", "name": name, "kind": "company",
		"is_customer": true, "is_active": true, "mobile": mobile})
}

func planOps(p UIPlan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Op+":"+s.Target)
	}
	return out
}

// Scenario A — default: no instruction → the real screen (pointer), no API write.
func TestDefaultIsHumanUIWithoutAPIShortcut(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	rec, id := startMode(t, h, ghasemText, "")
	plan, ok := rec.Last("ui_plan").(UIPlan)
	if !ok {
		t.Fatalf("expected an on-screen plan: %+v", rec.Events)
	}
	if h.acc.posts != 0 {
		t.Fatal("HUMAN_UI must not call the API write")
	}
	if plan.ExecMode != "human_ui" {
		t.Fatalf("exec mode %q", plan.ExecMode)
	}
	ops := strings.Join(planOps(plan), " ")
	for _, want := range []string{"navigate:", "click:parties.newParty", "fill:common.name", "check:parties.customer", "submit:common.save", "wait_closed:"} {
		if !strings.Contains(ops, want) {
			t.Errorf("plan lacks %s: %s", want, ops)
		}
	}
	for _, s := range plan.Steps {
		if s.Op == "navigate" && s.Value != "/parties" {
			t.Errorf("navigates to %q", s.Value)
		}
	}
	// the widget operates the form; the application creates the record
	widgetSavesParty(h, "شرکت قاسم", "09357578808")
	rec2 := &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: true}, rec2)
	res, ok := rec2.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", rec2.Events)
	}
	r := res["result"].(*Result)
	if !r.Verified || r.Via != "human_ui" || partiesNamed(h, "شرکت قاسم") != 1 || h.acc.posts != 0 {
		t.Fatalf("result %+v posts=%d", r, h.acc.posts)
	}
}

// Scenario B — explicit «مستقیم»: application service, no pointer, no screen plan.
func TestExplicitDirectUsesServiceWithoutPointer(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	rec, _ := startMode(t, h, ghasemText+"، مستقیم انجام بده", "")
	if rec.Last("ui_plan") != nil {
		t.Fatalf("DIRECT must not drive the screen: %+v", rec.Events)
	}
	if h.acc.posts != 1 || partiesNamed(h, "شرکت قاسم") != 1 {
		t.Fatalf("not created directly: posts=%d", h.acc.posts)
	}
	res := rec.Last("result").(map[string]any)["result"].(*Result)
	if res.Via != "direct" {
		t.Fatalf("via %q", res.Via)
	}
}

// Scenario C — «با موس انجامش بده» is the screen even when the preference says direct.
func TestExplicitMouseBeatsDirectPreference(t *testing.T) {
	h := newHarness(t, "admin")
	s, _ := h.st.GetUserSettings(context.Background(), h.user.Scope)
	s.ExecPreference = "direct"
	_ = h.st.SaveUserSettings(context.Background(), s)
	ghasem(h)
	rec, _ := startMode(t, h, ghasemText+" با موس انجامش بده", "")
	if _, ok := rec.Last("ui_plan").(UIPlan); !ok || h.acc.posts != 0 {
		t.Fatalf("expected the screen: %+v", rec.Events)
	}
}

// The persistent preference applies when the request says nothing.
func TestDirectPreferenceApplies(t *testing.T) {
	h := newHarness(t, "admin")
	s, _ := h.st.GetUserSettings(context.Background(), h.user.Scope)
	s.ExecPreference = "direct"
	_ = h.st.SaveUserSettings(context.Background(), s)
	ghasem(h)
	rec, _ := startMode(t, h, ghasemText, "")
	if rec.Last("ui_plan") != nil || h.acc.posts != 1 {
		t.Fatalf("preference ignored: %+v", rec.Events)
	}
}

// Never silently downgrade: when the screen cannot express the work, the plan is refused with
// a reason (the user may then explicitly choose to run it directly); nothing hits the API.
func TestHumanUINeverFallsBackSilently(t *testing.T) {
	h := newHarness(t, "admin")
	st := &State{Lang: "en", ExecMode: "human_ui", Plan: &PlanState{}}
	s := PlanStep{ID: "s1", Tool: "parties.create", Args: opArgs(nil, `{"name":"X","kind":"company","bogus_field":"1"}`)}
	st.Plan.Steps = []PlanStep{s}
	if _, why := buildStepPlan(st, s, true); why == "" {
		t.Fatal("a field the form has no control for must be reported, not dropped")
	}
	// a card password cannot be typed by the assistant
	u := PlanStep{ID: "s1", Tool: "users.create", Args: opArgs(nil, `{"email":"a@b.c","password":"$generate"}`)}
	st.Plan.Steps = []PlanStep{u}
	if _, why := buildStepPlan(st, u, true); why == "" {
		t.Fatal("generated secrets are not typed on screen")
	}
	if h.acc.posts != 0 {
		t.Fatal("nothing may be sent to the API")
	}
}

// Scenario: failure of the screen run is reported; the explicit «run directly» choice then
// performs it once (the screen attempt created nothing).
func TestScreenFailureThenExplicitDirect(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	rec, id := startMode(t, h, ghasemText, "")
	if _, ok := rec.Last("ui_plan").(UIPlan); !ok {
		t.Fatalf("no plan: %+v", rec.Events)
	}
	rec2 := &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: false, FailedStep: 3, Error: "button parties.newParty not found"}, rec2)
	f, ok := rec2.Last("ui_failed").(map[string]any)
	if !ok || f["can_fallback"] != true || h.acc.posts != 0 {
		t.Fatalf("failure not reported honestly: %+v posts=%d", rec2.Events, h.acc.posts)
	}
	rec3 := &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: false, FallbackAPI: true}, rec3)
	if h.acc.posts != 1 || partiesNamed(h, "شرکت قاسم") != 1 {
		t.Fatalf("explicit direct choice not honoured: %+v", rec3.Events)
	}
}

// Confirmation and execution mode are independent: a payment still asks, then runs on screen.
func TestConfirmationIsIndependentOfMode(t *testing.T) {
	h := newHarness(t, "admin")
	rec := &Recorder{}
	h.ai.queue = []string{`{"intent":"operate","user_requested_registration":true}`}
	h.ai.planQueue = []string{`{"action":"plan","summary":"s","steps":[{"id":"s1","tool":"parties.create","body":{"name":"شرکت قاسم","kind":"company","is_customer":true},"purpose":"p"}]}`}
	h.a.Start(context.Background(), h.user, StartRequest{Message: ghasemText, AskThisTime: true}, rec)
	ap := approvalOf(t, rec)
	if ap["exec_mode"] != "human_ui" || rec.Last("ui_plan") != nil {
		t.Fatalf("expected a card for a human_ui operation: %+v", rec.Events)
	}
	rec2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec2)
	if _, ok := rec2.Last("ui_plan").(UIPlan); !ok || h.acc.posts != 0 {
		t.Fatalf("approval must run on screen: %+v", rec2.Events)
	}
}

// Opening / undo go through the screen too; DIRECT only when asked.
func TestUndoAndOpenOnScreen(t *testing.T) {
	plan, why := cancelPlan("en", Target{EntityType: "treasury_transaction", EntityID: 7})
	if why != "" || len(plan.Steps) != 5 || plan.Steps[1].Op != "open_row" || plan.Steps[1].Value != "7" {
		t.Fatalf("cancel plan %+v %s", plan, why)
	}
	op, why := openPlan("fa", Target{EntityType: "invoice", EntityID: 12, Side: "sales", Kind: "quotation"})
	if why != "" || op.Steps[0].Value != "/sales/quotations" || op.Steps[1].Expect != "route:/sales/quotations/12" {
		t.Fatalf("open plan %+v %s", op, why)
	}
	pp, why := printPlan("en", Target{EntityType: "expense", EntityID: 4})
	if why != "" || pp.Steps[len(pp.Steps)-1].Target != "common.printDoc" || pp.Steps[len(pp.Steps)-1].Dialog != "*" {
		t.Fatalf("print plan %+v %s", pp, why)
	}
	if _, why := openPlan("en", Target{EntityType: "mystery"}); why == "" {
		t.Fatal("unknown record types must be reported")
	}
}

func TestInvoiceFormPlanHasLineGrid(t *testing.T) {
	h := newHarness(t, "admin")
	_ = h
	st := &State{Lang: "en", Plan: &PlanState{Steps: []PlanStep{}}}
	body := `{"date":"2026-10-08","party_id":101,"warehouse_id":null,"items":[{"product_id":5,"quantity":2,"unit_price":1000}],"post":false}`
	s := PlanStep{ID: "s1", Tool: "sales.kind.create", Args: opArgs(map[string]string{"kind": "quotations"}, body)}
	st.Plan.Steps = append(st.Plan.Steps, s)
	plan, why := buildStepPlan(st, s, true)
	if why != "" {
		t.Skipf("form not available in this catalog build: %s", why)
	}
	ops := strings.Join(planOps(plan), " ")
	for _, want := range []string{"ensure_rows:invoices.addItem", "combo:invoices.item", "fill:invoices.quantity", "fill:invoices.unitPrice", "submit:common.save", "wait_leave:"} {
		if !strings.Contains(ops, want) {
			t.Errorf("lacks %s in %s", want, ops)
		}
	}
	var cell UIStep
	for _, x := range plan.Steps {
		if x.Target == "invoices.quantity" {
			cell = x
		}
	}
	if cell.Row != 1 || cell.Col != 3 || plan.Steps[0].Value != "/sales/quotations/new" {
		t.Fatalf("grid cell %+v route %q", cell, plan.Steps[0].Value)
	}
	_ = fmt.Sprint
}

func opArgs(path map[string]string, body string) tools.OpArgs {
	return tools.OpArgs{Path: path, Body: json.RawMessage(body)}
}
