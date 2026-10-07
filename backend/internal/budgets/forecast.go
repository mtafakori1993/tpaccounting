package budgets

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// Forecasts project the full-year actual of a budget line. They are plain
// arithmetic on ledger actuals (or a manager's figure) — not statistical or
// "AI" predictions — and every result states its method and basis. Methods are
// pluggable: implement Forecaster and register it.

const (
	ForecastStraightLine = "straight_line"
	ForecastHistorical   = "historical_average"
	ForecastPriorYear    = "previous_year"
	ForecastManual       = "manual"
)

// ForecastInput is what a forecaster may use for one line.
type ForecastInput struct {
	Actuals []decimal.Decimal // per period of the fiscal year (index seq−1)
	Elapsed int               // periods started as of the analysis date
	Total   int               // periods in the year
	Prior   []decimal.Decimal // actuals of the previous fiscal year by period (nil = none)
	Manual  *ManualForecast   // latest manager override (nil = none)
	Window  int               // trailing periods for the historical average
	Places  int32
}

func (in ForecastInput) actualToDate() decimal.Decimal {
	s := decimal.Zero
	for i := 0; i < in.Elapsed && i < len(in.Actuals); i++ {
		s = s.Add(in.Actuals[i])
	}
	return s
}

// ForecastResult is a projected full-year amount and how it was obtained.
type ForecastResult struct {
	Method string          `json:"method"`
	Amount decimal.Decimal `json:"amount"`
	// Basis is a code describing the data used, e.g. "run_rate", "fallback_straight_line".
	Basis string `json:"basis"`
	// Periods is the number of periods of data the figure rests on.
	Periods int `json:"periods"`
	// Coverage grades how much data is behind the figure: high (≥ 6 periods),
	// medium (≥ 3), low, or manual. It is a data-sufficiency label, not a
	// statistical confidence interval.
	Coverage string `json:"coverage"`
}

type Forecaster interface {
	Code() string
	Forecast(in ForecastInput) ForecastResult
}

var forecasters = map[string]Forecaster{}

// RegisterForecaster adds a forecast method.
func RegisterForecaster(f Forecaster) { forecasters[f.Code()] = f }

// ForecastMethods lists the registered methods.
func ForecastMethods() []string {
	out := make([]string, 0, len(forecasters))
	for k := range forecasters {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return methodOrder[out[i]] < methodOrder[out[j]] })
	return out
}

var methodOrder = map[string]int{ForecastStraightLine: 1, ForecastHistorical: 2, ForecastPriorYear: 3, ForecastManual: 4}

func init() {
	RegisterForecaster(straightLine{})
	RegisterForecaster(historicalAverage{})
	RegisterForecaster(previousYear{})
	RegisterForecaster(manualForecast{})
}

func coverage(periods int) string {
	switch {
	case periods >= 6:
		return "high"
	case periods >= 3:
		return "medium"
	}
	return "low"
}

// straightLine extrapolates the year-to-date run rate (the original baseline).
type straightLine struct{}

func (straightLine) Code() string { return ForecastStraightLine }
func (straightLine) Forecast(in ForecastInput) ForecastResult {
	return ForecastResult{Method: ForecastStraightLine, Amount: Forecast(in.actualToDate(), in.Elapsed, in.Total), Basis: "run_rate",
		Periods: in.Elapsed, Coverage: coverage(in.Elapsed)}
}

// historicalAverage projects the remaining periods at the average of the last
// completed periods (the current period is still running, so it is excluded).
type historicalAverage struct{}

func (historicalAverage) Code() string { return ForecastHistorical }
func (historicalAverage) Forecast(in ForecastInput) ForecastResult {
	window := in.Window
	if window <= 0 {
		window = 3
	}
	completed := in.Elapsed - 1
	if in.Elapsed >= in.Total {
		completed = in.Total
	}
	if completed <= 0 {
		r := straightLine{}.Forecast(in)
		r.Method, r.Basis = ForecastHistorical, "fallback_straight_line"
		return r
	}
	from := completed - window
	if from < 0 {
		from = 0
	}
	sum := decimal.Zero
	for i := from; i < completed; i++ {
		sum = sum.Add(in.Actuals[i])
	}
	used := completed - from
	avg := sum.Div(decimal.NewFromInt(int64(used)))
	remaining := in.Total - in.Elapsed
	amount := in.actualToDate().Add(avg.Mul(decimal.NewFromInt(int64(remaining)))).Round(in.Places)
	return ForecastResult{Method: ForecastHistorical, Amount: amount, Basis: "trailing_average", Periods: used, Coverage: coverage(used)}
}

