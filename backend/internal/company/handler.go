// Package company manages the company profile, fiscal years, accounting
// periods and the automatic-accounting account mappings.
package company

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/storage"
)

type Company struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	NameEn             string `json:"name_en"`
	LegalName          string `json:"legal_name"`
	RegistrationNumber string `json:"registration_number"`
	NationalID         string `json:"national_id"`
	EconomicCode       string `json:"economic_code"`
	Address            string `json:"address"`
	PostalCode         string `json:"postal_code"`
	Phone              string `json:"phone"`
	Email              string `json:"email"`
	Website            string `json:"website"`
	HasLogo            bool   `json:"has_logo"`
	BaseCurrency       string `json:"base_currency"`
	AmountDecimals     int    `json:"amount_decimals"`
	Calendar           string `json:"calendar"`
	AllowNegativeStock bool   `json:"allow_negative_stock"`
}

type Handler struct {
	Pool    *pgxpool.Pool
	Storage storage.Storage
}

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("company.view")).Get("/", httpx.Handler(h.get))
	r.With(identity.Require("company.edit")).Put("/", httpx.Handler(h.update))
	r.Get("/logo", httpx.Handler(h.logo))
	r.With(identity.Require("company.edit")).Put("/logo", httpx.Handler(h.uploadLogo))
	r.With(identity.Require("company.edit")).Delete("/logo", httpx.Handler(h.deleteLogo))

	r.With(identity.Require("company.view")).Get("/fiscal-years", httpx.Handler(h.listYears))
	r.With(identity.Require("company.view")).Get("/fiscal-years/{id}", httpx.Handler(h.getYear))
	r.With(identity.Require("company.edit")).Post("/fiscal-years", httpx.Handler(h.createYear))
	r.With(identity.Require("company.edit")).Post("/fiscal-years/{id}/close", httpx.Handler(h.closeYear))
	r.With(identity.Require("company.edit")).Post("/fiscal-years/{id}/reopen", httpx.Handler(h.reopenYear))
	r.With(identity.Require("company.edit")).Put("/periods/{id}", httpx.Handler(h.lockPeriod))

	r.With(identity.Require("settings.view")).Get("/account-mappings", httpx.Handler(h.getMappings))
	r.With(identity.Require("settings.edit")).Put("/account-mappings", httpx.Handler(h.setMappings))
}

func Get(ctx context.Context, q db.DBTX, id int64) (*Company, error) {
	var c Company
	err := q.QueryRow(ctx, `
		SELECT id, name, name_en, legal_name, registration_number, national_id, economic_code, address, postal_code,
		       phone, email, website, logo_key IS NOT NULL, base_currency, amount_decimals, calendar, allow_negative_stock
		FROM companies WHERE id = $1`, id).Scan(&c.ID, &c.Name, &c.NameEn, &c.LegalName, &c.RegistrationNumber, &c.NationalID,
		&c.EconomicCode, &c.Address, &c.PostalCode, &c.Phone, &c.Email, &c.Website, &c.HasLogo, &c.BaseCurrency,
		&c.AmountDecimals, &c.Calendar, &c.AllowNegativeStock)
	return &c, err
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	c, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, c)
	return nil
}

