// Package costcenters manages cost centers (departments, projects) that
// transactions can be tagged with.
package costcenters

import (
	"context"
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
)

type CostCenter struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Kind        string `json:"kind"` // cost_center | department | project
	Name        string `json:"name"`
	NameEn      string `json:"name_en"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
}

func get(ctx context.Context, q db.DBTX, companyID, id int64) (*CostCenter, error) {
	var c CostCenter
	err := q.QueryRow(ctx, `SELECT id, code, kind, name, name_en, description, is_active FROM cost_centers WHERE company_id = $1 AND id = $2`, companyID, id).
		Scan(&c.ID, &c.Code, &c.Kind, &c.Name, &c.NameEn, &c.Description, &c.IsActive)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("cost_center")
	}
	return &c, err
}

func validate(c *CostCenter) error {
	c.Code, c.Name, c.NameEn, c.Description = strings.TrimSpace(c.Code), strings.TrimSpace(c.Name), strings.TrimSpace(c.NameEn), strings.TrimSpace(c.Description)
	if c.Kind == "" {
		c.Kind = "cost_center"
	}
	v := apperr.NewValidator()
	v.Check(c.Kind == "cost_center" || c.Kind == "department" || c.Kind == "project", "kind", "invalid")
	v.Required("code", c.Code)
	v.MaxLen("code", c.Code, 30)
	v.Required("name", c.Name)
	v.MaxLen("name", c.Name, 200)
	return v.Err()
}

// Create inserts a cost center (used by setup seeding too).
func Create(ctx context.Context, q db.DBTX, companyID int64, c CostCenter) (int64, error) {
	if err := validate(&c); err != nil {
		return 0, err
	}
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO cost_centers (company_id, code, kind, name, name_en, description) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		companyID, c.Code, c.Kind, c.Name, c.NameEn, c.Description).Scan(&id)
	return id, err
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("cost_centers.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("cost_centers.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("cost_centers.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("cost_centers.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	f := db.NewFilter("company_id = $1", p.CompanyID)
	if b := httpx.QueryBool(r, "active"); b != nil {
		f.Add("is_active = $N", *b)
	}
	if k := r.URL.Query().Get("kind"); k != "" {
		f.Add("kind = $N", k)
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT id, code, kind, name, name_en, description, is_active FROM cost_centers WHERE `+f.Where()+` ORDER BY code`, f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []CostCenter{}
	for rows.Next() {
		var c CostCenter
		if err := rows.Scan(&c.ID, &c.Code, &c.Kind, &c.Name, &c.NameEn, &c.Description, &c.IsActive); err != nil {
			return err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in CostCenter
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "cost_center", id, nil, in)
	})
	if err != nil {
		return err
	}
	c, err := get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, c)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in CostCenter
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := validate(&in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE cost_centers SET code = $3, name = $4, name_en = $5, description = $6, is_active = $7, kind = $8
			WHERE company_id = $1 AND id = $2`, p.CompanyID, id, in.Code, in.Name, in.NameEn, in.Description, in.IsActive, in.Kind); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "cost_center", id, before, in)
	})
	if err != nil {
		return err
	}
	c, err := get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, c)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM cost_centers WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "cost_center", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
