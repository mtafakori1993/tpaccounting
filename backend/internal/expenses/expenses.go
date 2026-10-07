// Package expenses records operating expenses with optional automatic accounting.
package expenses

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
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/treasury"
)

type Category struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	NameEn      string `json:"name_en"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	IsActive    bool   `json:"is_active"`
}

type Expense struct {
	ID                  int64           `json:"id"`
	Number              int64           `json:"number"`
	Date                civil.Date      `json:"date"`
	CategoryID          int64           `json:"category_id"`
	CategoryName        string          `json:"category_name"`
	AccountID           int64           `json:"account_id"`
	AccountName         string          `json:"account_name"`
	Amount              decimal.Decimal `json:"amount"`
	Description         string          `json:"description"`
	Reference           string          `json:"reference"`
	CostCenterID        *int64          `json:"cost_center_id"`
	CostCenterName      *string         `json:"cost_center_name"`
	PaymentMethod       string          `json:"payment_method"`
	TreasuryAccountID   *int64          `json:"treasury_account_id"`
	TreasuryAccountName *string         `json:"treasury_account_name"`
	PartyID             *int64          `json:"party_id"`
	PartyName           *string         `json:"party_name"`
	GenerateEntry       bool            `json:"generate_entry"`
	Status              string          `json:"status"`
	JournalEntryID      *int64          `json:"journal_entry_id"`
	JournalNumber       *int64          `json:"journal_number"`
	CreatedAt           time.Time       `json:"created_at"`
}

type Input struct {
	Date              civil.Date      `json:"date"`
	CategoryID        int64           `json:"category_id"`
	AccountID         *int64          `json:"account_id"`
	Amount            decimal.Decimal `json:"amount"`
	Description       string          `json:"description"`
	Reference         string          `json:"reference"`
	CostCenterID      *int64          `json:"cost_center_id"`
	PaymentMethod     string          `json:"payment_method"`
	TreasuryAccountID *int64          `json:"treasury_account_id"`
	PartyID           *int64          `json:"party_id"`
	GenerateEntry     *bool           `json:"generate_entry"`
	Post              bool            `json:"post"`
}

const cols = `e.id, e.number, e.date, e.category_id, c.name, e.account_id, a.code || ' - ' || a.name, e.amount, e.description, e.reference,
	e.cost_center_id, cc.name, e.payment_method, e.treasury_account_id, t.name, e.party_id, p.name, e.generate_entry, e.status,
	e.journal_entry_id, je.number, e.created_at`

const joins = ` FROM expenses e JOIN expense_categories c ON c.id = e.category_id JOIN accounts a ON a.id = e.account_id
	LEFT JOIN cost_centers cc ON cc.id = e.cost_center_id LEFT JOIN treasury_accounts t ON t.id = e.treasury_account_id
	LEFT JOIN parties p ON p.id = e.party_id LEFT JOIN journal_entries je ON je.id = e.journal_entry_id`

type rowScanner interface{ Scan(...any) error }

func scan(s rowScanner, x *Expense) error {
	return s.Scan(&x.ID, &x.Number, &x.Date, &x.CategoryID, &x.CategoryName, &x.AccountID, &x.AccountName, &x.Amount, &x.Description,
		&x.Reference, &x.CostCenterID, &x.CostCenterName, &x.PaymentMethod, &x.TreasuryAccountID, &x.TreasuryAccountName, &x.PartyID,
		&x.PartyName, &x.GenerateEntry, &x.Status, &x.JournalEntryID, &x.JournalNumber, &x.CreatedAt)
}

func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Expense, error) {
	var x Expense
	err := scan(q.QueryRow(ctx, `SELECT `+cols+joins+` WHERE e.company_id = $1 AND e.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("expense")
	}
	return &x, err
}

