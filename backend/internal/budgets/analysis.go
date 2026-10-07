package budgets

import (
	"context"
	"sort"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// actualSQL returns, per budget line and period, the posted ledger activity of
// the line's account subtree (and cost center, when the line has one). Revenue
// is measured as credit − debit, everything else as debit − credit. Closing
// entries are excluded; cancelled entries net out with their reversals.
const actualSQL = `
	SELECT bl.id, ap.seq,
	       SUM(CASE WHEN ba.type IN ('revenue', 'liability', 'equity') THEN l.credit - l.debit ELSE l.debit - l.credit END)
	FROM budget_lines bl
	JOIN budgets b ON b.id = bl.budget_id
	JOIN accounts ba ON ba.id = bl.account_id
	JOIN accounting_periods ap ON ap.fiscal_year_id = b.fiscal_year_id
	JOIN journal_entries e ON e.company_id = b.company_id AND e.posted_at IS NOT NULL AND e.source_type <> 'closing'
	                      AND e.date BETWEEN ap.start_date AND ap.end_date
	JOIN journal_entry_lines l ON l.entry_id = e.id
	JOIN accounts la ON la.id = l.account_id AND la.path LIKE ba.path || '%'
	WHERE b.company_id = $1 AND bl.budget_id = ANY($2) AND (bl.cost_center_id IS NULL OR l.cost_center_id = bl.cost_center_id)
	GROUP BY bl.id, ap.seq`

// Actuals returns line id → actual per period (index seq−1).
func Actuals(ctx context.Context, q db.DBTX, companyID int64, budgetIDs []int64, n int) (map[int64][]decimal.Decimal, error) {
	out := map[int64][]decimal.Decimal{}
	if len(budgetIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, actualSQL, companyID, budgetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var lid int64
		var seq int
		var amt decimal.Decimal
		if err := rows.Scan(&lid, &seq, &amt); err != nil {
			return nil, err
		}
		if out[lid] == nil {
			out[lid] = make([]decimal.Decimal, n)
		}
		if seq >= 1 && seq <= n {
			out[lid][seq-1] = amt
		}
	}
	return out, rows.Err()
}

type Cell struct {
	Seq    int             `json:"seq"`
	Budget decimal.Decimal `json:"budget"`
	Actual decimal.Decimal `json:"actual"`
}

type Row struct {
	Key            string           `json:"key"`
	Label          string           `json:"label"`
	BudgetID       *int64           `json:"budget_id,omitempty"`
	BudgetName     string           `json:"budget_name,omitempty"`
	LineID         *int64           `json:"line_id,omitempty"`
	AccountID      *int64           `json:"account_id,omitempty"`
	AccountCode    string           `json:"account_code,omitempty"`
	AccountName    string           `json:"account_name,omitempty"`
	AccountNameEn  string           `json:"account_name_en,omitempty"`
	CostCenterID   *int64           `json:"cost_center_id,omitempty"`
	CostCenterName *string          `json:"cost_center_name,omitempty"`
	CostCenterKind *string          `json:"cost_center_kind,omitempty"`
	Kind           string           `json:"kind"`
	Budget         decimal.Decimal  `json:"budget"`
	Actual         decimal.Decimal  `json:"actual"`
	Variance       decimal.Decimal  `json:"variance"`
	Utilization    *decimal.Decimal `json:"utilization"`
	AnnualBudget   decimal.Decimal  `json:"annual_budget"`
	ActualYTD      decimal.Decimal  `json:"actual_ytd"`
	Forecast       decimal.Decimal  `json:"forecast"`
	ForecastVar    decimal.Decimal  `json:"forecast_variance"`
	// How the forecast was obtained (see forecast.go).
	ForecastMethod   string          `json:"forecast_method,omitempty"`
	ForecastBasis    string          `json:"forecast_basis,omitempty"`
	ForecastPeriods  int             `json:"forecast_periods,omitempty"`
	ForecastCoverage string          `json:"forecast_coverage,omitempty"`
	ManualForecast   *ManualForecast `json:"manual_forecast,omitempty"`
	Over             bool            `json:"over"`
	Months           []Cell          `json:"months,omitempty"`
}

type KindTotals struct {
	Budget      decimal.Decimal  `json:"budget"`
	Actual      decimal.Decimal  `json:"actual"`
	Variance    decimal.Decimal  `json:"variance"`
	Annual      decimal.Decimal  `json:"annual"`
	Remaining   decimal.Decimal  `json:"remaining"`
	Forecast    decimal.Decimal  `json:"forecast"`
	Utilization *decimal.Decimal `json:"utilization"`
}

type Analysis struct {
	FiscalYearID int64        `json:"fiscal_year_id"`
	Budgets      []BudgetRef  `json:"budgets"`
	Periods      []PeriodInfo `json:"periods"`
	FromSeq      int          `json:"from_seq"`
	ToSeq        int          `json:"to_seq"`
	ElapsedSeq   int          `json:"elapsed_seq"`
	GroupBy      string       `json:"group_by"`
	// ForecastMethod applied to every row, the methods available and whether
	// a previous fiscal year exists for the previous-year method.
	ForecastMethod  string        `json:"forecast_method"`
	ForecastMethods []string      `json:"forecast_methods"`
	HasPriorYear    bool          `json:"has_prior_year"`
	Rows            []Row         `json:"rows"`
	Expense         KindTotals    `json:"expense"`
	Income          KindTotals    `json:"income"`
	Monthly         []MonthTotals `json:"monthly"`
}

type BudgetRef struct {
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

type MonthTotals struct {
	Seq           int             `json:"seq"`
	Name          string          `json:"name"`
	ExpenseBudget decimal.Decimal `json:"expense_budget"`
	ExpenseActual decimal.Decimal `json:"expense_actual"`
	IncomeBudget  decimal.Decimal `json:"income_budget"`
	IncomeActual  decimal.Decimal `json:"income_actual"`
}

type Filter struct {
	BudgetID     int64
	FiscalYearID int64
	FromSeq      int
	ToSeq        int
	Kind         string // expense | income | "" (all)
	CostCenterID int64
	GroupBy      string // line | account | cost_center | budget | month
	OverOnly     bool
	AsOf         civil.Date
	// ForecastMethod: straight_line (default) | historical_average | previous_year | manual.
	ForecastMethod string
}

// resolveBudgets picks the budgets analysed: one version, or all active budgets of the fiscal year.
func resolveBudgets(ctx context.Context, q db.DBTX, companyID int64, f *Filter) ([]Budget, error) {
	if f.BudgetID > 0 {
		b, err := Get(ctx, q, companyID, f.BudgetID, false)
		if err != nil {
			return nil, err
		}
		f.FiscalYearID = b.FiscalYearID
		return []Budget{*b}, nil
	}
	if f.FiscalYearID == 0 {
		err := q.QueryRow(ctx, `SELECT id FROM fiscal_years WHERE company_id = $1 ORDER BY ($2::date BETWEEN start_date AND end_date) DESC, start_date DESC LIMIT 1`,
			companyID, f.AsOf).Scan(&f.FiscalYearID)
		if db.IsNoRows(err) {
			return nil, apperr.Rule("no_fiscal_year", "no fiscal year").WithParams("date", f.AsOf.String())
		}
		if err != nil {
			return nil, err
		}
	}
	return List(ctx, q, companyID, ListFilter{FiscalYearID: f.FiscalYearID, Status: StatusActive})
}

// Analyze compares budgets with ledger actuals.
func Analyze(ctx context.Context, q db.DBTX, companyID int64, f Filter) (*Analysis, error) {
	if f.AsOf.IsZero() {
		f.AsOf = civil.Today()
	}
	budgets, err := resolveBudgets(ctx, q, companyID, &f)
	if err != nil {
		return nil, err
	}
	periods, err := Periods(ctx, q, f.FiscalYearID)
	if err != nil {
		return nil, err
	}
	n := len(periods)
	elapsed := 0
	for _, p := range periods {
		if !p.StartDate.After(f.AsOf) {
			elapsed = p.Seq
		}
	}
	if f.FromSeq < 1 {
		f.FromSeq = 1
	}
	if f.ToSeq < 1 || f.ToSeq > n {
		f.ToSeq = n
	}
	if f.GroupBy == "" {
		f.GroupBy = "line"
	}
	if _, ok := forecasters[f.ForecastMethod]; !ok {
		f.ForecastMethod = ForecastStraightLine
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	out := &Analysis{FiscalYearID: f.FiscalYearID, Budgets: []BudgetRef{}, Periods: periods, FromSeq: f.FromSeq, ToSeq: f.ToSeq, ElapsedSeq: elapsed,
		GroupBy: f.GroupBy, Rows: []Row{}, Monthly: make([]MonthTotals, n), ForecastMethod: f.ForecastMethod, ForecastMethods: ForecastMethods()}
	for i, p := range periods {
		out.Monthly[i] = MonthTotals{Seq: p.Seq, Name: p.Name}
	}
	ids := make([]int64, len(budgets))
	for i, b := range budgets {
		ids[i] = b.ID
		out.Budgets = append(out.Budgets, BudgetRef{ID: b.ID, Code: b.Code, Name: b.Name, Version: b.Version, Status: b.Status})
	}
	actuals, err := Actuals(ctx, q, companyID, ids, n)
	if err != nil {
		return nil, err
	}
	prior, hasPrior, err := PriorActuals(ctx, q, companyID, f.FiscalYearID, ids, n)
	if err != nil {
		return nil, err
	}
	out.HasPriorYear = hasPrior
	var lineIDs []int64
	var lineRows []Row
	budgetLines := map[int64][]Line{}
	for _, b := range budgets {
		lines, err := loadLines(ctx, q, b.ID, n)
		if err != nil {
			return nil, err
		}
		budgetLines[b.ID] = lines
		for _, l := range lines {
			lineIDs = append(lineIDs, l.ID)
		}
	}
	manual, err := manualForecasts(ctx, q, companyID, lineIDs)
	if err != nil {
		return nil, err
	}
	for _, b := range budgets {
		for _, l := range budgetLines[b.ID] {
			kind := Kind(l.AccountType)
			if f.Kind != "" && f.Kind != kind {
				continue
			}
			if f.CostCenterID > 0 && val(l.CostCenterID) != f.CostCenterID {
				continue
			}
			act := actuals[l.ID]
			if act == nil {
				act = make([]decimal.Decimal, n)
			}
			bid, lid, aid := b.ID, l.ID, l.AccountID
			r := Row{Key: strconv.FormatInt(l.ID, 10), Label: l.AccountCode + " " + l.AccountName, BudgetID: &bid, BudgetName: b.Name, LineID: &lid,
				AccountID: &aid, AccountCode: l.AccountCode, AccountName: l.AccountName, AccountNameEn: l.AccountNameEn, CostCenterID: l.CostCenterID,
				CostCenterName: l.CostCenterName, CostCenterKind: l.CostCenterKind, Kind: kind, AnnualBudget: l.AnnualAmount}
			for i := 0; i < n; i++ {
				seq := i + 1
				r.Months = append(r.Months, Cell{Seq: seq, Budget: l.Amounts[i], Actual: act[i]})
				if seq >= f.FromSeq && seq <= f.ToSeq {
					r.Budget = r.Budget.Add(l.Amounts[i])
					r.Actual = r.Actual.Add(act[i])
				}
				if seq <= elapsed {
					r.ActualYTD = r.ActualYTD.Add(act[i])
				}
				m := &out.Monthly[i]
				switch kind {
				case "expense":
					m.ExpenseBudget = m.ExpenseBudget.Add(l.Amounts[i])
					m.ExpenseActual = m.ExpenseActual.Add(act[i])
				case "income":
					m.IncomeBudget = m.IncomeBudget.Add(l.Amounts[i])
					m.IncomeActual = m.IncomeActual.Add(act[i])
				}
			}
			in := ForecastInput{Actuals: act, Elapsed: elapsed, Total: n, Manual: manual[l.ID], Places: places}
			if hasPrior {
				in.Prior = prior[l.ID]
				if in.Prior == nil {
					in.Prior = make([]decimal.Decimal, n)
				}
			}
			fr := RunForecast(f.ForecastMethod, in)
			r.Forecast, r.ForecastMethod, r.ForecastBasis, r.ForecastPeriods, r.ForecastCoverage = fr.Amount, fr.Method, fr.Basis, fr.Periods, fr.Coverage
			r.ManualForecast = manual[l.ID]
			finish(&r)
			lineRows = append(lineRows, r)
			t := &out.Expense
			if kind == "income" {
				t = &out.Income
			} else if kind != "expense" {
				continue
			}
			t.Budget = t.Budget.Add(r.Budget)
			t.Actual = t.Actual.Add(r.Actual)
			t.Annual = t.Annual.Add(r.AnnualBudget)
			t.Forecast = t.Forecast.Add(r.Forecast)
		}
	}
	for _, t := range []*KindTotals{&out.Expense, &out.Income} {
		t.Utilization = Utilization(t.Budget, t.Actual)
		t.Remaining = t.Annual.Sub(t.Actual)
	}
	out.Expense.Variance = Variance(out.Expense.Budget, out.Expense.Actual, false)
	out.Income.Variance = Variance(out.Income.Budget, out.Income.Actual, true)
	out.Rows = group(lineRows, f.GroupBy, periods)
	if f.OverOnly {
		filtered := []Row{}
		for _, r := range out.Rows {
			if r.Over {
				filtered = append(filtered, r)
			}
		}
		out.Rows = filtered
	}
	return out, nil
}

func finish(r *Row) {
	income := r.Kind == "income"
	r.Variance = Variance(r.Budget, r.Actual, income)
	r.Utilization = Utilization(r.Budget, r.Actual)
	r.ForecastVar = Variance(r.AnnualBudget, r.Forecast, income)
	r.Over = !income && r.Actual.GreaterThan(r.Budget)
}

// group aggregates line rows by the requested dimension.
func group(lines []Row, by string, periods []PeriodInfo) []Row {
	if by == "line" {
		if lines == nil {
			return []Row{}
		}
		return lines
	}
	if by == "month" {
		out := []Row{}
		for i, p := range periods {
			for _, kind := range []string{"expense", "income"} {
				r := Row{Key: strconv.Itoa(p.Seq) + ":" + kind, Label: p.Name, Kind: kind}
				for _, l := range lines {
					if l.Kind != kind || i >= len(l.Months) {
						continue
					}
					r.Budget = r.Budget.Add(l.Months[i].Budget)
					r.Actual = r.Actual.Add(l.Months[i].Actual)
					r.AnnualBudget = r.AnnualBudget.Add(l.Months[i].Budget)
				}
				if r.Budget.IsZero() && r.Actual.IsZero() {
					continue
				}
				r.Forecast = r.Actual
				r.ForecastBasis = "actual"
				finish(&r)
				out = append(out, r)
			}
		}
		return out
	}
	idx := map[string]int{}
	out := []Row{}
	for _, l := range lines {
		var key, label string
		r := Row{Kind: l.Kind, ForecastMethod: l.ForecastMethod}
		switch by {
		case "account":
			key, label = l.Kind+":"+strconv.FormatInt(*l.AccountID, 10), l.AccountCode+" "+l.AccountName
			r.AccountID, r.AccountCode, r.AccountName, r.AccountNameEn = l.AccountID, l.AccountCode, l.AccountName, l.AccountNameEn
		case "cost_center":
			key = l.Kind + ":" + strconv.FormatInt(val(l.CostCenterID), 10)
			if l.CostCenterName != nil {
				label = *l.CostCenterName
			}
			r.CostCenterID, r.CostCenterName, r.CostCenterKind = l.CostCenterID, l.CostCenterName, l.CostCenterKind
		default: // budget
			key, label = l.Kind+":"+strconv.FormatInt(*l.BudgetID, 10), l.BudgetName
			r.BudgetID, r.BudgetName = l.BudgetID, l.BudgetName
		}
		i, ok := idx[key]
		if !ok {
			r.Key, r.Label = key, label
			i = len(out)
			idx[key] = i
			out = append(out, r)
		}
		g := &out[i]
		g.Budget = g.Budget.Add(l.Budget)
		g.Actual = g.Actual.Add(l.Actual)
		g.AnnualBudget = g.AnnualBudget.Add(l.AnnualBudget)
		g.ActualYTD = g.ActualYTD.Add(l.ActualYTD)
		g.Forecast = g.Forecast.Add(l.Forecast)
		if len(g.Months) == 0 {
			g.Months = make([]Cell, len(l.Months))
		}
		for j, c := range l.Months {
			g.Months[j].Seq = c.Seq
			g.Months[j].Budget = g.Months[j].Budget.Add(c.Budget)
			g.Months[j].Actual = g.Months[j].Actual.Add(c.Actual)
		}
	}
	for i := range out {
		finish(&out[i])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Label < out[j].Label
	})
	return out
}

type Dashboard struct {
	*Analysis
	OverCount    int   `json:"over_count"`
	UnderCount   int   `json:"under_count"`
	TopVariances []Row `json:"top_variances"`
	OverItems    []Row `json:"over_items"`
	ByCostCenter []Row `json:"by_cost_center"`
	ByBudget     []Row `json:"by_budget"`
}

// DashboardData summarises year-to-date budget performance.
func DashboardData(ctx context.Context, q db.DBTX, companyID int64, f Filter) (*Dashboard, error) {
	f.GroupBy = "line"
	a, err := Analyze(ctx, q, companyID, f)
	if err != nil {
		return nil, err
	}
	// Without an explicit range the dashboard compares year to date.
	if f.ToSeq == 0 && a.ElapsedSeq > 0 && a.ElapsedSeq < len(a.Periods) {
		f.ToSeq = a.ElapsedSeq
		if a, err = Analyze(ctx, q, companyID, f); err != nil {
			return nil, err
		}
	}
	d := &Dashboard{Analysis: a, TopVariances: []Row{}, OverItems: []Row{}}
	lines := a.Rows
	for _, r := range lines {
		if r.Kind != "expense" {
			continue
		}
		if r.Over {
			d.OverCount++
			d.OverItems = append(d.OverItems, r)
		} else if r.Budget.IsPositive() {
			d.UnderCount++
		}
	}
	sorted := append([]Row(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Variance.Abs().GreaterThan(sorted[j].Variance.Abs()) })
	for _, r := range sorted {
		if len(d.TopVariances) == 8 {
			break
		}
		if !r.Variance.IsZero() {
			d.TopVariances = append(d.TopVariances, r)
		}
	}
	d.ByCostCenter = group(lines, "cost_center", a.Periods)
	d.ByBudget = group(lines, "budget", a.Periods)
	for _, rs := range [][]Row{d.ByCostCenter, d.ByBudget, d.OverItems, d.TopVariances} {
		for i := range rs {
			rs[i].Months = nil
		}
	}
	for i := range a.Rows {
		a.Rows[i].Months = nil
	}
	return d, nil
}

type DrillRow struct {
	EntryID        int64           `json:"entry_id"`
	EntryNumber    int64           `json:"entry_number"`
	Date           civil.Date      `json:"date"`
	Description    string          `json:"description"`
	SourceType     string          `json:"source_type"`
	SourceID       *int64          `json:"source_id"`
	AccountCode    string          `json:"account_code"`
	AccountName    string          `json:"account_name"`
	CostCenterName *string         `json:"cost_center_name"`
	PartyName      *string         `json:"party_name"`
	LineDesc       string          `json:"line_description"`
	Amount         decimal.Decimal `json:"amount"`
}

// Drilldown lists the posted journal lines behind a budget line's actual.
func Drilldown(ctx context.Context, q db.DBTX, companyID, lineID int64, fromSeq, toSeq int) ([]DrillRow, error) {
	var fyID, accountID int64
	var ccID *int64
	err := q.QueryRow(ctx, `SELECT b.fiscal_year_id, l.account_id, l.cost_center_id FROM budget_lines l JOIN budgets b ON b.id = l.budget_id
		WHERE b.company_id = $1 AND l.id = $2`, companyID, lineID).Scan(&fyID, &accountID, &ccID)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("budget_line")
	}
	if err != nil {
		return nil, err
	}
	if fromSeq < 1 {
		fromSeq = 1
	}
	if toSeq < fromSeq {
		toSeq = 24
	}
	rows, err := q.Query(ctx, `
		SELECT e.id, e.number, e.date, e.description, e.source_type, e.source_id, la.code, la.name, cc.name, p.name, l.description,
		       CASE WHEN ba.type IN ('revenue', 'liability', 'equity') THEN l.credit - l.debit ELSE l.debit - l.credit END
		FROM accounts ba
		JOIN accounting_periods ap ON ap.fiscal_year_id = $3 AND ap.seq BETWEEN $5 AND $6
		JOIN journal_entries e ON e.company_id = $1 AND e.posted_at IS NOT NULL AND e.source_type <> 'closing' AND e.date BETWEEN ap.start_date AND ap.end_date
		JOIN journal_entry_lines l ON l.entry_id = e.id
		JOIN accounts la ON la.id = l.account_id AND la.path LIKE ba.path || '%'
		LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		LEFT JOIN parties p ON p.id = l.party_id
		WHERE ba.id = $2 AND ($4::bigint IS NULL OR l.cost_center_id = $4)
		ORDER BY e.date, e.number, l.line_no LIMIT 2000`, companyID, accountID, fyID, ccID, fromSeq, toSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DrillRow{}
	for rows.Next() {
		var r DrillRow
		if err := rows.Scan(&r.EntryID, &r.EntryNumber, &r.Date, &r.Description, &r.SourceType, &r.SourceID, &r.AccountCode, &r.AccountName,
			&r.CostCenterName, &r.PartyName, &r.LineDesc, &r.Amount); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
