package reports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// AccountBalance is an account at the requested level with sums over its subtree.
type AccountBalance struct {
	AccountID     int64           `json:"account_id"`
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	NameEn        string          `json:"name_en"`
	Type          string          `json:"type"`
	Level         int             `json:"level"`
	OpeningDebit  decimal.Decimal `json:"opening_debit"`
	OpeningCredit decimal.Decimal `json:"opening_credit"`
	PeriodDebit   decimal.Decimal `json:"period_debit"`
	PeriodCredit  decimal.Decimal `json:"period_credit"`
	ClosingDebit  decimal.Decimal `json:"closing_debit"`
	ClosingCredit decimal.Decimal `json:"closing_credit"`
}

type balanceQuery struct {
	From, To       civil.Date
	Level          int
	Types          []string
	ExcludeClosing bool
	CostCenterID   int64
}

// accountBalances aggregates posted lines up to q.To into accounts of the
// requested level (leaf accounts above that level are included as-is).
func accountBalances(ctx context.Context, conn db.DBTX, companyID int64, bq balanceQuery) ([]AccountBalance, error) {
	from, to := Range(bq.From, bq.To)
	if bq.Level < 1 || bq.Level > 4 {
		bq.Level = 4
	}
	inner := db.NewFilter("e.company_id = $1 AND e.posted_at IS NOT NULL", companyID)
	fromArg := inner.Arg(from)
	inner.Add("e.date <= $N", to)
	if bq.ExcludeClosing {
		inner.AddRaw("e.source_type <> 'closing'")
	}
	if bq.CostCenterID > 0 {
		inner.Add("l.cost_center_id = $N", bq.CostCenterID)
	}
	outer := "a.company_id = $1 AND (a.level = " + inner.Arg(bq.Level) + " OR (a.level < " + inner.Arg(bq.Level) +
		" AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)))"
	if len(bq.Types) > 0 {
		outer += " AND a.type = ANY(" + inner.Arg(bq.Types) + ")"
	}
	sql := `
		WITH bal AS (
			SELECT l.account_id,
			       SUM(CASE WHEN e.date < ` + fromArg + ` THEN l.debit ELSE 0 END) AS od,
			       SUM(CASE WHEN e.date < ` + fromArg + ` THEN l.credit ELSE 0 END) AS oc,
			       SUM(CASE WHEN e.date >= ` + fromArg + ` THEN l.debit ELSE 0 END) AS pd,
			       SUM(CASE WHEN e.date >= ` + fromArg + ` THEN l.credit ELSE 0 END) AS pc
			FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
			WHERE ` + inner.Where() + `
			GROUP BY l.account_id
		)
		SELECT a.id, a.code, a.name, a.name_en, a.type, a.level,
		       SUM(b.od), SUM(b.oc), SUM(b.pd), SUM(b.pc)
		FROM accounts a
		JOIN accounts la ON la.company_id = a.company_id AND la.path LIKE a.path || '%'
		JOIN bal b ON b.account_id = la.id
		WHERE ` + outer + `
		GROUP BY a.id ORDER BY a.code`
	rows, err := conn.Query(ctx, sql, inner.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountBalance{}
	for rows.Next() {
		var r AccountBalance
		var od, oc decimal.Decimal
		if err := rows.Scan(&r.AccountID, &r.Code, &r.Name, &r.NameEn, &r.Type, &r.Level, &od, &oc, &r.PeriodDebit, &r.PeriodCredit); err != nil {
			return nil, err
		}
		r.OpeningDebit, r.OpeningCredit = split(od.Sub(oc))
		r.ClosingDebit, r.ClosingCredit = split(od.Sub(oc).Add(r.PeriodDebit).Sub(r.PeriodCredit))
		out = append(out, r)
	}
	return out, rows.Err()
}

// split turns a net debit−credit amount into (debit, credit) columns.
func split(net decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	if net.IsNegative() {
		return decimal.Zero, net.Neg()
	}
	return net, decimal.Zero
}

type TrialBalance struct {
	Rows   []AccountBalance `json:"rows"`
	Totals AccountBalance   `json:"totals"`
}

func TrialBalanceReport(ctx context.Context, conn db.DBTX, companyID int64, from, to civil.Date, level int, excludeClosing, showZero bool) (*TrialBalance, error) {
	rows, err := accountBalances(ctx, conn, companyID, balanceQuery{From: from, To: to, Level: level, ExcludeClosing: excludeClosing})
	if err != nil {
		return nil, err
	}
	out := &TrialBalance{Rows: []AccountBalance{}}
	for _, r := range rows {
		if !showZero && r.OpeningDebit.IsZero() && r.OpeningCredit.IsZero() && r.PeriodDebit.IsZero() && r.PeriodCredit.IsZero() {
			continue
		}
		t := &out.Totals
		t.OpeningDebit = t.OpeningDebit.Add(r.OpeningDebit)
		t.OpeningCredit = t.OpeningCredit.Add(r.OpeningCredit)
		t.PeriodDebit = t.PeriodDebit.Add(r.PeriodDebit)
		t.PeriodCredit = t.PeriodCredit.Add(r.PeriodCredit)
		t.ClosingDebit = t.ClosingDebit.Add(r.ClosingDebit)
		t.ClosingCredit = t.ClosingCredit.Add(r.ClosingCredit)
		out.Rows = append(out.Rows, r)
	}
	return out, nil
}

type StatementLine struct {
	AccountID int64           `json:"account_id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	NameEn    string          `json:"name_en"`
	Level     int             `json:"level"`
	Amount    decimal.Decimal `json:"amount"`
}

type ProfitLoss struct {
	Revenue       []StatementLine `json:"revenue"`
	CostOfSales   []StatementLine `json:"cost_of_sales"`
	Expenses      []StatementLine `json:"expenses"`
	TotalRevenue  decimal.Decimal `json:"total_revenue"`
	TotalCost     decimal.Decimal `json:"total_cost_of_sales"`
	GrossProfit   decimal.Decimal `json:"gross_profit"`
	TotalExpenses decimal.Decimal `json:"total_expenses"`
	NetProfit     decimal.Decimal `json:"net_profit"`
}

// ProfitLossReport computes the income statement for a period. Closing entries are excluded.
func ProfitLossReport(ctx context.Context, conn db.DBTX, companyID int64, from, to civil.Date, level int, costCenterID int64) (*ProfitLoss, error) {
	from, to = Range(from, to)
	rows, err := accountBalances(ctx, conn, companyID, balanceQuery{From: from, To: to, Level: level, ExcludeClosing: true,
		Types: []string{"revenue", "cost_of_sales", "expense"}, CostCenterID: costCenterID})
	if err != nil {
		return nil, err
	}
	out := &ProfitLoss{Revenue: []StatementLine{}, CostOfSales: []StatementLine{}, Expenses: []StatementLine{}}
	for _, r := range rows {
		net := r.PeriodDebit.Sub(r.PeriodCredit)
		if net.IsZero() {
			continue
		}
		line := StatementLine{AccountID: r.AccountID, Code: r.Code, Name: r.Name, NameEn: r.NameEn, Level: r.Level}
		switch r.Type {
		case "revenue":
			line.Amount = net.Neg()
			out.Revenue = append(out.Revenue, line)
			out.TotalRevenue = out.TotalRevenue.Add(line.Amount)
		case "cost_of_sales":
			line.Amount = net
			out.CostOfSales = append(out.CostOfSales, line)
			out.TotalCost = out.TotalCost.Add(net)
		default:
			line.Amount = net
			out.Expenses = append(out.Expenses, line)
			out.TotalExpenses = out.TotalExpenses.Add(net)
		}
	}
	out.GrossProfit = out.TotalRevenue.Sub(out.TotalCost)
	out.NetProfit = out.GrossProfit.Sub(out.TotalExpenses)
	return out, nil
}

type BalanceSheet struct {
	Assets           []StatementLine `json:"assets"`
	Liabilities      []StatementLine `json:"liabilities"`
	Equity           []StatementLine `json:"equity"`
	TotalAssets      decimal.Decimal `json:"total_assets"`
	TotalLiabilities decimal.Decimal `json:"total_liabilities"`
	TotalEquity      decimal.Decimal `json:"total_equity"`
	CurrentEarnings  decimal.Decimal `json:"current_earnings"`
	TotalLiabEquity  decimal.Decimal `json:"total_liabilities_and_equity"`
	Balanced         bool            `json:"balanced"`
}

// BalanceSheetReport reports financial position as of a date. Unclosed income
// and expenses appear as current-period earnings within equity.
func BalanceSheetReport(ctx context.Context, conn db.DBTX, companyID int64, asOf civil.Date, level int) (*BalanceSheet, error) {
	_, asOf = Range(civil.Date{}, asOf)
	rows, err := accountBalances(ctx, conn, companyID, balanceQuery{To: asOf, Level: level})
	if err != nil {
		return nil, err
	}
	out := &BalanceSheet{Assets: []StatementLine{}, Liabilities: []StatementLine{}, Equity: []StatementLine{}}
	for _, r := range rows {
		net := r.PeriodDebit.Sub(r.PeriodCredit) // with no from date, everything is "period"
		line := StatementLine{AccountID: r.AccountID, Code: r.Code, Name: r.Name, NameEn: r.NameEn, Level: r.Level}
		switch r.Type {
		case "asset":
			line.Amount = net
			if !net.IsZero() {
				out.Assets = append(out.Assets, line)
			}
			out.TotalAssets = out.TotalAssets.Add(net)
		case "liability":
			line.Amount = net.Neg()
			if !net.IsZero() {
				out.Liabilities = append(out.Liabilities, line)
			}
			out.TotalLiabilities = out.TotalLiabilities.Add(line.Amount)
		case "equity":
			line.Amount = net.Neg()
			if !net.IsZero() {
				out.Equity = append(out.Equity, line)
			}
			out.TotalEquity = out.TotalEquity.Add(line.Amount)
		default:
			out.CurrentEarnings = out.CurrentEarnings.Sub(net)
		}
	}
	out.TotalEquity = out.TotalEquity.Add(out.CurrentEarnings)
	out.TotalLiabEquity = out.TotalLiabilities.Add(out.TotalEquity)
	out.Balanced = out.TotalAssets.Equal(out.TotalLiabEquity)
	return out, nil
}

type CashRow struct {
	ID       int64           `json:"id"`
	Kind     string          `json:"kind"`
	Name     string          `json:"name"`
	BankName string          `json:"bank_name"`
	Opening  decimal.Decimal `json:"opening"`
	In       decimal.Decimal `json:"in"`
	Out      decimal.Decimal `json:"out"`
	Closing  decimal.Decimal `json:"closing"`
}

type CashPosition struct {
	Rows      []CashRow       `json:"rows"`
	TotalCash decimal.Decimal `json:"total_cash"`
	TotalBank decimal.Decimal `json:"total_bank"`
	Total     CashRow         `json:"total"`
}

func CashPositionReport(ctx context.Context, conn db.DBTX, companyID int64, from, to civil.Date) (*CashPosition, error) {
	from, to = Range(from, to)
	rows, err := conn.Query(ctx, `
		SELECT t.id, t.kind, t.name, t.bank_name, COALESCE(s.opening, 0), COALESCE(s.din, 0), COALESCE(s.dout, 0)
		FROM treasury_accounts t
		LEFT JOIN (
			SELECT l.account_id,
			       SUM(CASE WHEN e.date < $2 THEN l.debit - l.credit ELSE 0 END) AS opening,
			       SUM(CASE WHEN e.date >= $2 THEN l.debit ELSE 0 END) AS din,
			       SUM(CASE WHEN e.date >= $2 THEN l.credit ELSE 0 END) AS dout
			FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
			WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND e.date <= $3
			GROUP BY l.account_id
		) s ON s.account_id = t.account_id
		WHERE t.company_id = $1 ORDER BY t.kind, t.name`, companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &CashPosition{Rows: []CashRow{}}
	for rows.Next() {
		var r CashRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.BankName, &r.Opening, &r.In, &r.Out); err != nil {
			return nil, err
		}
		r.Closing = r.Opening.Add(r.In).Sub(r.Out)
		if r.Kind == "cash" {
			out.TotalCash = out.TotalCash.Add(r.Closing)
		} else {
			out.TotalBank = out.TotalBank.Add(r.Closing)
		}
		out.Total.Opening = out.Total.Opening.Add(r.Opening)
		out.Total.In = out.Total.In.Add(r.In)
		out.Total.Out = out.Total.Out.Add(r.Out)
		out.Total.Closing = out.Total.Closing.Add(r.Closing)
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

type CostCenterRow struct {
	CostCenterID *int64          `json:"cost_center_id"`
	Code         *string         `json:"code"`
	Name         *string         `json:"name"`
	Revenue      decimal.Decimal `json:"revenue"`
	CostOfSales  decimal.Decimal `json:"cost_of_sales"`
	Expenses     decimal.Decimal `json:"expenses"`
	Net          decimal.Decimal `json:"net"`
}

type CostCenterReport struct {
	Rows  []CostCenterRow `json:"rows"`
	Total CostCenterRow   `json:"total"`
}

// CostCenters summarises income-statement activity per cost center.
func CostCenters(ctx context.Context, conn db.DBTX, companyID int64, from, to civil.Date) (*CostCenterReport, error) {
	from, to = Range(from, to)
	rows, err := conn.Query(ctx, `
		SELECT cc.id, cc.code, cc.name,
		       COALESCE(SUM(CASE WHEN a.type = 'revenue' THEN l.credit - l.debit END), 0),
		       COALESCE(SUM(CASE WHEN a.type = 'cost_of_sales' THEN l.debit - l.credit END), 0),
		       COALESCE(SUM(CASE WHEN a.type = 'expense' THEN l.debit - l.credit END), 0)
		FROM journal_entry_lines l
		JOIN journal_entries e ON e.id = l.entry_id
		JOIN accounts a ON a.id = l.account_id
		LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		WHERE e.company_id = $1 AND e.posted_at IS NOT NULL AND e.date BETWEEN $2 AND $3 AND e.source_type <> 'closing'
		  AND a.type IN ('revenue', 'cost_of_sales', 'expense')
		GROUP BY cc.id ORDER BY cc.code NULLS LAST`, companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &CostCenterReport{Rows: []CostCenterRow{}}
	for rows.Next() {
		var r CostCenterRow
		if err := rows.Scan(&r.CostCenterID, &r.Code, &r.Name, &r.Revenue, &r.CostOfSales, &r.Expenses); err != nil {
			return nil, err
		}
		r.Net = r.Revenue.Sub(r.CostOfSales).Sub(r.Expenses)
		out.Total.Revenue = out.Total.Revenue.Add(r.Revenue)
		out.Total.CostOfSales = out.Total.CostOfSales.Add(r.CostOfSales)
		out.Total.Expenses = out.Total.Expenses.Add(r.Expenses)
		out.Total.Net = out.Total.Net.Add(r.Net)
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}
