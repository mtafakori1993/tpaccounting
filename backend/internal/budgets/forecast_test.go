package budgets

import (
	"testing"

	"github.com/shopspring/decimal"
)

func ds(vs ...string) []decimal.Decimal {
	out := make([]decimal.Decimal, len(vs))
	for i, v := range vs {
		out[i] = decimal.RequireFromString(v)
	}
	return out
}

func TestForecastMethods(t *testing.T) {
	// 12 periods, 4 started (the 4th is running).
	in := ForecastInput{Actuals: ds("100", "200", "300", "40", "0", "0", "0", "0", "0", "0", "0", "0"), Elapsed: 4, Total: 12, Places: 0}

	sl := RunForecast(ForecastStraightLine, in)
	// 640 to date, run rate 160 → 640 + 8×160 = 1920.
	if !sl.Amount.Equal(decimal.NewFromInt(1920)) || sl.Basis != "run_rate" || sl.Coverage != "medium" {
		t.Errorf("straight line: %+v", sl)
	}

	ha := RunForecast(ForecastHistorical, in)
	// Last 3 completed periods average 200 → 640 + 8×200 = 2240.
	if !ha.Amount.Equal(decimal.NewFromInt(2240)) || ha.Periods != 3 || ha.Basis != "trailing_average" {
		t.Errorf("historical: %+v", ha)
	}

	py := RunForecast(ForecastPriorYear, in)
	if py.Basis != "fallback_no_prior_year" || !py.Amount.Equal(sl.Amount) {
		t.Errorf("previous year without data must fall back: %+v", py)
	}
	in.Prior = ds("90", "90", "90", "90", "50", "50", "50", "50", "50", "50", "50", "50")
	py = RunForecast(ForecastPriorYear, in)
	// 640 + last year's periods 5–12 (8×50).
	if !py.Amount.Equal(decimal.NewFromInt(1040)) || py.Basis != "prior_year_same_periods" {
		t.Errorf("previous year: %+v", py)
	}

	mf := RunForecast(ForecastManual, in)
	if mf.Basis != "fallback_no_manual" {
		t.Errorf("manual without override: %+v", mf)
	}
	in.Manual = &ManualForecast{Amount: decimal.NewFromInt(5000)}
	mf = RunForecast(ForecastManual, in)
	if !mf.Amount.Equal(decimal.NewFromInt(5000)) || mf.Coverage != "manual" || mf.Method != ForecastManual {
		t.Errorf("manual: %+v", mf)
	}

	if RunForecast("crystal_ball", in).Method != ForecastStraightLine {
		t.Error("unknown methods must use the straight line")
	}
	if got := ForecastMethods(); len(got) != 4 || got[0] != ForecastStraightLine || got[3] != ForecastManual {
		t.Errorf("methods: %v", got)
	}
}

func TestHistoricalAverageEarlyYear(t *testing.T) {
	in := ForecastInput{Actuals: ds("50", "0", "0"), Elapsed: 1, Total: 3}
	if r := RunForecast(ForecastHistorical, in); r.Basis != "fallback_straight_line" {
		t.Errorf("no completed period: %+v", r)
	}
}
