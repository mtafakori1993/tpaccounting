// Package products manages goods and services and their categories.
package products

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type Product struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	SKU            string          `json:"sku"`
	Name           string          `json:"name"`
	NameEn         string          `json:"name_en"`
	Unit           string          `json:"unit"`
	CategoryID     *int64          `json:"category_id"`
	CategoryName   *string         `json:"category_name"`
	PurchasePrice  decimal.Decimal `json:"purchase_price"`
	SalesPrice     decimal.Decimal `json:"sales_price"`
	TaxRate        decimal.Decimal `json:"tax_rate"`
	TrackInventory bool            `json:"track_inventory"`
	IsActive       bool            `json:"is_active"`
	Description    string          `json:"description"`
	OnHand         decimal.Decimal `json:"on_hand"`
	StockValue     decimal.Decimal `json:"stock_value"`
}

const cols = `p.id, p.kind, p.sku, p.name, p.name_en, p.unit, p.category_id, c.name, p.purchase_price, p.sales_price,
	p.tax_rate, p.track_inventory, p.is_active, p.description,
	COALESCE(sb.qty, 0), COALESCE(sb.value, 0)`

const joins = ` FROM products p LEFT JOIN product_categories c ON c.id = p.category_id
	LEFT JOIN (SELECT product_id, SUM(quantity) qty, SUM(value) value FROM stock_balances GROUP BY product_id) sb ON sb.product_id = p.id`

func scan(row pgx.Row, p *Product) error {
	return row.Scan(&p.ID, &p.Kind, &p.SKU, &p.Name, &p.NameEn, &p.Unit, &p.CategoryID, &p.CategoryName, &p.PurchasePrice,
		&p.SalesPrice, &p.TaxRate, &p.TrackInventory, &p.IsActive, &p.Description, &p.OnHand, &p.StockValue)
}

func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Product, error) {
	var p Product
	err := scan(q.QueryRow(ctx, `SELECT `+cols+joins+` WHERE p.company_id = $1 AND p.id = $2`, companyID, id), &p)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("product")
	}
	return &p, err
}

func validate(ctx context.Context, q db.DBTX, companyID int64, p *Product) error {
	p.SKU, p.Name, p.NameEn, p.Unit, p.Description = strings.TrimSpace(p.SKU), strings.TrimSpace(p.Name),
		strings.TrimSpace(p.NameEn), strings.TrimSpace(p.Unit), strings.TrimSpace(p.Description)
	if p.Kind == "" {
		p.Kind = "goods"
	}
	if p.Kind == "service" {
		p.TrackInventory = false
	}
	v := apperr.NewValidator()
	v.Check(p.Kind == "goods" || p.Kind == "service", "kind", "invalid")
	v.Required("name", p.Name)
	v.MaxLen("name", p.Name, 200)
	v.MaxLen("sku", p.SKU, 50)
	v.MaxLen("unit", p.Unit, 30)
	v.Check(!p.PurchasePrice.IsNegative(), "purchase_price", "negative")
	v.Check(!p.SalesPrice.IsNegative(), "sales_price", "negative")
	v.Check(!p.TaxRate.IsNegative() && p.TaxRate.LessThanOrEqual(decimal.NewFromInt(100)), "tax_rate", "invalid_rate")
	if p.CategoryID != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM product_categories WHERE company_id = $1 AND id = $2)`, companyID, *p.CategoryID).Scan(&ok); err != nil {
			return err
		}
		v.Check(ok, "category_id", "invalid")
	}
	return v.Err()
}

func Create(ctx context.Context, q db.DBTX, companyID int64, p *Product) (int64, error) {
	if err := validate(ctx, q, companyID, p); err != nil {
		return 0, err
	}
	if p.SKU == "" {
		n, err := db.NextNumber(ctx, q, companyID, "product", 0)
		if err != nil {
			return 0, err
		}
		p.SKU = "P" + leftPad(n, 4)
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO products (company_id, kind, sku, name, name_en, unit, category_id, purchase_price, sales_price, tax_rate,
		                      track_inventory, is_active, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, TRUE, $12) RETURNING id`,
		companyID, p.Kind, p.SKU, p.Name, p.NameEn, p.Unit, p.CategoryID, p.PurchasePrice, p.SalesPrice, p.TaxRate,
		p.TrackInventory, p.Description).Scan(&id)
	return id, err
}

