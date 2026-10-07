// Package reports computes accounting and operational reports directly from
// posted journal lines and documents in PostgreSQL.
package reports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

const maxRows = 20000

var (
	minDate = civil.New(1900, 1, 1)
	maxDate = civil.New(9999, 12, 31)
)

// Range normalises an optional date range.
func Range(from, to civil.Date) (civil.Date, civil.Date) {
	if from.IsZero() {
		from = minDate
	}
	if to.IsZero() {
		to = maxDate
	}
	return from, to
}

type LedgerRow struct {
	EntryID       int64           `json:"entry_id"`
	Date          civil.Date      `json:"date"`
	Number        int64           `json:"number"`
	SourceType    string          `json:"source_type"`
	SourceID      *int64          `json:"source_id"`
	Reference     string          `json:"reference"`
	AccountID     int64           `json:"account_id"`
	AccountCode   string          `json:"account_code"`
	AccountName   string          `json:"account_name"`
	AccountNameEn string          `json:"account_name_en"`
	PartyID       *int64          `json:"party_id"`
	PartyName     *string         `json:"party_name"`
	CostCenter    *string         `json:"cost_center_name"`
	Description   string          `json:"description"`
	Debit         decimal.Decimal `json:"debit"`
	Credit        decimal.Decimal `json:"credit"`
	Balance       decimal.Decimal `json:"balance"`
}

type Ledger struct {
	Opening     decimal.Decimal `json:"opening"`
	TotalDebit  decimal.Decimal `json:"total_debit"`
	TotalCredit decimal.Decimal `json:"total_credit"`
	Closing     decimal.Decimal `json:"closing"`
	Rows        []LedgerRow     `json:"rows"`
	Truncated   bool            `json:"truncated"`
}

type LedgerFilter struct {
	AccountID    int64 // includes the whole subtree; 0 = all accounts
	PartyID      int64
	CostCenterID int64
	From, To     civil.Date
	Q            string
}

// AccountLedger returns lines with a running balance (debit − credit) for an
// account subtree and/or a party: the general/subsidiary ledger, account statement and party statement.
func AccountLedger(ctx context.Context, q db.DBTX, companyID int64, lf LedgerFilter) (*Ledger, error) {
	from, to := Range(lf.From, lf.To)
	f := db.NewFilter("e.company_id = $1 AND e.posted_at IS NOT NULL", companyID)
	if lf.AccountID > 0 {
		var path string
		if err := q.QueryRow(ctx, `SELECT path FROM accounts WHERE company_id = $1 AND id = $2`, companyID, lf.AccountID).Scan(&path); err != nil {
			if db.IsNoRows(err) {
				return nil, apperr.NotFound("account")
			}
			return nil, err
		}
		f.Add("a.path LIKE $N || '%'", path)
	}
	if lf.PartyID > 0 {
		f.Add("l.party_id = $N", lf.PartyID)
	}
	if lf.CostCenterID > 0 {
		f.Add("l.cost_center_id = $N", lf.CostCenterID)
	}
	if lf.AccountID == 0 && lf.PartyID == 0 && lf.CostCenterID == 0 {
		v := apperr.NewValidator()
		v.Check(false, "account_id", "required")
		return nil, v.Err()
	}
	base := ` FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id `
	out := &Ledger{Rows: []LedgerRow{}}
	of := f.Clone()
	of.Add("e.date < $N", from)
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit - l.credit), 0)`+base+`WHERE `+of.Where(), of.Args...).Scan(&out.Opening); err != nil {
		return nil, err
	}
	f.Add("e.date >= $N", from)
	f.Add("e.date <= $N", to)
	if lf.Q != "" {
		f.Add("(l.description ILIKE $N OR e.description ILIKE $N OR e.reference ILIKE $N)", httpx.Like(lf.Q))
	}
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(l.debit), 0), COALESCE(SUM(l.credit), 0)`+base+`WHERE `+f.Where(), f.Args...).
		Scan(&out.TotalDebit, &out.TotalCredit); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT e.id, e.date, e.number, e.source_type, e.source_id, e.reference, a.id, a.code, a.name, a.name_en, l.party_id, p.name, cc.name,
		       COALESCE(NULLIF(l.description, ''), e.description), l.debit, l.credit`+base+`
		LEFT JOIN parties p ON p.id = l.party_id LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		WHERE `+f.Where()+` ORDER BY e.date, e.number, l.line_no LIMIT `+itoa(maxRows+1), f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bal := out.Opening
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.EntryID, &r.Date, &r.Number, &r.SourceType, &r.SourceID, &r.Reference, &r.AccountID, &r.AccountCode,
			&r.AccountName, &r.AccountNameEn, &r.PartyID, &r.PartyName, &r.CostCenter, &r.Description, &r.Debit, &r.Credit); err != nil {
			return nil, err
		}
		if len(out.Rows) == maxRows {
			out.Truncated = true
			break
		}
		bal = bal.Add(r.Debit).Sub(r.Credit)
		r.Balance = bal
		out.Rows = append(out.Rows, r)
	}
	out.Closing = out.Opening.Add(out.TotalDebit).Sub(out.TotalCredit)
	return out, rows.Err()
}

type JournalReport struct {
	Rows        []LedgerRow     `json:"rows"`
	TotalDebit  decimal.Decimal `json:"total_debit"`
	TotalCredit decimal.Decimal `json:"total_credit"`
	Truncated   bool            `json:"truncated"`
}

// Journal lists every posted line in date order (the general journal).
func Journal(ctx context.Context, q db.DBTX, companyID int64, from, to civil.Date, search string) (*JournalReport, error) {
	from, to = Range(from, to)
	f := db.NewFilter("e.company_id = $1 AND e.posted_at IS NOT NULL", companyID)
	f.Add("e.date >= $N", from)
	f.Add("e.date <= $N", to)
	if search != "" {
		f.Add("(l.description ILIKE $N OR e.description ILIKE $N OR e.reference ILIKE $N OR a.name ILIKE $N OR a.code LIKE $N)", httpx.Like(search))
	}
	out := &JournalReport{Rows: []LedgerRow{}}
	rows, err := q.Query(ctx, `
		SELECT e.id, e.date, e.number, e.source_type, e.source_id, e.reference, a.id, a.code, a.name, a.name_en, l.party_id, p.name, cc.name,
		       COALESCE(NULLIF(l.description, ''), e.description), l.debit, l.credit
		FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id
		LEFT JOIN parties p ON p.id = l.party_id LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		WHERE `+f.Where()+` ORDER BY e.date, e.number, l.line_no LIMIT `+itoa(maxRows+1), f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.EntryID, &r.Date, &r.Number, &r.SourceType, &r.SourceID, &r.Reference, &r.AccountID, &r.AccountCode,
			&r.AccountName, &r.AccountNameEn, &r.PartyID, &r.PartyName, &r.CostCenter, &r.Description, &r.Debit, &r.Credit); err != nil {
			return nil, err
		}
		if len(out.Rows) == maxRows {
			out.Truncated = true
			break
		}
		out.TotalDebit = out.TotalDebit.Add(r.Debit)
		out.TotalCredit = out.TotalCredit.Add(r.Credit)
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }
