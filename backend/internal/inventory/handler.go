package inventory

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Warehouse struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	IsActive  bool   `json:"is_active"`
	IsDefault bool   `json:"is_default"`
}

// CreateWarehouse inserts a warehouse (also used by setup).
func CreateWarehouse(ctx context.Context, q db.DBTX, companyID int64, w Warehouse) (int64, error) {
	w.Code, w.Name, w.Address = strings.TrimSpace(w.Code), strings.TrimSpace(w.Name), strings.TrimSpace(w.Address)
	v := apperr.NewValidator()
	v.Required("code", w.Code)
	v.Required("name", w.Name)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if w.IsDefault {
		if _, err := q.Exec(ctx, `UPDATE warehouses SET is_default = FALSE WHERE company_id = $1`, companyID); err != nil {
			return 0, err
		}
	}
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO warehouses (company_id, code, name, address, is_default) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		companyID, w.Code, w.Name, w.Address, w.IsDefault).Scan(&id)
	return id, err
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("inventory.view")).Get("/warehouses", httpx.Handler(h.listWarehouses))
	r.With(identity.Require("inventory.create")).Post("/warehouses", httpx.Handler(h.saveWarehouse))
	r.With(identity.Require("inventory.edit")).Put("/warehouses/{id}", httpx.Handler(h.saveWarehouse))

	r.With(identity.Require("inventory.view")).Get("/documents", httpx.Handler(h.listDocs))
	r.With(identity.Require("inventory.view")).Get("/documents/{id}", httpx.Handler(h.getDoc))
	r.With(identity.Require("inventory.create")).Post("/documents", httpx.Handler(h.createDoc))
	r.With(identity.Require("inventory.edit")).Put("/documents/{id}", httpx.Handler(h.updateDoc))
	r.With(identity.Require("inventory.post")).Post("/documents/{id}/post", httpx.Handler(h.postDoc))
	r.With(identity.Require("inventory.cancel")).Post("/documents/{id}/cancel", httpx.Handler(h.cancelDoc))
	r.With(identity.Require("inventory.delete")).Delete("/documents/{id}", httpx.Handler(h.deleteDoc))

	r.With(identity.Require("inventory.view")).Get("/stock", httpx.Handler(h.stock))
	r.With(identity.Require("inventory.view")).Get("/ledger", httpx.Handler(h.ledger))
}

func (h *Handler) listWarehouses(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `SELECT id, code, name, address, is_active, is_default FROM warehouses WHERE company_id = $1 ORDER BY code`,
		identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Warehouse{}
	for rows.Next() {
		var x Warehouse
		if err := rows.Scan(&x.ID, &x.Code, &x.Name, &x.Address, &x.IsActive, &x.IsDefault); err != nil {
			return err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) saveWarehouse(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Warehouse
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	isCreate := r.Method == http.MethodPost
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if isCreate {
			id, err := CreateWarehouse(r.Context(), tx, p.CompanyID, in)
			if err != nil {
				return err
			}
			in.ID = id
			in.IsActive = true
			return audit.Record(r.Context(), tx, "create", "warehouse", id, nil, in)
		}
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		in.ID = id
		in.Code, in.Name, in.Address = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Address)
		v := apperr.NewValidator()
		v.Required("code", in.Code)
		v.Required("name", in.Name)
		v.Check(in.IsActive || !in.IsDefault, "is_active", "default_inactive")
		if err := v.Err(); err != nil {
			return err
		}
		if in.IsDefault {
			if _, err := tx.Exec(r.Context(), `UPDATE warehouses SET is_default = FALSE WHERE company_id = $1`, p.CompanyID); err != nil {
				return err
			}
		}
		tag, err := tx.Exec(r.Context(), `UPDATE warehouses SET code = $3, name = $4, address = $5, is_active = $6, is_default = $7
			WHERE company_id = $1 AND id = $2`, p.CompanyID, id, in.Code, in.Name, in.Address, in.IsActive, in.IsDefault)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("warehouse")
		}
		return audit.Record(r.Context(), tx, "update", "warehouse", id, nil, in)
	})
	if err != nil {
		return err
	}
	if isCreate {
		httpx.Created(w, in)
	} else {
		httpx.OK(w, in)
	}
	return nil
}

func (h *Handler) listDocs(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	page, err := listDocuments(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, q.Get("kind"), q.Get("status"), httpx.ParseList(r, "date", true))
	if err != nil {
		return err
	}
	httpx.OK(w, page)
	return nil
}

func (h *Handler) getDoc(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	d, err := getDocument(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, d)
	return nil
}

func (h *Handler) createDoc(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in DocInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "inventory.post"); err != nil {
			return err
		}
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = createDocument(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "stock_document", id, nil, in)
	})
	if err != nil {
		return err
	}
	d, err := getDocument(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, d)
	return nil
}

func (h *Handler) updateDoc(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in DocInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	if in.Post {
		if err := identity.Check(r.Context(), "inventory.post"); err != nil {
			return err
		}
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := updateDocument(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "stock_document", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.getDoc(w, r)
}

func (h *Handler) action(w http.ResponseWriter, r *http.Request, name string, fn func(context.Context, db.DBTX, int64, int64, int64) error) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := fn(r.Context(), tx, p.CompanyID, p.UserID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, name, "stock_document", id, nil, nil)
	})
	if err != nil {
		return err
	}
	return h.getDoc(w, r)
}

func (h *Handler) postDoc(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "post", postDocument)
}

func (h *Handler) cancelDoc(w http.ResponseWriter, r *http.Request) error {
	return h.action(w, r, "cancel", cancelDocument)
}

