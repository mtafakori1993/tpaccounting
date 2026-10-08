package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// "ایرانیکارت را به عنوان طرف حساب بساز و بعد پرداخت ۵ میلیون تومانی را ثبت کن."
const iranicardInterp = `{"intent":"register","user_requested_registration":true,"confidence":0.9,
 "event":{"type":"supplier_payment","counterparty":"ایرانیکارت","amount":"5000000","currency":"IRT","date":"2025-10-07","paid_by":"bank"}}`
const iranicardRead = `{"action":"read","calls":[{"tool":"parties.list","query":{"q":"ایرانیکارت","page_size":"5"}},{"tool":"treasury.accounts.list"}]}`
const iranicardPlan = `{"action":"plan","summary":"Create the counterparty Iranicard, then record a 50,000,000 IRR payment to it from Mellat.",
 "entities":{"counterparty_name":"ایرانیکارت","amount":50000000},
 "steps":[
  {"id":"s1","tool":"parties.create","body":{"name":"ایرانیکارت","kind":"company","is_supplier":true},"purpose":"create counterparty Iranicard"},
  {"id":"s2","tool":"treasury.transactions.create","body":{"kind":"payment","date":"2025-10-07","treasury_account_id":2,"party_id":"$s1.id","amount":"50000000","description":"payment to Iranicard","auto_allocate":true},"purpose":"record the payment"}]}`

func TestCreatesMissingCounterpartyThenContinuesThePayment(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{iranicardInterp}
	h.ai.planQueue = []string{iranicardRead, iranicardPlan}
	r1, conv := talk(t, h, "", "ایرانیکارت را به عنوان طرف حساب بساز و بعد پرداخت ۵ میلیون تومانی را ثبت کن.")
	if r1.Last("error") != nil || r1.Last("approval") == nil {
		t.Fatalf("expected a plan waiting for approval: %+v", r1.Events)
	}
	prev := r1.Last("preview").(map[string]any)
	steps := prev["steps"].([]map[string]any)
	if prev["action"] != "plan" || len(steps) != 2 || steps[0]["tool"] != "parties.create" {
		t.Fatalf("plan %+v", prev)
	}
	if h.acc.posts != 0 {
		t.Fatal("nothing may be written before approval")
	}
	// the planner saw the discovery results (no Iranicard) before planning
	if !strings.Contains(h.ai.planPrompts[1], `parties.list`) || !strings.Contains(h.ai.planPrompts[1], `"items":[]`) {
		t.Fatalf("read results not given back to the planner:\n%s", h.ai.planPrompts[1])
	}
	// the earlier understanding (amount, counterparty) was carried into the plan state
	if !strings.Contains(h.ai.planPrompts[0], "ایرانیکارت") || !strings.Contains(h.ai.planPrompts[0], "5000000") {
		t.Fatalf("handover lost the request's facts:\n%s", h.ai.planPrompts[0])
	}
	ap := r1.Last("approval").(map[string]any)
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	res, ok := rec.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", rec.Events)
	}
	if !res["result"].(*Result).Verified {
		t.Fatalf("steps not verified: %+v", res)
	}
	// counterparty created, and the payment uses ITS new id
	var cp map[string]any
	for _, p := range h.acc.parties {
		if p["name"] == "ایرانیکارت" {
			cp = p
		}
	}
	if cp == nil || len(h.acc.txs) != 1 || fmt.Sprint(h.acc.txs[0]["party_id"]) != fmt.Sprint(cp["id"]) || h.acc.txs[0]["kind"] != "payment" {
		t.Fatalf("workflow not completed with the new id: party=%v txs=%v", cp, h.acc.txs)
	}
	// state: both steps completed, nothing pending; tool calls in the conversation; audit has before/after records
	_, st, _ := h.a.load(context.Background(), h.user, ap["task_id"].(string))
	if len(st.Plan.Completed()) != 2 || len(st.Plan.Pending()) != 0 || st.Plan.Status != "done" {
		t.Fatalf("plan state %+v", st.Plan)
	}
	view, _ := h.a.ConversationView(context.Background(), h.user, conv)
	var tl []string
	for _, m := range view["messages"].([]ConvMessage) {
		if m.Role == "tool" {
			tl = append(tl, m.Text)
		}
	}
	if !strings.Contains(strings.Join(tl, ","), "parties.create,verify,treasury.transactions.create,verify") {
		t.Fatalf("tool calls missing from conversation state: %v", tl)
	}
	entries, _ := h.st.ListAudit(context.Background(), h.user.Scope, ap["task_id"].(string), 200)
	changes := 0
	for _, e := range entries {
		if e.Kind == "record_change" {
			changes++
			if !strings.Contains(string(e.Data), `"user":"admin@demo"`) || !strings.Contains(string(e.Data), "request") {
				t.Fatalf("record change not attributable: %s", e.Data)
			}
		}
	}
	if changes != 2 {
		t.Fatalf("each step must be audited with its before/after state, got %d", changes)
	}
}