// previousYear adds last year's actuals of the remaining periods to the actual to date.
type previousYear struct{}

func (previousYear) Code() string { return ForecastPriorYear }
func (previousYear) Forecast(in ForecastInput) ForecastResult {
	if in.Prior == nil {
		r := straightLine{}.Forecast(in)
		r.Method, r.Basis = ForecastPriorYear, "fallback_no_prior_year"
		return r
	}
	amount := in.actualToDate()
	for i := in.Elapsed; i < in.Total && i < len(in.Prior); i++ {
		amount = amount.Add(in.Prior[i])
	}
	return ForecastResult{Method: ForecastPriorYear, Amount: amount, Basis: "prior_year_same_periods", Periods: len(in.Prior), Coverage: coverage(len(in.Prior))}
}

// manualForecast uses the manager's figure; without one it falls back to the run rate.
type manualForecast struct{}

func (manualForecast) Code() string { return ForecastManual }
func (manualForecast) Forecast(in ForecastInput) ForecastResult {
	if in.Manual == nil {
		r := straightLine{}.Forecast(in)
		r.Method, r.Basis = ForecastManual, "fallback_no_manual"
		return r
	}
	return ForecastResult{Method: ForecastManual, Amount: in.Manual.Amount, Basis: "manager_override", Coverage: "manual"}
}

// RunForecast applies a method; unknown methods use the straight line.
func RunForecast(method string, in ForecastInput) ForecastResult {
	f, ok := forecasters[method]
	if !ok {
		f = straightLine{}
	}
	return f.Forecast(in)
}

// ManualForecast is a manager's override of a line's full-year forecast.
type ManualForecast struct {
	ID        int64           `json:"id"`
	LineID    int64           `json:"line_id"`
	Amount    decimal.Decimal `json:"amount"`
	Reason    string          `json:"reason"`
	UserName  *string         `json:"user_name"`
	CreatedAt time.Time       `json:"created_at"`
}

func scanManual(rows interface{ Scan(...any) error }, m *ManualForecast) error {
	return rows.Scan(&m.ID, &m.LineID, &m.Amount, &m.Reason, &m.UserName, &m.CreatedAt)
}

