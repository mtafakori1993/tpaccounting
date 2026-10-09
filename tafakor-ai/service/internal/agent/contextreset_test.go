package agent

import (
	"context"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"net/http"
	"strings"
	"testing"
)

const antennaText = "یه رسید خرید آنتن برای سرایداری شرکت ۱۲۰۰۰۰۰ تومان بزن"

// the planner of the first conversation never finds a way (the production symptom)
var stuck = []string{`{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`, `{"action":"bogus"}`}

func withExpenses(h *harness) {
	js := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	h.acc.extra.HandleFunc("POST /api/v1/expenses", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.acc.mu.Lock()
		defer h.acc.mu.Unlock()
		h.acc.posts++
		body["id"] = 500 + len(h.acc.expenses)
		body["number"] = 900 + len(h.acc.expenses)
		body["status"] = "posted"
		h.acc.expenses = append(h.acc.expenses, body)
		js(w, body)
	})
	h.acc.extra.HandleFunc("GET /api/v1/expenses/categories", func(w http.ResponseWriter, r *http.Request) {
		js(w, []map[string]any{{"id": 11, "name": "Software", "account_code": "8105", "account_name": "Software", "is_active": true}})
	})
	h.acc.extra.HandleFunc("GET /api/v1/expenses/{id}", func(w http.ResponseWriter, r *http.Request) {
		h.acc.mu.Lock()
		defer h.acc.mu.Unlock()
		for _, x := range h.acc.expenses {
			if fmt.Sprint(x["id"]) == r.PathValue("id") {
				js(w, x)
				return
			}
		}
		w.WriteHeader(404)
	})
}

func convOf(r *Recorder) string {
	if c, ok := r.Last("conversation").(map[string]any); ok {
		return c["conversation_id"].(string)
	}
	return ""
}

func requestOf(r *Recorder) string {
	for _, e := range r.Events {
		if e.Name == "request" {
			return e.Data.(map[string]any)["request_id"].(string)
		}
	}
	return ""
}

// 16. Exact regression: the antenna receipt is stuck in one conversation; sent again,
// the conflict is detected, ONE fresh context gets the exact text, asks the accounting
// questions, approval happens normally, and exactly one expense is registered.
func TestAntennaReceiptRecoversInAFreshContext(t *testing.T) {
	h := newHarness(t, "admin")
	withExpenses(h)
	h.ai.queue = []string{`{"intent":"operate","confidence":0.6,"event":{}}`,
		// the fresh context understands it (who paid / from which account are asked)
		`{"intent":"register","user_requested_registration":true,"confidence":0.9,"event":{"type":"expense","subject":"آنتن سرایداری","amount":"1200000","currency":"IRT","date":"2025-10-08"}}`}
	h.ai.planQueue = append([]string{}, stuck...)
	r1, conv1 := talk(t, h, "", antennaText)
	if !strings.Contains(messages(r1), "safe way") && !strings.Contains(messages(r1), "راه مطمئنی") {
		t.Fatalf("first attempt should be stuck: %s %+v", messages(r1), r1.Events)
	}
	r2 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: antennaText, ConversationID: conv1}, r2)
	conv2 := convOf(r2)
	if conv2 == "" || conv2 == conv1 {
		t.Fatalf("no fresh conversation: %s → %s\n%+v", conv1, conv2, r2.Events)
	}
	if !strings.Contains(messages(r2), "fresh context") {
		t.Fatalf("the user was not told: %s", messages(r2))
	}
	// exact text, byte for byte, is the fresh conversation's first message
	view, _ := h.a.ConversationView(context.Background(), h.user, conv2)
	msgs := view["messages"].([]ConvMessage)
	if len(msgs) == 0 || msgs[0].Role != "user" || msgs[0].Text != antennaText {
		t.Fatalf("first message of the fresh context: %+v", msgs)
	}
	// the fresh context asks the missing accounting questions; answer them
	rec := r2
	var asked []string
	for i := 0; i < 4 && rec.Last("approval") == nil; i++ {
		q, ok := rec.Last("question").(*classify.Question)
		if !ok {
			break
		}
		asked = append(asked, q.Field)
		val := "bank"
		if len(q.Options) > 0 {
			val = q.Options[0].Value
		}
		rec = &Recorder{}
		h.a.Answer(context.Background(), h.user, taskOf(r2), AnswerRequest{QuestionID: q.ID, Value: val}, rec)
	}
	if len(asked) == 0 {
		t.Fatalf("the fresh context asked no accounting question: %+v", r2.Events)
	}
	ap, ok := rec.Last("approval").(map[string]any)
	if !ok {
		t.Fatalf("no approval in the fresh context: %+v", rec.Events)
	}
	if h.acc.posts != 0 {
		t.Fatal("something was registered before approval")
	}
	r3 := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string), ExecMode: "direct"}, r3)
	if res, ok := r3.Last("result").(map[string]any); !ok || !res["result"].(*Result).Verified {
		t.Fatalf("not verified: %+v", r3.Events)
	}
	if len(h.acc.expenses) != 1 {
		t.Fatalf("expected exactly one expense, got %d", len(h.acc.expenses))
	}
	// one request id across both contexts, with the reason recorded
	rid := requestOf(r2)
	tr, err := h.a.RequestTrace(context.Background(), h.user, rid)
	if err != nil {
		t.Fatal(err)
	}
	atts := tr["attempts"].([]map[string]any)
	if len(atts) != 2 || atts[0]["result"] != "CONTEXT_CONFLICT" || atts[0]["conversation_id"] != conv1 || atts[1]["conversation_id"] != conv2 || tr["context_reset_count"] != 1 {
		t.Fatalf("trace %+v", tr)
	}
	reset := tr["context_resets"].([]map[string]any)[0]
	if reset["causing_state"] == "" || reset["architecture_bug"] != true || !strings.Contains(fmt.Sprint(reset["reasons"]), "SAME_REQUEST_FAILED_IN_CONTEXT") {
		t.Fatalf("reset cause not recorded: %+v", reset)
	}
	m, _ := h.a.ContextMetrics(context.Background(), h.user)
	if m["context_reset_count"] != 1 || m["same_request_failed_in_original_context"] != 1 {
		t.Fatalf("metrics %+v", m)
	}
}

