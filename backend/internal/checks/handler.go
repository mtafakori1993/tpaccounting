package checks

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/export"
	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Handler struct {
	Pool *pgxpool.Pool
	PDF  *pdf.Service
}

func (h *Handler) Routes(r chi.Router) {
	view := identity.Require("checks.view")
	r.With(view).Get("/", httpx.Handler(h.list))
	r.With(view).Get("/dashboard", httpx.Handler(h.dashboard))
	r.With(view).Get("/banks", httpx.Handler(h.banks))
	r.With(view).Get("/reports/by-bank", httpx.Handler(h.grouped("bank")))
	r.With(view).Get("/reports/by-party", httpx.Handler(h.grouped("party")))
	r.With(view).Get("/reports/by-status", httpx.Handler(h.grouped("status")))
	r.With(view).Get("/reports/by-month", httpx.Handler(h.grouped("month")))
	r.With(view).Get("/reports/cash-flow", httpx.Handler(h.cashFlow))
	r.With(view).Get("/reports/{report}", httpx.Handler(h.report))
	r.With(view).Get("/summary", httpx.Handler(h.summary))
	r.With(identity.Require("checks.export")).Get("/export", httpx.Handler(h.export))
	r.With(view).Get("/{id}", httpx.Handler(h.get))
	r.With(view).Get("/{id}/transitions", httpx.Handler(h.transitions))
	r.With(identity.Require("checks.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("checks.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("checks.delete")).Delete("/{id}", httpx.Handler(h.delete))
	// The permission depends on the action (post vs cancel) and is checked inside.
	r.Post("/{id}/transitions", httpx.Handler(h.transition))
}

func parseFilter(r *http.Request) (Filter, error) {
	q := r.URL.Query()
	lf := Filter{
		Type: q.Get("check_type"), Statuses: splitList(q.Get("status")), BankID: httpx.QueryInt64(r, "bank_id"),
		PartyID: httpx.QueryInt64(r, "party_id"), TreasuryAccountID: httpx.QueryInt64(r, "treasury_account_id"),
		Live: q.Get("live") == "true", Overdue: q.Get("overdue") == "true",
	}
	for _, s := range lf.Statuses {
		if !validStatus[s] {
			v := apperr.NewValidator()
			v.Check(false, "status", "invalid")
			return lf, v.Err()
		}
	}
	if lf.Type != "" && lf.Type != TypeReceived && lf.Type != TypeIssued {
		v := apperr.NewValidator()
		v.Check(false, "check_type", "invalid")
		return lf, v.Err()
	}
	var err error
	if lf.DueFrom, err = httpx.QueryDate(r, "due_from"); err != nil {
		return lf, err
	}
	if lf.DueTo, err = httpx.QueryDate(r, "due_to"); err != nil {
		return lf, err
	}
	for name, dst := range map[string]*decimal.Decimal{"min_amount": &lf.MinAmount, "max_amount": &lf.MaxAmount} {
		if s := q.Get(name); s != "" {
			d, err := decimal.NewFromString(s)
			if err != nil {
				v := apperr.NewValidator()
				v.Check(false, name, "invalid")
				return lf, v.Err()
			}
			*dst = d
		}
	}
	return lf, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	lf, err := parseFilter(r)
	if err != nil {
		return err
	}
	out, err := List(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, lf, httpx.ParseList(r, "due_date", false))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

// applyReport narrows lf to one of the predefined list reports.
func applyReport(name string, lf *Filter, today civil.Date) error {
	switch name {
	case "", "all":
	case "received":
		lf.Type = TypeReceived
	case "issued":
		lf.Type = TypeIssued
	case "due":
		lf.Live = true
		if lf.DueFrom.IsZero() && lf.DueTo.IsZero() {
			lf.DueFrom, lf.DueTo = today, today.AddDays(30)
		}
	case "overdue":
		lf.Overdue = true
	case "returned":
		lf.Statuses = []string{StatusReturned}
	case "in-custody":
		lf.Type = TypeReceived
		lf.Statuses = []string{StatusReceived, StatusInCustody}
	default:
		return apperr.NotFound("route")
	}
	return nil
}

// report serves the predefined list reports; all accept the list filters too.
func (h *Handler) report(w http.ResponseWriter, r *http.Request) error {
	lf, err := parseFilter(r)
	if err != nil {
		return err
	}
	if err := applyReport(chi.URLParam(r, "report"), &lf, civil.Today()); err != nil {
		return err
	}
	out, err := List(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, lf, httpx.ParseList(r, "due_date", false))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

// summary returns counts and amounts of the filtered checks (KPIs of the report page).
func (h *Handler) summary(w http.ResponseWriter, r *http.Request) error {
	lf, err := parseFilter(r)
	if err != nil {
		return err
	}
	if err := applyReport(r.URL.Query().Get("report"), &lf, civil.Today()); err != nil {
		return err
	}
	out, err := Summarise(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, lf, httpx.ParseList(r, "due_date", false).Q)
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

// export writes the complete filtered dataset (not just one page) as CSV, Excel or PDF.
func (h *Handler) export(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	lf, err := parseFilter(r)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	report := q.Get("report")
	if err := applyReport(report, &lf, civil.Today()); err != nil {
		return err
	}
	lp := httpx.ParseList(r, "due_date", false)
	rep, err := BuildExport(r.Context(), h.Pool, p.CompanyID, lf, lp, report, q.Get("lang"))
	if err != nil {
		return err
	}
	if err := audit.Record(r.Context(), h.Pool, "export", "check", 0, nil, map[string]any{"report": report, "format": q.Get("format"), "rows": len(rep.Rows)}); err != nil {
		return err
	}
	return export.Write(r.Context(), w, h.PDF, p.CompanyID, rep, q.Get("format"))
}

func (h *Handler) grouped(by string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		lf, err := parseFilter(r)
		if err != nil {
			return err
		}
		out, err := Grouped(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, by, lf)
		if err != nil {
			return err
		}
		httpx.OK(w, out)
		return nil
	}
}

func (h *Handler) cashFlow(w http.ResponseWriter, r *http.Request) error {
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	out, err := CashFlowImpact(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, from, to, r.URL.Query().Get("group_by"))
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) error {
	d, err := DashboardData(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, d)
	return nil
}

type Bank struct {
	ID     int64  `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	NameEn string `json:"name_en"`
}

func (h *Handler) banks(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `SELECT id, code, name, name_en FROM banks WHERE is_active ORDER BY code = 'other', id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Bank{}
	for rows.Next() {
		var b Bank
		if err := rows.Scan(&b.ID, &b.Code, &b.Name, &b.NameEn); err != nil {
			return err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	x, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, x)
	return nil
}

func (h *Handler) transitions(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	x, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, x.Transitions)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	// Registering a received check, or issuing at once, books it in the ledger.
	if in.CheckType == TypeReceived || in.Issue {
		if err := identity.Check(r.Context(), "checks.post"); err != nil {
			return err
		}
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		x, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		x.Transitions = nil
		return audit.Record(r.Context(), tx, "create", "check", id, nil, x)
	})
	if err != nil {
		return err
	}
	x, err := Get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, x)
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
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := Update(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		after, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		before.Transitions, after.Transitions = nil, nil
		return audit.Record(r.Context(), tx, "update", "check", id, before, after)
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
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := Delete(r.Context(), tx, p.CompanyID, id); err != nil {
			return err
		}
		before.Transitions = nil
		return audit.Record(r.Context(), tx, "delete", "check", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) transition(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in TransitionInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if err := identity.Check(r.Context(), "checks.view"); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := identity.Check(r.Context(), PermissionFor(before.CheckType, in.Action)); err != nil {
			return err
		}
		if err := Apply(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		after, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		last := after.Transitions[len(after.Transitions)-1]
		return audit.Record(r.Context(), tx, in.Action, "check", id,
			map[string]any{"status": before.Status},
			map[string]any{"status": after.Status, "date": in.Date, "reason": in.Reason, "journal_entry_id": last.JournalEntryID,
				"reversal_entry_ids": last.ReversalEntryIDs, "treasury_account_id": in.TreasuryAccountID, "party_id": in.PartyID})
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}
