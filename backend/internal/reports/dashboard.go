package reports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

type DashInvoice struct {
	ID          int64           `json:"id"`
	Side        string          `json:"side"`
	Number      int64           `json:"number"`
	Date        civil.Date      `json:"date"`
	DueDate     *civil.Date     `json:"due_date"`
	PartyName   string          `json:"party_name"`
	Total       decimal.Decimal `json:"total"`
	Outstanding decimal.Decimal `json:"outstanding"`
}

type DashEntry struct {
	ID          int64           `json:"id"`
	Number      int64           `json:"number"`
	Date        civil.Date      `json:"date"`
	Description string          `json:"description"`
	SourceType  string          `json:"source_type"`
	Amount      decimal.Decimal `json:"amount"`
}

type Dashboard struct {
	MonthStart       civil.Date      `json:"month_start"`
	YearStart        civil.Date      `json:"year_start"`
	Today            civil.Date      `json:"today"`
	CashBalance      decimal.Decimal `json:"cash_balance"`
	BankBalance      decimal.Decimal `json:"bank_balance"`
	Receivables      decimal.Decimal `json:"receivables"`
	Payables         decimal.Decimal `json:"payables"`
	SalesMonth       decimal.Decimal `json:"sales_month"`
	ExpensesMonth    decimal.Decimal `json:"expenses_month"`
	NetProfitYear    decimal.Decimal `json:"net_profit_year"`
	RevenueYear      decimal.Decimal `json:"revenue_year"`
	RecentEntries    []DashEntry     `json:"recent_entries"`
	OpenInvoices     []DashInvoice   `json:"outstanding_invoices"`
	UpcomingPayments []DashInvoice   `json:"upcoming_payments"`
}

func DashboardData(ctx context.Context, conn db.DBTX, companyID int64) (*Dashboard, error) {
	today := civil.Today()
	var calendar string
	if err := conn.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1`, companyID).Scan(&calendar); err != nil {
		return nil, err
	}
	d := &Dashboard{Today: today, RecentEntries: []DashEntry{}, OpenInvoices: []DashInvoice{}, UpcomingPayments: []DashInvoice{}}
	if calendar == "jalali" {
		jy, jm, _ := jalali.FromGregorian(today.Time)
		d.MonthStart = civil.Date{Time: jalali.ToGregorian(jy, jm, 1)}
	} else {
		d.MonthStart = civil.New(today.Year(), today.Month(), 1)
	}
	if err := conn.QueryRow(ctx, `SELECT COALESCE((SELECT start_date FROM fiscal_years WHERE company_id = $1 AND $2::date BETWEEN start_date AND end_date), $3::date)`,
		companyID, today, d.MonthStart).Scan(&d.YearStart); err != nil {
		return nil, err
	}

	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit - l.credit) FILTER (WHERE t.kind = 'cash'), 0), COALESCE(SUM(l.debit - l.credit) FILTER (WHERE t.kind = 'bank'), 0)
		FROM treasury_accounts t JOIN journal_entry_lines l ON l.account_id = t.account_id JOIN journal_entries e ON e.id = l.entry_id
		WHERE t.company_id = $1 AND e.posted_at IS NOT NULL`, companyID).Scan(&d.CashBalance, &d.BankBalance); err != nil {
		return nil, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(b) FILTER (WHERE b > 0), 0), COALESCE(-SUM(b) FILTER (WHERE b < 0), 0) FROM (
			SELECT SUM(l.debit - l.credit) AS b FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
			WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND l.party_id IS NOT NULL GROUP BY l.party_id) x`, companyID).
		Scan(&d.Receivables, &d.Payables); err != nil {
		return nil, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN kind = 'return' THEN -(total - tax_total) ELSE total - tax_total END), 0)
		FROM invoices WHERE company_id = $1 AND side = 'sales' AND kind IN ('invoice', 'return') AND status = 'posted' AND date >= $2`,
		companyID, d.MonthStart).Scan(&d.SalesMonth); err != nil {
		return nil, err
	}
	if err := conn.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit - l.credit), 0) FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
		JOIN accounts a ON a.id = l.account_id
		WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND a.type = 'expense' AND e.date >= $2 AND e.source_type <> 'closing'`,
		companyID, d.MonthStart).Scan(&d.ExpensesMonth); err != nil {
		return nil, err
	}
	pl, err := ProfitLossReport(ctx, conn, companyID, d.YearStart, today, 2, 0)
	if err != nil {
		return nil, err
	}
	d.NetProfitYear, d.RevenueYear = pl.NetProfit, pl.TotalRevenue

	rows, err := conn.Query(ctx, `SELECT id, number, date, description, source_type, total_debit FROM journal_entries
		WHERE company_id = $1 AND posted_at IS NOT NULL ORDER BY posted_at DESC, id DESC LIMIT 8`, companyID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e DashEntry
		if err := rows.Scan(&e.ID, &e.Number, &e.Date, &e.Description, &e.SourceType, &e.Amount); err != nil {
			rows.Close()
			return nil, err
		}
		d.RecentEntries = append(d.RecentEntries, e)
	}
	rows.Close()

	loadInvoices := func(side string, extra string, target *[]DashInvoice) error {
		rows, err := conn.Query(ctx, `
			SELECT i.id, i.side, i.number, i.date, i.due_date, p.name, i.total, i.total - i.paid_amount
			FROM invoices i JOIN parties p ON p.id = i.party_id
			WHERE i.company_id = $1 AND i.side = $2 AND i.kind = 'invoice' AND i.status = 'posted' AND i.paid_amount < i.total `+extra+`
			ORDER BY COALESCE(i.due_date, i.date), i.id LIMIT 8`, companyID, side)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var x DashInvoice
			if err := rows.Scan(&x.ID, &x.Side, &x.Number, &x.Date, &x.DueDate, &x.PartyName, &x.Total, &x.Outstanding); err != nil {
				return err
			}
			*target = append(*target, x)
		}
		return rows.Err()
	}
	if err := loadInvoices("sales", "", &d.OpenInvoices); err != nil {
		return nil, err
	}
	if err := loadInvoices("purchase", "AND COALESCE(i.due_date, i.date) <= CURRENT_DATE + 30", &d.UpcomingPayments); err != nil {
		return nil, err
	}
	return d, nil
}
