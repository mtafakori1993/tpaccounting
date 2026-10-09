package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
)

// «یه مشتری به اسم شرکت قاسم ایجاد کن شماره تماسش ۰۹۳۵۷۵۷۸۸۰۸» (D40).
const ghasemText = "یه مشتری به اسم شرکت قاسم ایجاد کن شماره تماسش ۰۹۳۵۷۵۷۸۸۰۸"
const ghasemPlan = `{"action":"plan","summary":"Create the customer شرکت قاسم.",
 "steps":[{"id":"s1","tool":"parties.create","body":{"name":"شرکت قاسم","kind":"company","is_customer":true,"mobile":"09357578808"},"purpose":"ایجاد مشتری شرکت قاسم"}]}`

func ghasem(h *harness) {
	h.ai.queue = []string{`{"intent":"operate","user_requested_registration":true}`}
	h.ai.planQueue = []string{ghasemPlan}
}

func partiesNamed(h *harness, name string) int {
	h.acc.mu.Lock()
	defer h.acc.mu.Unlock()
	n := 0
	for _, p := range h.acc.parties {
		if p["name"] == name {
			n++
		}
	}
	return n
}

// Direct: a routine customer creation the user asked for runs without a confirmation card.
func TestCustomerCreationRunsDirectly(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	r, conv := talk(t, h, "", ghasemText)
	if r.Last("approval") != nil {
		t.Fatalf("a routine customer creation must not wait for a confirmation card: %+v", r.Events)
	}
	res, ok := r.Last("result").(map[string]any)
	if !ok || !res["result"].(*Result).Verified || partiesNamed(h, "شرکت قاسم") != 1 {
		t.Fatalf("customer not created: %+v", r.Events)
	}
	prev := r.Last("preview").(map[string]any)
	d := prev["decision"]
	step := prev["steps"].([]map[string]any)[0]
	if fmt.Sprint(d) == "" || !strings.Contains(fmt.Sprint(d), "DIRECT") || step["operation"] != "create_customer" {
		t.Fatalf("decision %+v step %+v", d, step)
	}
	if s := fmt.Sprint(res["summary"]); strings.Contains(s, "POST") || strings.Contains(s, "/parties") {
		t.Fatalf("endpoint shown to the user: %s", s)
	}
	// the created customer is the active document
	view, _ := h.a.ConversationView(context.Background(), h.user, conv)
	act, _ := view["active"].(*Artifact)
	if act == nil || act.Type != "party" || act.Label != "شرکت قاسم" || act.ID == 0 {
		t.Fatalf("active artifact %+v", view["active"])
	}
	// audited as the agent's act on the user's behalf, with before/after
	all, _ := h.st.ListAudit(context.Background(), h.user.Scope, r.Last("done").(map[string]any)["task_id"].(string), 200)
	found := false
	for _, e := range all {
		if e.Kind == "record_change" {
			found = true
			for _, want := range []string{`"actor":"tafakor_ai"`, `"mode":"auto"`, `"user":"admin@demo"`, `"tool":"parties.create"`, `"after"`} {
				if !strings.Contains(string(e.Data), want) {
					t.Fatalf("audit lacks %s: %s", want, e.Data)
				}
			}
		}
	}
	if !found {
		t.Fatal("record change not audited")
	}
}

// UI preview requested in the message: the real party form opens pre-filled; nothing is
// submitted and the action stays pending.
func TestCustomerShowOnScreenOpensTheRealFormPrefilled(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	r, _ := talk(t, h, "", ghasemText+" روی صفحه نشانم بده")
	plan, ok := r.Last("ui_plan").(UIPlan)
	if !ok {
		t.Fatalf("no form opened: %+v", r.Events)
	}
	if plan.Mode != "preview" || h.acc.posts != 0 {
		t.Fatalf("preview must not submit: mode=%s posts=%d", plan.Mode, h.acc.posts)
	}
	got := map[string]string{}
	for _, s := range plan.Steps {
		if s.Op == "submit" || s.Op == "wait_closed" {
			t.Fatalf("preview contains a submit step: %+v", plan.Steps)
		}
		got[s.Op+":"+s.Target] = s.Value
	}
	if got["navigate:"] != "/parties" {
		t.Fatalf("form not opened: %+v", plan.Steps)
	}
	if _, ok := got["click:parties.newParty"]; !ok {
		t.Fatalf("New party control not used: %+v", plan.Steps)
	}
	if got["fill:common.name"] != "شرکت قاسم" || got["fill:parties.mobile"] != "09357578808" || got["check:parties.customer"] != "true" {
		t.Fatalf("form not pre-filled: %+v", got)
	}
	_, st, _ := h.a.load(context.Background(), h.user, plan.TaskID)
	if st.Stage != StageUIPreview || len(st.Plan.Pending()) != 1 {
		t.Fatalf("pending action not kept: stage=%s", st.Stage)
	}
}

