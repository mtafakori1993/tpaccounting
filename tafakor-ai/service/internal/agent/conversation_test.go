package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// The model's answers for the two Galatea turns. Turn 2 is deliberately imperfect:
// it forgets "no code" and misnames the operation — state handling must not depend on it.
const galateaTurn1 = `{"intent":"register","user_requested_registration":true,"confidence":0.9,
 "event":{"type":"inventory_receipt","product_name":"Galatea","unit_value":"10000000","currency":"IRT","manufactured_internally":true}}`
const galateaTurn2 = `{"intent":"register","user_requested_registration":true,"confidence":0.8,"intent_changed":false,
 "event":{"type":"purchase","quantity":"10","unit":"piece","warehouse":"default"}}`

const msg1 = "Add a product named Galatea worth 10 million tomans to our inventory. It is manufactured by us."
const msg2 = "10 units, piece, default warehouse, no code."

func talk(t *testing.T, h *harness, convID, text string) (*Recorder, string) {
	t.Helper()
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: text, ExecMode: "api", ConversationID: convID}, rec)
	c, ok := rec.Last("conversation").(map[string]any)
	if !ok {
		t.Fatalf("no conversation event: %+v", rec.Events)
	}
	return rec, c["conversation_id"].(string)
}

func questions(r *Recorder) []string {
	var out []string
	for _, e := range r.Events {
		if e.Name == "question" {
			b, _ := json.Marshal(e.Data)
			out = append(out, string(b))
		}
	}
	return out
}

func taskOf(r *Recorder) string {
	if t, ok := r.Last("task").(map[string]any); ok {
		return t["task_id"].(string)
	}
	return ""
}

// 1 + 2 + 3 + 4 + 5: the Galatea conversation.
func TestMultiTurnInventoryKeepsStateAndAsksOnlyForMissing(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{galateaTurn1, galateaTurn2}

	r1, conv := talk(t, h, "", msg1)
	q1 := questions(r1)
	if len(q1) != 1 {
		t.Fatalf("turn 1 must ask exactly one question about the missing fields, got %v (events %+v)", q1, r1.Events)
	}
	low := strings.ToLower(q1[0])
	for _, must := range []string{"how many", "unit"} {
		if !strings.Contains(low, must) {
			t.Errorf("turn 1 question should ask for %q: %s", must, q1[0])
		}
	}
	for _, never := range []string{"product name", "value per unit", "purchase", "sales", "price"} {
		if strings.Contains(low, never) {
			t.Errorf("turn 1 asked for something already known (%q): %s", never, q1[0])
		}
	}

	r2, conv2 := talk(t, h, conv, msg2)
	if conv2 != conv {
		t.Fatal("follow-up must stay in the same conversation")
	}
	if q := questions(r2); len(q) != 0 {
		t.Fatalf("turn 2: everything is known, but the assistant asked again: %v", q)
	}
	if taskOf(r2) != taskOf(r1) {
		t.Fatal("the follow-up must continue the same operation, not start a new one")
	}
	ap, ok := r2.Last("approval").(map[string]any)
	if !ok {
		t.Fatalf("complete state must go to the approval step: %+v", r2.Events)
	}
	prev := r2.Last("preview").(map[string]any)
	d := prev["display"].(classify.Display)
	if prev["action"] != "create_inventory_receipt" {
		t.Fatalf("original intent lost: %v", prev["action"])
	}
	if d.Product != "Galatea" || d.Quantity != "10" || d.Unit != "piece" || d.UnitValue != "100000000" || d.Amount != "1000000000" {
		t.Fatalf("merged state wrong (10,000,000 toman = 100,000,000 rial per unit): %+v", d)
	}
	if !strings.Contains(d.ProductCode, "none") {
		t.Fatalf(`"no code" must be a completed field: %q`, d.ProductCode)
	}
	for _, r := range ap["reasons"].([]map[string]string) {
		if r["code"] == "open_period" {
			t.Fatal("period check must use the receipt date (open fiscal year in the fake API)")
		}
	}
	if d.Warehouse != "انبار مرکزی" {
		t.Fatalf("default warehouse: %q", d.Warehouse)
	}

	// the frame shows the statuses explicitly
	_, st, _ := h.a.load(context.Background(), h.user, taskOf(r2))
	f := st.Frame
	if f.Status["product_name"] != classify.Known || f.Status["quantity"] != classify.Known || f.Status["product_code"] != classify.Null {
		t.Fatalf("statuses %+v", f.Status)
	}
	if st.Interp.Event.Type != classify.EvInventoryIn {
		t.Fatalf("intent changed to %s", st.Interp.Event.Type)
	}

	// the second interpretation ran in follow-up mode with the state and history
	if len(h.ai.prompts) != 2 || !strings.Contains(h.ai.prompts[1], "<operation_in_progress>") ||
		!strings.Contains(h.ai.prompts[1], `product_name: "Galatea"`) || !strings.Contains(h.ai.prompts[1], "<conversation_so_far>") ||
		!strings.Contains(h.ai.prompts[1], "Galatea worth 10 million") {
		t.Fatalf("follow-up prompt lacks state/history:\n%s", h.ai.prompts[len(h.ai.prompts)-1])
	}

	// approve → executed once, verified; tool calls and results are in the conversation state (8)
	rec := &Recorder{}
	h.a.Approve(context.Background(), h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string), ExecMode: "api"}, rec)
	res, ok := rec.Last("result").(map[string]any)
	if !ok || !res["result"].(*Result).Verified {
		t.Fatalf("not registered/verified: %+v", rec.Events)
	}
	if len(h.acc.products) != 1 || h.acc.products[0]["sku"] != "" || h.acc.products[0]["unit"] != "piece" || len(h.acc.stockDocs) != 1 {
		t.Fatalf("product/stock receipt wrong: %+v %+v", h.acc.products, h.acc.stockDocs)
	}
	view, err := h.a.ConversationView(context.Background(), h.user, conv)
	if err != nil {
		t.Fatal(err)
	}
	msgs := view["messages"].([]ConvMessage)
	var tools []string
	users := 0
	for _, m := range msgs {
		if m.Role == "tool" {
			tools = append(tools, m.Text)
		}
		if m.Role == "user" && m.Kind == "text" {
			users++
		}
	}
	if strings.Join(tools, ",") != "create_inventory_receipt,verify" {
		t.Fatalf("tool calls/results missing from conversation state: %v", tools)
	}
	if users != 2 {
		t.Fatalf("both user turns must be in the transcript, got %d", users)
	}
}

