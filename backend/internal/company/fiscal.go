package company

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

type Period struct {
	ID        int64      `json:"id"`
	Seq       int        `json:"seq"`
	Name      string     `json:"name"`
	StartDate civil.Date `json:"start_date"`
	EndDate   civil.Date `json:"end_date"`
	IsLocked  bool       `json:"is_locked"`
}

type FiscalYear struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	StartDate      civil.Date `json:"start_date"`
	EndDate        civil.Date `json:"end_date"`
	Status         string     `json:"status"`
	ClosingEntryID *int64     `json:"closing_entry_id"`
	Periods        []Period   `json:"periods,omitempty"`
}

type FiscalYearInput struct {
	Name      string     `json:"name"`
	StartDate civil.Date `json:"start_date"`
	EndDate   civil.Date `json:"end_date"`
}

type periodSpan struct {
	name       string
	start, end civil.Date
}

// MonthlyPeriods splits [start, end] into calendar months of the given calendar.
func MonthlyPeriods(start, end civil.Date, calendar string) []periodSpan {
	var out []periodSpan
	cur := start
	for !cur.After(end) {
		var monthEnd civil.Date
		var name string
		if calendar == "jalali" {
			jy, jm, _ := jalali.FromGregorian(cur.Time)
			last := jalali.ToGregorian(jy, jm, jalali.MonthLength(jy, jm))
			monthEnd = civil.Date{Time: last}
			name = fmt.Sprintf("%s %d", jalali.MonthNames[jm-1], jy)
		} else {
			first := time.Date(cur.Year(), cur.Month(), 1, 0, 0, 0, 0, time.UTC)
			monthEnd = civil.Date{Time: first.AddDate(0, 1, -1)}
			name = cur.Format("January 2006")
		}
		if monthEnd.After(end) {
			monthEnd = end
		}
		out = append(out, periodSpan{name: name, start: cur, end: monthEnd})
		cur = monthEnd.AddDays(1)
	}
	return out
}

// DefaultFiscalYear returns the calendar year containing date.
func DefaultFiscalYear(date civil.Date, calendar string) FiscalYearInput {
	if calendar == "jalali" {
		jy, _, _ := jalali.FromGregorian(date.Time)
		return FiscalYearInput{
			Name:      fmt.Sprintf("%d", jy),
			StartDate: civil.Date{Time: jalali.ToGregorian(jy, 1, 1)},
			EndDate:   civil.Date{Time: jalali.ToGregorian(jy+1, 1, 1).AddDate(0, 0, -1)},
		}
	}
	return FiscalYearInput{
		Name:      fmt.Sprintf("%d", date.Year()),
		StartDate: civil.New(date.Year(), 1, 1),
		EndDate:   civil.New(date.Year(), 12, 31),
	}
}

