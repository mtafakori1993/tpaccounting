// Package accounts manages the hierarchical chart of accounts:
// group (1) → general (2) → subsidiary (3) → detailed (4).
package accounts

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

const MaxLevel = 4

var Types = []string{"asset", "liability", "equity", "revenue", "cost_of_sales", "expense"}

type Account struct {
	ID          int64            `json:"id"`
	ParentID    *int64           `json:"parent_id"`
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	NameEn      string           `json:"name_en"`
	Type        string           `json:"type"`
	Level       int              `json:"level"`
	IsActive    bool             `json:"is_active"`
	Description string           `json:"description"`
	HasChildren bool             `json:"has_children"`
	Postable    bool             `json:"postable"`
	Balance     *decimal.Decimal `json:"balance,omitempty"`
}

type Input struct {
	ParentID    *int64 `json:"parent_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	NameEn      string `json:"name_en"`
	Type        string `json:"type"`
	Description string `json:"description"`
	IsActive    *bool  `json:"is_active"`
}

const selectCols = `a.id, a.parent_id, a.code, a.name, a.name_en, a.type, a.level, a.is_active, a.description,
	EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)`

func scan(row pgx.Row, a *Account) error {
	err := row.Scan(&a.ID, &a.ParentID, &a.Code, &a.Name, &a.NameEn, &a.Type, &a.Level, &a.IsActive, &a.Description, &a.HasChildren)
	a.Postable = !a.HasChildren && a.Level >= 2
	return err
}

func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Account, error) {
	var a Account
	err := scan(q.QueryRow(ctx, `SELECT `+selectCols+` FROM accounts a WHERE a.company_id = $1 AND a.id = $2`, companyID, id), &a)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("account")
	}
	return &a, err
}

func validTypes(t string) bool {
	for _, x := range Types {
		if x == t {
			return true
		}
	}
	return false
}

func normalise(in *Input) {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.NameEn = strings.TrimSpace(in.NameEn)
	in.Description = strings.TrimSpace(in.Description)
}

func validateBasic(v *apperr.Validator, in Input) {
	v.Required("code", in.Code)
	v.Check(in.Code == "" || isDigits(in.Code), "code", "digits_only")
	v.MaxLen("code", in.Code, 20)
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 200)
	v.MaxLen("name_en", in.NameEn, 200)
	v.MaxLen("description", in.Description, 1000)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func hasLines(ctx context.Context, q db.DBTX, id int64) (bool, error) {
	var b bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM journal_entry_lines WHERE account_id = $1)`, id).Scan(&b)
	return b, err
}