// 10. Never more than one automatic reset: the fresh context failing too stops normally.
func TestFreshContextDoesNotLoop(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","confidence":0.6,"event":{}}`
	h.ai.planQueue = append(append(append([]string{}, stuck...), stuck...), stuck...)
	_, conv1 := talk(t, h, "", antennaText)
	r2 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: antennaText, ConversationID: conv1}, r2)
	conv2 := convOf(r2)
	// the same request fails in the fresh context too: sent again there, no third context
	r3 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: antennaText, ConversationID: conv2, RequestID: requestOf(r2)}, r3)
	if c := convOf(r3); c != conv2 {
		t.Fatalf("a second automatic reset happened: %s → %s", conv2, c)
	}
	tr, _ := h.a.RequestTrace(context.Background(), h.user, requestOf(r2))
	if tr["context_reset_count"] != 1 {
		t.Fatalf("resets %v", tr["context_reset_count"])
	}
}

// 15. Attachments are preserved exactly: same upload ids, same content hash, no copy.
func TestFreshContextKeepsTheExactAttachment(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.extraction = `{"document_type":"receipt","fields":{"amount":{"value":"1200000","confidence":0.9}}}`
	h.ai.interp = `{"intent":"operate","confidence":0.6,"event":{}}`
	h.ai.planQueue = append([]string{}, stuck...)
	up := h.upload(t)
	text := "این رسید خرید را ثبت کن"
	r1 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: text, Uploads: []string{up}}, r1)
	conv1 := convOf(r1)
	h.ai.planQueue = append([]string{}, stuck...)
	r2 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: text, Uploads: []string{up}, ConversationID: conv1}, r2)
	if convOf(r2) == conv1 {
		t.Fatalf("no reset: %+v", r2.Events)
	}
	_, st, err := h.a.load(context.Background(), h.user, taskOf(r2))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pngBytes)
	if len(st.Uploads) != 1 || st.Uploads[0].ID != up || st.Uploads[0].SHA256 != hex.EncodeToString(sum[:]) || st.UserText != text {
		t.Fatalf("fresh context got %+v / %q", st.Uploads, st.UserText)
	}
	if h.acc.posts != 0 {
		t.Fatal("a mutation happened before fresh planning")
	}
}

// 17. Contextual follow-ups never reset.
func TestFollowUpsStayInTheConversation(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"chat","confidence":0.9,"event":{}}`
	_, conv := talk(t, h, "", "یه کارمند به اسم علی بساز")
	for _, s := range []string{"حقوقش رو ۳۰ میلیون کن", "حالا بهش کمک هزینه سفر هم اضافه کن"} {
		r, c := talk(t, h, conv, s)
		if c != conv || strings.Contains(messages(r), "fresh context") {
			t.Fatalf("%q reset the conversation (%s → %s)", s, conv, c)
		}
	}
}

// 18/19. A new independent request after a stopped/failed task does not inherit it.
func TestNewRequestDoesNotInheritAStoppedTask(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","confidence":0.6,"event":{}}`
	h.ai.planQueue = append([]string{}, stuck...)
	_, conv := talk(t, h, "", "مرکز هزینه عیاشی رو حذف کن")
	h.ai.planQueue = []string{`{"action":"answer","text":"Cost centers: پروژه وب‌سایت."}`}
	r, c2 := talk(t, h, conv, "مراکز هزینه رو لیست کن")
	if c2 == conv {
		t.Fatalf("stopped task was continued in place: %+v", r.Events)
	}
	last := h.ai.planPrompts[len(h.ai.planPrompts)-1]
	if strings.Contains(last, "عیاشی") {
		t.Fatalf("the fresh request inherited the stopped task:\n%s", last)
	}
}

// 5. The fresh context receives the request byte for byte (hash of the raw text, spaces included).
func TestFreshContextResubmitsTheRawBytes(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"operate","confidence":0.6,"event":{}}`
	h.ai.planQueue = append(append([]string{}, stuck...), stuck...)
	raw := "  " + antennaText + "\n"
	r1 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: raw}, r1)
	r2 := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: raw, ConversationID: convOf(r1)}, r2)
	tr, err := h.a.RequestTrace(context.Background(), h.user, requestOf(r2))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])
	reset := tr["context_resets"].([]map[string]any)[0]
	atts := tr["attempts"].([]map[string]any)
	if reset["message_sha256"] != want || len(atts) != 2 || atts[1]["message_sha256"] != want {
		t.Fatalf("raw bytes changed: want %s, reset %v, attempt2 %v", want, reset["message_sha256"], atts)
	}
}