// manualForecasts returns the forecast in force (newest) per line.
func manualForecasts(ctx context.Context, q db.DBTX, companyID int64, lineIDs []int64) (map[int64]*ManualForecast, error) {
	out := map[int64]*ManualForecast{}
	if len(lineIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON (f.line_id) f.id, f.line_id, f.amount, f.reason, u.full_name, f.created_at
		FROM budget_forecasts f LEFT JOIN users u ON u.id = f.created_by WHERE f.company_id = $1 AND f.line_id = ANY($2) ORDER BY f.line_id, f.id DESC`,
		companyID, lineIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m ManualForecast
		if err := scanManual(rows, &m); err != nil {
			return nil, err
		}
		out[m.LineID] = &m
	}
	return out, rows.Err()
}

// ForecastHistory lists every manual forecast of a line, newest first.
func ForecastHistory(ctx context.Context, q db.DBTX, companyID, lineID int64) ([]ManualForecast, error) {
	rows, err := q.Query(ctx, `SELECT f.id, f.line_id, f.amount, f.reason, u.full_name, f.created_at FROM budget_forecasts f
		LEFT JOIN users u ON u.id = f.created_by WHERE f.company_id = $1 AND f.line_id = $2 ORDER BY f.id DESC`, companyID, lineID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManualForecast{}
	for rows.Next() {
		var m ManualForecast
		if err := scanManual(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type ManualInput struct {
	Amount decimal.Decimal `json:"amount"`
	Reason string          `json:"reason"`
}

// SetManualForecast records a manager's forecast for a line of an approved or active budget.
func SetManualForecast(ctx context.Context, q db.DBTX, companyID, userID, lineID int64, in ManualInput) (int64, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return 0, err
	}
	v := apperr.NewValidator()
	v.Check(!in.Amount.IsNegative(), "amount", "negative")
	v.Check(in.Amount.Equal(in.Amount.Round(places)), "amount", "too_many_decimals")
	v.Required("reason", in.Reason)
	v.MaxLen("reason", in.Reason, 500)
	if err := v.Err(); err != nil {
		return 0, err
	}
	var status string
	err = q.QueryRow(ctx, `SELECT b.status FROM budget_lines l JOIN budgets b ON b.id = l.budget_id WHERE b.company_id = $1 AND l.id = $2`, companyID, lineID).Scan(&status)
	if db.IsNoRows(err) {
		return 0, apperr.NotFound("budget_line")
	}
	if err != nil {
		return 0, err
	}
	if status == StatusArchived {
		return 0, apperr.Rule("budget_archived", "forecasts cannot be set on an archived version")
	}
	var id int64
	err = q.QueryRow(ctx, `INSERT INTO budget_forecasts (company_id, line_id, amount, reason, created_by) VALUES ($1, $2, $3, $4, NULLIF($5::bigint, 0)) RETURNING id`,
		companyID, lineID, in.Amount, in.Reason, userID).Scan(&id)
	return id, err
}

// priorActualSQL is actualSQL against another fiscal year's periods.
const priorActualSQL = `
	SELECT bl.id, ap.seq,
	       SUM(CASE WHEN ba.type IN ('revenue', 'liability', 'equity') THEN l.credit - l.debit ELSE l.debit - l.credit END)
	FROM budget_lines bl
	JOIN budgets b ON b.id = bl.budget_id
	JOIN accounts ba ON ba.id = bl.account_id
	JOIN accounting_periods ap ON ap.fiscal_year_id = $3
	JOIN journal_entries e ON e.company_id = b.company_id AND e.posted_at IS NOT NULL AND e.source_type <> 'closing'
	                      AND e.date BETWEEN ap.start_date AND ap.end_date
	JOIN journal_entry_lines l ON l.entry_id = e.id
	JOIN accounts la ON la.id = l.account_id AND la.path LIKE ba.path || '%'
	WHERE b.company_id = $1 AND bl.budget_id = ANY($2) AND (bl.cost_center_id IS NULL OR l.cost_center_id = bl.cost_center_id)
	GROUP BY bl.id, ap.seq`

// PriorActuals returns, per line, the previous fiscal year's actuals by period
// (nil map when there is no previous fiscal year with the same period count).
func PriorActuals(ctx context.Context, q db.DBTX, companyID, fiscalYearID int64, budgetIDs []int64, n int) (map[int64][]decimal.Decimal, bool, error) {
	var prior int64
	var periods int
	err := q.QueryRow(ctx, `SELECT p.id, (SELECT count(*) FROM accounting_periods ap WHERE ap.fiscal_year_id = p.id)
		FROM fiscal_years cur JOIN fiscal_years p ON p.company_id = cur.company_id AND p.end_date < cur.start_date
		WHERE cur.id = $1 AND cur.company_id = $2 ORDER BY p.end_date DESC LIMIT 1`, fiscalYearID, companyID).Scan(&prior, &periods)
	if db.IsNoRows(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if periods != n || len(budgetIDs) == 0 {
		return nil, false, nil
	}
	rows, err := q.Query(ctx, priorActualSQL, companyID, budgetIDs, prior)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := map[int64][]decimal.Decimal{}
	for rows.Next() {
		var lid int64
		var seq int
		var amt decimal.Decimal
		if err := rows.Scan(&lid, &seq, &amt); err != nil {
			return nil, false, err
		}
		if out[lid] == nil {
			out[lid] = make([]decimal.Decimal, n)
		}
		if seq >= 1 && seq <= n {
			out[lid][seq-1] = amt
		}
	}
	return out, true, rows.Err()
}
