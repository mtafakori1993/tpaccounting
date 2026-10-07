package reports

import (
	"context"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

type AgingRow struct {
	PartyID   int64           `json:"party_id"`
	PartyCode string          `json:"party_code"`
	PartyName string          `json:"party_name"`
	Phone     string          `json:"phone"`
	Balance   decimal.Decimal `json:"balance"`
	Current   decimal.Decimal `json:"current"`
	Days30    decimal.Decimal `json:"days_1_30"`
	Days60    decimal.Decimal `json:"days_31_60"`
	Days90    decimal.Decimal `json:"days_61_90"`
	Over90    decimal.Decimal `json:"days_over_90"`
}

type Aging struct {
	Rows  []AgingRow `json:"rows"`
	Total AgingRow   `json:"total"`
}

// AgingReport lists party balances (receivable: owed to us; payable: owed by us)
// with overdue buckets taken from open invoices by due date.
func AgingReport(ctx context.Context, conn db.DBTX, companyID int64, asOf civil.Date, payable bool) (*Aging, error) {
	if asOf.IsZero() {
		asOf = civil.Today()
	}
	sign := "1"
	side := "sales"
	if payable {
		sign = "-1"
		side = "purchase"
	}
	rows, err := conn.Query(ctx, `
		WITH bal AS (
			SELECT l.party_id, SUM(l.debit - l.credit) * `+sign+` AS balance
			FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
			WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND e.date <= $2 AND l.party_id IS NOT NULL
			GROUP BY l.party_id
		), open AS (
			SELECT party_id, ($2::date - COALESCE(due_date, date)) AS days, total - paid_amount AS amount
			FROM invoices WHERE company_id = $1 AND side = $3 AND kind = 'invoice' AND status = 'posted' AND paid_amount < total AND date <= $2
		)
		SELECT p.id, p.code, p.name, COALESCE(NULLIF(p.mobile, ''), p.phone), b.balance,
		       COALESCE(SUM(o.amount) FILTER (WHERE o.days <= 0), 0),
		       COALESCE(SUM(o.amount) FILTER (WHERE o.days BETWEEN 1 AND 30), 0),
		       COALESCE(SUM(o.amount) FILTER (WHERE o.days BETWEEN 31 AND 60), 0),
		       COALESCE(SUM(o.amount) FILTER (WHERE o.days BETWEEN 61 AND 90), 0),
		       COALESCE(SUM(o.amount) FILTER (WHERE o.days > 90), 0)
		FROM bal b JOIN parties p ON p.id = b.party_id
		LEFT JOIN open o ON o.party_id = p.id
		WHERE b.balance > 0
		GROUP BY p.id, b.balance ORDER BY b.balance DESC`, companyID, asOf, side)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Aging{Rows: []AgingRow{}}
	for rows.Next() {
		var r AgingRow
		if err := rows.Scan(&r.PartyID, &r.PartyCode, &r.PartyName, &r.Phone, &r.Balance, &r.Current, &r.Days30, &r.Days60, &r.Days90, &r.Over90); err != nil {
			return nil, err
		}
		t := &out.Total
		t.Balance = t.Balance.Add(r.Balance)
		t.Current = t.Current.Add(r.Current)
		t.Days30 = t.Days30.Add(r.Days30)
		t.Days60 = t.Days60.Add(r.Days60)
		t.Days90 = t.Days90.Add(r.Days90)
		t.Over90 = t.Over90.Add(r.Over90)
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

type TradeRow struct {
	Key      string          `json:"key"`
	ID       *int64          `json:"id"`
	Label    string          `json:"label"`
	Code     string          `json:"code"`
	Count    int             `json:"count"`
	Quantity decimal.Decimal `json:"quantity"`
	Gross    decimal.Decimal `json:"gross"`
	Discount decimal.Decimal `json:"discount"`
	Net      decimal.Decimal `json:"net"`
	Tax      decimal.Decimal `json:"tax"`
	Total    decimal.Decimal `json:"total"`
}

type TradeReport struct {
	GroupBy string     `json:"group_by"`
	Rows    []TradeRow `json:"rows"`
	Total   TradeRow   `json:"total"`
}

// TradeSummary summarises posted sales or purchases (net of returns) grouped
// by party, product or month.
func TradeSummary(ctx context.Context, conn db.DBTX, companyID int64, side string, from, to civil.Date, groupBy, calendar string) (*TradeReport, error) {
	from, to = Range(from, to)
	var keySQL, labelSQL, codeSQL, idSQL string
	switch groupBy {
	case "product":
		keySQL, idSQL, labelSQL, codeSQL = "COALESCE(it.product_id::text, it.description)", "it.product_id", "COALESCE(pr.name, it.description)", "COALESCE(pr.sku, '')"
	case "month":
		keySQL, idSQL, labelSQL, codeSQL = "i.date::text", "NULL::bigint", "i.date::text", "''"
	default:
		groupBy = "party"
		keySQL, idSQL, labelSQL, codeSQL = "i.party_id::text", "i.party_id", "p.name", "p.code"
	}
	rows, err := conn.Query(ctx, `
		SELECT `+keySQL+`, `+idSQL+`, `+labelSQL+`, `+codeSQL+`, count(DISTINCT i.id) FILTER (WHERE i.kind = 'invoice'),
		       SUM(CASE WHEN i.kind = 'return' THEN -it.quantity ELSE it.quantity END),
		       SUM(CASE WHEN i.kind = 'return' THEN -1 ELSE 1 END * (it.net_amount + it.discount)),
		       SUM(CASE WHEN i.kind = 'return' THEN -1 ELSE 1 END * it.discount),
		       SUM(CASE WHEN i.kind = 'return' THEN -1 ELSE 1 END * it.net_amount),
		       SUM(CASE WHEN i.kind = 'return' THEN -1 ELSE 1 END * it.tax_amount),
		       SUM(CASE WHEN i.kind = 'return' THEN -1 ELSE 1 END * it.total)
		FROM invoices i JOIN invoice_items it ON it.invoice_id = i.id JOIN parties p ON p.id = i.party_id
		LEFT JOIN products pr ON pr.id = it.product_id
		WHERE i.company_id = $1 AND i.side = $2 AND i.kind IN ('invoice', 'return') AND i.status = 'posted' AND i.date BETWEEN $3 AND $4
		GROUP BY 1, 2, 3, 4 ORDER BY 1`, companyID, side, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &TradeReport{GroupBy: groupBy, Rows: []TradeRow{}}
	months := map[string]*TradeRow{}
	var order []string
	for rows.Next() {
		var r TradeRow
		if err := rows.Scan(&r.Key, &r.ID, &r.Label, &r.Code, &r.Count, &r.Quantity, &r.Gross, &r.Discount, &r.Net, &r.Tax, &r.Total); err != nil {
			return nil, err
		}
		addTrade(&out.Total, r)
		if groupBy != "month" {
			out.Rows = append(out.Rows, r)
			continue
		}
		d, err := civil.Parse(r.Key)
		if err != nil {
			return nil, err
		}
		key := monthKey(d, calendar)
		m, ok := months[key]
		if !ok {
			m = &TradeRow{Key: key, Label: key}
			months[key] = m
			order = append(order, key)
		}
		addTrade(m, r)
	}
	if groupBy == "month" {
		sort.Strings(order)
		for _, k := range order {
			out.Rows = append(out.Rows, *months[k])
		}
	} else {
		sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].Net.GreaterThan(out.Rows[j].Net) })
	}
	return out, rows.Err()
}

func addTrade(t *TradeRow, r TradeRow) {
	t.Count += r.Count
	t.Quantity = t.Quantity.Add(r.Quantity)
	t.Gross = t.Gross.Add(r.Gross)
	t.Discount = t.Discount.Add(r.Discount)
	t.Net = t.Net.Add(r.Net)
	t.Tax = t.Tax.Add(r.Tax)
	t.Total = t.Total.Add(r.Total)
}

// monthKey returns "YYYY-MM" in the company calendar.
func monthKey(d civil.Date, calendar string) string {
	if calendar == "jalali" {
		y, m, _ := jalali.FromGregorian(d.Time)
		return fmt.Sprintf("%04d-%02d", y, m)
	}
	return d.Format("2006-01")
}

type ExpenseRow struct {
	ID     *int64          `json:"id"`
	Code   string          `json:"code"`
	Label  string          `json:"label"`
	Amount decimal.Decimal `json:"amount"`
}

type ExpenseReport struct {
	GroupBy string          `json:"group_by"`
	Rows    []ExpenseRow    `json:"rows"`
	Total   decimal.Decimal `json:"total"`
}

// ExpensesSummary totals expense-type ledger activity by account or cost center.
func ExpensesSummary(ctx context.Context, conn db.DBTX, companyID int64, from, to civil.Date, groupBy string, costCenterID int64) (*ExpenseReport, error) {
	from, to = Range(from, to)
	sel := "a.id, a.code, a.name"
	group := "a.id"
	join := ""
	switch groupBy {
	case "cost_center":
		sel, group, join = "cc.id, COALESCE(cc.code, ''), COALESCE(cc.name, '')", "cc.id, cc.code, cc.name", " LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id"
	case "month":
		sel, group = "NULL::bigint, e.date::text, e.date::text", "e.date"
	default:
		groupBy = "account"
	}
	f := db.NewFilter("e.company_id = $1 AND e.posted_at IS NOT NULL AND a.type = 'expense' AND e.source_type <> 'closing'", companyID)
	f.Add("e.date >= $N", from)
	f.Add("e.date <= $N", to)
	if costCenterID > 0 {
		f.Add("l.cost_center_id = $N", costCenterID)
	}
	rows, err := conn.Query(ctx, `SELECT `+sel+`, SUM(l.debit - l.credit)
		FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id JOIN accounts a ON a.id = l.account_id`+join+`
		WHERE `+f.Where()+` GROUP BY `+group+` HAVING SUM(l.debit - l.credit) <> 0 ORDER BY 2`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &ExpenseReport{GroupBy: groupBy, Rows: []ExpenseRow{}}
	var calendar string
	if groupBy == "month" {
		if err := conn.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1`, companyID).Scan(&calendar); err != nil {
			return nil, err
		}
	}
	months := map[string]int{}
	for rows.Next() {
		var r ExpenseRow
		if err := rows.Scan(&r.ID, &r.Code, &r.Label, &r.Amount); err != nil {
			return nil, err
		}
		out.Total = out.Total.Add(r.Amount)
		if groupBy == "month" {
			d, err := civil.Parse(r.Code)
			if err != nil {
				return nil, err
			}
			k := monthKey(d, calendar)
			if i, ok := months[k]; ok {
				out.Rows[i].Amount = out.Rows[i].Amount.Add(r.Amount)
				continue
			}
			months[k] = len(out.Rows)
			r.Code, r.Label = k, k
		}
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}
