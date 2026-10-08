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
		t.Fatalf("expected a plan waiting for approval: %+v\nPROMPTS:%s", r1.Events, strings.Join(h.ai.planPrompts, "\n----\n"))
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

// Parity: security operations (here: creating a user) run like any other operation the user may do,
// with human approval and a critical warning. Passwords never pass through the model, the plan state,
// the audit log or the transcript: the system generates one and shows it once.
func TestSecurityOperationsRunWithApprovalAndAGeneratedPassword(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{
		`{"action":"plan","steps":[{"id":"s1","tool":"users.create","body":{"email":"ali@demo.test","full_name":"Ali","role_id":2,"password":"hunter2hunter2"}}]}`,
		`{"action":"plan","summary":"create user Ali","steps":[{"id":"s1","tool":"users.create","body":{"email":"ali@demo.test","full_name":"Ali","role_id":2,"password":"$generate"},"purpose":"create user Ali"}]}`,
	}
	r, _ := talk(t, h, "", "make a new user for Ali")
	if !strings.Contains(h.ai.planPrompts[1], "never write a password") {
		t.Fatalf("a model-written password must be refused:\n%s", h.ai.planPrompts[1])
	}
	ap, ok := r.Last("approval").(map[string]any)
	if !ok {
		t.Fatalf("user creation must be offered for approval, not refused: %+v", r.Events)
	}
	steps := r.Last("preview").(map[string]any)["steps"].([]map[string]any)
	if steps[0]["critical"] != true || steps[0]["classification"] != "REQUIRES_APPROVAL" {
		t.Fatalf("security operations need approval with a critical warning: %+v", steps[0])
	}
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	if rec.Last("result") == nil || len(h.acc.users) != 1 {
		t.Fatalf("user not created: %+v", rec.Events)
	}
	pw, _ := h.acc.users[0]["password"].(string)
	if len(pw) < 16 || pw == "$generate" {
		t.Fatalf("a strong password must be generated, got %q", pw)
	}
	sec, _ := rec.Last("secret").(map[string]any)
	if sec == nil || fmt.Sprint(sec["values"]) != fmt.Sprint(map[string]string{"password": pw}) {
		t.Fatalf("the generated password must be shown once to the user: %+v", rec.Events)
	}
	// nowhere else: not in the stored task/conversation, the audit log or anything the model sees
	task, _ := h.st.GetTask(context.Background(), h.user.Scope, ap["task_id"].(string))
	audit, _ := h.st.ListAudit(context.Background(), h.user.Scope, ap["task_id"].(string), 500)
	ab, _ := json.Marshal(audit)
	convs, _ := json.Marshal(task)
	for name, blob := range map[string]string{"task": string(convs), "audit": string(ab), "prompts": strings.Join(h.ai.planPrompts, "")} {
		if strings.Contains(blob, pw) {
			t.Fatalf("generated password leaked into %s", name)
		}
	}
	// a user without the permission cannot (same rule as the app)
	v := newHarness(t, "viewer")
	v.ai.interp = h.ai.interp
	v.ai.planQueue = []string{`{"action":"plan","steps":[{"id":"s1","tool":"users.create","body":{"email":"x@y","full_name":"X","password":"$generate"}}]}`}
	rv, _ := talk(t, v, "", "make a new user")
	if rv.Last("approval") != nil || len(v.acc.users) != 0 {
		t.Fatal("a viewer must not be able to create users through the assistant")
	}
}

// Session/credential operations are the only exclusions; the registry refuses them even when forged.
func TestSessionOperationsAreNotBusinessOperations(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{`{"action":"plan","steps":[{"id":"s1","tool":"auth.logout.create","body":{}}]}`,
		`{"action":"answer","text":"You are signed in; signing out is done from your profile menu."}`}
	r, _ := talk(t, h, "", "log me out")
	if r.Last("approval") != nil || !strings.Contains(h.ai.planPrompts[1], "session/credential operation") {
		t.Fatalf("sign-out must not be planned: %+v", r.Events)
	}
	if _, err := h.a.Tools.Call(context.Background(), h.a.env(h.user), "auth.login.create", json.RawMessage(`{"body":{}}`), &tools.Authorization{}); err == nil {
		t.Fatal("session operation reachable through the registry")
	}
}

// Upload endpoints receive the file the user attached in the conversation.
func TestUploadOperationsUseTheUsersAttachedFile(t *testing.T) {
	h := newHarness(t, "admin")
	id := h.upload(t)
	args := json.RawMessage(`{"body":{"entity_type":"treasury_transaction","entity_id":"7","upload_id":"` + id + `"}}`)
	auth := &tools.Authorization{PayloadHash: tools.PayloadHash("attachments.create", args), Mode: "human"}
	if _, err := h.a.Tools.Call(context.Background(), h.a.env(h.user), "attachments.create", args, auth); err != nil {
		t.Fatal(err)
	}
	if got := h.acc.attachments["treasury_transaction:7"]; len(got) != 1 || got[0] != "receipt.png" {
		t.Fatalf("file not sent to the app: %v", h.acc.attachments)
	}
	bad := json.RawMessage(`{"body":{"entity_type":"treasury_transaction","entity_id":"7","upload_id":"not-a-real-upload"}}`)
	if _, err := h.a.Tools.Call(context.Background(), h.a.env(h.user), "attachments.create", bad, &tools.Authorization{PayloadHash: tools.PayloadHash("attachments.create", bad)}); err == nil {
		t.Fatal("an unknown upload id must be refused")
	}
}

// A request the fixed flows cannot finish (here a transfer) goes to the planner instead of "please do it in the app".
func TestFixedFlowDeadEndsAreHandedToThePlanner(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{`{"intent":"register","user_requested_registration":true,"event":{"type":"transfer","amount":"10000000","currency":"IRT","date":"2025-10-07"}}`}
	h.ai.planQueue = []string{`{"action":"plan","summary":"transfer","steps":[{"id":"s1","tool":"treasury.transactions.create","body":{"kind":"transfer","date":"2025-10-07","treasury_account_id":1,"to_treasury_account_id":2,"amount":"100000000"},"purpose":"transfer cash to bank"}]}`}
	r, _ := talk(t, h, "", "۱۰ میلیون تومان از صندوق به بانک منتقل کن")
	if r.Last("approval") == nil {
		t.Fatalf("transfer must be planned: %+v", r.Events)
	}
	for _, ev := range r.Events {
		if strings.Contains(fmt.Sprint(ev.Data), "Treasury →") || strings.Contains(fmt.Sprint(ev.Data), "پشتیبانی نمی") {
			t.Fatalf("must not send the user to the app: %+v", ev)
		}
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