// resolve validates input and returns the expense account to use.
func resolve(ctx context.Context, q db.DBTX, companyID int64, in *Input) (int64, error) {
	in.Description, in.Reference = strings.TrimSpace(in.Description), strings.TrimSpace(in.Reference)
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return 0, err
	}
	v := apperr.NewValidator()
	v.Check(!in.Date.IsZero(), "date", "required")
	v.RequiredID("category_id", in.CategoryID)
	v.Check(in.Amount.IsPositive(), "amount", "positive")
	v.Check(in.Amount.Equal(in.Amount.Round(places)), "amount", "too_many_decimals")
	v.Check(in.PaymentMethod == "cash" || in.PaymentMethod == "bank" || in.PaymentMethod == "credit", "payment_method", "invalid")
	if in.PaymentMethod == "credit" {
		v.Check(in.PartyID != nil, "party_id", "required")
		in.TreasuryAccountID = nil
	} else {
		v.Check(in.TreasuryAccountID != nil, "treasury_account_id", "required")
	}
	v.MaxLen("description", in.Description, 500)
	if err := v.Err(); err != nil {
		return 0, err
	}
	var catAccount int64
	var active bool
	err = q.QueryRow(ctx, `SELECT account_id, is_active FROM expense_categories WHERE company_id = $1 AND id = $2`, companyID, in.CategoryID).Scan(&catAccount, &active)
	if db.IsNoRows(err) || (err == nil && !active) {
		v.Check(false, "category_id", "invalid")
		return 0, v.Err()
	}
	if err != nil {
		return 0, err
	}
	if in.TreasuryAccountID != nil {
		var kind string
		if err := q.QueryRow(ctx, `SELECT kind FROM treasury_accounts WHERE company_id = $1 AND id = $2`, companyID, *in.TreasuryAccountID).Scan(&kind); err != nil {
			v.Check(false, "treasury_account_id", "invalid")
			return 0, v.Err()
		}
		v.Check(kind == in.PaymentMethod, "treasury_account_id", "invalid")
	}
	if err := v.Err(); err != nil {
		return 0, err
	}
	if in.AccountID != nil {
		return *in.AccountID, nil
	}
	return catAccount, nil
}

