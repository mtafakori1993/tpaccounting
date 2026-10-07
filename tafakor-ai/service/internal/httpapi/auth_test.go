package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

type nopAI struct{}

func (nopAI) Stream(context.Context, provider.Request, func(string)) (provider.Result, error) {
	return provider.Result{}, provider.ErrNotConfigured
}
func (nopAI) Complete(context.Context, provider.Request) (provider.Result, error) {
	return provider.Result{}, provider.ErrNotConfigured
}
func (nopAI) Health(context.Context) error { return provider.ErrNotConfigured }
func (nopAI) DefaultModel() string          { return "m" }

func fakeAccounting(t *testing.T, calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		c, err := r.Cookie("tp_session")
		if r.URL.Path != "/api/v1/auth/me" || err != nil || c.Value != "good" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"x"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":7,"email":"a@b","full_name":"A","permissions":["treasury.view"],"company":{"id":1,"name":"Demo","base_currency":"IRR"}}`))
	}))
}

func newTestServer(t *testing.T, acc string) http.Handler {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(config.Config{RateLimitPerMin: 1000}, st, accounting.New(acc), nopAI{})
	s.Register(func(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
		mux.Handle("GET /ai-api/whoami", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			WriteJSON(w, 200, map[string]any{"user": u.Scope.UserID, "company": u.Scope.CompanyID})
		})))
		mux.Handle("POST /ai-api/echo", auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	})
	return s.Handler()
}

func TestSessionValidatedOnEveryRequest(t *testing.T) {
	var calls atomic.Int32
	acc := fakeAccounting(t, &calls)
	defer acc.Close()
	h := newTestServer(t, acc.URL)

	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("GET", "/ai-api/whoami", nil)
		r.AddCookie(&http.Cookie{Name: "tp_session", Value: "good"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("session must be validated on every request, got %d calls", calls.Load())
	}

	r := httptest.NewRequest("GET", "/ai-api/whoami", nil)
	r.AddCookie(&http.Cookie{Name: "tp_session", Value: "bad"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("invalid session must be rejected, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/ai-api/whoami", nil))
	if w.Code != 401 {
		t.Fatal("missing session must be rejected")
	}
}

func TestCSRFRequiredForWrites(t *testing.T) {
	var calls atomic.Int32
	acc := fakeAccounting(t, &calls)
	defer acc.Close()
	h := newTestServer(t, acc.URL)
	r := httptest.NewRequest("POST", "/ai-api/echo", nil)
	r.AddCookie(&http.Cookie{Name: "tp_session", Value: "good"})
	r.AddCookie(&http.Cookie{Name: "tp_csrf", Value: "tok"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("missing CSRF header must be rejected, got %d", w.Code)
	}
	r.Header.Set("X-CSRF-Token", "tok")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatalf("valid CSRF rejected: %d", w.Code)
	}
}

func TestHealthzIsPublic(t *testing.T) {
	h := newTestServer(t, "http://127.0.0.1:1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/ai-api/healthz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/ai-api/loader.js", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") == "" {
		t.Fatal("loader not served")
	}
}

func TestRateLimitPerUser(t *testing.T) {
	l := newLimiter(2)
	a := store.Scope{CompanyID: 1, UserID: 1}
	b := store.Scope{CompanyID: 1, UserID: 2}
	if !l.allow(a, true) || !l.allow(a, true) {
		t.Fatal("first requests allowed")
	}
	if l.allow(a, true) {
		t.Fatal("third write within a minute must be limited")
	}
	if !l.allow(b, true) {
		t.Fatal("limits are per user")
	}
}