func TestPlannerGetsFeedbackForInventedToolsAndFields(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{
		`{"action":"plan","steps":[{"id":"s1","tool":"parties.magic_create","body":{"name":"X"}}]}`,
		`{"action":"plan","steps":[{"id":"s1","tool":"parties.create","body":{"name":"X","favourite_color":"blue"}}]}`,
		`{"action":"plan","summary":"create X","steps":[{"id":"s1","tool":"parties.create","body":{"name":"X","kind":"company","is_customer":true}}]}`,
	}
	r, _ := talk(t, h, "", "create customer X")
	if r.Last("approval") == nil {
		t.Fatalf("valid plan expected after feedback: %+v", r.Events)
	}
	p := h.ai.planPrompts
	if len(p) != 3 || !strings.Contains(p[1], "unknown tool parties.magic_create") || !strings.Contains(p[2], `unknown field "favourite_color"`) {
		t.Fatalf("validation feedback missing:\n%s", strings.Join(p, "\n-----\n"))
	}
}

func TestRestrictedOperationsAreNeverExecuted(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{
		`{"action":"plan","steps":[{"id":"s1","tool":"users.create","body":{"email":"x@y","full_name":"X"}}]}`,
		`{"action":"answer","text":"I can't manage users; an administrator does that in Settings → Users."}`,
	}
	r, _ := talk(t, h, "", "make a new admin user for Ali")
	if r.Last("approval") != nil || !strings.Contains(h.ai.planPrompts[1], "NOT_SAFE_TO_AUTOMATE") || r.Last("message") == nil {
		t.Fatalf("user management must be refused: %+v", r.Events)
	}
	// and the registry itself refuses even a forged call
	if _, err := h.a.Tools.Call(context.Background(), h.a.env(h.user), "users.create", json.RawMessage(`{"body":{}}`), &tools.Authorization{}); err == nil {
		t.Fatal("restricted operation reachable through the registry")
	}
}

func TestPlannerRespectsTheUsersPermissions(t *testing.T) {
	h := newHarness(t, "viewer")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{`{"action":"plan","steps":[{"id":"s1","tool":"parties.create","body":{"name":"X","kind":"company","is_customer":true}}]}`}
	r, _ := talk(t, h, "", "create customer X")
	if r.Last("approval") != nil || h.acc.posts != 0 {
		t.Fatalf("viewer must not be able to create parties: %+v", r.Events)
	}
	if !strings.Contains(fmt.Sprint(r.Last("message")), "parties.create") {
		t.Fatalf("plain explanation expected: %v", r.Last("message"))
	}
	// the catalog the viewer's planner sees does not even contain the write tools
	if strings.Contains(h.a.catalogText(h.user), "parties.create") {
		t.Fatal("catalog must be filtered by the user's permissions")
	}
}

