package agent

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
)

var latin = regexp.MustCompile(`[A-Za-z]{2,}`)

func persianUser(h *harness) { h.user.Me.Language = "fa" }

// ---------- Persian interface → Persian record text (D45) ----------

const medicinePlanEN = `{"action":"plan","summary":"Record the payment for medicine to Iranicard.",
 "steps":[{"id":"s1","tool":"treasury.transactions.create","body":{"kind":"payment","date":"2025-10-07","treasury_account_id":2,"party_id":101,"amount":"50000000","description":"payment for medicine"},"purpose":"payment for medicine"}]}`

func TestPersianInterfacePlannerIsToldToWritePersian(t *testing.T) {
	h := newHarness(t, "admin")
	persianUser(h)
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	fixed := strings.NewReplacer("Record the payment for medicine to Iranicard.", "ثبت پرداخت بابت دارو", "payment for medicine", "پرداخت بابت دارو").Replace(medicinePlanEN)
	h.ai.planQueue = []string{medicinePlanEN, fixed}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "پرداخت ۵ میلیون تومان به Company X بابت دارو ثبت کن", ExecMode: "api"}, rec)
	if len(h.ai.planPrompts) != 2 || !strings.Contains(h.ai.planPrompts[1], "LANGUAGE CHECK FAILED") || !strings.Contains(h.ai.planPrompts[1], "payment") {
		t.Fatalf("the planner must be told to rewrite the English text: %d prompts", len(h.ai.planPrompts))
	}
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	if len(h.acc.txs) != 1 || h.acc.txs[0]["description"] != "پرداخت بابت دارو" {
		t.Fatalf("description: %v", h.acc.txs)
	}
}

func TestPersianInterfaceEnglishIsTranslatedWhenThePlannerInsists(t *testing.T) {
	h := newHarness(t, "admin")
	persianUser(h)
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{medicinePlanEN, medicinePlanEN}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "پرداخت ۵ میلیون تومان به Company X بابت دارو ثبت کن", ExecMode: "api"}, rec)
	prev, _ := rec.Last("preview").(map[string]any)
	if prev == nil {
		t.Fatalf("no plan: %+v", rec.Events)
	}
	d := fmt.Sprintf("%+v", prev["display"])
	if latin.MatchString(strings.ReplaceAll(d, "Iranicard", "")) && strings.Contains(d, "medicine") {
		t.Fatalf("summary still English: %s", d)
	}
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	desc := fmt.Sprint(h.acc.txs[0]["description"])
	if latin.MatchString(desc) || !strings.Contains(desc, "دارو") || !strings.Contains(desc, "پرداخت") {
		t.Fatalf("description must be Persian: %q", desc)
	}
}

// The built-in registration path: an English purpose from the model becomes Persian.
func TestPersianInterfaceBuiltinDescriptionIsPersian(t *testing.T) {
	h := newHarness(t, "admin")
	persianUser(h)
	h.ai.extraction = loanExtraction
	h.ai.interp = `{"intent":"register","user_requested_registration":true,"confidence":0.9,
"event":{"type":"loan_received","counterparty":"محمد تفکری","purpose":"loan for company expenses"}}`
	rec := &Recorder{}
	id := h.a.Start(context.Background(), h.user, StartRequest{Message: "این وام را از محمد تفکری برای هزینه‌های شرکت گرفتیم", Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	rec = &Recorder{}
	h.a.Answer(context.Background(), h.user, id, AnswerRequest{QuestionID: "counter_account", Value: "500"}, rec)
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	if len(h.acc.txs) != 1 {
		t.Fatalf("not registered: %+v", rec.Events)
	}
	if desc := fmt.Sprint(h.acc.txs[0]["description"]); latin.MatchString(desc) || !strings.Contains(desc, "وام") {
		t.Fatalf("English in a Persian record: %q", desc)
	}
	// and the receipt image is on the record
	if got := h.acc.attachments["treasury_transaction:"+fmt.Sprint(h.acc.txs[0]["id"])]; len(got) != 1 {
		t.Fatalf("source image not attached: %v", h.acc.attachments)
	}
}

// ---------- PDF → record → original file attached and downloadable ----------

var pdfBytes = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")

func TestPDFSourceIsAttachedAndDownloadable(t *testing.T) {
	h := newHarness(t, "admin")
	id, err := h.a.Files.Save(context.Background(), h.user.Scope.CompanyID, h.user.Scope.UserID, "scan", "application/pdf", pdfBytes)
	if err != nil {
		t.Fatal(err)
	}
	h.ai.extraction = loanExtraction
	h.ai.interp = `{"intent":"operate","user_requested_registration":true}`
	h.ai.planQueue = []string{receiptPlan}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "create Iranicard and register this payment", Uploads: []string{id}, ExecMode: "api"}, rec)
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	r := rec.Last("result").(map[string]any)["result"].(*Result)
	if len(r.Files) != 1 || r.Files[0].Name != "scan.pdf" {
		t.Fatalf("a nameless PDF gets its extension and is reported: %+v", r.Files)
	}
	// the link is the application's own download, and it serves the original bytes
	got, ctype, err := h.a.Acc.Raw(context.Background(), h.user.Cred, accounting.Call{Method: http.MethodGet,
		Path: strings.TrimPrefix(r.Files[0].URL, "/api/v1")})
	if err != nil || !bytes.Equal(got, pdfBytes) || !strings.HasPrefix(ctype, "application/pdf") {
		t.Fatalf("download: err=%v type=%s same=%v", err, ctype, bytes.Equal(got, pdfBytes))
	}
}

