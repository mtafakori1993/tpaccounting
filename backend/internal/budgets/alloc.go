// Package budgets implements versioned budgets per fiscal year, their
// comparison with posted ledger actuals, and budget control applied when
// journal entries are posted. The ledger stays the single source of actuals:
// nothing here stores an "actual" balance.
package budgets

import (
	"github.com/shopspring/decimal"
)

var hundred = decimal.NewFromInt(100)

// Allocation methods.
const (
	MethodManual     = "manual"
	MethodEqual      = "equal"
	MethodPercentage = "percentage"
	MethodHistorical = "historical"
)

// Distribute splits annual over n periods by weights (any non-negative scale).
// Rounding differences go to the last period with a non-zero weight so the
// parts always add up to annual exactly. Zero total weight means equal parts.
func Distribute(annual decimal.Decimal, weights []decimal.Decimal, n int, places int32) []decimal.Decimal {
	out := make([]decimal.Decimal, n)
	if n == 0 {
		return out
	}
	w := make([]decimal.Decimal, n)
	total := decimal.Zero
	for i := 0; i < n; i++ {
		if i < len(weights) && weights[i].IsPositive() {
			w[i] = weights[i]
			total = total.Add(w[i])
		}
	}
	if !total.IsPositive() {
		for i := range w {
			w[i] = decimal.NewFromInt(1)
		}
		total = decimal.NewFromInt(int64(n))
	}
	last := 0
	for i := range w {
		if w[i].IsPositive() {
			last = i
		}
	}
	rest := annual
	for i := range w {
		if i == last {
			out[i] = rest
			continue
		}
		out[i] = annual.Mul(w[i]).Div(total).Round(places)
		rest = rest.Sub(out[i])
	}
	return out
}

// Equal splits annual into n equal parts.
func Equal(annual decimal.Decimal, n int, places int32) []decimal.Decimal {
	return Distribute(annual, nil, n, places)
}

// Sum adds decimals.
func Sum(vs []decimal.Decimal) decimal.Decimal {
	s := decimal.Zero
	for _, v := range vs {
		s = s.Add(v)
	}
	return s
}

// Variance returns the favourable variance: budget − actual for spending,
// actual − budget for income.
func Variance(budget, actual decimal.Decimal, income bool) decimal.Decimal {
	if income {
		return actual.Sub(budget)
	}
	return budget.Sub(actual)
}

// Utilization is actual as a percentage of budget (nil when there is no budget).
func Utilization(budget, actual decimal.Decimal) *decimal.Decimal {
	if !budget.IsPositive() {
		return nil
	}
	u := actual.Mul(hundred).Div(budget).Round(2)
	return &u
}

// Forecast extrapolates the run rate of the elapsed periods over the year.
func Forecast(actualToDate decimal.Decimal, elapsed, total int) decimal.Decimal {
	if elapsed <= 0 || total <= 0 {
		return actualToDate
	}
	if elapsed >= total {
		return actualToDate
	}
	return actualToDate.Add(actualToDate.Div(decimal.NewFromInt(int64(elapsed))).Mul(decimal.NewFromInt(int64(total - elapsed)))).Round(2)
}