func TestPlanFailureKeepsCompletedStepsAndResumes(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	bad := strings.Replace(iranicardPlan, `"amount":"50000000"`, `"amount":"13"`, 1)
	fixed := `{"action":"plan","summary":"retry the payment","steps":[{"id":"s3","tool":"treasury.transactions.create","body":{"kind":"payment","date":"2025-10-07","treasury_account_id":2,"party_id":"$s1.id","amount":"50000000","auto_allocate":true},"purpose":"record the payment"}]}`
	h.ai.planQueue = []string{bad, fixed}
	r1, conv := talk(t, h, "", "create Iranicard and pay it 13 rial")
	ap := r1.Last("approval").(map[string]any)
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	msg := fmt.Sprint(rec.Last("error"))
	if !strings.Contains(msg, "s2") || !strings.Contains(msg, "create counterparty") {
		t.Fatalf("failure must name the step and what was already done: %s", msg)
	}
	created := len(h.acc.parties)
	// follow-up: the planner keeps the completed step and only re-plans what is left
	r2, _ := talk(t, h, conv, "use 50 million rial from Mellat instead")
	if r2.Last("approval") == nil {
		t.Fatalf("resume plan expected: %+v", r2.Events)
	}
	last := h.ai.planPrompts[len(h.ai.planPrompts)-1]
	if !strings.Contains(last, "step s1 [done] parties.create") || strings.Contains(last, "step s2 [failed]") {
		t.Fatalf("completed steps must be kept, failed pending ones re-planned:\n%s", last)
	}
	ap2 := r2.Last("approval").(map[string]any)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap2["task_id"].(string), ApproveRequest{Token: ap2["token"].(string)}, rec)
	if rec.Last("result") == nil || len(h.acc.parties) != created || len(h.acc.txs) != 1 {
		t.Fatalf("resume must not recreate the counterparty: parties %d→%d txs %d events %+v", created, len(h.acc.parties), len(h.acc.txs), rec.Events)
	}
}

func TestPlanApprovalIsBoundToTheExactSteps(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	other := strings.Replace(iranicardPlan, `"amount":"50000000"`, `"amount":"90000000"`, 1)
	h.ai.planQueue = []string{iranicardPlan, other}
	r1, conv := talk(t, h, "", "create Iranicard and pay it 5 million toman")
	ap1 := r1.Last("approval").(map[string]any)
	talk(t, h, conv, "actually make it 9 million toman") // re-planned → different payload
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap1["task_id"].(string), ApproveRequest{Token: ap1["token"].(string)}, rec)
	if rec.Last("error") == nil || h.acc.posts != 0 {
		t.Fatal("an approval for the old plan must not execute the changed plan")
	}
}