// Create inserts an account, enforcing the hierarchy rules.
func Create(ctx context.Context, q db.DBTX, companyID int64, in Input) (int64, error) {
	normalise(&in)
	v := apperr.NewValidator()
	validateBasic(v, in)
	level := 1
	parentPath := "/"
	if in.ParentID != nil {
		parent, err := Get(ctx, q, companyID, *in.ParentID)
		if err != nil {
			v.Check(false, "parent_id", "invalid")
			return 0, v.Err()
		}
		v.Check(parent.Level < MaxLevel, "parent_id", "max_level")
		v.Check(parent.IsActive, "parent_id", "inactive")
		v.Check(in.Code == "" || (strings.HasPrefix(in.Code, parent.Code) && len(in.Code) > len(parent.Code)), "code", "code_prefix")
		in.Type = parent.Type // children always inherit the parent's type
		level = parent.Level + 1
		if !parent.HasChildren {
			used, err := hasLines(ctx, q, parent.ID)
			if err != nil {
				return 0, err
			}
			v.Check(!used, "parent_id", "parent_has_postings")
			var mapped bool
			if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_mappings WHERE account_id = $1 AND key NOT IN ('cash_parent', 'bank_parent'))`, parent.ID).Scan(&mapped); err != nil {
				return 0, err
			}
			v.Check(!mapped, "parent_id", "parent_is_mapped")
		}
		if err := q.QueryRow(ctx, `SELECT path FROM accounts WHERE id = $1`, parent.ID).Scan(&parentPath); err != nil {
			return 0, err
		}
	}
	v.Check(validTypes(in.Type), "type", "invalid")
	if err := v.Err(); err != nil {
		return 0, err
	}
	active := true
	if in.IsActive != nil {
		active = *in.IsActive
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO accounts (company_id, parent_id, code, name, name_en, type, level, is_active, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		companyID, in.ParentID, in.Code, in.Name, in.NameEn, in.Type, level, active, in.Description).Scan(&id); err != nil {
		return 0, err
	}
	_, err := q.Exec(ctx, `UPDATE accounts SET path = $2 WHERE id = $1`, id, parentPath+strconv.FormatInt(id, 10)+"/")
	return id, err
}

// Update edits an account. Parent and level are immutable; the code can change
// only while the account has no children.
func Update(ctx context.Context, q db.DBTX, companyID, id int64, in Input) error {
	normalise(&in)
	cur, err := Get(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	v := apperr.NewValidator()
	validateBasic(v, in)
	if in.Code != cur.Code {
		v.Check(!cur.HasChildren, "code", "has_children")
		if cur.ParentID != nil {
			parent, err := Get(ctx, q, companyID, *cur.ParentID)
			if err != nil {
				return err
			}
			v.Check(strings.HasPrefix(in.Code, parent.Code) && len(in.Code) > len(parent.Code), "code", "code_prefix")
		}
	}
	typ := cur.Type
	if in.Type != "" && in.Type != cur.Type {
		used, err := hasLines(ctx, q, id)
		if err != nil {
			return err
		}
		v.Check(cur.Level == 1 && !cur.HasChildren && !used, "type", "type_locked")
		v.Check(validTypes(in.Type), "type", "invalid")
		typ = in.Type
	}
	active := cur.IsActive
	if in.IsActive != nil {
		active = *in.IsActive
	}
	if !active && cur.IsActive {
		var activeChildren, mapped bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE parent_id = $1 AND is_active)`, id).Scan(&activeChildren); err != nil {
			return err
		}
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_mappings WHERE account_id = $1)`, id).Scan(&mapped); err != nil {
			return err
		}
		v.Check(!activeChildren, "is_active", "has_active_children")
		v.Check(!mapped, "is_active", "mapped_account")
	}
	if active && !cur.IsActive && cur.ParentID != nil {
		parent, err := Get(ctx, q, companyID, *cur.ParentID)
		if err != nil {
			return err
		}
		v.Check(parent.IsActive, "is_active", "parent_inactive")
	}
	if err := v.Err(); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `
		UPDATE accounts SET code = $3, name = $4, name_en = $5, type = $6, is_active = $7, description = $8, updated_at = now()
		WHERE company_id = $1 AND id = $2`, companyID, id, in.Code, in.Name, in.NameEn, typ, active, in.Description)
	return err
}

// NextCode suggests the next free child code under parent (or the next root code).
func NextCode(ctx context.Context, q db.DBTX, companyID int64, parentID *int64) (string, error) {
	prefix := ""
	width := 1
	if parentID != nil {
		p, err := Get(ctx, q, companyID, *parentID)
		if err != nil {
			return "", err
		}
		prefix = p.Code
		width = 2
	}
	rows, err := q.Query(ctx, `SELECT code FROM accounts WHERE company_id = $1 AND parent_id IS NOT DISTINCT FROM $2`, companyID, parentID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	maxSuffix := int64(0)
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return "", err
		}
		suffix := strings.TrimPrefix(c, prefix)
		if len(suffix) > width {
			width = len(suffix)
		}
		if n, err := strconv.ParseInt(suffix, 10, 64); err == nil && n > maxSuffix {
			maxSuffix = n
		}
	}
	return prefix + fmt.Sprintf("%0*d", width, maxSuffix+1), rows.Err()
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("accounts.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("accounts.view")).Get("/next-code", httpx.Handler(h.nextCode))
	r.With(identity.Require("accounts.view")).Get("/{id}", httpx.Handler(h.get))
	r.With(identity.Require("accounts.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("accounts.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("accounts.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

// list returns the whole chart (it is small) ordered by code, optionally with balances.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	f := db.NewFilter("a.company_id = $1", p.CompanyID)
	qv := r.URL.Query()
	if t := qv.Get("type"); t != "" {
		f.Add("a.type = $N", t)
	}
	if b := httpx.QueryBool(r, "active"); b != nil {
		f.Add("a.is_active = $N", *b)
	}
	if qv.Get("postable") == "true" {
		f.AddRaw("a.level >= 2 AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)")
	}
	if s := strings.TrimSpace(qv.Get("q")); s != "" {
		f.Add("(a.code LIKE $N || '%' OR a.name ILIKE "+f.Arg(httpx.Like(s))+" OR a.name_en ILIKE "+f.Arg(httpx.Like(s))+")", s)
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT `+selectCols+` FROM accounts a WHERE `+f.Where()+` ORDER BY a.code`, f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Account{}
	for rows.Next() {
		var a Account
		if err := scan(rows, &a); err != nil {
			return err
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if qv.Get("with_balance") == "true" {
		if err := h.attachBalances(r.Context(), p.CompanyID, items); err != nil {
			return err
		}
	}
	httpx.OK(w, items)
	return nil
}

// attachBalances computes posted debit−credit balances rolled up the hierarchy.
func (h *Handler) attachBalances(ctx context.Context, companyID int64, items []Account) error {
	rows, err := h.Pool.Query(ctx, `
		SELECT a.path, SUM(l.debit - l.credit)
		FROM journal_entry_lines l
		JOIN journal_entries e ON e.id = l.entry_id AND e.posted_at IS NOT NULL
		JOIN accounts a ON a.id = l.account_id
		WHERE e.company_id = $1 GROUP BY a.path`, companyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	sums := map[int64]decimal.Decimal{}
	for rows.Next() {
		var path string
		var bal decimal.Decimal
		if err := rows.Scan(&path, &bal); err != nil {
			return err
		}
		for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
			if id, err := strconv.ParseInt(part, 10, 64); err == nil {
				sums[id] = sums[id].Add(bal)
			}
		}
	}
	for i := range items {
		b := sums[items[i].ID]
		items[i].Balance = &b
	}
	return rows.Err()
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	a, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, a)
	return nil
}

func (h *Handler) nextCode(w http.ResponseWriter, r *http.Request) error {
	var parent *int64
	if v := httpx.QueryInt64(r, "parent_id"); v > 0 {
		parent = &v
	}
	code, err := NextCode(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, parent)
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]string{"code": code})
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "account", id, nil, in)
	})
	if err != nil {
		return err
	}
	a, err := Get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, a)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := Update(r.Context(), tx, p.CompanyID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "account", id, before, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if before.HasChildren {
			return apperr.Rule("has_children", "delete or move child accounts first")
		}
		used, err := hasLines(r.Context(), tx, id)
		if err != nil {
			return err
		}
		if used {
			return apperr.Rule("in_use", "the account has transactions; disable it instead")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM accounts WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "account", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