func Create(ctx context.Context, q db.DBTX, companyID, userID int64, in Input) (int64, error) {
	account, err := resolve(ctx, q, companyID, &in)
	if err != nil {
		return 0, err
	}
	gen := in.GenerateEntry == nil || *in.GenerateEntry
	n, err := db.NextNumber(ctx, q, companyID, "expense", 0)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO expenses (company_id, number, date, category_id, account_id, amount, description, reference, cost_center_id,
		                      payment_method, treasury_account_id, party_id, generate_entry, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14) RETURNING id`,
		companyID, n, in.Date, in.CategoryID, account, in.Amount, in.Description, in.Reference, in.CostCenterID, in.PaymentMethod,
		in.TreasuryAccountID, in.PartyID, gen, userID).Scan(&id); err != nil {
		return 0, err
	}
	if in.Post {
		return id, Post(ctx, q, companyID, userID, id)
	}
	return id, nil
}

func Update(ctx context.Context, q db.DBTX, companyID, userID, id int64, in Input) error {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM expenses WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&status); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("expense")
		}
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be edited")
	}
	account, err := resolve(ctx, q, companyID, &in)
	if err != nil {
		return err
	}
	gen := in.GenerateEntry == nil || *in.GenerateEntry
	if _, err := q.Exec(ctx, `
		UPDATE expenses SET date = $3, category_id = $4, account_id = $5, amount = $6, description = $7, reference = $8,
		       cost_center_id = $9, payment_method = $10, treasury_account_id = $11, party_id = $12, generate_entry = $13
		WHERE company_id = $1 AND id = $2`, companyID, id, in.Date, in.CategoryID, account, in.Amount, in.Description, in.Reference,
		in.CostCenterID, in.PaymentMethod, in.TreasuryAccountID, in.PartyID, gen); err != nil {
		return err
	}
	if in.Post {
		return Post(ctx, q, companyID, userID, id)
	}
	return nil
}

// Post finalises an expense and, if enabled, books it to the ledger.
func Post(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM expenses WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&status); err != nil {
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be posted")
	}
	x, err := Get(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	var entryID *int64
	if x.GenerateEntry {
		var credit int64
		if x.PaymentMethod == "credit" {
			var override *int64
			if err := q.QueryRow(ctx, `SELECT payable_account_id FROM parties WHERE id = $1`, *x.PartyID).Scan(&override); err != nil {
				return err
			}
			if override != nil {
				credit = *override
			} else if credit, err = ledger.Mapping(ctx, q, companyID, ledger.MapPayable); err != nil {
				return err
			}
		} else {
			t, err := treasury.GetAccount(ctx, q, companyID, *x.TreasuryAccountID)
			if err != nil {
				return err
			}
			if t.Kind == "cash" && t.Balance.LessThan(x.Amount) {
				return apperr.Rule("insufficient_funds", "the cash balance is not sufficient").WithParams("balance", t.Balance.String())
			}
			credit = t.AccountID
		}
		var b ledger.Builder
		var creditParty *int64
		if x.PaymentMethod == "credit" {
			creditParty = x.PartyID
		}
		b.Debit(x.AccountID, nil, x.CostCenterID, x.Description, x.Amount)
		b.Credit(credit, creditParty, nil, x.Description, x.Amount)
		desc := x.Description
		if desc == "" {
			desc = x.CategoryName
		}
		eid, err := ledger.PostAuto(ctx, q, companyID, userID, x.Date, desc, fmt.Sprintf("EXP-%d", x.Number), b.Lines(),
			ledger.Source{Type: "expense", ID: id})
		if err != nil {
			return err
		}
		entryID = &eid
	} else if _, err := ledger.ResolveFiscalYear(ctx, q, companyID, x.Date); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE expenses SET status = 'posted', posted_at = now(), journal_entry_id = $2 WHERE id = $1`, id, entryID)
	return err
}