func TestReadOnlyQuestionsAreAnsweredFromTheSoftware(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"query","query":{"kind":"other"}}`
	h.ai.planQueue = []string{`{"action":"read","calls":[{"tool":"parties.list","query":{"q":"Company X"}}]}`,
		`{"action":"answer","text":"Company X is a customer (code 0002)."}`}
	r, _ := talk(t, h, "", "what do we know about Company X?")
	if fmt.Sprint(r.Last("message")) == "<nil>" || r.Last("approval") != nil || h.acc.posts != 0 {
		t.Fatalf("read-only work runs without approval and changes nothing: %+v", r.Events)
	}
}

func TestResolveRefs(t *testing.T) {
	p := &PlanState{Steps: []PlanStep{{ID: "s1", Status: "done", Result: json.RawMessage(`{"id":201,"items":[{"id":7}]}`)}}}
	out, err := resolveRefs(p, tools.OpArgs{Path: map[string]string{"id": "$s1.id"}, Body: json.RawMessage(`{"party_id":"$s1.id","line":"$s1.items.0.id","note":"for $s1.id"}`)})
	if err != nil || out.Path["id"] != "201" || string(out.Body) != `{"line":7,"note":"for 201","party_id":201}` {
		t.Fatalf("%v %v %s", err, out.Path, out.Body)
	}
	if _, err := resolveRefs(p, tools.OpArgs{Body: json.RawMessage(`{"x":"$s9.id"}`)}); err == nil {
		t.Fatal("unknown step reference must fail")
	}
}

// The application rejects a value: the planner gets the exact error, corrects the plan and asks again.
func TestPlannerRepairsAPlanTheAppRejected(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	bad := strings.Replace(iranicardPlan, `"kind":"company"`, `"kind":"supplier"`, 1)
	h.ai.planQueue = []string{bad, iranicardPlan}
	r1, _ := talk(t, h, "", "create Iranicard and pay it 5 million toman")
	ap := r1.Last("approval").(map[string]any)
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	ap2, ok := rec.Last("approval").(map[string]any)
	if !ok || rec.Last("message") == nil || len(h.acc.parties) != 2 {
		t.Fatalf("expected a corrected plan awaiting confirmation and nothing created: %+v", rec.Events)
	}
	if fb := h.ai.planPrompts[len(h.ai.planPrompts)-1]; !strings.Contains(fb, `"kind":"invalid"`) {
		t.Fatalf("the application's error must be given to the planner:\n%s", fb)
	}
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap2["task_id"].(string), ApproveRequest{Token: ap2["token"].(string)}, rec)
	if rec.Last("result") == nil || len(h.acc.txs) != 1 {
		t.Fatalf("corrected plan must complete: %+v", rec.Events)
	}
}

// Regression: a plan without entities is saved and reloaded; the follow-up must not crash.
func TestPlanFollowUpAfterReloadWithoutEntities(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{`{"action":"ask","question":"Which transaction?"}`, `{"action":"answer","text":"No matching deposit exists."}`}
	_, conv := talk(t, h, "", "match the deposit with its invoice")
	r2, _ := talk(t, h, conv, "the one from Bank Mellat")
	if r2.Last("message") == nil || r2.Last("error") != nil {
		t.Fatalf("follow-up after reload failed: %+v", r2.Events)
	}
}

// A model slip (150,000,000,000 for "15 million toman") never reaches the approval card.
func TestAmountGuardCatchesWrongZeros(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	wrong := `{"action":"plan","summary":"pay","steps":[{"id":"s1","tool":"treasury.transactions.create","body":{"kind":"payment","date":"2025-10-07","treasury_account_id":2,"party_id":101,"amount":"150000000000"},"purpose":"pay"}]}`
	right := strings.Replace(wrong, "150000000000", "150000000", 1)
	h.ai.planQueue = []string{wrong, right}
	r, _ := talk(t, h, "", "پرداخت ۱۵ میلیون تومانی به Company X را از بانک ملت ثبت کن")
	if r.Last("approval") == nil {
		t.Fatalf("corrected plan expected: %+v", r.Events)
	}
	fb := h.ai.planPrompts[1]
	if !strings.Contains(fb, "AMOUNT CHECK FAILED: s1.amount=150000000000") || !strings.Contains(fb, "150000000") {
		t.Fatalf("guard feedback missing:\n%s", fb)
	}
	steps := r.Last("preview").(map[string]any)["steps"].([]map[string]any)
	if am := steps[0]["amounts"].([]string); len(am) != 1 || am[0] != "amount: 150,000,000" {
		t.Fatalf("card must show the amount formatted: %v", am)
	}
	if !strings.Contains(string(steps[0]["args"].(json.RawMessage)), `"amount":"150000000"`) {
		t.Fatalf("approved plan must carry the stated amount: %s", steps[0]["args"])
	}
	// amounts read from the application (e.g. an invoice's outstanding) are allowed
	p := &PlanState{UserTexts: []string{"pay the open invoice"}, Reads: []ReadRecord{{Raw: json.RawMessage(`{"items":[{"outstanding":"12345000"}]}`)}}}
	if fb := checkAmounts(&State{}, p, []PlanStep{{ID: "s1", Args: tools.OpArgs{Body: json.RawMessage(`{"amount":"12345000"}`)}}}, true); fb != "" {
		t.Fatalf("read amount rejected: %s", fb)
	}
	// unit price derived from a stated total and quantity is allowed
	p = &PlanState{UserTexts: []string{"10 units for 100 million rial in total"}}
	if fb := checkAmounts(&State{}, p, []PlanStep{{ID: "s1", Args: tools.OpArgs{Body: json.RawMessage(`{"items":[{"quantity":"10","unit_price":"10000000"}]}`)}}}, true); fb != "" {
		t.Fatalf("derived unit price rejected: %s", fb)
	}
}

// Smaller models put lookups into plans; they run as reads (no approval) and the model plans only the changes.
func TestLookupsInAPlanRunAsReads(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"query","query":{"kind":"other"}}`
	h.ai.planQueue = []string{`{"action":"plan","summary":"look up","steps":[{"id":"s1","tool":"parties.list","query":{"q":"Company X"}},{"id":"s2","tool":"parties.list","query":{"q":"$s1.items.0.name"}}]}`,
		`{"action":"answer","text":"Company X is a customer."}`}
	r, _ := talk(t, h, "", "who is Company X?")
	if r.Last("approval") != nil || r.Last("message") == nil {
		t.Fatalf("a lookup-only plan must not need approval: %+v", r.Events)
	}
	fb := h.ai.planPrompts[1]
	if !strings.Contains(fb, "Read tools are not plan steps") || !strings.Contains(fb, `parties.list {"query":{"q":"Company X"}}`) {
		t.Fatalf("lookup must be run and fed back:\n%s", fb)
	}
	if strings.Contains(fb, "$s1.items.0.name\"}} →") {
		t.Fatal("a lookup depending on another lookup must not run with an unresolved reference")
	}
}
