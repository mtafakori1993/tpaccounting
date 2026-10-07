package reports

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.Use(identity.Require("reports.view"))
	r.Get("/journal", httpx.Handler(h.journal))
	r.Get("/ledger", httpx.Handler(h.ledger))
	r.Get("/trial-balance", httpx.Handler(h.trialBalance))
	r.Get("/profit-loss", httpx.Handler(h.profitLoss))
	r.Get("/balance-sheet", httpx.Handler(h.balanceSheet))
	r.Get("/cash-position", httpx.Handler(h.cashPosition))
	r.Get("/receivables", httpx.Handler(h.aging(false)))
	r.Get("/payables", httpx.Handler(h.aging(true)))
	r.Get("/sales", httpx.Handler(h.trade("sales")))
	r.Get("/purchases", httpx.Handler(h.trade("purchase")))
	r.Get("/expenses", httpx.Handler(h.expenses))
	r.Get("/cost-centers", httpx.Handler(h.costCenters))
}

// DashboardHandler serves the dashboard summary (any authenticated user).
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) error {
	d, err := DashboardData(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, d)
	return nil
}

func dates(r *http.Request) (civil.Date, civil.Date, error) {
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return from, from, err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return from, to, err
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		v := apperr.NewValidator()
		v.Check(false, "to", "end_before_start")
		return from, to, v.Err()
	}
	return from, to, nil
}

func level(r *http.Request, def int) int {
	if l, err := strconv.Atoi(r.URL.Query().Get("level")); err == nil && l >= 1 && l <= 4 {
		return l
	}
	return def
}

func (h *Handler) journal(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := Journal(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to, r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) ledger(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := AccountLedger(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, LedgerFilter{
		AccountID: httpx.QueryInt64(r, "account_id"), PartyID: httpx.QueryInt64(r, "party_id"),
		CostCenterID: httpx.QueryInt64(r, "cost_center_id"), From: from, To: to, Q: r.URL.Query().Get("q"),
	})
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) trialBalance(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	out, err := TrialBalanceReport(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to, level(r, 3),
		q.Get("exclude_closing") == "true", q.Get("show_zero") == "true")
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) profitLoss(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := ProfitLossReport(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to, level(r, 3), httpx.QueryInt64(r, "cost_center_id"))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) balanceSheet(w http.ResponseWriter, r *http.Request) error {
	asOf, err := httpx.QueryDate(r, "as_of")
	if err != nil {
		return err
	}
	if asOf.IsZero() {
		asOf = civil.Today()
	}
	out, err := BalanceSheetReport(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, asOf, level(r, 3))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) cashPosition(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := CashPositionReport(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to)
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) aging(payable bool) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		asOf, err := httpx.QueryDate(r, "as_of")
		if err != nil {
			return err
		}
		out, err := AgingReport(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, asOf, payable)
		if err != nil {
			return err
		}
		httpx.OK(w, out)
		return nil
	}
}

func (h *Handler) trade(side string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		from, to, err := dates(r)
		if err != nil {
			return err
		}
		p := identity.From(r.Context())
		var calendar string
		if err := h.Pool.QueryRow(r.Context(), `SELECT calendar FROM companies WHERE id = $1`, p.CompanyID).Scan(&calendar); err != nil {
			return err
		}
		out, err := TradeSummary(r.Context(), h.Pool, p.CompanyID, side, from, to, r.URL.Query().Get("group_by"), calendar)
		if err != nil {
			return err
		}
		httpx.OK(w, out)
		return nil
	}
}

func (h *Handler) expenses(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := ExpensesSummary(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to, r.URL.Query().Get("group_by"),
		httpx.QueryInt64(r, "cost_center_id"))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) costCenters(w http.ResponseWriter, r *http.Request) error {
	from, to, err := dates(r)
	if err != nil {
		return err
	}
	out, err := CostCenters(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to)
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}