func Cancel(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	var entryID *int64
	if err := q.QueryRow(ctx, `SELECT status, journal_entry_id FROM expenses WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&status, &entryID); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("expense")
		}
		return err
	}
	if status == "cancelled" {
		return apperr.Rule("already_cancelled", "the expense is already cancelled")
	}
	if entryID != nil {
		if _, err := ledger.Cancel(ctx, q, companyID, userID, *entryID, civil.Date{}, true); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `UPDATE expenses SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, id)
	return err
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("expenses.view")).Get("/categories", httpx.Handler(h.listCategories))
	r.With(identity.Require("expenses.create")).Post("/categories", httpx.Handler(h.saveCategory))
	r.With(identity.Require("expenses.edit")).Put("/categories/{id}", httpx.Handler(h.saveCategory))

	r.With(identity.Require("expenses.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("expenses.view")).Get("/{id}", httpx.Handler(h.get))
	r.With(identity.Require("expenses.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("expenses.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("expenses.post")).Post("/{id}/post", httpx.Handler(h.post))
	r.With(identity.Require("expenses.cancel")).Post("/{id}/cancel", httpx.Handler(h.cancel))
	r.With(identity.Require("expenses.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

// CreateCategory inserts a category (also used by setup).
func CreateCategory(ctx context.Context, q db.DBTX, companyID int64, c Category) (int64, error) {
	c.Name, c.NameEn = strings.TrimSpace(c.Name), strings.TrimSpace(c.NameEn)
	v := apperr.NewValidator()
	v.Required("name", c.Name)
	v.RequiredID("account_id", c.AccountID)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if err := checkExpenseAccount(ctx, q, companyID, c.AccountID); err != nil {
		return 0, err
	}
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO expense_categories (company_id, name, name_en, account_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		companyID, c.Name, c.NameEn, c.AccountID).Scan(&id)
	return id, err
}

func checkExpenseAccount(ctx context.Context, q db.DBTX, companyID, accountID int64) error {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts a WHERE a.company_id = $1 AND a.id = $2 AND a.type IN ('expense', 'cost_of_sales')
		AND a.is_active AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id))`, companyID, accountID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		v := apperr.NewValidator()
		v.Check(false, "account_id", "invalid")
		return v.Err()
	}
	return nil
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `SELECT c.id, c.name, c.name_en, c.account_id, a.code, a.name, c.is_active
		FROM expense_categories c JOIN accounts a ON a.id = c.account_id WHERE c.company_id = $1 ORDER BY c.name`, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.NameEn, &c.AccountID, &c.AccountCode, &c.AccountName, &c.IsActive); err != nil {
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

func (h *Handler) saveCategory(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Category
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	isCreate := r.Method == http.MethodPost
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if isCreate {
			id, err := CreateCategory(r.Context(), tx, p.CompanyID, in)
			if err != nil {
				return err
			}
			in.ID, in.IsActive = id, true
			return audit.Record(r.Context(), tx, "create", "expense_category", id, nil, in)
		}
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		in.ID = id
		in.Name, in.NameEn = strings.TrimSpace(in.Name), strings.TrimSpace(in.NameEn)
		v := apperr.NewValidator()
		v.Required("name", in.Name)
		if err := v.Err(); err != nil {
			return err
		}
		if err := checkExpenseAccount(r.Context(), tx, p.CompanyID, in.AccountID); err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `UPDATE expense_categories SET name = $3, name_en = $4, account_id = $5, is_active = $6 WHERE company_id = $1 AND id = $2`,
			p.CompanyID, id, in.Name, in.NameEn, in.AccountID, in.IsActive)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("expense_category")
		}
		return audit.Record(r.Context(), tx, "update", "expense_category", id, nil, in)
	})
	if err != nil {
		return err
	}
	if isCreate {
		httpx.Created(w, in)
	} else {
		httpx.OK(w, in)
	}
	return nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	lp := httpx.ParseList(r, "date", true)
	f := db.NewFilter("e.company_id = $1", p.CompanyID)
	qv := r.URL.Query()
	if s := qv.Get("status"); s != "" {
		f.Add("e.status = $N", s)
	}
	if c := httpx.QueryInt64(r, "category_id"); c > 0 {
		f.Add("e.category_id = $N", c)
	}
	if c := httpx.QueryInt64(r, "cost_center_id"); c > 0 {
		f.Add("e.cost_center_id = $N", c)
	}
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	if !from.IsZero() {
		f.Add("e.date >= $N", from)
	}
	if !to.IsZero() {
		f.Add("e.date <= $N", to)
	}
	if lp.Q != "" {
		f.Add("(e.description ILIKE $N OR e.reference ILIKE $N OR c.name ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM expenses e JOIN expense_categories c ON c.id = e.category_id WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return err
	}
	order := lp.OrderBy(map[string]string{"date": "e.date", "number": "e.number", "amount": "e.amount", "category": "c.name"}, "e.date")
	rows, err := h.Pool.Query(r.Context(), `SELECT `+cols+joins+` WHERE `+f.Where()+` ORDER BY `+order+`, e.id DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var items []Expense
	for rows.Next() {
		var x Expense
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
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "expenses.post"); err != nil {
			return err
		}
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "expense", id, nil, in)
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
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "expenses.post"); err != nil {
			return err
		}
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := Update(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "expense", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request, name string, fn func(context.Context, db.DBTX, int64, int64, int64) error) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := fn(r.Context(), tx, p.CompanyID, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, name, "expense", id, nil, nil)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "post", Post)
}
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "cancel", Cancel)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM expenses WHERE company_id = $1 AND id = $2 AND status = 'draft'`, p.CompanyID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.Rule("not_deletable", "only drafts can be deleted")
		}
		return audit.Record(r.Context(), tx, "delete", "expense", id, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