// ---------- HUMAN_UI preview saved by the user (D46) ----------

func ghasemPreviewWithFile(t *testing.T, h *harness) (UIPlan, string) {
	t.Helper()
	ghasem(h)
	h.ai.extraction = `{"document_kind":"business_card"}`
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: ghasemText + " روی صفحه نشانم بده", Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	plan, ok := rec.Last("ui_plan").(UIPlan)
	if !ok || plan.Mode != "preview" {
		t.Fatalf("no preview form: %+v", rec.Events)
	}
	if len(plan.Watch) == 0 || plan.Watch[0].Op != "submit" {
		t.Fatalf("the widget must be told which save to watch: %+v", plan.Watch)
	}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true}, &Recorder{})
	conv := rec.Last("conversation").(map[string]any)["conversation_id"].(string)
	return plan, conv
}

// the user presses Save on the real form: the app creates the record as the user
func userSavesParty(t *testing.T, h *harness, name string) int64 {
	t.Helper()
	var out map[string]any
	if err := h.a.Acc.Do(context.Background(), h.user.Cred, accounting.Call{Method: http.MethodPost, Path: "/parties",
		Body: map[string]any{"name": name, "kind": "company", "is_customer": true, "mobile": "09357578808"}}, &out); err != nil {
		t.Fatal(err)
	}
	return int64Of(out["id"])
}

func TestUserSavedPreviewFormGetsTheSourceFileAttached(t *testing.T) {
	h := newHarness(t, "admin")
	plan, _ := ghasemPreviewWithFile(t, h)
	// closing the form without saving registers and attaches nothing
	rec := &Recorder{}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, Closed: true}, rec)
	if rec.Last("result") != nil || len(h.acc.attachments) != 0 || rec.Last("message") == nil {
		t.Fatalf("closed without saving must claim nothing: %+v", rec.Events)
	}
	id := userSavesParty(t, h, "شرکت قاسم")
	rec = &Recorder{}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, UserSaved: true, Closed: true}, rec)
	res, ok := rec.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("the saved record was not recognised: %+v", rec.Events)
	}
	if got := h.acc.attachments[fmt.Sprintf("party:%d", id)]; len(got) != 1 || got[0] != "receipt.png" {
		t.Fatalf("source file not attached to the record the user saved: %v", h.acc.attachments)
	}
	r := res["result"].(*Result)
	if len(r.Files) != 1 || r.Files[0].EntityID != id || h.acc.posts != 1 {
		t.Fatalf("result %+v posts=%d", r.Files, h.acc.posts)
	}
	// a late report (the widget's watcher again) changes nothing
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, UserSaved: true, Closed: true}, &Recorder{})
	if got := h.acc.attachments[fmt.Sprintf("party:%d", id)]; len(got) != 1 || partiesNamed(h, "شرکت قاسم") != 1 {
		t.Fatalf("repeated report duplicated something: %v", got)
	}
}

