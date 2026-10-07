// Package setup implements the first-run wizard: it creates the company, its
// first fiscal year, the default chart of accounts and the administrator.
package setup

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/auth"
	"github.com/tafakorpardazan/tpaccounting/internal/company"
	"github.com/tafakorpardazan/tpaccounting/internal/inventory"
	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/seed"
	"github.com/tafakorpardazan/tpaccounting/internal/users"
)

type Handler struct {
	Pool *pgxpool.Pool
	Auth *auth.Service
}

type Input struct {
	Company    company.Company          `json:"company"`
	FiscalYear *company.FiscalYearInput `json:"fiscal_year"`
	SeedChart  *bool                    `json:"seed_chart"`
	Admin      struct {
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	} `json:"admin"`
	Language string `json:"language"`
}

func (h *Handler) needsSetup(r *http.Request) (bool, error) {
	var n int
	err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM companies`).Scan(&n)
	return n == 0, err
}

func (h *Handler) Status(w http.ResponseWriter, r *http.Request) error {
	needs, err := h.needsSetup(r)
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]bool{"needs_setup": needs})
	return nil
}

func (h *Handler) Run(w http.ResponseWriter, r *http.Request) error {
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := company.Validate(&in.Company); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		// Serialise concurrent setup attempts and make setup one-shot.
		if _, err := tx.Exec(r.Context(), `LOCK TABLE companies IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM companies`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("already_setup", "the system is already set up")
		}
		c := in.Company
		var companyID int64
		if err := tx.QueryRow(r.Context(), `
			INSERT INTO companies (name, name_en, legal_name, registration_number, national_id, economic_code, address, postal_code,
			                       phone, email, website, base_currency, amount_decimals, calendar)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
			c.Name, c.NameEn, c.LegalName, c.RegistrationNumber, c.NationalID, c.EconomicCode, c.Address, c.PostalCode, c.Phone,
			c.Email, c.Website, c.BaseCurrency, c.AmountDecimals, c.Calendar).Scan(&companyID); err != nil {
			return err
		}
		fy := company.DefaultFiscalYear(civil.Today(), c.Calendar)
		if in.FiscalYear != nil && !in.FiscalYear.StartDate.IsZero() {
			fy = *in.FiscalYear
		}
		if _, err := company.CreateFiscalYear(r.Context(), tx, companyID, fy); err != nil {
			return err
		}
		if in.SeedChart == nil || *in.SeedChart {
			if err := seed.Company(r.Context(), tx, companyID); err != nil {
				return err
			}
		} else if _, err := inventory.CreateWarehouse(r.Context(), tx, companyID, inventory.Warehouse{Code: "01", Name: "انبار مرکزی", IsDefault: true}); err != nil {
			return err
		}
		var adminRole int64
		if err := tx.QueryRow(r.Context(), `SELECT id FROM roles WHERE code = 'admin'`).Scan(&adminRole); err != nil {
			return err
		}
		userID, err := users.Create(r.Context(), tx, companyID, users.Input{
			Email: in.Admin.Email, FullName: in.Admin.FullName, Password: in.Admin.Password, RoleID: adminRole,
		})
		if err != nil {
			if e, ok := apperr.As(err); ok && e.Fields != nil {
				prefixed := map[string]string{}
				for k, v := range e.Fields {
					prefixed["admin."+k] = v
				}
				e.Fields = prefixed
			}
			return err
		}
		lang := in.Language
		if lang != "en" {
			lang = "fa"
		}
		if _, err := tx.Exec(r.Context(), `UPDATE users SET language = $2 WHERE id = $1`, userID, lang); err != nil {
			return err
		}
		if err := pdf.EnsureDefaults(r.Context(), tx); err != nil {
			return err
		}
		ctx := identity.WithPrincipal(r.Context(), &identity.Principal{UserID: userID, CompanyID: companyID})
		if err := audit.Record(ctx, tx, "setup", "company", companyID, nil, c); err != nil {
			return err
		}
		return h.Auth.StartSession(ctx, tx, w, userID, companyID)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
