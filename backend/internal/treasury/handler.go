package treasury

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/invoices"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("treasury.view")).Get("/accounts", httpx.Handler(h.listAccounts))
	r.With(identity.Require("treasury.view")).Get("/accounts/{id}", httpx.Handler(h.getAccount))
	r.With(identity.Require("treasury.create")).Post("/accounts", httpx.Handler(h.createAccount))
	r.With(identity.Require("treasury.edit")).Put("/accounts/{id}", httpx.Handler(h.updateAccount))

	r.With(identity.Require("treasury.view")).Get("/transactions", httpx.Handler(h.listTx))
	r.With(identity.Require("treasury.view")).Get("/transactions/{id}", httpx.Handler(h.getTx))
	r.With(identity.Require("treasury.create"), identity.Require("treasury.post")).Post("/transactions", httpx.Handler(h.createTx))
	r.With(identity.Require("treasury.cancel")).Post("/transactions/{id}/cancel", httpx.Handler(h.cancelTx))
	r.With(identity.Require("treasury.edit")).Post("/transactions/{id}/allocations", httpx.Handler(h.allocateTx))
	r.With(identity.Require("treasury.edit")).Delete("/transactions/{id}/allocations", httpx.Handler(h.releaseTx))
	r.With(identity.Require("treasury.view")).Get("/open-items", httpx.Handler(h.openItems))
}

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) error {
	items, err := ListAccounts(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, r.URL.Query().Get("kind"))
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	a, err := GetAccount(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, a)
	return nil
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Account
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = CreateAccount(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "treasury_account", id, nil, in)
	})
	if err != nil {
		return err
	}
	a, err := GetAccount(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, a)
	return nil
}

func (h *Handler) updateAccount(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in Account
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetAccount(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := UpdateAccount(r.Context(), tx, p.CompanyID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "treasury_account", id, before, in)
	})
	if err != nil {
		return err
	}
	return h.getAccount(w, r)
}

func (h *Handler) listTx(w http.ResponseWriter, r *http.Request) error {
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	page, err := ListTx(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, TxFilter{
		Kind: q.Get("kind"), TreasuryAccountID: httpx.QueryInt64(r, "treasury_account_id"), PartyID: httpx.QueryInt64(r, "party_id"),
		Status: q.Get("status"), From: from, To: to,
	}, httpx.ParseList(r, "date", true))
	if err != nil {
		return err
	}
	httpx.OK(w, page)
	return nil
}

func (h *Handler) getTx(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	t, err := GetTx(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, t)
	return nil
}

func (h *Handler) createTx(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in TxInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = CreateTx(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "treasury_transaction", id, nil, in)
	})
	if err != nil {
		return err
	}
	t, err := GetTx(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, t)
	return nil
}

func (h *Handler) cancelTx(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := CancelTx(r.Context(), tx, p.CompanyID, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "cancel", "treasury_transaction", id, nil, nil)
	})
	if err != nil {
		return err
	}
	return h.getTx(w, r)
}

func (h *Handler) openItems(w http.ResponseWriter, r *http.Request) error {
	partyID := httpx.QueryInt64(r, "party_id")
	kind := r.URL.Query().Get("kind")
	if partyID == 0 || (kind != "receipt" && kind != "payment") {
		v := apperr.NewValidator()
		v.Check(partyID > 0, "party_id", "required")
		v.Check(kind == "receipt" || kind == "payment", "kind", "invalid")
		return v.Err()
	}
	items, err := invoices.OpenItems(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, partyID, kind, false)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

// allocateTx settles open invoices with an existing receipt or payment.
func (h *Handler) allocateTx(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in AllocationsInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetTx(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := AllocateTx(r.Context(), tx, p.CompanyID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "allocate", "treasury_transaction", id, before.Allocations, in)
	})
	if err != nil {
		return err
	}
	return h.getTx(w, r)
}

// releaseTx removes the invoice allocations of a receipt or payment.
func (h *Handler) releaseTx(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetTx(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := ReleaseTxAllocations(r.Context(), tx, p.CompanyID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "release_allocations", "treasury_transaction", id, before.Allocations, nil)
	})
	if err != nil {
		return err
	}
	return h.getTx(w, r)
}
