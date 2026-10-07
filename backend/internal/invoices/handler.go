package invoices

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

// Handler serves one side (sales or purchase). The permission module is
// "sales" or "purchases".
type Handler struct {
	Pool   *pgxpool.Pool
	Side   string
	Module string
}

var kindPaths = map[string]string{"invoices": KindInvoice, "returns": KindReturn, "quotations": KindQuotation}

func (h *Handler) Routes(r chi.Router) {
	perm := func(a string) func(http.Handler) http.Handler { return identity.Require(h.Module + "." + a) }
	r.Route("/{kind}", func(r chi.Router) {
		r.With(perm("view")).Get("/", httpx.Handler(h.list))
		r.With(perm("create")).Post("/", httpx.Handler(h.create))
		r.With(perm("view")).Get("/{id}", httpx.Handler(h.get))
		r.With(perm("edit")).Put("/{id}", httpx.Handler(h.update))
		r.With(perm("post")).Post("/{id}/post", httpx.Handler(h.post))
		r.With(perm("cancel")).Post("/{id}/cancel", httpx.Handler(h.cancel))
		r.With(perm("delete")).Delete("/{id}", httpx.Handler(h.delete))
		r.With(perm("create")).Post("/{id}/convert", httpx.Handler(h.convert))
	})
}

func (h *Handler) kind(r *http.Request) (string, error) {
	k, ok := kindPaths[chi.URLParam(r, "kind")]
	if !ok || (k == KindQuotation && h.Side != SideSales) {
		return "", apperr.NotFound("route")
	}
	return k, nil
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	kind, err := h.kind(r)
	if err != nil {
		return err
	}
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := List(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, ListFilter{
		Side: h.Side, Kind: kind, Status: q.Get("status"), PaymentStatus: q.Get("payment_status"),
		PartyID: httpx.QueryInt64(r, "party_id"), From: from, To: to,
	}, httpx.ParseList(r, "date", true))
	if err != nil {
		return err
	}
	httpx.OK(w, page)
	return nil
}

// load fetches the document and verifies it belongs to this side and kind.
func (h *Handler) load(ctx context.Context, q db.DBTX, r *http.Request) (*Invoice, error) {
	kind, err := h.kind(r)
	if err != nil {
		return nil, err
	}
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return nil, err
	}
	inv, err := Get(ctx, q, identity.From(ctx).CompanyID, id)
	if err != nil {
		return nil, err
	}
	if inv.Side != h.Side || inv.Kind != kind {
		return nil, apperr.NotFound("invoice")
	}
	return inv, nil
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	inv, err := h.load(r.Context(), h.Pool, r)
	if err != nil {
		return err
	}
	httpx.OK(w, inv)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	kind, err := h.kind(r)
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), h.Module+".post"); err != nil {
			return err
		}
	}
	var id int64
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, p.UserID, h.Side, kind, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "invoice", id, nil, in)
	})
	if err != nil {
		return err
	}
	inv, err := Get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, inv)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), h.Module+".post"); err != nil {
			return err
		}
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := h.load(r.Context(), tx, r)
		if err != nil {
			return err
		}
		if err := Update(r.Context(), tx, p.CompanyID, p.UserID, before.ID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "invoice", before.ID, before, in)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request, name string, fn func(context.Context, db.DBTX, int64, int64, int64) error) error {
	p := identity.From(r.Context())
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		inv, err := h.load(r.Context(), tx, r)
		if err != nil {
			return err
		}
		if err := fn(r.Context(), tx, p.CompanyID, p.UserID, inv.ID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, name, "invoice", inv.ID, map[string]any{"status": inv.Status}, nil)
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

func (h *Handler) post(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "post", Post)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "cancel", Cancel)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		inv, err := h.load(r.Context(), tx, r)
		if err != nil {
			return err
		}
		if err := DeleteDraft(r.Context(), tx, p.CompanyID, inv.ID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "invoice", inv.ID, inv, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) convert(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var newID int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		qt, err := h.load(r.Context(), tx, r)
		if err != nil {
			return err
		}
		if newID, err = ConvertQuotation(r.Context(), tx, p.CompanyID, p.UserID, qt.ID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "convert", "invoice", qt.ID, nil, map[string]any{"invoice_id": newID})
	})
	if err != nil {
		return err
	}
	inv, err := Get(r.Context(), h.Pool, p.CompanyID, newID)
	if err != nil {
		return err
	}
	httpx.Created(w, inv)
	return nil
}