func (h *Handler) deleteDoc(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM stock_documents WHERE company_id = $1 AND id = $2 AND status = 'draft'`, p.CompanyID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.Rule("not_deletable", "only drafts can be deleted")
		}
		return audit.Record(r.Context(), tx, "delete", "stock_document", id, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

type StockRow struct {
	ProductID     int64           `json:"product_id"`
	SKU           string          `json:"sku"`
	ProductName   string          `json:"product_name"`
	Unit          string          `json:"unit"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseName string          `json:"warehouse_name"`
	Quantity      decimal.Decimal `json:"quantity"`
	Value         decimal.Decimal `json:"value"`
	AverageCost   decimal.Decimal `json:"average_cost"`
}

// stock reports current on-hand quantity and value per product and warehouse.
func (h *Handler) stock(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	f := db.NewFilter("pr.company_id = $1", p.CompanyID)
	if wid := httpx.QueryInt64(r, "warehouse_id"); wid > 0 {
		f.Add("b.warehouse_id = $N", wid)
	}
	if pid := httpx.QueryInt64(r, "product_id"); pid > 0 {
		f.Add("b.product_id = $N", pid)
	}
	if s := strings.TrimSpace(r.URL.Query().Get("q")); s != "" {
		f.Add("(pr.name ILIKE $N OR pr.sku ILIKE $N)", httpx.Like(s))
	}
	if r.URL.Query().Get("nonzero") != "false" {
		f.AddRaw("(b.quantity <> 0 OR b.value <> 0)")
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT b.product_id, pr.sku, pr.name, pr.unit, b.warehouse_id, w.name, b.quantity, b.value,
		       CASE WHEN b.quantity > 0 THEN round(b.value / b.quantity, 2) ELSE 0 END
		FROM stock_balances b JOIN products pr ON pr.id = b.product_id JOIN warehouses w ON w.id = b.warehouse_id
		WHERE `+f.Where()+` ORDER BY pr.name, w.name`, f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []StockRow{}
	for rows.Next() {
		var s StockRow
		if err := rows.Scan(&s.ProductID, &s.SKU, &s.ProductName, &s.Unit, &s.WarehouseID, &s.WarehouseName, &s.Quantity, &s.Value, &s.AverageCost); err != nil {
			return err
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

type Movement struct {
	ID            int64           `json:"id"`
	Date          civil.Date      `json:"date"`
	WarehouseName string          `json:"warehouse_name"`
	SourceType    string          `json:"source_type"`
	SourceID      int64           `json:"source_id"`
	Description   string          `json:"description"`
	QtyIn         decimal.Decimal `json:"qty_in"`
	QtyOut        decimal.Decimal `json:"qty_out"`
	UnitCost      decimal.Decimal `json:"unit_cost"`
	Value         decimal.Decimal `json:"value"`
	BalanceQty    decimal.Decimal `json:"balance_qty"`
	BalanceValue  decimal.Decimal `json:"balance_value"`
	CreatedAt     time.Time       `json:"created_at"`
}

type StockLedger struct {
	OpeningQty   decimal.Decimal `json:"opening_qty"`
	OpeningValue decimal.Decimal `json:"opening_value"`
	Movements    []Movement      `json:"movements"`
	ClosingQty   decimal.Decimal `json:"closing_qty"`
	ClosingValue decimal.Decimal `json:"closing_value"`
}

// ledger returns a product's movement history with running balances.
func (h *Handler) ledger(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	productID := httpx.QueryInt64(r, "product_id")
	if productID == 0 {
		v := apperr.NewValidator()
		v.Check(false, "product_id", "required")
		return v.Err()
	}
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	f := db.NewFilter("m.company_id = $1 AND m.product_id = $2", p.CompanyID, productID)
	if wid := httpx.QueryInt64(r, "warehouse_id"); wid > 0 {
		f.Add("m.warehouse_id = $N", wid)
	}
	out := StockLedger{Movements: []Movement{}}
	if !from.IsZero() {
		of := f.Clone()
		of.Add("m.date < $N", from)
		if err := h.Pool.QueryRow(r.Context(), `SELECT COALESCE(SUM(quantity), 0), COALESCE(SUM(value), 0) FROM stock_movements m WHERE `+of.Where(), of.Args...).
			Scan(&out.OpeningQty, &out.OpeningValue); err != nil {
			return err
		}
		f.Add("m.date >= $N", from)
	}
	if !to.IsZero() {
		f.Add("m.date <= $N", to)
	}
	rows, err := h.Pool.Query(r.Context(), `
		SELECT m.id, m.date, w.name, m.source_type, m.source_id, m.description, m.quantity, m.unit_cost, m.value, m.created_at
		FROM stock_movements m JOIN warehouses w ON w.id = m.warehouse_id
		WHERE `+f.Where()+` ORDER BY m.date, m.id`, f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	bq, bv := out.OpeningQty, out.OpeningValue
	for rows.Next() {
		var m Movement
		var qty decimal.Decimal
		if err := rows.Scan(&m.ID, &m.Date, &m.WarehouseName, &m.SourceType, &m.SourceID, &m.Description, &qty, &m.UnitCost, &m.Value, &m.CreatedAt); err != nil {
			return err
		}
		if qty.IsPositive() {
			m.QtyIn = qty
		} else {
			m.QtyOut = qty.Neg()
		}
		bq, bv = bq.Add(qty), bv.Add(m.Value)
		m.BalanceQty, m.BalanceValue = bq, bv
		out.Movements = append(out.Movements, m)
	}
	out.ClosingQty, out.ClosingValue = bq, bv
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}