// CreateFiscalYear inserts a fiscal year and its monthly accounting periods.
func CreateFiscalYear(ctx context.Context, q db.DBTX, companyID int64, in FiscalYearInput) (int64, error) {
	v := apperr.NewValidator()
	v.Required("name", in.Name)
	v.Check(!in.StartDate.IsZero(), "start_date", "required")
	v.Check(!in.EndDate.IsZero(), "end_date", "required")
	v.Check(in.StartDate.IsZero() || in.EndDate.IsZero() || in.EndDate.After(in.StartDate), "end_date", "end_before_start")
	v.Check(in.EndDate.Sub(in.StartDate.Time) < 24*time.Hour*550, "end_date", "too_long")
	if err := v.Err(); err != nil {
		return 0, err
	}
	var calendar string
	if err := q.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1 FOR UPDATE`, companyID).Scan(&calendar); err != nil {
		return 0, err
	}
	var overlaps bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM fiscal_years WHERE company_id = $1 AND start_date <= $3 AND end_date >= $2)`,
		companyID, in.StartDate, in.EndDate).Scan(&overlaps); err != nil {
		return 0, err
	}
	if overlaps {
		return 0, apperr.Rule("fiscal_year_overlap", "the fiscal year overlaps an existing year")
	}
	var id int64
	if err := q.QueryRow(ctx, `INSERT INTO fiscal_years (company_id, name, start_date, end_date) VALUES ($1, $2, $3, $4) RETURNING id`,
		companyID, in.Name, in.StartDate, in.EndDate).Scan(&id); err != nil {
		return 0, err
	}
	for i, p := range MonthlyPeriods(in.StartDate, in.EndDate, calendar) {
		if _, err := q.Exec(ctx, `
			INSERT INTO accounting_periods (company_id, fiscal_year_id, seq, name, start_date, end_date)
			VALUES ($1, $2, $3, $4, $5, $6)`, companyID, id, i+1, p.name, p.start, p.end); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func listFiscalYears(ctx context.Context, q db.DBTX, companyID int64) ([]FiscalYear, error) {
	rows, err := q.Query(ctx, `SELECT id, name, start_date, end_date, status, closing_entry_id FROM fiscal_years WHERE company_id = $1 ORDER BY start_date DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FiscalYear{}
	for rows.Next() {
		var f FiscalYear
		if err := rows.Scan(&f.ID, &f.Name, &f.StartDate, &f.EndDate, &f.Status, &f.ClosingEntryID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func getFiscalYear(ctx context.Context, q db.DBTX, companyID, id int64) (*FiscalYear, error) {
	var f FiscalYear
	err := q.QueryRow(ctx, `SELECT id, name, start_date, end_date, status, closing_entry_id FROM fiscal_years WHERE company_id = $1 AND id = $2`, companyID, id).
		Scan(&f.ID, &f.Name, &f.StartDate, &f.EndDate, &f.Status, &f.ClosingEntryID)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("fiscal_year")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT id, seq, name, start_date, end_date, is_locked FROM accounting_periods WHERE fiscal_year_id = $1 ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	f.Periods = []Period{}
	for rows.Next() {
		var p Period
		if err := rows.Scan(&p.ID, &p.Seq, &p.Name, &p.StartDate, &p.EndDate, &p.IsLocked); err != nil {
			return nil, err
		}
		f.Periods = append(f.Periods, p)
	}
	return &f, rows.Err()
}

// CloseFiscalYear transfers the year's income and expense balances to retained
// earnings with a closing entry, then closes the year to further posting.
func CloseFiscalYear(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var (
		status     string
		start, end civil.Date
	)
	if err := q.QueryRow(ctx, `SELECT status, start_date, end_date FROM fiscal_years WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).
		Scan(&status, &start, &end); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("fiscal_year")
		}
		return err
	}
	if status != "open" {
		return apperr.Rule("fiscal_year_closed", "the fiscal year is already closed")
	}
	var drafts int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM journal_entries WHERE company_id = $1 AND fiscal_year_id = $2 AND status = 'draft'`, companyID, id).Scan(&drafts); err != nil {
		return err
	}
	if drafts > 0 {
		return apperr.Rule("drafts_exist", "post or delete draft entries before closing the year").WithParams("count", drafts)
	}
	retained, err := ledger.Mapping(ctx, q, companyID, ledger.MapRetainedEarnings)
	if err != nil {
		return err
	}
	rows, err := q.Query(ctx, `
		SELECT l.account_id, SUM(l.debit - l.credit)
		FROM journal_entry_lines l
		JOIN journal_entries e ON e.id = l.entry_id
		JOIN accounts a ON a.id = l.account_id
		WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND e.date BETWEEN $2 AND $3
		  AND a.type IN ('revenue', 'cost_of_sales', 'expense') AND e.source_type <> 'closing'
		GROUP BY l.account_id HAVING SUM(l.debit - l.credit) <> 0`, companyID, start, end)
	if err != nil {
		return err
	}
	var b ledger.Builder
	net := decimal.Zero
	for rows.Next() {
		var acc int64
		var bal decimal.Decimal
		if err := rows.Scan(&acc, &bal); err != nil {
			rows.Close()
			return err
		}
		b.Credit(acc, nil, nil, "", bal) // negative balances flip to the debit side
		net = net.Add(bal)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var closingID *int64
	if !net.IsZero() || len(b.Lines()) > 0 {
		b.Debit(retained, nil, nil, "", net)
		cid, err := ledger.PostAuto(ctx, q, companyID, userID, end, "Closing entry", "", b.Lines(), ledger.Source{Type: ledger.SourceClosing, ID: id})
		if err != nil {
			return err
		}
		closingID = &cid
	}
	_, err = q.Exec(ctx, `UPDATE fiscal_years SET status = 'closed', closed_at = now(), closing_entry_id = $3 WHERE company_id = $1 AND id = $2`, companyID, id, closingID)
	return err
}

// ReopenFiscalYear reverses the closing entry and reopens the year.
func ReopenFiscalYear(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	var closingID *int64
	if err := q.QueryRow(ctx, `SELECT status, closing_entry_id FROM fiscal_years WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).
		Scan(&status, &closingID); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("fiscal_year")
		}
		return err
	}
	if status != "closed" {
		return apperr.Rule("fiscal_year_open", "the fiscal year is not closed")
	}
	if _, err := q.Exec(ctx, `UPDATE fiscal_years SET status = 'open', closed_at = NULL, closing_entry_id = NULL WHERE id = $1`, id); err != nil {
		return err
	}
	if closingID != nil {
		if _, err := ledger.Cancel(ctx, q, companyID, userID, *closingID, civil.Date{}, true); err != nil {
			return err
		}
	}
	return nil
}
