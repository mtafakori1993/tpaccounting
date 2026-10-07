// Package inventory implements warehouses, the perpetual stock ledger with
// moving-average costing, and stock documents (receipt, issue, transfer, adjustment).
package inventory

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/money"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// Source identifies the document that caused a stock movement.
type Source struct {
	Type string
	ID   int64
}

type balance struct {
	qty, value decimal.Decimal
}

// lockBalance returns the current balance row for update, creating it if needed.
func lockBalance(ctx context.Context, q db.DBTX, productID, warehouseID int64) (balance, error) {
	if _, err := q.Exec(ctx, `INSERT INTO stock_balances (product_id, warehouse_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, productID, warehouseID); err != nil {
		return balance{}, err
	}
	var b balance
	err := q.QueryRow(ctx, `SELECT quantity, value FROM stock_balances WHERE product_id = $1 AND warehouse_id = $2 FOR UPDATE`, productID, warehouseID).
		Scan(&b.qty, &b.value)
	return b, err
}

// checkItem verifies that the product is a stock item and the warehouse is usable.
func checkItem(ctx context.Context, q db.DBTX, companyID, productID, warehouseID int64) (name string, err error) {
	var tracked bool
	err = q.QueryRow(ctx, `SELECT name, track_inventory FROM products WHERE company_id = $1 AND id = $2`, companyID, productID).Scan(&name, &tracked)
	if db.IsNoRows(err) {
		return "", apperr.NotFound("product")
	}
	if err != nil {
		return "", err
	}
	if !tracked {
		return "", apperr.Rule("product_not_stocked", "the product does not track inventory").WithParams("product", name)
	}
	var active bool
	err = q.QueryRow(ctx, `SELECT is_active FROM warehouses WHERE company_id = $1 AND id = $2`, companyID, warehouseID).Scan(&active)
	if db.IsNoRows(err) {
		return "", apperr.NotFound("warehouse")
	}
	if err != nil {
		return "", err
	}
	if !active {
		return "", apperr.Rule("warehouse_inactive", "the warehouse is disabled")
	}
	return name, nil
}

func record(ctx context.Context, q db.DBTX, companyID, productID, warehouseID int64, date civil.Date, qty, value decimal.Decimal, desc string, src Source) error {
	unit := decimal.Zero
	if !qty.IsZero() {
		unit = value.Div(qty).Abs().Round(2)
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO stock_movements (company_id, product_id, warehouse_id, date, quantity, unit_cost, value, source_type, source_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		companyID, productID, warehouseID, date, qty, unit, value, src.Type, src.ID, desc); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE stock_balances SET quantity = quantity + $3, value = value + $4 WHERE product_id = $1 AND warehouse_id = $2`,
		productID, warehouseID, qty, value)
	return err
}

// Receive adds qty units with the given total value to stock.
func Receive(ctx context.Context, q db.DBTX, companyID, productID, warehouseID int64, date civil.Date, qty, value decimal.Decimal, desc string, src Source) error {
	if !qty.IsPositive() {
		return apperr.Rule("invalid_quantity", "quantity must be positive")
	}
	if _, err := checkItem(ctx, q, companyID, productID, warehouseID); err != nil {
		return err
	}
	if _, err := lockBalance(ctx, q, productID, warehouseID); err != nil {
		return err
	}
	return record(ctx, q, companyID, productID, warehouseID, date, qty, value, desc, src)
}

// Issue removes qty units at moving-average cost and returns the value issued.
func Issue(ctx context.Context, q db.DBTX, companyID, productID, warehouseID int64, date civil.Date, qty decimal.Decimal, desc string, src Source) (decimal.Decimal, error) {
	if !qty.IsPositive() {
		return decimal.Zero, apperr.Rule("invalid_quantity", "quantity must be positive")
	}
	name, err := checkItem(ctx, q, companyID, productID, warehouseID)
	if err != nil {
		return decimal.Zero, err
	}
	b, err := lockBalance(ctx, q, productID, warehouseID)
	if err != nil {
		return decimal.Zero, err
	}
	var allowNegative bool
	var places int32
	if err := q.QueryRow(ctx, `SELECT allow_negative_stock, amount_decimals FROM companies WHERE id = $1`, companyID).Scan(&allowNegative, &places); err != nil {
		return decimal.Zero, err
	}
	var value decimal.Decimal
	switch {
	case b.qty.GreaterThanOrEqual(qty):
		value = money.IssueValue(b.qty, b.value, qty, places)
	case !allowNegative:
		return decimal.Zero, apperr.Rule("insufficient_stock", "not enough stock").
			WithParams("product", name, "available", b.qty.String(), "requested", qty.String())
	default:
		// Negative stock allowed: cost the shortfall at the last average or the purchase price.
		unit := money.AverageCost(b.qty, b.value)
		if unit.IsZero() {
			if err := q.QueryRow(ctx, `SELECT purchase_price FROM products WHERE id = $1`, productID).Scan(&unit); err != nil {
				return decimal.Zero, err
			}
		}
		value = money.Round(unit.Mul(qty), places)
	}
	return value, record(ctx, q, companyID, productID, warehouseID, date, qty.Neg(), value.Neg(), desc, src)
}

// Reverse posts exact opposite movements for everything a source recorded.
// It is used when a posted document is cancelled.
func Reverse(ctx context.Context, q db.DBTX, companyID int64, src Source, date civil.Date, reversal Source) error {
	rows, err := q.Query(ctx, `SELECT product_id, warehouse_id, quantity, value, description FROM stock_movements
		WHERE company_id = $1 AND source_type = $2 AND source_id = $3 ORDER BY id`, companyID, src.Type, src.ID)
	if err != nil {
		return err
	}
	type mv struct {
		product, warehouse int64
		qty, value         decimal.Decimal
		desc               string
	}
	var moves []mv
	for rows.Next() {
		var m mv
		if err := rows.Scan(&m.product, &m.warehouse, &m.qty, &m.value, &m.desc); err != nil {
			rows.Close()
			return err
		}
		moves = append(moves, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	var allowNegative bool
	if err := q.QueryRow(ctx, `SELECT allow_negative_stock FROM companies WHERE id = $1`, companyID).Scan(&allowNegative); err != nil {
		return err
	}
	for _, m := range moves {
		b, err := lockBalance(ctx, q, m.product, m.warehouse)
		if err != nil {
			return err
		}
		if m.qty.IsPositive() && !allowNegative && b.qty.LessThan(m.qty) {
			var name string
			_ = q.QueryRow(ctx, `SELECT name FROM products WHERE id = $1`, m.product).Scan(&name)
			return apperr.Rule("insufficient_stock", "not enough stock to reverse this document").
				WithParams("product", name, "available", b.qty.String(), "requested", m.qty.String())
		}
		if err := record(ctx, q, companyID, m.product, m.warehouse, date, m.qty.Neg(), m.value.Neg(), m.desc, reversal); err != nil {
			return err
		}
	}
	return nil
}

// DefaultWarehouse returns the company's default warehouse id, or 0.
func DefaultWarehouse(ctx context.Context, q db.DBTX, companyID int64) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT id FROM warehouses WHERE company_id = $1 AND is_active ORDER BY is_default DESC, id LIMIT 1`, companyID).Scan(&id)
	if db.IsNoRows(err) {
		return 0, nil
	}
	return id, err
}
