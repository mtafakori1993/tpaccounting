package budgets

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	view := identity.Require("budgets.view")
	r.With(view).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("budgets.create")).Post("/", httpx.Handler(h.create))
	r.With(view).Get("/dashboard", httpx.Handler(h.dashboard))
	r.With(view).Get("/drilldown", httpx.Handler(h.drilldown))
	r.With(view).Get("/reports/{report}", httpx.Handler(h.report))

	r.With(view).Get("/control", httpx.Handler(h.control))
	r.With(view).Get("/control/rules", httpx.Handler(h.listRules))
	r.With(identity.Require("budgets.edit")).Post("/control/rules", httpx.Handler(h.saveRule))
	r.With(identity.Require("budgets.edit")).Put("/control/rules/{id}", httpx.Handler(h.saveRule))
	r.With(identity.Require("budgets.edit")).Delete("/control/rules/{id}", httpx.Handler(h.deleteRule))
	r.With(view).Get("/control/events", httpx.Handler(h.events))
	r.With(view).Get("/control/events/{id}", httpx.Handler(h.event))

	r.With(view).Get("/lines/{id}/forecasts", httpx.Handler(h.forecastHistory))
	r.With(identity.Require("budgets.forecast")).Post("/lines/{id}/forecasts", httpx.Handler(h.setForecast))
	r.With(view).Post("/control/check", httpx.Handler(h.check))

	r.With(view).Get("/{id}", httpx.Handler(h.get))
	r.With(view).Get("/{id}/analysis", httpx.Handler(h.analysis))
	r.With(identity.Require("budgets.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("budgets.delete")).Delete("/{id}", httpx.Handler(h.delete))
	r.With(identity.Require("budgets.create")).Post("/{id}/revise", httpx.Handler(h.revise))
	for action := range workflow {
		r.Post("/{id}/"+action, httpx.Handler(h.transition(action)))
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	items, err := List(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, ListFilter{
		FiscalYearID: httpx.QueryInt64(r, "fiscal_year_id"), Status: q.Get("status"), LatestOnly: q.Get("latest") == "true",
	})
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	b, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id, true)
	if err != nil {
		return err
	}
	httpx.OK(w, b)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "budget", id, nil, in)
	})
	if err != nil {
		return err
	}
	b, err := Get(r.Context(), h.Pool, p.CompanyID, id, true)
	if err != nil {
		return err
	}
	httpx.Created(w, b)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id, true)
		if err != nil {
			return err
		}
		if err := Update(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "budget", id, before, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id, false)
		if err != nil {
			return err
		}
		if err := Delete(r.Context(), tx, p.CompanyID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "budget", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) transition(action string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p := identity.From(r.Context())
		if err := identity.Check(r.Context(), Permission(action)); err != nil {
			return err
		}
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
			before, err := Get(r.Context(), tx, p.CompanyID, id, false)
			if err != nil {
				return err
			}
			if err := Transition(r.Context(), tx, p.CompanyID, p.UserID, id, action); err != nil {
				return err
			}
			after, err := Get(r.Context(), tx, p.CompanyID, id, false)
			if err != nil {
				return err
			}
			return audit.Record(r.Context(), tx, "budget_"+action, "budget", id,
				map[string]any{"status": before.Status, "version": before.Version},
				map[string]any{"status": after.Status, "version": after.Version, "expense_total": after.ExpenseTotal, "income_total": after.IncomeTotal})
		})
		if err != nil {
			return err
		}
		return h.get(w, r)
	}
}

func (h *Handler) revise(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var nid int64
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if nid, err = Revise(r.Context(), tx, p.CompanyID, p.UserID, id, in.Reason); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "budget_revise", "budget", nid, map[string]any{"from_version_id": id}, map[string]any{"reason": in.Reason})
	})
	if err != nil {
		return err
	}
	b, err := Get(r.Context(), h.Pool, p.CompanyID, nid, true)
	if err != nil {
		return err
	}
	httpx.Created(w, b)
	return nil
}

func parseFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from_seq"))
	to, _ := strconv.Atoi(q.Get("to_seq"))
	f := Filter{BudgetID: httpx.QueryInt64(r, "budget_id"), FiscalYearID: httpx.QueryInt64(r, "fiscal_year_id"), FromSeq: from, ToSeq: to,
		Kind: q.Get("kind"), CostCenterID: httpx.QueryInt64(r, "cost_center_id"), GroupBy: q.Get("group_by"), OverOnly: q.Get("over_only") == "true",
		ForecastMethod: q.Get("forecast_method")}
	asOf, err := httpx.QueryDate(r, "as_of")
	f.AsOf = asOf
	v := apperr.NewValidator()
	v.Check(f.Kind == "" || f.Kind == "expense" || f.Kind == "income" || f.Kind == "other", "kind", "invalid")
	_, okMethod := forecasters[f.ForecastMethod]
	v.Check(f.ForecastMethod == "" || okMethod, "forecast_method", "invalid")
	v.Check(f.GroupBy == "" || map[string]bool{"line": true, "account": true, "cost_center": true, "budget": true, "month": true}[f.GroupBy], "group_by", "invalid")
	if err := v.Err(); err != nil {
		return f, err
	}
	return f, err
}

func (h *Handler) analysis(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	f, err := parseFilter(r)
	if err != nil {
		return err
	}
	f.BudgetID = id
	a, err := Analyze(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, f)
	if err != nil {
		return err
	}
	httpx.OK(w, a)
	return nil
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) error {
	f, err := parseFilter(r)
	if err != nil {
		return err
	}
	projectsOnly := false
	switch chi.URLParam(r, "report") {
	case "actual-vs-budget", "variance", "forecast":
	case "expense":
		f.Kind = "expense"
	case "income":
		f.Kind = "income"
	case "cost-center":
		f.GroupBy = "cost_center"
	case "project":
		f.GroupBy, projectsOnly = "cost_center", true
	case "monthly":
		f.GroupBy = "month"
	case "annual":
		f.GroupBy = "budget"
	case "over-budget":
		f.OverOnly = true
	default:
		return apperr.NotFound("report")
	}
	a, err := Analyze(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, f)
	if err != nil {
		return err
	}
	if projectsOnly {
		rows := []Row{}
		for _, row := range a.Rows {
			if row.CostCenterKind != nil && *row.CostCenterKind == "project" {
				rows = append(rows, row)
			}
		}
		a.Rows = rows
	}
	if f.GroupBy != "month" {
		for i := range a.Rows {
			a.Rows[i].Months = nil
		}
	}
	httpx.OK(w, a)
	return nil
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) error {
	f, err := parseFilter(r)
	if err != nil {
		return err
	}
	d, err := DashboardData(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, f)
	if err != nil {
		return err
	}
	httpx.OK(w, d)
	return nil
}

func (h *Handler) drilldown(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	from, _ := strconv.Atoi(q.Get("from_seq"))
	to, _ := strconv.Atoi(q.Get("to_seq"))
	lineID := httpx.QueryInt64(r, "line_id")
	if lineID == 0 {
		v := apperr.NewValidator()
		v.Check(false, "line_id", "required")
		return v.Err()
	}
	rows, err := Drilldown(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, lineID, from, to)
	if err != nil {
		return err
	}
	httpx.OK(w, rows)
	return nil
}

func (h *Handler) control(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	rules, err := ListRules(r.Context(), h.Pool, p.CompanyID, false)
	if err != nil {
		return err
	}
	events, err := ListEvents(r.Context(), h.Pool, p.CompanyID, 50)
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"rules": rules, "events": events, "default_rule": DefaultRule})
	return nil
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request) error {
	rules, err := ListRules(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, false)
	if err != nil {
		return err
	}
	httpx.OK(w, rules)
	return nil
}

func (h *Handler) saveRule(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Rule
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	if r.Method == http.MethodPut {
		var err error
		if id, err = httpx.IDParam(r, "id"); err != nil {
			return err
		}
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		action := "update"
		if id == 0 {
			action = "create"
		}
		var err error
		if id, err = SaveRule(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, action, "budget_rule", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.listRules(w, r)
}

func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM budget_control_rules WHERE company_id = $1 AND id = $2`, p.CompanyID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("budget_rule")
		}
		return audit.Record(r.Context(), tx, "delete", "budget_rule", id, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) event(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	e, err := GetEvent(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, e)
	return nil
}

func (h *Handler) forecastHistory(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	items, err := ForecastHistory(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) setForecast(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in ManualInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		fid, err := SetManualForecast(r.Context(), tx, p.CompanyID, p.UserID, id, in)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "set_forecast", "budget_line", id, nil, map[string]any{"forecast_id": fid, "amount": in.Amount, "reason": in.Reason})
	})
	if err != nil {
		return err
	}
	return h.forecastHistory(w, r)
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) error {
	items, err := ListEvents(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, 500)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) check(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in CheckInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	out, err := Check(r.Context(), h.Pool, p.CompanyID, in, p.RoleCode)
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}
