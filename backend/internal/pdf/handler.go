package pdf

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

// Routes for document rendering (any signed-in user).
func (s *Service) Routes(r chi.Router) {
	r.Post("/", httpx.Handler(s.render))
}

// SettingsRoutes for the letterhead configuration.
func (s *Service) SettingsRoutes(r chi.Router) {
	r.With(identity.Require("settings.view")).Get("/", httpx.Handler(s.getSettings))
	r.With(identity.Require("settings.edit")).Put("/", httpx.Handler(s.putSettings))
	r.With(identity.Require("settings.view")).Post("/preview", httpx.Handler(s.preview))
}

func writePDF(w http.ResponseWriter, name string, b []byte) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="document.pdf"; filename*=UTF-8''%s.pdf`, url.PathEscape(name)))
	w.Header().Set("Content-Length", fmt.Sprint(len(b)))
	// The PDF viewer must be allowed to display it in a frame of this site.
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	_, _ = w.Write(b)
}

func fileName(d Document) string {
	n := strings.TrimSpace(d.Title + " " + d.Number)
	if n == "" {
		return "document"
	}
	return n
}

func (s *Service) renderErr(err error) error {
	if errors.Is(err, ErrUnavailable) {
		return apperr.New(http.StatusServiceUnavailable, "pdf_unavailable", "the PDF renderer is not installed")
	}
	if _, ok := apperr.As(err); ok {
		return err
	}
	return fmt.Errorf("render pdf: %w", err)
}

func (s *Service) render(w http.ResponseWriter, r *http.Request) error {
	var doc Document
	if err := httpx.Decode(r, &doc); err != nil {
		return err
	}
	b, err := s.Render(r.Context(), identity.From(r.Context()).CompanyID, doc, nil)
	if err != nil {
		return s.renderErr(err)
	}
	writePDF(w, fileName(doc), b)
	return nil
}

type settingsResponse struct {
	Settings  Settings `json:"settings"`
	Defaults  Settings `json:"defaults"`
	Variables []string `json:"variables"`
}

func (s *Service) getSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := LoadSettings(r.Context(), s.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, settingsResponse{Settings: st, Defaults: Defaults(), Variables: Variables})
	return nil
}

func (s *Service) putSettings(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Settings
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), s.Pool, func(tx pgx.Tx) error {
		before, err := LoadSettings(r.Context(), tx, p.CompanyID)
		if err != nil {
			return err
		}
		if err := SaveSettings(r.Context(), tx, p.CompanyID, p.UserID, &in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "letterhead", 0, before, in)
	})
	if err != nil {
		return err
	}
	return s.getSettings(w, r)
}

type previewInput struct {
	Settings Settings `json:"settings"`
	Lang     string   `json:"lang"`
	Pages    int      `json:"pages"`
}

// preview renders a sample document with unsaved settings.
func (s *Service) preview(w http.ResponseWriter, r *http.Request) error {
	var in previewInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	doc := SampleDocument(in.Lang, in.Pages)
	b, err := s.Render(r.Context(), identity.From(r.Context()).CompanyID, doc, &in.Settings)
	if err != nil {
		return s.renderErr(err)
	}
	writePDF(w, "letterhead-preview", b)
	return nil
}
