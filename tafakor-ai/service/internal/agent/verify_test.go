package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
)

// startForUI runs a clean advance request in UI mode up to the approval and approves it.
func startForUI(t *testing.T, h *harness) string {
	h.ai.interp = advanceInterp
	ctx := context.Background()
	rec := &Recorder{}
	id := h.a.Start(ctx, h.user, StartRequest{Message: "register the advance from Company X", ExecMode: "ui"}, rec)
	ap := approvalOf(t, rec)
	rec = &Recorder{}
	h.a.Approve(ctx, h.user, id, ApproveRequest{Token: ap["token"].(string), ExecMode: "ui"}, rec)
	if rec.Last("ui_plan") == nil {
		t.Fatalf("expected a UI plan: %+v", rec.Events)
	}
	return id
}

func TestUIReportedSuccessButNothingCreatedIsNotSuccess(t *testing.T) {
	h := newHarness(t, "admin")
	id := startForUI(t, h)
	rec := &Recorder{}
	// the widget claims success, but the accounting API has no such document
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: true}, rec)
	if rec.Last("result") != nil || rec.Last("ui_failed") == nil {
		t.Fatalf("success must come from verified state only: %+v", rec.Events)
	}
	_, st, _ := h.a.load(context.Background(), h.user, id)
	if st.Stage != StageUIFailed {
		t.Fatal(st.Stage)
	}
	// explicit fallback registers the SAME approved payload once
	rec = &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{FallbackAPI: true}, rec)
	if h.acc.posts != 1 || rec.Last("result") == nil {
		t.Fatalf("fallback failed: %+v", rec.Events)
	}
	rec = &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{FallbackAPI: true}, rec)
	if h.acc.posts != 1 {
		t.Fatal("fallback executed twice")
	}
}

func TestUICreatedDocumentWithWrongValuesIsReportedUnverified(t *testing.T) {
	h := newHarness(t, "admin")
	id := startForUI(t, h)
	// simulate the form saving a document with the right amount but the wrong account
	h.acc.mu.Lock()
	h.acc.txs = append(h.acc.txs, map[string]any{"id": 2000, "kind": "receipt", "number": 9, "date": "2025-10-07", "treasury_account_id": 99,
		"party_id": 101, "cost_center_id": 30, "amount": "50000000", "status": "posted", "created_at": time.Now().UTC().Format(time.RFC3339Nano)})
	h.acc.mu.Unlock()
	rec := &Recorder{}
	h.a.UIResult(context.Background(), h.user, id, UIResult{OK: true}, rec)
	res, ok := rec.Last("result").(map[string]any)
	if !ok {
		t.Fatalf("no result: %+v", rec.Events)
	}
	r := res["result"].(*Result)
	if r.Verified || !strings.Contains(strings.Join(r.Mismatches, ","), "treasury_account") {
		t.Fatalf("mismatch must be reported: %+v", r)
	}
	if !strings.Contains(res["summary"].(string), "could not verify") {
		t.Fatalf("summary must say it is unverified: %v", res["summary"])
	}
}

func TestVisionLocateReturnsOnlyACandidateIndex(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"index": 99}` // out of range → treated as "no match"
	srv := httpapi.New(config.Config{RateLimitPerMin: 1000}, h.st, h.a.Acc, h.ai)
	srv.Register((&HTTP{A: h.a}).Register)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	body := `{"goal":{"op":"fill","target":"common.amount","label":"x"},"candidates":[{"n":1,"role":"input","name":"Amount"}],"image":"data:image/jpeg;base64,/9j/4AAQ"}`
	do := func() map[string]any {
		req, _ := http.NewRequest("POST", ts.URL+"/ai-api/ui/locate", strings.NewReader(body))
		req.Header.Set("X-CSRF-Token", "csrf-tok")
		req.AddCookie(&http.Cookie{Name: "tp_session", Value: "admin"})
		req.AddCookie(&http.Cookie{Name: "tp_csrf", Value: "csrf-tok"})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	if out := do(); out["index"].(float64) != 0 {
		t.Fatalf("out-of-range answer must be rejected: %v", out)
	}
	h.ai.interp = `{"index": 1}`
	if out := do(); out["index"].(float64) != 1 {
		t.Fatalf("valid answer: %v", out)
	}
	entries, _ := h.st.ListAudit(context.Background(), h.user.Scope, "", 10)
	if len(entries) == 0 || entries[0].Kind != "vision_locate" || !strings.Contains(string(entries[0].Data), "screenshot") {
		t.Fatal("vision call not audited with a screenshot reference")
	}
}