// Validate checks company profile fields.
func Validate(c *Company) error {
	trim := func(s *string) { *s = strings.TrimSpace(*s) }
	for _, s := range []*string{&c.Name, &c.NameEn, &c.LegalName, &c.RegistrationNumber, &c.NationalID, &c.EconomicCode,
		&c.Address, &c.PostalCode, &c.Phone, &c.Email, &c.Website, &c.BaseCurrency} {
		trim(s)
	}
	c.BaseCurrency = strings.ToUpper(c.BaseCurrency)
	if c.BaseCurrency == "" {
		c.BaseCurrency = "IRR"
	}
	if c.Calendar == "" {
		c.Calendar = "jalali"
	}
	v := apperr.NewValidator()
	v.Required("name", c.Name)
	v.MaxLen("name", c.Name, 200)
	v.MaxLen("address", c.Address, 500)
	v.Check(c.Email == "" || strings.Contains(c.Email, "@"), "email", "invalid_email")
	v.Check(len(c.BaseCurrency) == 3, "base_currency", "invalid")
	v.Check(c.AmountDecimals >= 0 && c.AmountDecimals <= 2, "amount_decimals", "invalid")
	v.Check(c.Calendar == "jalali" || c.Calendar == "gregorian", "calendar", "invalid")
	v.Check(c.NationalID == "" || isDigits(c.NationalID), "national_id", "digits_only")
	v.Check(c.EconomicCode == "" || isDigits(c.EconomicCode), "economic_code", "digits_only")
	return v.Err()
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Company
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := Validate(&in); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE companies SET name = $2, name_en = $3, legal_name = $4, registration_number = $5, national_id = $6,
			       economic_code = $7, address = $8, postal_code = $9, phone = $10, email = $11, website = $12,
			       base_currency = $13, amount_decimals = $14, calendar = $15, allow_negative_stock = $16, updated_at = now()
			WHERE id = $1`, p.CompanyID, in.Name, in.NameEn, in.LegalName, in.RegistrationNumber, in.NationalID, in.EconomicCode,
			in.Address, in.PostalCode, in.Phone, in.Email, in.Website, in.BaseCurrency, in.AmountDecimals, in.Calendar,
			in.AllowNegativeStock); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "company", p.CompanyID, before, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

var logoTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

func (h *Handler) logo(w http.ResponseWriter, r *http.Request) error {
	var key *string
	if err := h.Pool.QueryRow(r.Context(), `SELECT logo_key FROM companies WHERE id = $1`, identity.From(r.Context()).CompanyID).Scan(&key); err != nil {
		return err
	}
	if key == nil {
		return apperr.NotFound("logo")
	}
	f, err := h.Storage.Open(r.Context(), *key)
	if err != nil {
		return apperr.NotFound("logo")
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	w.Header().Set("Content-Type", http.DetectContentType(head[:n]))
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(head[:n])
	_, err = io.Copy(w, f)
	return err
}

func (h *Handler) uploadLogo(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	const maxLogo = 2 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxLogo+4096)
	file, _, err := r.FormFile("file")
	if err != nil {
		return apperr.BadRequest("file_required", "a logo file is required")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLogo+1))
	if err != nil {
		return err
	}
	if len(data) > maxLogo {
		return apperr.Rule("file_too_large", "the file is too large").WithParams("max_mb", 2)
	}
	if !logoTypes[http.DetectContentType(data)] {
		return apperr.Rule("invalid_file_type", "the logo must be PNG, JPEG or WebP")
	}
	key := storage.NewKey("logos")
	if _, err := h.Storage.Put(r.Context(), key, bytes.NewReader(data), maxLogo); err != nil {
		return err
	}
	var old *string
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT logo_key FROM companies WHERE id = $1 FOR UPDATE`, p.CompanyID).Scan(&old); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE companies SET logo_key = $2, updated_at = now() WHERE id = $1`, p.CompanyID, key); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update_logo", "company", p.CompanyID, nil, nil)
	})
	if err != nil {
		_ = h.Storage.Delete(r.Context(), key)
		return err
	}
	if old != nil {
		_ = h.Storage.Delete(r.Context(), *old)
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) deleteLogo(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var old *string
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `SELECT logo_key FROM companies WHERE id = $1 FOR UPDATE`, p.CompanyID).Scan(&old); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE companies SET logo_key = NULL WHERE id = $1`, p.CompanyID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete_logo", "company", p.CompanyID, nil, nil)
	})
	if err != nil {
		return err
	}
	if old != nil {
		_ = h.Storage.Delete(r.Context(), *old)
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) listYears(w http.ResponseWriter, r *http.Request) error {
	ys, err := listFiscalYears(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, ys)
	return nil
}

func (h *Handler) getYear(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	y, err := getFiscalYear(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, y)
	return nil
}

func (h *Handler) createYear(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in FiscalYearInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = CreateFiscalYear(r.Context(), tx, p.CompanyID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "fiscal_year", id, nil, in)
	})
	if err != nil {
		return err
	}
	y, err := getFiscalYear(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, y)
	return nil
}

func (h *Handler) yearAction(w http.ResponseWriter, r *http.Request, action string, fn func(ctx context.Context, q db.DBTX, companyID, userID, id int64) error) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := fn(r.Context(), tx, p.CompanyID, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, action, "fiscal_year", id, nil, nil)
	})
	if err != nil {
		return err
	}
	return h.getYear(w, r)
}

func (h *Handler) closeYear(w http.ResponseWriter, r *http.Request) error {
	return h.yearAction(w, r, "close", CloseFiscalYear)
}

func (h *Handler) reopenYear(w http.ResponseWriter, r *http.Request) error {
	return h.yearAction(w, r, "reopen", ReopenFiscalYear)
}

func (h *Handler) lockPeriod(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		IsLocked bool `json:"is_locked"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `UPDATE accounting_periods SET is_locked = $3 WHERE id = $1 AND company_id = $2`, id, p.CompanyID, in.IsLocked)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("period")
		}
		return audit.Record(r.Context(), tx, "update", "accounting_period", id, nil, in)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) getMappings(w http.ResponseWriter, r *http.Request) error {
	m, err := listMappings(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, m)
	return nil
}

func (h *Handler) setMappings(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in map[string]int64
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := listMappings(r.Context(), tx, p.CompanyID)
		if err != nil {
			return err
		}
		if err := SetMappings(r.Context(), tx, p.CompanyID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "account_mappings", 0, before, in)
	})
	if err != nil {
		return err
	}
	return h.getMappings(w, r)
}
