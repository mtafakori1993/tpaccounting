package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

// Filter narrows check lists and reports.
type Filter struct {
	Type              string
	Statuses          []string
	Live              bool
	Overdue           bool
	BankID            int64
	PartyID           int64
	TreasuryAccountID int64
	DueFrom, DueTo    civil.Date
	MinAmount         decimal.Decimal
	MaxAmount         decimal.Decimal
}

// liveCond is the SQL condition for outstanding checks of either type.
const liveCond = `((c.check_type = 'received' AND c.status IN ('received', 'in_custody', 'deposited'))
	OR (c.check_type = 'issued' AND c.status IN ('issued', 'delivered')))`

func (lf Filter) apply(f *db.Filter, today civil.Date) {
	if lf.Type != "" {
		f.Add("c.check_type = $N", lf.Type)
	}
	if len(lf.Statuses) > 0 {
		f.Add("c.status = ANY($N)", lf.Statuses)
	}
	if lf.Live || lf.Overdue {
		f.AddRaw(liveCond)
	}
	if lf.Overdue {
		f.Add("c.due_date < $N", today)
	}
	if lf.BankID > 0 {
		f.Add("c.bank_id = $N", lf.BankID)
	}
	if lf.PartyID > 0 {
		f.Add("(c.party_id = $N OR c.holder_party_id = $N)", lf.PartyID)
	}
	if lf.TreasuryAccountID > 0 {
		f.Add("c.treasury_account_id = $N", lf.TreasuryAccountID)
	}
	if !lf.DueFrom.IsZero() {
		f.Add("c.due_date >= $N", lf.DueFrom)
	}
	if !lf.DueTo.IsZero() {
		f.Add("c.due_date <= $N", lf.DueTo)
	}
	if lf.MinAmount.IsPositive() {
		f.Add("c.amount >= $N", lf.MinAmount)
	}
	if lf.MaxAmount.IsPositive() {
		f.Add("c.amount <= $N", lf.MaxAmount)
	}
}

type ListResult struct {
	httpx.Page[Check]
	TotalAmount decimal.Decimal `json:"total_amount"`
}

func List(ctx context.Context, q db.DBTX, companyID int64, lf Filter, lp httpx.ListParams) (*ListResult, error) {
	today := civil.Today()
	f := db.NewFilter("c.company_id = $1", companyID)
	lf.apply(f, today)
	addSearch(f, lp.Q)
	out := &ListResult{}
	if err := q.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(c.amount), 0) FROM checks c JOIN parties p ON p.id = c.party_id WHERE `+f.Where(), f.Args...).
		Scan(&out.Total, &out.TotalAmount); err != nil {
		return nil, err
	}
	order := lp.OrderBy(map[string]string{
		"due_date": "c.due_date", "number": "c.number", "amount": "c.amount", "check_number": "c.check_number", "party": "p.name",
		"status": "c.status", "bank": "c.bank_name", "issue_date": "c.issue_date",
	}, "c.due_date")
	rows, err := q.Query(ctx, `SELECT `+cols+joins+` WHERE `+f.Where()+` ORDER BY `+order+`, c.id DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Check{}
	for rows.Next() {
		var x Check
		if err := scan(rows, &x, today); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	out.Page = httpx.NewPage(items, out.Total, lp)
	return out, rows.Err()
}

// Bucket is a count and an amount.
type Bucket struct {
	Count  int             `json:"count"`
	Amount decimal.Decimal `json:"amount"`
}

type Dashboard struct {
	Today              civil.Date `json:"today"`
	WeekEnd            civil.Date `json:"week_end"`
	ReceivedLive       Bucket     `json:"received_live"`
	IssuedLive         Bucket     `json:"issued_live"`
	InCustody          Bucket     `json:"in_custody"`
	AwaitingCollection Bucket     `json:"awaiting_collection"`
	DueTodayReceived   Bucket     `json:"due_today_received"`
	DueTodayIssued     Bucket     `json:"due_today_issued"`
	DueWeekReceived    Bucket     `json:"due_week_received"`
	DueWeekIssued      Bucket     `json:"due_week_issued"`
	OverdueReceived    Bucket     `json:"overdue_received"`
	OverdueIssued      Bucket     `json:"overdue_issued"`
	ReturnedReceived   Bucket     `json:"returned_received"`
	ReturnedIssued     Bucket     `json:"returned_issued"`
	AmountDue          Bucket     `json:"amount_due"`
	UpcomingReceipts   []Check    `json:"upcoming_receipts"`
	UpcomingPayments   []Check    `json:"upcoming_payments"`
}