// The user edited the values on the form before saving: the only new record is theirs.
func TestUserEditedAndSavedPreviewFormIsStillFound(t *testing.T) {
	h := newHarness(t, "admin")
	plan, _ := ghasemPreviewWithFile(t, h)
	id := userSavesParty(t, h, "شرکت قاسم و پسران")
	rec := &Recorder{}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, UserSaved: true, Closed: true}, rec)
	if rec.Last("result") == nil || len(h.acc.attachments[fmt.Sprintf("party:%d", id)]) != 1 {
		t.Fatalf("edited record not found / file not attached: %+v %v", rec.Events, h.acc.attachments)
	}
}

// Saved by hand, then «تأیید و ثبت» in the chat: no second record; the file goes to theirs.
func TestConfirmAfterUserSavedDoesNotRegisterTwice(t *testing.T) {
	h := newHarness(t, "admin")
	_, conv := ghasemPreviewWithFile(t, h)
	id := userSavesParty(t, h, "شرکت قاسم")
	r, _ := talk(t, h, conv, "تأیید و ثبت")
	if partiesNamed(h, "شرکت قاسم") != 1 || h.acc.posts != 1 {
		t.Fatalf("registered twice: posts=%d", h.acc.posts)
	}
	if r.Last("result") == nil || len(h.acc.attachments[fmt.Sprintf("party:%d", id)]) != 1 {
		t.Fatalf("file not attached to the hand-saved record: %+v %v", r.Events, h.acc.attachments)
	}
}

// Nothing saved yet: the user is told so and nothing is claimed.
func TestUserSaveReportWithoutARecordClaimsNothing(t *testing.T) {
	h := newHarness(t, "admin")
	plan, _ := ghasemPreviewWithFile(t, h)
	rec := &Recorder{}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, UserSaved: true, Closed: true}, rec)
	if rec.Last("result") != nil || len(h.acc.attachments) != 0 {
		t.Fatalf("claimed a record that does not exist: %+v", rec.Events)
	}
	_, st, _ := h.a.load(context.Background(), h.user, plan.TaskID)
	if st.Stage != StageUIPreview {
		t.Fatalf("the operation must stay open: %s", st.Stage)
	}
}

// Live regression: the planner adds its own attachments.create step for the user's file
// ("این سند مربوط به اوست"). After the user saves the first form by hand, that step is
// not shown as another form: the system attaches the file and reports it.
func TestUserSavedFormThenPlannedSourceAttachRunsServerSide(t *testing.T) {
	h := newHarness(t, "admin")
	persianUser(h)
	up := h.upload(t)
	h.ai.queue = []string{`{"intent":"operate","user_requested_registration":true}`}
	h.ai.planQueue = []string{`{"action":"plan","summary":"ایجاد مشتری و پیوست سند",
 "steps":[{"id":"s1","tool":"parties.create","body":{"name":"شرکت قاسم","kind":"company","is_customer":true,"mobile":"09357578808"},"purpose":"ایجاد مشتری شرکت قاسم"},
  {"id":"s2","tool":"attachments.create","body":{"entity_type":"party","entity_id":"$s1.id","upload_id":"` + up + `"},"purpose":"پیوست سند به مشتری"}]}`}
	h.ai.extraction = `{"document_kind":"business_card"}`
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: ghasemText + " این سند مربوط به اوست. روی صفحه نشانم بده", Uploads: []string{up}, ExecMode: "api"}, rec)
	plan, ok := rec.Last("ui_plan").(UIPlan)
	if !ok {
		t.Fatalf("no preview: %+v\n%s", rec.Events, h.ai.planPrompts[len(h.ai.planPrompts)-1])
	}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true}, &Recorder{})
	id := userSavesParty(t, h, "شرکت قاسم")
	rec = &Recorder{}
	h.a.UIResult(context.Background(), h.user, plan.TaskID, UIResult{OK: true, Preview: true, UserSaved: true, Closed: true}, rec)
	if rec.Last("ui_plan") != nil {
		t.Fatalf("attaching the source file must not open another form: %+v", rec.Events)
	}
	res, ok := rec.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", rec.Events)
	}
	if got := h.acc.attachments[fmt.Sprintf("party:%d", id)]; len(got) != 1 {
		t.Fatalf("attached %v", h.acc.attachments)
	}
	if r := res["result"].(*Result); len(r.Files) != 1 || r.Files[0].EntityID != id {
		t.Fatalf("files %+v", r.Files)
	}
}
