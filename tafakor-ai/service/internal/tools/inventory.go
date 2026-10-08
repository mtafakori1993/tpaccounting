package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
)

type Product struct {
	ID             int64  `json:"id"`
	Kind           string `json:"kind"`
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	Unit           string `json:"unit"`
	TrackInventory bool   `json:"track_inventory"`
	IsActive       bool   `json:"is_active"`
}

type Warehouse struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	IsDefault bool   `json:"is_default"`
}

type StockDocument struct {
	ID             int64  `json:"id"`
	Kind           string `json:"kind"`
	Number         int64  `json:"number"`
	Date           string `json:"date"`
	WarehouseID    int64  `json:"warehouse_id"`
	WarehouseName  string `json:"warehouse_name"`
	Status         string `json:"status"`
	JournalEntryID *int64 `json:"journal_entry_id"`
	Description    string `json:"description"`
	Lines          []struct {
		ProductID   int64           `json:"product_id"`
		ProductName string          `json:"product_name"`
		Unit        string          `json:"unit"`
		Quantity    decimal.Decimal `json:"quantity"`
		UnitCost    decimal.Decimal `json:"unit_cost"`
	} `json:"lines"`
}

func ListProducts(ctx context.Context, env Env, q string) ([]Product, error) {
	v := url.Values{"page_size": {"200"}}
	if q != "" {
		v.Set("q", q)
	}
	raw, err := get(ctx, env, "/products", v)
	if err != nil {
		return nil, err
	}
	return decodeList[Product](raw)
}

func ListWarehouses(ctx context.Context, env Env) ([]Warehouse, error) {
	raw, err := get(ctx, env, "/inventory/warehouses", nil)
	if err != nil {
		return nil, err
	}
	return decodeList[Warehouse](raw)
}

// NewProduct describes a product to create when it does not exist yet.
type NewProduct struct {
	Name string `json:"name"`
	SKU  string `json:"sku"` // empty = the app numbers it
	Unit string `json:"unit"`
}

// InventoryReceiptArgs: add goods to stock with a posted stock receipt (Dr inventory /
// Cr the counter account — by default the posting rule "inventory adjustment").
type InventoryReceiptArgs struct {
	Date             string      `json:"date"`
	WarehouseID      int64       `json:"warehouse_id"`
	ProductID        *int64      `json:"product_id"`
	NewProduct       *NewProduct `json:"new_product"`
	Quantity         string      `json:"quantity"`
	UnitCost         string      `json:"unit_cost"`
	CounterAccountID *int64      `json:"counter_account_id"`
	Description      string      `json:"description"`
}

func (a *InventoryReceiptArgs) Validate(decimals int) error {
	if err := validDate(a.Date); err != nil {
		return err
	}
	if a.WarehouseID <= 0 {
		return fmt.Errorf("%w: warehouse_id required", ErrInvalidInput)
	}
	if (a.ProductID == nil) == (a.NewProduct == nil) {
		return fmt.Errorf("%w: exactly one of product_id or new_product", ErrInvalidInput)
	}
	if a.NewProduct != nil {
		var err error
		if a.NewProduct.Name, err = cleanText(a.NewProduct.Name, 200); err != nil || a.NewProduct.Name == "" {
			return fmt.Errorf("%w: product name", ErrInvalidInput)
		}
		if a.NewProduct.SKU, err = cleanText(a.NewProduct.SKU, 50); err != nil {
			return err
		}
		if a.NewProduct.Unit, err = cleanText(a.NewProduct.Unit, 30); err != nil {
			return err
		}
	}
	q, err := decimal.NewFromString(a.Quantity)
	if err != nil || !q.IsPositive() {
		return fmt.Errorf("%w: quantity must be positive", ErrInvalidInput)
	}
	if _, err := validAmount(a.UnitCost, 4); err != nil {
		return err
	}
	if err := positive(a.CounterAccountID, "counter_account_id"); err != nil {
		return err
	}
	a.Description, err = cleanText(a.Description, 500)
	return err
}

func createInventoryReceipt(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a InventoryReceiptArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.Validate(decimalsOf(env)); err != nil {
		return nil, err
	}
	productID := int64(0)
	if a.ProductID != nil {
		productID = *a.ProductID
	} else {
		if env.Me != nil && !env.Me.Can("products.create") {
			return nil, fmt.Errorf("%w (products.create)", ErrNoPermission)
		}
		// Re-check by name first so a retry never creates a second product.
		existing, err := ListProducts(ctx, env, a.NewProduct.Name)
		if err != nil {
			return nil, err
		}
		for _, p := range existing {
			if strings.EqualFold(strings.TrimSpace(p.Name), a.NewProduct.Name) {
				productID = p.ID
			}
		}
		if productID == 0 {
			var p Product
			body := map[string]any{"kind": "goods", "name": a.NewProduct.Name, "sku": a.NewProduct.SKU, "unit": a.NewProduct.Unit,
				"purchase_price": a.UnitCost, "sales_price": "0", "tax_rate": "0", "track_inventory": true, "is_active": true}
			if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/products", Body: body}, &p); err != nil {
				return nil, err
			}
			productID = p.ID
		}
	}
	body := map[string]any{"kind": "receipt", "date": a.Date, "warehouse_id": a.WarehouseID, "counter_account_id": a.CounterAccountID,
		"description": a.Description, "reference": "", "post": true,
		"lines": []map[string]any{{"product_id": productID, "quantity": a.Quantity, "unit_cost": a.UnitCost, "description": ""}}}
	var d StockDocument
	if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/inventory/documents", Body: body}, &d); err != nil {
		return nil, err
	}
	return &d, nil
}