func leftPad(n int64, w int) string {
	s := decimal.NewFromInt(n).String()
	for len(s) < w {
		s = "0" + s
	}
	return s
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) Routes(r chi.Router) {
	r.With(identity.Require("products.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("products.view")).Get("/categories", httpx.Handler(h.listCategories))
	r.With(identity.Require("products.create")).Post("/categories", httpx.Handler(h.saveCategory))
	r.With(identity.Require("products.edit")).Put("/categories/{id}", httpx.Handler(h.saveCategory))
	r.With(identity.Require("products.delete")).Delete("/categories/{id}", httpx.Handler(h.deleteCategory))
	r.With(identity.Require("products.view")).Get("/{id}", httpx.Handler(h.get))
	r.With(identity.Require("products.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("products.edit")).Put("/{id}", httpx.Handler(h.update))
	r.With(identity.Require("products.delete")).Delete("/{id}", httpx.Handler(h.delete))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	lp := httpx.ParseList(r, "name", false)
	f := db.NewFilter("p.company_id = $1", p.CompanyID)
	qv := r.URL.Query()
	if k := qv.Get("kind"); k != "" {
		f.Add("p.kind = $N", k)
	}
	if c := httpx.QueryInt64(r, "category_id"); c > 0 {
		f.Add("p.category_id = $N", c)
	}
	if b := httpx.QueryBool(r, "active"); b != nil {
		f.Add("p.is_active = $N", *b)
	}
	if qv.Get("tracked") == "true" {
		f.AddRaw("p.track_inventory")
	}
	if lp.Q != "" {
		f.Add("(p.name ILIKE $N OR p.sku ILIKE $N OR p.name_en ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM products p WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return err
	}
	order := lp.OrderBy(map[string]string{"name": "p.name", "sku": "p.sku", "sales_price": "p.sales_price",
		"purchase_price": "p.purchase_price", "on_hand": "15"}, "p.name")
	rows, err := h.Pool.Query(r.Context(), `SELECT `+cols+joins+` WHERE `+f.Where()+` ORDER BY `+order+`, p.id`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var items []Product
	for rows.Next() {
		var x Product
		if err := scan(rows, &x); err != nil {
			return err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, httpx.NewPage(items, total, lp))
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

// createInput lets goods default to tracked inventory when the flag is omitted.
type createInput struct {
	Product
	TrackInventory *bool `json:"track_inventory"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var raw createInput
	if err := httpx.Decode(r, &raw); err != nil {
		return err
	}
	in := raw.Product
	in.TrackInventory = raw.TrackInventory == nil || *raw.TrackInventory
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, &in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "product", id, nil, in)
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
	var in Product
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := Get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if in.SKU == "" {
			in.SKU = before.SKU
		}
		if err := validate(r.Context(), tx, p.CompanyID, &in); err != nil {
			return err
		}
		if before.Kind != in.Kind || before.TrackInventory != in.TrackInventory {
			var moved bool
			if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM stock_movements WHERE product_id = $1)`, id).Scan(&moved); err != nil {
				return err
			}
			if moved {
				return apperr.Rule("product_has_stock", "the inventory behaviour of a product with stock movements cannot change")
			}
		}
		if _, err := tx.Exec(r.Context(), `
			UPDATE products SET kind = $3, sku = $4, name = $5, name_en = $6, unit = $7, category_id = $8, purchase_price = $9,
			       sales_price = $10, tax_rate = $11, track_inventory = $12, is_active = $13, description = $14, updated_at = now()
			WHERE company_id = $1 AND id = $2`, p.CompanyID, id, in.Kind, in.SKU, in.Name, in.NameEn, in.Unit, in.CategoryID,
			in.PurchasePrice, in.SalesPrice, in.TaxRate, in.TrackInventory, in.IsActive, in.Description); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "product", id, before, in)
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
		var used bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM invoice_items WHERE product_id = $1)
			OR EXISTS (SELECT 1 FROM stock_movements WHERE product_id = $1)
			OR EXISTS (SELECT 1 FROM stock_document_lines WHERE product_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return apperr.Rule("in_use", "the product is used in documents; disable it instead")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM stock_balances WHERE product_id = $1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM products WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "product", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

type Category struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ProductCount int    `json:"product_count"`
}

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `
		SELECT c.id, c.name, c.description, (SELECT count(*) FROM products p WHERE p.category_id = c.id)
		FROM product_categories c WHERE c.company_id = $1 ORDER BY c.name`, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.ProductCount); err != nil {
			return err
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) saveCategory(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Category
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	v := apperr.NewValidator()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 100)
	if err := v.Err(); err != nil {
		return err
	}
	isCreate := r.Method == http.MethodPost
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if isCreate {
			if err := tx.QueryRow(r.Context(), `INSERT INTO product_categories (company_id, name, description) VALUES ($1, $2, $3) RETURNING id`,
				p.CompanyID, in.Name, in.Description).Scan(&in.ID); err != nil {
				return err
			}
			return audit.Record(r.Context(), tx, "create", "product_category", in.ID, nil, in)
		}
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		in.ID = id
		tag, err := tx.Exec(r.Context(), `UPDATE product_categories SET name = $3, description = $4 WHERE company_id = $1 AND id = $2`,
			p.CompanyID, id, in.Name, in.Description)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("product_category")
		}
		return audit.Record(r.Context(), tx, "update", "product_category", id, nil, in)
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

func (h *Handler) deleteCategory(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(r.Context(), `DELETE FROM product_categories WHERE company_id = $1 AND id = $2`, p.CompanyID, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("product_category")
		}
		return audit.Record(r.Context(), tx, "delete", "product_category", id, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
