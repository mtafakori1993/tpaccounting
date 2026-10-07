// Package parties manages customers, suppliers and other counterparties.
package parties

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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

type Party struct {
	ID                  int64           `json:"id"`
	Code                string          `json:"code"`
	Kind                string          `json:"kind"`
	Name                string          `json:"name"`
	IsCustomer          bool            `json:"is_customer"`
	IsSupplier          bool            `json:"is_supplier"`
	IsOther             bool            `json:"is_other"`
	NationalID          string          `json:"national_id"`
	EconomicCode        string          `json:"economic_code"`
	RegistrationNumber  string          `json:"registration_number"`
	Phone               string          `json:"phone"`
	Mobile              string          `json:"mobile"`
	Email               string          `json:"email"`
	Address             string          `json:"address"`
	PostalCode          string          `json:"postal_code"`
	BankName            string          `json:"bank_name"`
	BankAccountNumber   string          `json:"bank_account_number"`
	IBAN                string          `json:"iban"`
	Notes               string          `json:"notes"`
	ReceivableAccountID *int64          `json:"receivable_account_id"`
	PayableAccountID    *int64          `json:"payable_account_id"`
	IsActive            bool            `json:"is_active"`
	Balance             decimal.Decimal `json:"balance"`
	CreatedAt           time.Time       `json:"created_at"`
}

const cols = `p.id, p.code, p.kind, p.name, p.is_customer, p.is_supplier, p.is_other, p.national_id, p.economic_code,
	p.registration_number, p.phone, p.mobile, p.email, p.address, p.postal_code, p.bank_name, p.bank_account_number,
	p.iban, p.notes, p.receivable_account_id, p.payable_account_id, p.is_active,
	COALESCE((SELECT SUM(l.debit - l.credit) FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
	          WHERE l.party_id = p.id AND e.posted_at IS NOT NULL), 0), p.created_at`

func scan(row pgx.Row, p *Party) error {
	return row.Scan(&p.ID, &p.Code, &p.Kind, &p.Name, &p.IsCustomer, &p.IsSupplier, &p.IsOther, &p.NationalID, &p.EconomicCode,
		&p.RegistrationNumber, &p.Phone, &p.Mobile, &p.Email, &p.Address, &p.PostalCode, &p.BankName, &p.BankAccountNumber,
		&p.IBAN, &p.Notes, &p.ReceivableAccountID, &p.PayableAccountID, &p.IsActive, &p.Balance, &p.CreatedAt)
}

func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Party, error) {
	var p Party
	err := scan(q.QueryRow(ctx, `SELECT `+cols+` FROM parties p WHERE p.company_id = $1 AND p.id = $2`, companyID, id), &p)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("party")
	}
	return &p, err
}

