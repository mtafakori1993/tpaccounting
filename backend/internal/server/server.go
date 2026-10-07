// Package server wires all modules into the HTTP router.
package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/accounts"
	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/attachments"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/auth"
	"github.com/tafakorpardazan/tpaccounting/internal/budgets"
	"github.com/tafakorpardazan/tpaccounting/internal/checks"
	"github.com/tafakorpardazan/tpaccounting/internal/company"
	"github.com/tafakorpardazan/tpaccounting/internal/costcenters"
	"github.com/tafakorpardazan/tpaccounting/internal/expenses"
	"github.com/tafakorpardazan/tpaccounting/internal/inventory"
	"github.com/tafakorpardazan/tpaccounting/internal/invoices"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/parties"
	"github.com/tafakorpardazan/tpaccounting/internal/payroll"
	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/config"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/storage"
	"github.com/tafakorpardazan/tpaccounting/internal/products"
	"github.com/tafakorpardazan/tpaccounting/internal/reports"
	"github.com/tafakorpardazan/tpaccounting/internal/setup"
	"github.com/tafakorpardazan/tpaccounting/internal/treasury"
	"github.com/tafakorpardazan/tpaccounting/internal/users"
)

func New(cfg config.Config, pool *pgxpool.Pool, store storage.Storage) http.Handler {
	authSvc := auth.NewService(pool, cfg)
	// Budget control runs inside every posting transaction.
	ledger.OnPost = budgets.Control
	r := chi.NewRouter()
	r.Use(recoverer, logRequests, securityHeaders(cfg.CookieSecure, cfg.AIServiceURL != ""), cors(cfg.AllowedOrigins), authSvc.Meta)

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})

	rep := &reports.Handler{Pool: pool}
	chrome := cfg.ChromePath
	if chrome == "" {
		chrome = pdf.FindChrome()
	}
	pdfs := &pdf.Service{Pool: pool, Storage: store, Renderer: pdf.NewRenderer(chrome)}
	setupH := &setup.Handler{Pool: pool, Auth: authSvc}

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/setup/status", httpx.Handler(setupH.Status))
		api.Post("/setup", httpx.Handler(setupH.Run))
		api.Route("/auth", func(ar chi.Router) {
			authSvc.PublicRoutes(ar)
			ar.Group(func(g chi.Router) {
				g.Use(authSvc.Authenticate)
				authSvc.Routes(g)
			})
		})

		api.Group(func(p chi.Router) {
			p.Use(authSvc.Authenticate, budgets.LoggingMiddleware(pool))
			p.Get("/dashboard", httpx.Handler(rep.Dashboard))
			p.Route("/company", (&company.Handler{Pool: pool, Storage: store}).Routes)
			p.Route("/accounts", (&accounts.Handler{Pool: pool}).Routes)
			p.Route("/parties", (&parties.Handler{Pool: pool}).Routes)
			p.Route("/cost-centers", (&costcenters.Handler{Pool: pool}).Routes)
			p.Route("/journal-entries", (&ledger.Handler{Pool: pool}).Routes)
			p.Route("/products", (&products.Handler{Pool: pool}).Routes)
			p.Route("/inventory", (&inventory.Handler{Pool: pool}).Routes)
			p.Route("/sales", (&invoices.Handler{Pool: pool, Side: invoices.SideSales, Module: "sales"}).Routes)
			p.Route("/purchases", (&invoices.Handler{Pool: pool, Side: invoices.SidePurchase, Module: "purchases"}).Routes)
			p.Route("/treasury", (&treasury.Handler{Pool: pool}).Routes)
			p.Route("/checks", (&checks.Handler{Pool: pool, PDF: pdfs}).Routes)
			ph := &payroll.Handler{Pool: pool, PDF: pdfs}
			p.Route("/employees", ph.EmployeeRoutes)
			p.Route("/payroll", ph.Routes)
			p.Route("/budgets", (&budgets.Handler{Pool: pool}).Routes)
			p.Route("/expenses", (&expenses.Handler{Pool: pool}).Routes)
			p.Route("/reports", rep.Routes)
			p.Route("/pdf", pdfs.Routes)
			p.Route("/settings/letterhead", pdfs.SettingsRoutes)
			p.Route("/attachments", (&attachments.Handler{Pool: pool, Storage: store, MaxBytes: cfg.MaxUploadBytes}).Routes)
			uh := &users.Handler{Pool: pool}
			p.Route("/users", uh.UserRoutes)
			p.Route("/roles", uh.RoleRoutes)
			p.With(identity.Require("audit.view")).Get("/audit", httpx.Handler((&audit.Handler{Pool: pool}).List))
		})
		api.NotFound(func(w http.ResponseWriter, req *http.Request) { httpx.Error(w, req, apperr.NotFound("route")) })
	})

	r.Handle("/ai-api/*", aiProxy(cfg.AIServiceURL)) // Tafakor AI touchpoint (b)
	r.Handle("/*", spa(cfg.StaticDir))
	return r
}

// spa serves the built frontend and falls back to index.html for client-side routes.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/api/") {
			httpx.Error(w, r, apperr.NotFound("route"))
			return
		}
		p := filepath.Join(dir, filepath.FromSlash(clean))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			if strings.HasPrefix(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fs.ServeHTTP(w, r)
			return
		}
		// Missing build assets must 404 (not fall back to index.html) so a stale
		// tab can detect that a new version was deployed and reload itself.
		if strings.HasPrefix(clean, "/assets/") || filepath.Ext(clean) != "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dir, "index.html"))
	})
}
