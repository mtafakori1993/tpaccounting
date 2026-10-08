package agent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
)

// Real HTTP round trip: the JSON body must still be readable although the handler
// streams server-sent events (regression: HTTP/1.1 closes the body after headers).
func TestTasksEndpointOverHTTP(t *testing.T) {
	h := newHarness(t, "admin")
	h.ai.interp = `{"intent":"chat","reply":"hi"}`
	srv := httpapi.New(config.Config{RateLimitPerMin: 1000}, h.st, h.a.Acc, h.ai)
	srv.Register((&HTTP{A: h.a}).Register)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/ai-api/tasks", strings.NewReader(`{"message":"hello there","exec_mode":"api"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", "csrf-tok")
	req.AddCookie(&http.Cookie{Name: "tp_session", Value: "admin"})
	req.AddCookie(&http.Cookie{Name: "tp_csrf", Value: "csrf-tok"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, "event: task") || !strings.Contains(body, "event: done") || strings.Contains(body, "invalid request") {
		t.Fatalf("unexpected stream:\n%s", body)
	}
	// write endpoints without CSRF are rejected before any work
	req2, _ := http.NewRequest("POST", ts.URL+"/ai-api/tasks", strings.NewReader(`{"message":"x"}`))
	req2.AddCookie(&http.Cookie{Name: "tp_session", Value: "admin"})
	resp2, _ := http.DefaultClient.Do(req2)
	if resp2.StatusCode != 403 {
		t.Fatalf("CSRF not enforced: %d", resp2.StatusCode)
	}
}