// DashboardData summarises outstanding checks as of today.
func DashboardData(ctx context.Context, q db.DBTX, companyID int64) (*Dashboard, error) {
	today := civil.Today()
	d := &Dashboard{Today: today, WeekEnd: today.AddDays(6)}
	b := func(dst *Bucket, cond string, args ...any) error {
		all := append([]any{companyID}, args...)
		return q.QueryRow(ctx, `SELECT count(*), COALESCE(SUM(c.amount), 0) FROM checks c WHERE c.company_id = $1 AND `+cond, all...).Scan(&dst.Count, &dst.Amount)
	}
	recvLive := `c.check_type = 'received' AND c.status IN ('received', 'in_custody', 'deposited')`
	issLive := `c.check_type = 'issued' AND c.status IN ('issued', 'delivered')`
	steps := []struct {
		dst  *Bucket
		cond string
		args []any
	}{
		{&d.ReceivedLive, recvLive, nil},
		{&d.IssuedLive, issLive, nil},
		{&d.InCustody, `c.check_type = 'received' AND c.status IN ('received', 'in_custody')`, nil},
		{&d.AwaitingCollection, `c.check_type = 'received' AND c.status = 'deposited'`, nil},
		{&d.DueTodayReceived, recvLive + ` AND c.due_date = $2`, []any{today}},
		{&d.DueTodayIssued, issLive + ` AND c.due_date = $2`, []any{today}},
		{&d.DueWeekReceived, recvLive + ` AND c.due_date BETWEEN $2 AND $3`, []any{today, d.WeekEnd}},
		{&d.DueWeekIssued, issLive + ` AND c.due_date BETWEEN $2 AND $3`, []any{today, d.WeekEnd}},
		{&d.OverdueReceived, recvLive + ` AND c.due_date < $2`, []any{today}},
		{&d.OverdueIssued, issLive + ` AND c.due_date < $2`, []any{today}},
		{&d.ReturnedReceived, `c.check_type = 'received' AND c.status = 'returned'`, nil},
		{&d.ReturnedIssued, `c.check_type = 'issued' AND c.status = 'returned'`, nil},
		{&d.AmountDue, recvLive + ` AND c.due_date <= $2`, []any{today}},
	}
	for _, s := range steps {
		if err := b(s.dst, s.cond, s.args...); err != nil {
			return nil, err
		}
	}
	upcoming := func(cond string) ([]Check, error) {
		rows, err := q.Query(ctx, `SELECT `+cols+joins+` WHERE c.company_id = $1 AND `+cond+` AND c.due_date <= $2 ORDER BY c.due_date, c.id LIMIT 10`,
			companyID, today.AddDays(30))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []Check{}
		for rows.Next() {
			var x Check
			if err := scan(rows, &x, today); err != nil {
				return nil, err
			}
			out = append(out, x)
		}
		return out, rows.Err()
	}
	var err error
	if d.UpcomingReceipts, err = upcoming(recvLive); err != nil {
		return nil, err
	}
	if d.UpcomingPayments, err = upcoming(issLive); err != nil {
		return nil, err
	}
	return d, nil
}

type GroupRow struct {
	ID         *int64          `json:"id"`
	Label      string          `json:"label"`
	Count      int             `json:"count"`
	Amount     decimal.Decimal `json:"amount"`
	LiveCount  int             `json:"live_count"`
	LiveAmount decimal.Decimal `json:"live_amount"`
	Overdue    decimal.Decimal `json:"overdue_amount"`
}

type GroupReport struct {
	GroupBy string     `json:"group_by"`
	Rows    []GroupRow `json:"rows"`
	Total   GroupRow   `json:"total"`
}

