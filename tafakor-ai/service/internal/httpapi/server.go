// Package httpapi exposes the AI service under /ai-api (proxied same-origin by the
// accounting server). Every endpoint except health and the static widget requires
// a valid accounting session.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/config"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

type Server struct {
	Cfg     config.Config
	Store   store.Repository
	Acc     *accounting.Client
	AI      provider.AIProvider
	limiter *limiter
	mux     *http.ServeMux
	// extra route registrars added by later layers (agent, tools, …)
	register []func(mux *http.ServeMux, auth func(http.Handler) http.Handler)
}

func New(cfg config.Config, st store.Repository, acc *accounting.Client, ai provider.AIProvider) *Server {
	return &Server{Cfg: cfg, Store: st, Acc: acc, AI: ai, limiter: newLimiter(cfg.RateLimitPerMin)}
}

// Register adds routes from another package.
func (s *Server) Register(fn func(mux *http.ServeMux, auth func(http.Handler) http.Handler)) {
	s.register = append(s.register, fn)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := s.authenticate
	mux.HandleFunc("GET /ai-api/healthz", s.healthz)
	mux.HandleFunc("GET /ai-api/loader.js", s.loader)
	mux.Handle("GET /ai-api/widget/", http.StripPrefix("/ai-api/widget/", staticDir(s.Cfg.WidgetDir)))
	mux.Handle("GET /ai-api/health/provider", auth(http.HandlerFunc(s.providerHealth)))
	for _, fn := range s.register {
		fn(mux, auth)
	}
	mux.HandleFunc("/ai-api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", "Unknown assistant endpoint.")
	})
	s.mux = mux
	return recoverer(securityHeaders(mux))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "storage", "storage unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) providerHealth(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	err := s.AI.Health(r.Context())
	out := map[string]any{"ok": err == nil, "model": s.AI.DefaultModel(), "latency_ms": time.Since(start).Milliseconds(),
		"proxy": config.RedactURL(s.Cfg.LLM.ProxyURL)}
	if err != nil {
		out["message"] = provider.PlainMessage(err)
	}
	writeJSON(w, http.StatusOK, out)
}

// loader injects the widget bundle. It is tiny and cacheable; the bundle itself is versioned by mtime.
func (s *Server) loader(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	v := "0"
	if st, err := os.Stat(filepath.Join(s.Cfg.WidgetDir, "tafakor-ai.js")); err == nil {
		v = st.ModTime().UTC().Format("20060102150405")
	}
	_, _ = w.Write([]byte(`(function(){if(window.__tafakorAI)return;window.__tafakorAI=1;` +
		`var s=document.createElement('script');s.src='/ai-api/widget/tafakor-ai.js?v=` + v + `';s.defer=true;document.head.appendChild(s);})();`))
}

func staticDir(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=300")
		fs.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if !strings.HasPrefix(r.URL.Path, "/ai-api/widget/") && r.URL.Path != "/ai-api/loader.js" {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic", "path", r.URL.Path, "err", p)
				writeErr(w, http.StatusInternalServerError, "internal", "Unexpected assistant error. Nothing was registered.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteJSON is exported for route registrars in other packages.
func WriteJSON(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

// WriteErr is exported for route registrars in other packages.
func WriteErr(w http.ResponseWriter, status int, code, msg string) { writeErr(w, status, code, msg) }

// DecodeJSON reads a size-limited JSON body, rejecting unknown fields.
func DecodeJSON(r *http.Request, v any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
