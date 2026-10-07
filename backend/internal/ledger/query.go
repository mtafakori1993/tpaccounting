package ledger

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

type Line struct {
	ID             int64           `json:"id"`
	LineNo         int             `json:"line_no"`
	AccountID      int64           `json:"account_id"`
	AccountCode    string          `json:"account_code"`
	AccountName    string          `json:"account_name"`
	AccountNameEn  string          `json:"account_name_en"`
	PartyID        *int64          `json:"party_id"`
	PartyName      *string         `json:"party_name"`
	CostCenterID   *int64          `json:"cost_center_id"`
	CostCenterName *string         `json:"cost_center_name"`
	Description    string          `json:"description"`
	Debit          decimal.Decimal `json:"debit"`
	Credit         decimal.Decimal `json:"credit"`
}

type Entry struct {
	ID            int64           `json:"id"`
	FiscalYearID  int64           `json:"fiscal_year_id"`
	Number        int64           `json:"number"`
	Date          civil.Date      `json:"date"`
	Description   string          `json:"description"`
	Reference     string          `json:"reference"`
	Status        string          `json:"status"`
	SourceType    string          `json:"source_type"`
	SourceID      *int64          `json:"source_id"`
	ReversalOfID  *int64          `json:"reversal_of_id"`
	ReversalOfNo  *int64          `json:"reversal_of_number"`
	ReversedByID  *int64          `json:"reversed_by_id"`
	ReversedByNo  *int64          `json:"reversed_by_number"`
	TotalDebit    decimal.Decimal `json:"total_debit"`
	TotalCredit   decimal.Decimal `json:"total_credit"`
	PostedAt      *time.Time      `json:"posted_at"`
	PostedByName  *string         `json:"posted_by_name"`
	CancelledAt   *time.Time      `json:"cancelled_at"`
	CreatedByName *string         `json:"created_by_name"`
	CreatedAt     time.Time       `json:"created_at"`
	Lines         []Line          `json:"lines,omitempty"`
}

const entryColumns = `
	e.id, e.fiscal_year_id, e.number, e.date, e.description, e.reference, e.status, e.source_type, e.source_id,
	e.reversal_of_id, ro.number, e.reversed_by_id, rb.number, e.total_debit, e.total_credit,
	e.posted_at, pu.full_name, e.cancelled_at, cu.full_name, e.created_at`

const entryJoins = `
	LEFT JOIN journal_entries ro ON ro.id = e.reversal_of_id
	LEFT JOIN journal_entries rb ON rb.id = e.reversed_by_id
	LEFT JOIN users pu ON pu.id = e.posted_by
	LEFT JOIN users cu ON cu.id = e.created_by`

type scanner interface{ Scan(dest ...any) error }

func scanEntry(s scanner, e *Entry) error {
	return s.Scan(&e.ID, &e.FiscalYearID, &e.Number, &e.Date, &e.Description, &e.Reference, &e.Status, &e.SourceType,
		&e.SourceID, &e.ReversalOfID, &e.ReversalOfNo, &e.ReversedByID, &e.ReversedByNo, &e.TotalDebit, &e.TotalCredit,
		&e.PostedAt, &e.PostedByName, &e.CancelledAt, &e.CreatedByName, &e.CreatedAt)
}

// Get loads an entry with its lines.
func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Entry, error) {
	var e Entry
	err := scanEntry(q.QueryRow(ctx, `SELECT `+entryColumns+` FROM journal_entries e `+entryJoins+`
		WHERE e.id = $1 AND e.company_id = $2`, id, companyID), &e)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("journal_entry")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT l.id, l.line_no, l.account_id, a.code, a.name, a.name_en, l.party_id, p.name,
		       l.cost_center_id, cc.name, l.description, l.debit, l.credit
		FROM journal_entry_lines l
		JOIN accounts a ON a.id = l.account_id
		LEFT JOIN parties p ON p.id = l.party_id
		LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		WHERE l.entry_id = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	e.Lines = []Line{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.LineNo, &l.AccountID, &l.AccountCode, &l.AccountName, &l.AccountNameEn, &l.PartyID,
			&l.PartyName, &l.CostCenterID, &l.CostCenterName, &l.Description, &l.Debit, &l.Credit); err != nil {
			return nil, err
		}
		e.Lines = append(e.Lines, l)
	}
	return &e, rows.Err()
}

type ListFilter struct {
	Status     string
	SourceType string
	From, To   civil.Date
	AccountID  int64
	PartyID    int64
}

// List returns a page of entries.
func List(ctx context.Context, q db.DBTX, companyID int64, f ListFilter, lp httpx.ListParams) (httpx.Page[Entry], error) {
	w := db.NewFilter("e.company_id = $1", companyID)
	if f.Status != "" {
		w.Add("e.status = $N", f.Status)
	}
	if f.SourceType != "" {
		w.Add("e.source_type = $N", f.SourceType)
	}
	if !f.From.IsZero() {
		w.Add("e.date >= $N", f.From)
	}
	if !f.To.IsZero() {
		w.Add("e.date <= $N", f.To)
	}
	if f.AccountID > 0 {
		w.Add("EXISTS (SELECT 1 FROM journal_entry_lines l WHERE l.entry_id = e.id AND l.account_id = $N)", f.AccountID)
	}
	if f.PartyID > 0 {
		w.Add("EXISTS (SELECT 1 FROM journal_entry_lines l WHERE l.entry_id = e.id AND l.party_id = $N)", f.PartyID)
	}
	if lp.Q != "" {
		w.Add("(e.description ILIKE $N OR e.reference ILIKE $N OR e.number::text = "+w.Arg(lp.Q)+")", httpx.Like(lp.Q))
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM journal_entries e WHERE `+w.Where(), w.Args...).Scan(&total); err != nil {
		return httpx.Page[Entry]{}, err
	}
	order := lp.OrderBy(map[string]string{
		"date": "e.date", "number": "e.number", "total": "e.total_debit", "status": "e.status", "created_at": "e.created_at",
	}, "e.date")
	rows, err := q.Query(ctx, `SELECT `+entryColumns+` FROM journal_entries e `+entryJoins+`
		WHERE `+w.Where()+` ORDER BY `+order+`, e.number DESC`+w.Paging(lp.PageSize, lp.Offset()), w.Args...)
	if err != nil {
		return httpx.Page[Entry]{}, err
	}
	defer rows.Close()
	var items []Entry
	for rows.Next() {
		var e Entry
		if err := scanEntry(rows, &e); err != nil {
			return httpx.Page[Entry]{}, err
		}
		items = append(items, e)
	}
	return httpx.NewPage(items, total, lp), rows.Err()
}