// Grouped totals checks by bank, party, status or due month.
func Grouped(ctx context.Context, q db.DBTX, companyID int64, groupBy string, lf Filter) (*GroupReport, error) {
	today := civil.Today()
	var sel, group string
	switch groupBy {
	case "party":
		sel, group = "p.id, p.name", "p.id, p.name"
	case "status":
		sel, group = "NULL::bigint, c.check_type || ':' || c.status", "c.check_type, c.status"
	case "month":
		sel, group = "NULL::bigint, c.due_date::text", "c.due_date"
	default:
		groupBy = "bank"
		sel, group = "c.bank_id, COALESCE(NULLIF(c.bank_name, ''), b.name, '')", "c.bank_id, COALESCE(NULLIF(c.bank_name, ''), b.name, '')"
	}
	f := db.NewFilter("c.company_id = $1", companyID)
	lf.apply(f, today)
	todayArg := f.Arg(today)
	rows, err := q.Query(ctx, `SELECT `+sel+`, count(*), COALESCE(SUM(c.amount), 0),
		count(*) FILTER (WHERE `+liveCond+`), COALESCE(SUM(c.amount) FILTER (WHERE `+liveCond+`), 0),
		COALESCE(SUM(c.amount) FILTER (WHERE `+liveCond+` AND c.due_date < `+todayArg+`), 0)
		FROM checks c JOIN parties p ON p.id = c.party_id LEFT JOIN banks b ON b.id = c.bank_id
		WHERE `+f.Where()+` GROUP BY `+group+` ORDER BY 2`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &GroupReport{GroupBy: groupBy, Rows: []GroupRow{}}
	var calendar string
	if groupBy == "month" {
		if err := q.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1`, companyID).Scan(&calendar); err != nil {
			return nil, err
		}
	}
	months := map[string]int{}
	for rows.Next() {
		var r GroupRow
		if err := rows.Scan(&r.ID, &r.Label, &r.Count, &r.Amount, &r.LiveCount, &r.LiveAmount, &r.Overdue); err != nil {
			return nil, err
		}
		t := &out.Total
		t.Count += r.Count
		t.Amount = t.Amount.Add(r.Amount)
		t.LiveCount += r.LiveCount
		t.LiveAmount = t.LiveAmount.Add(r.LiveAmount)
		t.Overdue = t.Overdue.Add(r.Overdue)
		if groupBy == "month" {
			d, err := civil.Parse(r.Label)
			if err != nil {
				return nil, err
			}
			k := MonthKey(d, calendar)
			if i, ok := months[k]; ok {
				m := &out.Rows[i]
				m.Count += r.Count
				m.Amount = m.Amount.Add(r.Amount)
				m.LiveCount += r.LiveCount
				m.LiveAmount = m.LiveAmount.Add(r.LiveAmount)
				m.Overdue = m.Overdue.Add(r.Overdue)
				continue
			}
			months[k] = len(out.Rows)
			r.Label = k
		}
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

// MonthKey returns "YYYY-MM" in the company calendar.
func MonthKey(d civil.Date, calendar string) string {
	if calendar == "jalali" {
		y, m, _ := jalali.FromGregorian(d.Time)
		return fmt.Sprintf("%04d-%02d", y, m)
	}
	return d.Format("2006-01")
}

type FlowRow struct {
	Key        string          `json:"key"`
	From       civil.Date      `json:"from"`
	Inflow     decimal.Decimal `json:"inflow"`
	Outflow    decimal.Decimal `json:"outflow"`
	Net        decimal.Decimal `json:"net"`
	Cumulative decimal.Decimal `json:"cumulative"`
	InCount    int             `json:"in_count"`
	OutCount   int             `json:"out_count"`
}

type CashFlow struct {
	GroupBy string          `json:"group_by"`
	Opening decimal.Decimal `json:"overdue_net"`
	Rows    []FlowRow       `json:"rows"`
	Inflow  decimal.Decimal `json:"inflow"`
	Outflow decimal.Decimal `json:"outflow"`
	Net     decimal.Decimal `json:"net"`
}

// CashFlowImpact projects the cash effect of outstanding checks by due date:
// received checks are future inflows, issued checks future outflows. Overdue
// outstanding checks are reported separately as the opening position.
func CashFlowImpact(ctx context.Context, q db.DBTX, companyID int64, from, to civil.Date, groupBy string) (*CashFlow, error) {
	if from.IsZero() {
		from = civil.Today()
	}
	if to.IsZero() {
		to = from.AddDays(90)
	}
	if groupBy != "day" && groupBy != "week" {
		groupBy = "month"
	}
	var calendar string
	if err := q.QueryRow(ctx, `SELECT calendar FROM companies WHERE id = $1`, companyID).Scan(&calendar); err != nil {
		return nil, err
	}
	out := &CashFlow{GroupBy: groupBy, Rows: []FlowRow{}}
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN c.check_type = 'received' THEN c.amount ELSE -c.amount END), 0)
		FROM checks c WHERE c.company_id = $1 AND `+liveCond+` AND c.due_date < $2`, companyID, from).Scan(&out.Opening); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT c.due_date, c.check_type, count(*), SUM(c.amount) FROM checks c
		WHERE c.company_id = $1 AND `+liveCond+` AND c.due_date BETWEEN $2 AND $3 GROUP BY c.due_date, c.check_type ORDER BY c.due_date`,
		companyID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var d civil.Date
		var typ string
		var n int
		var amt decimal.Decimal
		if err := rows.Scan(&d, &typ, &n, &amt); err != nil {
			return nil, err
		}
		key, start := d.String(), d
		switch groupBy {
		case "month":
			key = MonthKey(d, calendar)
		case "week":
			// Weeks start on Saturday (Iranian week).
			offset := (int(d.Weekday()) + 1) % 7
			start = d.AddDays(-offset)
			key = start.String()
		}
		i, ok := idx[key]
		if !ok {
			i = len(out.Rows)
			idx[key] = i
			out.Rows = append(out.Rows, FlowRow{Key: key, From: start})
		}
		r := &out.Rows[i]
		if typ == TypeReceived {
			r.Inflow = r.Inflow.Add(amt)
			r.InCount += n
		} else {
			r.Outflow = r.Outflow.Add(amt)
			r.OutCount += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cum := out.Opening
	for i := range out.Rows {
		r := &out.Rows[i]
		r.Net = r.Inflow.Sub(r.Outflow)
		cum = cum.Add(r.Net)
		r.Cumulative = cum
		out.Inflow = out.Inflow.Add(r.Inflow)
		out.Outflow = out.Outflow.Add(r.Outflow)
	}
	out.Net = out.Inflow.Sub(out.Outflow)
	return out, nil
}

// splitList parses a comma-separated query value.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