// 4: a follow-up the model mislabels keeps the operation; an explicit change starts a new one.
func TestIntentOnlyChangesWhenTheUserSaysSo(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{galateaTurn1,
		`{"intent":"register","user_requested_registration":true,"event":{"type":"sales_invoice","quantity":"10"}}`,
		`{"intent":"query","intent_changed":true,"query":{"kind":"expenses"}}`}
	_, conv := talk(t, h, "", msg1)
	r2, _ := talk(t, h, conv, "10")
	if prev, ok := r2.Last("preview").(map[string]any); ok && prev["action"] != "create_inventory_receipt" {
		t.Fatalf("operation changed without the user asking: %v", prev["action"])
	}
	q := questions(r2)
	if len(q) != 1 || strings.Contains(strings.ToLower(q[0]), "how many") || !strings.Contains(strings.ToLower(q[0]), "unit") {
		t.Fatalf("after giving the quantity only the unit is missing: %v", q)
	}
	r3, _ := talk(t, h, conv, "forget that — how much did we spend this month?")
	if r3.Last("question") != nil || r3.Last("message") == nil {
		t.Fatalf("explicit change of subject must be honoured: %+v", r3.Events)
	}
}

// 6 + 7: state survives separate HTTP requests and a page refresh (GET conversation).
func TestConversationSurvivesRequestsAndRefresh(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{galateaTurn1, galateaTurn2}
	srv := httpapi.New(config.Config{RateLimitPerMin: 1000}, h.st, h.a.Acc, h.ai)
	srv.Register((&HTTP{A: h.a}).Register)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	post := func(body string) map[string][]string {
		req, _ := http.NewRequest("POST", ts.URL+"/ai-api/tasks", strings.NewReader(body))
		req.Header.Set("X-CSRF-Token", "csrf-tok")
		req.AddCookie(&http.Cookie{Name: "tp_session", Value: "admin"})
		req.AddCookie(&http.Cookie{Name: "tp_csrf", Value: "csrf-tok"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out := map[string][]string{}
		sc := bufio.NewScanner(resp.Body)
		ev := ""
		for sc.Scan() {
			l := sc.Text()
			if strings.HasPrefix(l, "event: ") {
				ev = strings.TrimPrefix(l, "event: ")
			} else if strings.HasPrefix(l, "data: ") {
				out[ev] = append(out[ev], strings.TrimPrefix(l, "data: "))
			}
		}
		return out
	}
	e1 := post(`{"message":` + jsonStr(msg1) + `,"exec_mode":"api"}`)
	var c struct {
		ID string `json:"conversation_id"`
	}
	_ = json.Unmarshal([]byte(e1["conversation"][0]), &c)

	// "refresh": the widget reloads the conversation by id
	get := func() map[string]any {
		req, _ := http.NewRequest("GET", ts.URL+"/ai-api/conversations/"+c.ID, nil)
		req.AddCookie(&http.Cookie{Name: "tp_session", Value: "admin"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var v map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&v)
		return v
	}
	v := get()
	task, _ := v["task"].(map[string]any)
	if task == nil || task["stage"] != StageQuestion || len(v["messages"].([]any)) < 2 {
		t.Fatalf("refresh must restore the open question and transcript: %+v", v)
	}

	e2 := post(`{"message":` + jsonStr(msg2) + `,"exec_mode":"api","conversation_id":"` + c.ID + `"}`)
	if len(e2["question"]) != 0 || len(e2["approval"]) != 1 {
		t.Fatalf("second request lost the state: %v", e2)
	}
	if v := get(); v["version"].(float64) < 2 {
		t.Fatalf("conversation not persisted per request: %v", v["version"])
	}
	// another user cannot read it
	req, _ := http.NewRequest("GET", ts.URL+"/ai-api/conversations/"+c.ID, nil)
	req.AddCookie(&http.Cookie{Name: "tp_session", Value: "viewer"})
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 404 {
		t.Fatalf("conversation leaked to another user: %d", resp.StatusCode)
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// 9: concurrent messages are serialized; a stale writer never overwrites newer state.
func TestConcurrentMessagesDoNotLoseState(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"chat","reply":"ok"}`
	_, conv := talk(t, h, "", "hello")
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := &Recorder{}
			h.a.Start(context.Background(), h.user, StartRequest{Message: fmt.Sprintf("message %d", i), ConversationID: conv}, rec)
		}(i)
	}
	wg.Wait()
	view, _ := h.a.ConversationView(context.Background(), h.user, conv)
	users := map[string]bool{}
	for _, m := range view["messages"].([]ConvMessage) {
		if m.Role == "user" {
			users[m.Text] = true
		}
	}
	if len(users) != 7 {
		t.Fatalf("lost user turns under concurrency: %d of 7 (%v)", len(users), users)
	}
	if view["version"].(int64) != 7 {
		t.Fatalf("each request must produce exactly one new version, got %v", view["version"])
	}
	// a writer holding an old version is rejected and nothing is overwritten
	c, _ := h.a.openConversation(context.Background(), h.user, conv)
	stale := *c
	c.add(ConvMessage{Role: "user", Text: "newer"})
	if err := h.a.saveConversation(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	stale.add(ConvMessage{Role: "user", Text: "stale"})
	if err := h.a.saveConversation(context.Background(), &stale); !errors.Is(err, ErrConversationChanged) {
		t.Fatalf("stale write must fail, got %v", err)
	}
	row, _ := h.st.GetConversation(context.Background(), store.Scope{CompanyID: 1, UserID: 1}, conv)
	if strings.Contains(string(row.State), `"stale"`) || !strings.Contains(string(row.State), `"newer"`) {
		t.Fatal("stale state overwrote newer state")
	}
}

// Free-text answers to a "details" question card are merged exactly like typed follow-ups.
func TestDetailsCardAnswerIsMergedLikeAFollowUp(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.queue = []string{galateaTurn1, galateaTurn2}
	r1, _ := talk(t, h, "", msg1)
	q := r1.Last("question")
	b, _ := json.Marshal(q)
	var qq struct{ ID string }
	_ = json.Unmarshal(b, &qq)
	rec := &Recorder{}
	h.a.Answer(context.Background(), h.user, taskOf(r1), AnswerRequest{QuestionID: qq.ID, Value: msg2}, rec)
	if rec.Last("question") != nil || rec.Last("approval") == nil {
		t.Fatalf("card answer not merged: %+v", rec.Events)
	}
}
