package ledger

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("journal.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("journal.view")).Get("/{id}", httpx.Handler(h.get))
	r.With(identity.Require("journal.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("journal.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("journal.post")).Post("/{id}/post", httpx.Handler(h.post))
	r.With(identity.Require("journal.cancel")).Post("/{id}/cancel", httpx.Handler(h.cancel))
	r.With(identity.Require("journal.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := List(r.Context(), h.Pool, p.CompanyID, ListFilter{
		Status: q.Get("status"), SourceType: q.Get("source_type"), From: from, To: to,
		AccountID: httpx.QueryInt64(r, "account_id"), PartyID: httpx.QueryInt64(r, "party_id"),
	}, httpx.ParseList(r, "date", true))
	if err != nil {
		return err
	}
	httpx.OK(w, page)
	return nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	e, err := Get(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, e)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in EntryInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "journal.post"); err != nil {
			return err
		}
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, p.UserID, in, Source{}); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "journal_entry", id, nil, in)
	})
	if err != nil {
		return err
	}
	e, err := Get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, e)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in EntryInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "journal.post"); err != nil {
			return err
		}
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := Update(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "journal_entry", id, before, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := Post(r.Context(), tx, p.CompanyID, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "post", "journal_entry", id, nil, nil)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

type cancelInput struct {
	Date civil.Date `json:"date"`
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in cancelInput
	_ = httpx.Decode(r, &in) // body is optional
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		revID, err := Cancel(r.Context(), tx, p.CompanyID, p.UserID, id, in.Date, false)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "cancel", "journal_entry", id, nil, map[string]any{"reversal_id": revID})
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
		if err := DeleteDraft(r.Context(), tx, p.CompanyID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "journal_entry", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