func validate(ctx context.Context, q db.DBTX, companyID int64, p *Party) error {
	for _, s := range []*string{&p.Code, &p.Name, &p.NationalID, &p.EconomicCode, &p.RegistrationNumber, &p.Phone, &p.Mobile,
		&p.Email, &p.Address, &p.PostalCode, &p.BankName, &p.BankAccountNumber, &p.IBAN, &p.Notes} {
		*s = strings.TrimSpace(*s)
	}
	p.IBAN = strings.ToUpper(strings.ReplaceAll(p.IBAN, " ", ""))
	if p.Kind == "" {
		p.Kind = "company"
	}
	v := apperr.NewValidator()
	v.Required("name", p.Name)
	v.MaxLen("name", p.Name, 200)
	v.MaxLen("code", p.Code, 30)
	v.Check(p.Kind == "person" || p.Kind == "company", "kind", "invalid")
	v.Check(p.IsCustomer || p.IsSupplier || p.IsOther, "is_customer", "role_required")
	v.Check(p.NationalID == "" || isDigits(p.NationalID), "national_id", "digits_only")
	v.Check(p.Kind != "person" || p.NationalID == "" || len(p.NationalID) == 10, "national_id", "invalid_national_id")
	v.Check(p.EconomicCode == "" || isDigits(p.EconomicCode), "economic_code", "digits_only")
	v.Check(p.Email == "" || strings.Contains(p.Email, "@"), "email", "invalid_email")
	v.Check(p.IBAN == "" || (strings.HasPrefix(p.IBAN, "IR") && len(p.IBAN) == 26) || (!strings.HasPrefix(p.IBAN, "IR") && len(p.IBAN) >= 15 && len(p.IBAN) <= 34), "iban", "invalid_iban")
	v.MaxLen("notes", p.Notes, 2000)
	check := func(field string, id *int64, typ string) {
		if id == nil {
			return
		}
		var ok bool
		_ = q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts a WHERE a.company_id = $1 AND a.id = $2 AND a.type = $3 AND a.is_active
			AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id))`, companyID, *id, typ).Scan(&ok)
		v.Check(ok, field, "invalid")
	}
	check("receivable_account_id", p.ReceivableAccountID, "asset")
	check("payable_account_id", p.PayableAccountID, "liability")
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

func Create(ctx context.Context, q db.DBTX, companyID int64, p *Party) (int64, error) {
	if err := validate(ctx, q, companyID, p); err != nil {
		return 0, err
	}
	if p.Code == "" {
		n, err := db.NextNumber(ctx, q, companyID, "party", 0)
		if err != nil {
			return 0, err
		}
		p.Code = fmt.Sprintf("%04d", n)
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO parties (company_id, code, kind, name, is_customer, is_supplier, is_other, national_id, economic_code,
		       registration_number, phone, mobile, email, address, postal_code, bank_name, bank_account_number, iban, notes,
		       receivable_account_id, payable_account_id, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, TRUE)
		RETURNING id`, companyID, p.Code, p.Kind, p.Name, p.IsCustomer, p.IsSupplier, p.IsOther, p.NationalID, p.EconomicCode,
		p.RegistrationNumber, p.Phone, p.Mobile, p.Email, p.Address, p.PostalCode, p.BankName, p.BankAccountNumber, p.IBAN,
		p.Notes, p.ReceivableAccountID, p.PayableAccountID).Scan(&id)
	return id, err
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("parties.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("parties.view")).Get("/{id}", httpx.Handler(h.get))
	r.With(identity.Require("parties.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("parties.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("parties.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	lp := httpx.ParseList(r, "name", false)
	f := db.NewFilter("p.company_id = $1", p.CompanyID)
	switch r.URL.Query().Get("role") {
	case "customer":
		f.AddRaw("p.is_customer")
	case "supplier":
		f.AddRaw("p.is_supplier")
	case "other":
		f.AddRaw("p.is_other")
	}
	if b := httpx.QueryBool(r, "active"); b != nil {
		f.Add("p.is_active = $N", *b)
	}
	if lp.Q != "" {
		f.Add("(p.name ILIKE $N OR p.code ILIKE $N OR p.mobile ILIKE $N OR p.phone ILIKE $N OR p.national_id ILIKE $N OR p.economic_code ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM parties p WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return err
	}
	order := lp.OrderBy(map[string]string{"name": "p.name", "code": "p.code", "created_at": "p.created_at", "balance": "23"}, "p.name")
	rows, err := h.Pool.Query(r.Context(), `SELECT `+cols+` FROM parties p WHERE `+f.Where()+` ORDER BY `+order+`, p.id`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var items []Party
	for rows.Next() {
		var x Party
		if err := scan(rows, &x); err != nil {
			return err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, httpx.NewPage(items, total, lp))
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	x, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, x)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Party
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, &in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "party", id, nil, in)
	})
	if err != nil {
		return err
	}
	x, err := Get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, x)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in Party
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if in.Code == "" {
			in.Code = before.Code
		}
		if err := validate(r.Context(), tx, p.CompanyID, &in); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE parties SET code = $3, kind = $4, name = $5, is_customer = $6, is_supplier = $7, is_other = $8,
			       national_id = $9, economic_code = $10, registration_number = $11, phone = $12, mobile = $13, email = $14,
			       address = $15, postal_code = $16, bank_name = $17, bank_account_number = $18, iban = $19, notes = $20,
			       receivable_account_id = $21, payable_account_id = $22, is_active = $23, updated_at = now()
			WHERE company_id = $1 AND id = $2`, p.CompanyID, id, in.Code, in.Kind, in.Name, in.IsCustomer, in.IsSupplier, in.IsOther,
			in.NationalID, in.EconomicCode, in.RegistrationNumber, in.Phone, in.Mobile, in.Email, in.Address, in.PostalCode,
			in.BankName, in.BankAccountNumber, in.IBAN, in.Notes, in.ReceivableAccountID, in.PayableAccountID, in.IsActive); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "party", id, before, in)
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
		var used bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM journal_entry_lines WHERE party_id = $1)
			OR EXISTS (SELECT 1 FROM invoices WHERE party_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return apperr.Rule("in_use", "the party has transactions; disable it instead")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM parties WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "party", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