// Preview → «تأیید و ثبت»: the SAME pending action is executed once, without re-planning.
func TestCustomerPreviewThenConfirmRegistersTheSameAction(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	ctx := context.Background()
	r, conv := talk(t, h, "", ghasemText+" روی صفحه نشانم بده")
	plan := r.Last("ui_plan").(UIPlan)
	h.a.UIResult(ctx, h.user, plan.TaskID, UIResult{OK: true, Preview: true}, &Recorder{})
	rounds := len(h.ai.planPrompts)
	r2, _ := talk(t, h, conv, "تأیید و ثبت")
	if len(h.ai.planPrompts) != rounds {
		t.Fatal("the confirmed action was re-planned instead of executed")
	}
	if r2.Last("result") == nil || partiesNamed(h, "شرکت قاسم") != 1 || h.acc.posts != 1 {
		t.Fatalf("expected exactly one customer: posts=%d events=%+v", h.acc.posts, r2.Events)
	}
	var created map[string]any
	for _, p := range h.acc.parties {
		if p["name"] == "شرکت قاسم" {
			created = p
		}
	}
	if created["mobile"] != "09357578808" || created["is_customer"] != true {
		t.Fatalf("not the previewed payload: %+v", created)
	}
	// a second confirmation cannot create it again
	talk(t, h, conv, "تأیید و ثبت")
	if partiesNamed(h, "شرکت قاسم") != 1 {
		t.Fatal("duplicate created by a repeated confirmation")
	}
}

// The approval card's "show me on screen" button opens the same real form.
func TestCustomerPreviewButtonOpensTheForm(t *testing.T) {
	h := newHarness(t, "admin")
	ghasem(h)
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: ghasemText, ExecMode: "api", AskThisTime: true}, rec)
	ap := approvalOf(t, rec)
	if ap["can_preview"] != true {
		t.Fatalf("preview not offered for a form that exists: %+v", ap)
	}
	rec2 := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string), ExecMode: "ui", Preview: true}, rec2)
	if _, ok := rec2.Last("ui_plan").(UIPlan); !ok || h.acc.posts != 0 {
		t.Fatalf("form not opened: %+v", rec2.Events)
	}
}

// Duplicate: an existing customer with the same phone (stored as +98…) is found and no
// second party is created.
func TestCustomerDuplicateByNormalizedPhoneIsNotCreated(t *testing.T) {
	h := newHarness(t, "admin")
	h.acc.mu.Lock()
	h.acc.parties = append(h.acc.parties, map[string]any{"id": 150, "code": "0150", "name": "قاسم و شرکا", "kind": "company", "is_customer": true, "is_active": true, "mobile": "+989357578808"})
	h.acc.mu.Unlock()
	ghasem(h)
	r, _ := talk(t, h, "", ghasemText)
	if h.acc.posts != 0 || partiesNamed(h, "شرکت قاسم") != 0 {
		t.Fatal("a duplicate customer was created")
	}
	msg, _ := r.Last("message").(map[string]any)
	if !strings.Contains(fmt.Sprint(msg["text"]), "قاسم و شرکا") {
		t.Fatalf("existing customer not reported: %+v", r.Events)
	}
	if ap := approvalOf(t, r); ap["duplicate"] != true {
		t.Fatalf("duplicate not flagged: %+v", ap)
	}
}

// Permission: a user who may not create parties is refused, as in the app.
func TestCustomerCreationRespectsPermission(t *testing.T) {
	h := newHarness(t, "viewer")
	ghasem(h)
	r, _ := talk(t, h, "", ghasemText)
	if h.acc.posts != 0 || r.Last("result") != nil || r.Last("approval") != nil {
		t.Fatalf("viewer created a party: %+v", r.Events)
	}
}

func TestDetectConfirmAndPhoneKey(t *testing.T) {
	for text, want := range map[string]bool{"تأیید و ثبت": true, "ثبتش کن": true, "مستقیم ثبت کن": true, "confirm": true,
		"ثبت نکن": false, "تایید نمی‌کنم": false, "ثبت کن ولی شماره رو ۰۹۱۲ کن": false, "سلام": false} {
		if got := classify.DetectConfirm(text); got != want {
			t.Errorf("DetectConfirm(%q) = %v", text, got)
		}
	}
	for _, p := range []string{"۰۹۳۵۷۵۷۸۸۰۸", "09357578808", "+989357578808", "0098 935-757-8808", "(0935) 757 8808"} {
		if k := phoneKey(p); k != "9357578808" {
			t.Errorf("phoneKey(%q) = %q", p, k)
		}
	}
}
