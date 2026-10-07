package pdf

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/storage"
)

// Service is the only way to produce a PDF. Render always applies the
// company's letterhead, so no document can bypass it.
type Service struct {
	Pool     *pgxpool.Pool
	Storage  storage.Storage
	Renderer *Renderer
}

// Render produces an A4 PDF of doc with the company's letterhead. When
// override is non-nil it is used instead of the stored settings (preview).
func (s *Service) Render(ctx context.Context, companyID int64, doc Document, override *Settings) ([]byte, error) {
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	settings := Defaults()
	if override != nil {
		settings = *override
		if err := settings.Normalize(); err != nil {
			return nil, err
		}
	} else {
		var err error
		if settings, err = LoadSettings(ctx, s.Pool, companyID); err != nil {
			return nil, err
		}
	}
	// The letterhead can be switched off separately for exports and for printing;
	// the document still goes through the same engine and page geometry.
	if (doc.Purpose == "print" && !settings.ForPrint) || (doc.Purpose != "print" && !settings.Enabled) {
		settings.Enabled = false
	}
	c, err := s.context(ctx, s.Pool, companyID, settings)
	if err != nil {
		return nil, err
	}
	c.DocumentNumber, c.DocumentDate, c.DocumentTitle = doc.Number, doc.Date, doc.Title
	return s.Renderer.Print(ctx, Compose(settings, doc, c))
}

// Compose turns settings + document into a print job (pure; used by tests).
func Compose(settings Settings, doc Document, c Context) printJob {
	if doc.Lang != "en" {
		doc.Lang = "fa"
	}
	orientation := settings.Page.Orientation
	if doc.Orientation != "" {
		orientation = doc.Orientation
	}
	l := newLayout(settings, orientation)
	body, err := bodyHTML(l, doc, c)
	if err != nil {
		body = "<p>render error</p>"
	}
	header := `<span></span>`
	if l.HeaderArea > 0 && settings.Header.Mode == "every" {
		header = pageTemplate(headerHTML(l, c, doc.Lang), "top")
	}
	footer := `<span></span>`
	if l.FooterArea > 0 {
		footer = pageTemplate(footerHTML(l, c, doc.Lang), "bottom")
	}
	return printJob{
		HTML: body, Header: header, Footer: footer,
		WidthMM: l.PageW, HeightMM: l.PageH,
		MarginTop: l.ContentTop, MarginBottom: l.ContentBot,
		MarginLeft: settings.Page.MarginLeftMM, MarginRight: settings.Page.MarginRightMM,
	}
}

func (s *Service) context(ctx context.Context, q db.DBTX, companyID int64, settings Settings) (Context, error) {
	var c Context
	var logoKey *string
	err := q.QueryRow(ctx, `SELECT name, registration_number, national_id, economic_code, phone, email, website, address, logo_key
		FROM companies WHERE id = $1`, companyID).Scan(&c.CompanyName, &c.CompanyRegistration, &c.CompanyNationalID, &c.CompanyEconomicCode,
		&c.CompanyPhone, &c.CompanyEmail, &c.CompanyWebsite, &c.CompanyAddress, &logoKey)
	if err != nil {
		return c, err
	}
	if settings.Header.LogoSource == "company" && logoKey != nil && s.Storage != nil {
		if f, err := s.Storage.Open(ctx, *logoKey); err == nil {
			b, _ := io.ReadAll(io.LimitReader(f, 4<<20))
			f.Close()
			c.CompanyLogo = "data:" + http.DetectContentType(b) + ";base64," + base64.StdEncoding.EncodeToString(b)
		}
	}
	return c, nil
}
