package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// the payment on the attached bank receipt (loanExtraction: 250,000,000 IRR)
var receiptPlan = strings.Replace(iranicardPlan, `"amount":"50000000"`, `"amount":"250000000"`, 1)

// A file sent with a multi-step request is attached to the document the plan registers —
// the payment, not the counterparty created on the way — and is verified on the record.
func TestPlanAttachesTheOriginalFileToTheRegisteredRecord(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.extraction = loanExtraction
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{receiptPlan}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "create Iranicard and register this payment", Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	prev, _ := rec.Last("preview").(map[string]any)
	if prev == nil || !strings.Contains(fmt.Sprintf("%+v", prev["display"]), "receipt.png") {
		t.Fatalf("the plan preview must say which file will be attached: %+v", prev)
	}
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	res, ok := rec.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", rec.Events)
	}
	if len(h.acc.txs) != 1 {
		t.Fatalf("payment not registered: %v", h.acc.txs)
	}
	txKey := "treasury_transaction:" + fmt.Sprint(h.acc.txs[0]["id"])
	if got := h.acc.attachments[txKey]; len(got) != 1 || got[0] != "receipt.png" {
		t.Fatalf("original file not attached to the payment: %v", h.acc.attachments)
	}
	for k := range h.acc.attachments {
		if strings.HasPrefix(k, "party:") {
			t.Fatalf("the file belongs to the payment, not the new counterparty: %v", h.acc.attachments)
		}
	}
	r := res["result"].(*Result)
	if len(r.Files) != 1 || r.Files[0].ID == 0 || !strings.HasPrefix(r.Files[0].URL, "/api/v1/attachments/") || r.Files[0].EntityType != "treasury_transaction" {
		t.Fatalf("the result must link the stored file: %+v", r.Files)
	}
	if !strings.Contains(fmt.Sprint(res["summary"]), "receipt.png") {
		t.Fatalf("summary must mention the attachment: %v", res["summary"])
	}
	// approving again (a retry) never attaches the same file twice
	task, st, _ := h.a.load(context.Background(), h.user, ap["task_id"].(string))
	h.a.attachUploads(context.Background(), h.user, task, &State{Lang: "fa", Uploads: st.Uploads,
		Result: &Result{EntityType: "treasury_transaction", EntityID: r.Files[0].EntityID}}, &Recorder{})
	if got := h.acc.attachments[txKey]; len(got) != 1 {
		t.Fatalf("attached twice: %v", got)
	}
}

// A document sent on its own registers nothing; "register it" in the next message
// registers it AND attaches the same original file.
func TestBareUploadIsAttachedByTheNextRequest(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.extraction = loanExtraction
	h.ai.queue = []string{`{"intent":"chat"}`, `{"intent":"operate","user_requested_registration":true}`}
	h.ai.planQueue = []string{receiptPlan}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	conv := rec.Last("conversation").(map[string]any)["conversation_id"].(string)
	if h.acc.posts != 0 {
		t.Fatal("a bare document must not be registered")
	}
	r2, _ := talk(t, h, conv, "ثبتش کن")
	ap := approvalOf(t, r2)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	if len(h.acc.txs) != 1 {
		t.Fatalf("not registered: %+v", rec.Events)
	}
	if got := h.acc.attachments["treasury_transaction:"+fmt.Sprint(h.acc.txs[0]["id"])]; len(got) != 1 {
		t.Fatalf("the earlier file must be attached to what the follow-up registered: %v", h.acc.attachments)
	}
	// linked once: a later, unrelated request does not inherit it again
	c, _ := h.a.openConversation(context.Background(), h.user, conv)
	if c.state.Pending != nil {
		t.Fatalf("pending files must be cleared once used: %+v", c.state.Pending)
	}
}

func TestAttachmentNameGetsTheRealExtension(t *testing.T) {
	cases := map[string]string{"blob": "blob.png", "receipt.png": "receipt.png", "scan.PNG": "scan.PNG", "photo.jpeg": "photo.jpeg.png"}
	for in, want := range cases {
		if got := tools.AttachmentName(in, pngBytes); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
	if got := tools.AttachmentName("x", []byte("%PDF-1.4\n")); got != "x.pdf" {
		t.Errorf("pdf: %s", got)
	}
}

func TestEveryAttachableRecordTypeMapsToTheApp(t *testing.T) {
	cases := map[string]Artifact{
		"invoice": {Type: "purchase_invoice", Side: "purchase", Kind: "invoice"}, "treasury_transaction": {Type: "treasury_receipt"},
		"expense": {Type: "expense"}, "journal_entry": {Type: "journal_entry"}, "party": {Type: "party"}, "stock_document": {Type: "stock_document"},
		"check": {Type: "checks"}, "employee": {Type: "employees"}, "payroll_period": {Type: "payroll_periods"}, "budget": {Type: "budgets"},
	}
	for want, a := range cases {
		if got := attachEntity(a); got != want || !tools.AttachEntities[got] {
			t.Errorf("%s → %q, want %q", a.Type, got, want)
		}
	}
	if attachEntity(Artifact{Type: "products"}) != "" {
		t.Error("products take no attachments in the app")
	}
	steps := []PlanStep{{Status: "done", Artifact: &Artifact{Type: "party", ID: 1}}, {Status: "done", Artifact: &Artifact{Type: "expense", ID: 9}},
		{Status: "done", Artifact: &Artifact{Type: "treasury_payment", ID: 4}}}
	if r := referenceRecord(steps); r == nil || r.ID != 9 {
		t.Fatalf("the first registered document is the reference record: %+v", r)
	}
	if r := referenceRecord(steps[:1]); r == nil || r.Type != "party" {
		t.Fatalf("a master record is used when nothing else was registered: %+v", r)
	}
}
