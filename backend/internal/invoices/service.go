package invoices

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/inventory"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/money"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

func scope(side, kind string) string { return "inv_" + side + "_" + kind }

type productInfo struct {
	name    string
	kind    string
	tracked bool
	active  bool
	unit    string
}

func loadProducts(ctx context.Context, q db.DBTX, companyID int64, items []ItemInput) (map[int64]productInfo, error) {
	ids := []int64{}
	for _, it := range items {
		if it.ProductID != nil {
			ids = append(ids, *it.ProductID)
		}
	}
	out := map[int64]productInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, name, kind, track_inventory, is_active, unit FROM products WHERE company_id = $1 AND id = ANY($2)`, companyID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p productInfo
		if err := rows.Scan(&id, &p.name, &p.kind, &p.tracked, &p.active, &p.unit); err != nil {
			return nil, err
		}
		out[id] = p
	}
	return out, rows.Err()
}

// validate checks the full input against the database and fills defaults.
func validate(ctx context.Context, q db.DBTX, companyID int64, side, kind string, in *Input, places int32) error {
	in.Reference, in.Notes = strings.TrimSpace(in.Reference), strings.TrimSpace(in.Notes)
	v := apperr.NewValidator()
	v.Check(!in.Date.IsZero(), "date", "required")
	v.Check(in.DueDate == nil || in.DueDate.IsZero() || !in.DueDate.Before(in.Date), "due_date", "before_date")
	if in.DueDate != nil && in.DueDate.IsZero() {
		in.DueDate = nil
	}
	v.RequiredID("party_id", in.PartyID)
	v.MaxLen("reference", in.Reference, 100)
	v.MaxLen("notes", in.Notes, 2000)
	ValidateItems(v, in.Items, places)
	if in.PartyID > 0 {
		var isCustomer, isSupplier, active bool
		err := q.QueryRow(ctx, `SELECT is_customer, is_supplier, is_active FROM parties WHERE company_id = $1 AND id = $2`, companyID, in.PartyID).
			Scan(&isCustomer, &isSupplier, &active)
		if db.IsNoRows(err) {
			v.Check(false, "party_id", "invalid")
		} else if err != nil {
			return err
		} else {
			v.Check(active, "party_id", "inactive")
			v.Check((side == SideSales && isCustomer) || (side == SidePurchase && isSupplier), "party_id", "party_role")
		}
	}
	products, err := loadProducts(ctx, q, companyID, in.Items)
	if err != nil {
		return err
	}
	needsStock := false
	for i, it := range in.Items {
		if it.ProductID == nil {
			continue
		}
		p, ok := products[*it.ProductID]
		f := fmt.Sprintf("items.%d.product_id", i)
		v.Check(ok, f, "invalid")
		v.Check(!ok || p.active, f, "inactive")
		if ok {
			if strings.TrimSpace(in.Items[i].Unit) == "" {
				in.Items[i].Unit = p.unit
			}
			needsStock = needsStock || p.tracked
		}
	}
	if kind != KindQuotation && needsStock {
		if in.WarehouseID == nil {
			wid, err := inventory.DefaultWarehouse(ctx, q, companyID)
			if err != nil {
				return err
			}
			if wid > 0 {
				in.WarehouseID = &wid
			}
		}
		v.Check(in.WarehouseID != nil, "warehouse_id", "required")
	}
	if in.RelatedInvoiceID != nil {
		var rSide, rKind, rStatus string
		var rParty int64
		err := q.QueryRow(ctx, `SELECT side, kind, status, party_id FROM invoices WHERE company_id = $1 AND id = $2`, companyID, *in.RelatedInvoiceID).
			Scan(&rSide, &rKind, &rStatus, &rParty)
		switch {
		case db.IsNoRows(err):
			v.Check(false, "related_invoice_id", "invalid")
		case err != nil:
			return err
		case kind == KindReturn:
			v.Check(rSide == side && rKind == KindInvoice && rStatus == "posted", "related_invoice_id", "invalid")
			v.Check(rParty == in.PartyID, "related_invoice_id", "party_mismatch")
		case kind == KindInvoice:
			v.Check(rSide == side && rKind == KindQuotation, "related_invoice_id", "invalid")
		default:
			v.Check(false, "related_invoice_id", "invalid")
		}
	}
	return v.Err()
}

func saveItems(ctx context.Context, q db.DBTX, invoiceID int64, items []ItemInput, res []money.LineResult) error {
	if _, err := q.Exec(ctx, `DELETE FROM invoice_items WHERE invoice_id = $1`, invoiceID); err != nil {
		return err
	}
	for i, it := range items {
		r := res[i]
		if _, err := q.Exec(ctx, `
			INSERT INTO invoice_items (invoice_id, line_no, product_id, description, quantity, unit, unit_price, discount, tax_rate,
			                           net_amount, tax_amount, total)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			invoiceID, i+1, it.ProductID, strings.TrimSpace(it.Description), it.Quantity, strings.TrimSpace(it.Unit), it.UnitPrice,
			r.Discount, it.TaxRate, r.Net, r.Tax, r.Total); err != nil {
			return err
		}
	}
	return nil
}

// Create stores a new document (and posts it when requested) in the caller's transaction.
func Create(ctx context.Context, q db.DBTX, companyID, userID int64, side, kind string, in Input) (int64, error) {
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return 0, err
	}
	if err := validate(ctx, q, companyID, side, kind, &in, places); err != nil {
		return 0, err
	}
	res, tot := Compute(in.Items, places)
	number := in.Number
	if number > 0 {
		if err := db.BumpNumber(ctx, q, companyID, scope(side, kind), 0, number); err != nil {
			return 0, err
		}
	} else if number, err = db.NextNumber(ctx, q, companyID, scope(side, kind), 0); err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO invoices (company_id, side, kind, number, date, due_date, party_id, warehouse_id, cost_center_id, related_invoice_id,
		                      reference, notes, subtotal, discount_total, tax_total, total, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17) RETURNING id`,
		companyID, side, kind, number, in.Date, in.DueDate, in.PartyID, in.WarehouseID, in.CostCenterID, in.RelatedInvoiceID,
		in.Reference, in.Notes, tot.Subtotal, tot.Discount, tot.Tax, tot.Total, userID).Scan(&id); err != nil {
		return 0, err
	}
	if err := saveItems(ctx, q, id, in.Items, res); err != nil {
		return 0, err
	}
	if in.Post {
		return id, Post(ctx, q, companyID, userID, id)
	}
	return id, nil
}

func lockInvoice(ctx context.Context, q db.DBTX, companyID, id int64) (side, kind, status string, err error) {
	err = q.QueryRow(ctx, `SELECT side, kind, status FROM invoices WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&side, &kind, &status)
	if db.IsNoRows(err) {
		err = apperr.NotFound("invoice")
	}
	return
}

// Update replaces a draft document.
func Update(ctx context.Context, q db.DBTX, companyID, userID, id int64, in Input) error {
	side, kind, status, err := lockInvoice(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be edited")
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	if err := validate(ctx, q, companyID, side, kind, &in, places); err != nil {
		return err
	}
	res, tot := Compute(in.Items, places)
	numberSQL := "number"
	args := []any{companyID, id, in.Date, in.DueDate, in.PartyID, in.WarehouseID, in.CostCenterID, in.RelatedInvoiceID,
		in.Reference, in.Notes, tot.Subtotal, tot.Discount, tot.Tax, tot.Total}
	if in.Number > 0 {
		if err := db.BumpNumber(ctx, q, companyID, scope(side, kind), 0, in.Number); err != nil {
			return err
		}
		args = append(args, in.Number)
		numberSQL = "$15"
	}
	if _, err := q.Exec(ctx, `
		UPDATE invoices SET date = $3, due_date = $4, party_id = $5, warehouse_id = $6, cost_center_id = $7, related_invoice_id = $8,
		       reference = $9, notes = $10, subtotal = $11, discount_total = $12, tax_total = $13, total = $14, number = `+numberSQL+`,
		       updated_at = now()
		WHERE company_id = $1 AND id = $2`, args...); err != nil {
		return err
	}
	if err := saveItems(ctx, q, id, in.Items, res); err != nil {
		return err
	}
	if in.Post {
		return Post(ctx, q, companyID, userID, id)
	}
	return nil
}

// Post finalises a document: it creates the journal entry, moves stock and,
// for returns, settles the original invoice — all in the caller's transaction.
func Post(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	_, kind, status, err := lockInvoice(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be posted")
	}
	inv, err := Get(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if kind == KindQuotation {
		_, err := q.Exec(ctx, `UPDATE invoices SET status = 'posted', posted_at = now() WHERE id = $1`, id)
		return err
	}
	if !inv.Total.IsPositive() {
		return apperr.Rule("zero_total", "the document total must be greater than zero")
	}
	if _, err := ledger.ResolveFiscalYear(ctx, q, companyID, inv.Date); err != nil {
		return err
	}
	if kind == KindReturn {
		if err := checkReturnQuantities(ctx, q, inv); err != nil {
			return err
		}
	}
	lines, err := buildEntry(ctx, q, companyID, inv)
	if err != nil {
		return err
	}
	entryID, err := ledger.PostAuto(ctx, q, companyID, userID, inv.Date, entryDescription(inv), entryReference(inv), lines,
		ledger.Source{Type: inv.Side + "_" + inv.Kind, ID: inv.ID})
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE invoices SET status = 'posted', posted_at = now(), journal_entry_id = $2 WHERE id = $1`, id, entryID); err != nil {
		return err
	}
	if kind == KindReturn && inv.RelatedInvoiceID != nil {
		return applyCreditNote(ctx, q, companyID, inv)
	}
	return nil
}

func entryDescription(inv *Invoice) string {
	return fmt.Sprintf("%s %s #%d - %s", inv.Side, inv.Kind, inv.Number, inv.PartyName)
}

func entryReference(inv *Invoice) string {
	prefix := map[string]string{"sales_invoice": "SI", "sales_return": "SR", "purchase_invoice": "PI", "purchase_return": "PR"}[inv.Side+"_"+inv.Kind]
	return fmt.Sprintf("%s-%d", prefix, inv.Number)
}

// buildEntry produces the journal lines for a document and applies its stock movements.
func buildEntry(ctx context.Context, q db.DBTX, companyID int64, inv *Invoice) ([]ledger.LineInput, error) {
	maps, err := ledger.Mappings(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	req := func(key string) (int64, error) { return ledger.Require(maps, key) }
	var party struct{ receivable, payable *int64 }
	if err := q.QueryRow(ctx, `SELECT receivable_account_id, payable_account_id FROM parties WHERE id = $1`, inv.PartyID).
		Scan(&party.receivable, &party.payable); err != nil {
		return nil, err
	}
	partyAccount := func(override *int64, key string) (int64, error) {
		if override != nil {
			return *override, nil
		}
		return req(key)
	}
	pid := &inv.PartyID
	cc := inv.CostCenterID
	src := inventory.Source{Type: inv.Side + "_" + inv.Kind, ID: inv.ID}
	desc := entryDescription(inv)
	var b ledger.Builder

	switch inv.Side + "_" + inv.Kind {
	case "sales_invoice":
		ar, err := partyAccount(party.receivable, ledger.MapReceivable)
		if err != nil {
			return nil, err
		}
		taxAcc, err := req(ledger.MapTaxOutput)
		if err != nil && !inv.TaxTotal.IsZero() {
			return nil, err
		}
		b.Debit(ar, pid, nil, "", inv.Total)
		for _, it := range inv.Items {
			revKey := ledger.MapServiceRevenue
			if it.ProductID != nil && it.TrackInventory {
				revKey = ledger.MapSales
			} else if it.ProductID != nil {
				var kind string
				if err := q.QueryRow(ctx, `SELECT kind FROM products WHERE id = $1`, *it.ProductID).Scan(&kind); err != nil {
					return nil, err
				}
				if kind == "goods" {
					revKey = ledger.MapSales
				}
			}
			rev, err := req(revKey)
			if err != nil {
				return nil, err
			}
			b.Credit(rev, nil, cc, "", it.NetAmount.Add(it.Discount))
			if it.Discount.IsPositive() {
				disc, err := req(ledger.MapSalesDiscount)
				if err != nil {
					return nil, err
				}
				b.Debit(disc, nil, cc, "", it.Discount)
			}
			b.Credit(taxAcc, nil, nil, "", it.TaxAmount)
			if it.TrackInventory {
				cost, err := inventory.Issue(ctx, q, companyID, *it.ProductID, *inv.WarehouseID, inv.Date, it.Quantity, desc, src)
				if err != nil {
					return nil, err
				}
				if err := costLines(&b, maps, cc, cost, true); err != nil {
					return nil, err
				}
			}
		}

	case "sales_return":
		ar, err := partyAccount(party.receivable, ledger.MapReceivable)
		if err != nil {
			return nil, err
		}
		ret, err := req(ledger.MapSalesReturns)
		if err != nil {
			return nil, err
		}
		taxAcc, err := req(ledger.MapTaxOutput)
		if err != nil && !inv.TaxTotal.IsZero() {
			return nil, err
		}
		b.Credit(ar, pid, nil, "", inv.Total)
		for _, it := range inv.Items {
			b.Debit(ret, nil, cc, "", it.NetAmount)
			b.Debit(taxAcc, nil, nil, "", it.TaxAmount)
			if it.TrackInventory {
				unit, err := returnUnitCost(ctx, q, inv, *it.ProductID)
				if err != nil {
					return nil, err
				}
				value := money.Round(unit.Mul(it.Quantity), places)
				if err := inventory.Receive(ctx, q, companyID, *it.ProductID, *inv.WarehouseID, inv.Date, it.Quantity, value, desc, src); err != nil {
					return nil, err
				}
				if err := costLines(&b, maps, cc, value, false); err != nil {
					return nil, err
				}
			}
		}

	case "purchase_invoice":
		ap, err := partyAccount(party.payable, ledger.MapPayable)
		if err != nil {
			return nil, err
		}
		taxAcc, err := req(ledger.MapTaxInput)
		if err != nil && !inv.TaxTotal.IsZero() {
			return nil, err
		}
		b.Credit(ap, pid, nil, "", inv.Total)
		for _, it := range inv.Items {
			if it.TrackInventory {
				invAcc, err := req(ledger.MapInventory)
				if err != nil {
					return nil, err
				}
				if err := inventory.Receive(ctx, q, companyID, *it.ProductID, *inv.WarehouseID, inv.Date, it.Quantity, it.NetAmount, desc, src); err != nil {
					return nil, err
				}
				b.Debit(invAcc, nil, nil, "", it.NetAmount)
			} else {
				pur, err := req(ledger.MapPurchases)
				if err != nil {
					return nil, err
				}
				b.Debit(pur, nil, cc, "", it.NetAmount)
			}
			b.Debit(taxAcc, nil, nil, "", it.TaxAmount)
		}

	case "purchase_return":
		ap, err := partyAccount(party.payable, ledger.MapPayable)
		if err != nil {
			return nil, err
		}
		taxAcc, err := req(ledger.MapTaxInput)
		if err != nil && !inv.TaxTotal.IsZero() {
			return nil, err
		}
		b.Debit(ap, pid, nil, "", inv.Total)
		for _, it := range inv.Items {
			b.Credit(taxAcc, nil, nil, "", it.TaxAmount)
			if it.TrackInventory {
				invAcc, err := req(ledger.MapInventory)
				if err != nil {
					return nil, err
				}
				cost, err := inventory.Issue(ctx, q, companyID, *it.ProductID, *inv.WarehouseID, inv.Date, it.Quantity, desc, src)
				if err != nil {
					return nil, err
				}
				b.Credit(invAcc, nil, nil, "", cost)
				// Difference between the returned price and the carrying cost goes to cost of sales.
				if diff := it.NetAmount.Sub(cost); !diff.IsZero() {
					cogs, err := req(ledger.MapCOGS)
					if err != nil {
						return nil, err
					}
					b.Credit(cogs, nil, cc, "", diff)
				}
			} else {
				pur, err := req(ledger.MapPurchases)
				if err != nil {
					return nil, err
				}
				b.Credit(pur, nil, cc, "", it.NetAmount)
			}
		}
	default:
		return nil, apperr.Rule("invalid_document", "unsupported document type")
	}
	return b.Lines(), nil
}

// costLines books cost of goods sold against inventory (issue=true) or back (issue=false).
func costLines(b *ledger.Builder, maps map[string]int64, cc *int64, value decimal.Decimal, issue bool) error {
	if value.IsZero() {
		return nil
	}
	cogs, err := ledger.Require(maps, ledger.MapCOGS)
	if err != nil {
		return err
	}
	inv, err := ledger.Require(maps, ledger.MapInventory)
	if err != nil {
		return err
	}
	if issue {
		b.Debit(cogs, nil, cc, "", value)
		b.Credit(inv, nil, nil, "", value)
	} else {
		b.Debit(inv, nil, nil, "", value)
		b.Credit(cogs, nil, cc, "", value)
	}
	return nil
}

// returnUnitCost is the cost at which returned goods re-enter stock: the cost
// they left at on the original invoice, else the current average, else the purchase price.
func returnUnitCost(ctx context.Context, q db.DBTX, inv *Invoice, productID int64) (decimal.Decimal, error) {
	var unit decimal.Decimal
	if inv.RelatedInvoiceID != nil {
		err := q.QueryRow(ctx, `SELECT COALESCE(SUM(value) / NULLIF(SUM(quantity), 0), 0) FROM stock_movements
			WHERE source_type = 'sales_invoice' AND source_id = $1 AND product_id = $2`, *inv.RelatedInvoiceID, productID).Scan(&unit)
		if err != nil {
			return unit, err
		}
		if unit.IsPositive() {
			return unit, nil
		}
	}
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT value / NULLIF(quantity, 0) FROM stock_balances WHERE product_id = $1 AND warehouse_id = $2 AND quantity > 0),
		(SELECT purchase_price FROM products WHERE id = $1))`, productID, *inv.WarehouseID).Scan(&unit)
	return unit, err
}

// checkReturnQuantities prevents returning more of a product than the original invoice delivered.
func checkReturnQuantities(ctx context.Context, q db.DBTX, inv *Invoice) error {
	if inv.RelatedInvoiceID == nil {
		return nil
	}
	want := map[int64]decimal.Decimal{}
	for _, it := range inv.Items {
		if it.ProductID != nil {
			want[*it.ProductID] = want[*it.ProductID].Add(it.Quantity)
		}
	}
	for pid, qty := range want {
		var sold, returned decimal.Decimal
		if err := q.QueryRow(ctx, `
			SELECT COALESCE((SELECT SUM(quantity) FROM invoice_items WHERE invoice_id = $1 AND product_id = $2), 0),
			       COALESCE((SELECT SUM(it.quantity) FROM invoice_items it JOIN invoices r ON r.id = it.invoice_id
			                 WHERE r.related_invoice_id = $1 AND r.kind = 'return' AND r.status = 'posted' AND it.product_id = $2 AND r.id <> $3), 0)`,
			*inv.RelatedInvoiceID, pid, inv.ID).Scan(&sold, &returned); err != nil {
			return err
		}
		if qty.GreaterThan(sold.Sub(returned)) {
			var name string
			_ = q.QueryRow(ctx, `SELECT name FROM products WHERE id = $1`, pid).Scan(&name)
			return apperr.Rule("return_exceeds", "returned quantity exceeds the invoiced quantity").
				WithParams("product", name, "available", sold.Sub(returned).String())
		}
	}
	return nil
}

// applyCreditNote settles the original invoice with a posted return, up to its outstanding amount.
func applyCreditNote(ctx context.Context, q db.DBTX, companyID int64, ret *Invoice) error {
	var total, paid decimal.Decimal
	if err := q.QueryRow(ctx, `SELECT total, paid_amount FROM invoices WHERE id = $1 FOR UPDATE`, *ret.RelatedInvoiceID).Scan(&total, &paid); err != nil {
		return err
	}
	amount := decimal.Min(ret.Total, total.Sub(paid))
	if !amount.IsPositive() {
		return nil
	}
	return addAllocation(ctx, q, companyID, *ret.RelatedInvoiceID, nil, &ret.ID, nil, amount)
}

func addAllocation(ctx context.Context, q db.DBTX, companyID, invoiceID int64, txID, creditID, checkID *int64, amount decimal.Decimal) error {
	if _, err := q.Exec(ctx, `INSERT INTO payment_allocations (company_id, invoice_id, transaction_id, credit_invoice_id, check_id, amount) VALUES ($1, $2, $3, $4, $5, $6)`,
		companyID, invoiceID, txID, creditID, checkID, amount); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE invoices SET paid_amount = paid_amount + $2 WHERE id = $1`, invoiceID, amount); err != nil {
		return err
	}
	if creditID != nil {
		_, err := q.Exec(ctx, `UPDATE invoices SET paid_amount = paid_amount + $2 WHERE id = $1`, *creditID, amount)
		return err
	}
	return nil
}

// Cancel cancels a draft or posted document. Posted documents are reversed in
// the ledger and in stock; documents with payments must be unsettled first.
func Cancel(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	_, kind, status, err := lockInvoice(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if status == "cancelled" {
		return apperr.Rule("already_cancelled", "the document is already cancelled")
	}
	if status == "posted" && kind != KindQuotation {
		inv, err := Get(ctx, q, companyID, id)
		if err != nil {
			return err
		}
		var payments, returns int
		if err := q.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM payment_allocations WHERE (invoice_id = $1 AND credit_invoice_id IS NULL)),
			(SELECT count(*) FROM invoices WHERE related_invoice_id = $1 AND kind = 'return' AND status = 'posted')`, id).Scan(&payments, &returns); err != nil {
			return err
		}
		if payments > 0 {
			return apperr.Rule("has_payments", "cancel the payments allocated to this document first")
		}
		if returns > 0 {
			return apperr.Rule("has_returns", "cancel the returns of this invoice first")
		}
		// A refund paid against this return must be cancelled first too.
		var refunded int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE invoice_id = $1`, id).Scan(&refunded); err != nil {
			return err
		}
		if kind == KindReturn {
			if err := removeCreditNote(ctx, q, id); err != nil {
				return err
			}
		} else if refunded > 0 {
			return apperr.Rule("has_payments", "cancel the settlements of this document first")
		}
		src := inventory.Source{Type: inv.Side + "_" + inv.Kind, ID: id}
		if err := inventory.Reverse(ctx, q, companyID, src, inv.Date, inventory.Source{Type: src.Type + "_cancel", ID: id}); err != nil {
			return err
		}
		if inv.JournalEntryID != nil {
			if _, err := ledger.Cancel(ctx, q, companyID, userID, *inv.JournalEntryID, civil.Date{}, true); err != nil {
				return err
			}
		}
	}
	_, err = q.Exec(ctx, `UPDATE invoices SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, id)
	return err
}

func removeCreditNote(ctx context.Context, q db.DBTX, returnID int64) error {
	var txAllocs int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM payment_allocations WHERE invoice_id = $1 AND transaction_id IS NOT NULL`, returnID).Scan(&txAllocs); err != nil {
		return err
	}
	if txAllocs > 0 {
		return apperr.Rule("has_payments", "cancel the refunds of this return first")
	}
	rows, err := q.Query(ctx, `DELETE FROM payment_allocations WHERE credit_invoice_id = $1 RETURNING invoice_id, amount`, returnID)
	if err != nil {
		return err
	}
	type alloc struct {
		invoice int64
		amount  decimal.Decimal
	}
	var removed []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.invoice, &a.amount); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, a)
	}
	rows.Close()
	for _, a := range removed {
		if _, err := q.Exec(ctx, `UPDATE invoices SET paid_amount = paid_amount - $2 WHERE id = $1`, a.invoice, a.amount); err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE invoices SET paid_amount = paid_amount - $2 WHERE id = $1`, returnID, a.amount); err != nil {
			return err
		}
	}
	return rows.Err()
}

// DeleteDraft removes a draft document.
func DeleteDraft(ctx context.Context, q db.DBTX, companyID, id int64) error {
	_, _, status, err := lockInvoice(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_deletable", "only drafts can be deleted; cancel posted documents instead")
	}
	_, err = q.Exec(ctx, `DELETE FROM invoices WHERE id = $1`, id)
	return err
}

// ConvertQuotation creates a draft invoice from a quotation.
func ConvertQuotation(ctx context.Context, q db.DBTX, companyID, userID, id int64) (int64, error) {
	_, kind, status, err := lockInvoice(ctx, q, companyID, id)
	if err != nil {
		return 0, err
	}
	if kind != KindQuotation || status == "cancelled" {
		return 0, apperr.Rule("invalid_document", "only active quotations can be converted")
	}
	qt, err := Get(ctx, q, companyID, id)
	if err != nil {
		return 0, err
	}
	in := Input{Date: civil.Today(), PartyID: qt.PartyID, CostCenterID: qt.CostCenterID, RelatedInvoiceID: &qt.ID,
		Reference: qt.Reference, Notes: qt.Notes}
	for _, it := range qt.Items {
		in.Items = append(in.Items, ItemInput{ProductID: it.ProductID, Description: it.Description, Quantity: it.Quantity, Unit: it.Unit,
			UnitPrice: it.UnitPrice, Discount: it.Discount, TaxRate: it.TaxRate})
	}
	return Create(ctx, q, companyID, userID, SideSales, KindInvoice, in)
}

// OpenItem is an outstanding document a payment can settle.
type OpenItem struct {
	ID          int64           `json:"id"`
	Side        string          `json:"side"`
	Kind        string          `json:"kind"`
	Number      int64           `json:"number"`
	Date        civil.Date      `json:"date"`
	DueDate     *civil.Date     `json:"due_date"`
	Total       decimal.Decimal `json:"total"`
	Outstanding decimal.Decimal `json:"outstanding"`
}

// SettleableKinds returns which documents a treasury receipt or payment settles:
// receipts settle sales invoices and purchase returns; payments the opposite.
func SettleableKinds(txKind string) [][2]string {
	if txKind == "receipt" {
		return [][2]string{{SideSales, KindInvoice}, {SidePurchase, KindReturn}}
	}
	return [][2]string{{SidePurchase, KindInvoice}, {SideSales, KindReturn}}
}

// OpenItems lists a party's posted, not fully settled documents for a transaction kind.
func OpenItems(ctx context.Context, q db.DBTX, companyID, partyID int64, txKind string, lock bool) ([]OpenItem, error) {
	kinds := SettleableKinds(txKind)
	sql := `SELECT id, side, kind, number, date, due_date, total, total - paid_amount FROM invoices
		WHERE company_id = $1 AND party_id = $2 AND status = 'posted' AND paid_amount < total
		  AND ((side = $3 AND kind = $4) OR (side = $5 AND kind = $6)) ORDER BY date, id`
	if lock {
		sql += " FOR UPDATE"
	}
	rows, err := q.Query(ctx, sql, companyID, partyID, kinds[0][0], kinds[0][1], kinds[1][0], kinds[1][1])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OpenItem{}
	for rows.Next() {
		var o OpenItem
		if err := rows.Scan(&o.ID, &o.Side, &o.Kind, &o.Number, &o.Date, &o.DueDate, &o.Total, &o.Outstanding); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// AllocationInput is an explicit allocation chosen by the user.
type AllocationInput struct {
	InvoiceID int64           `json:"invoice_id"`
	Amount    decimal.Decimal `json:"amount"`
}

// AllocatePayment settles open documents with a treasury transaction. Explicit
// allocations are validated; otherwise the amount is applied oldest-first.
func AllocatePayment(ctx context.Context, q db.DBTX, companyID, partyID, txID int64, txKind string, amount decimal.Decimal, explicit []AllocationInput, auto bool) error {
	return allocate(ctx, q, companyID, partyID, &txID, nil, txKind, amount, explicit, auto)
}

// AllocateCheck settles open documents with a check: a received check acts as
// a receipt, an issued check as a payment.
func AllocateCheck(ctx context.Context, q db.DBTX, companyID, partyID, checkID int64, txKind string, amount decimal.Decimal, explicit []AllocationInput, auto bool) error {
	return allocate(ctx, q, companyID, partyID, nil, &checkID, txKind, amount, explicit, auto)
}

func allocate(ctx context.Context, q db.DBTX, companyID, partyID int64, txID, checkID *int64, txKind string, amount decimal.Decimal, explicit []AllocationInput, auto bool) error {
	open, err := OpenItems(ctx, q, companyID, partyID, txKind, true)
	if err != nil {
		return err
	}
	var allocs []money.Allocation
	if len(explicit) > 0 {
		byID := map[int64]OpenItem{}
		for _, o := range open {
			byID[o.ID] = o
		}
		sum := decimal.Zero
		v := apperr.NewValidator()
		for i, a := range explicit {
			o, ok := byID[a.InvoiceID]
			f := fmt.Sprintf("allocations.%d.amount", i)
			v.Check(ok, fmt.Sprintf("allocations.%d.invoice_id", i), "invalid")
			v.Check(a.Amount.IsPositive(), f, "positive")
			v.Check(!ok || a.Amount.LessThanOrEqual(o.Outstanding), f, "exceeds_outstanding")
			sum = sum.Add(a.Amount)
			allocs = append(allocs, money.Allocation{ID: a.InvoiceID, Amount: a.Amount})
		}
		v.Check(sum.LessThanOrEqual(amount), "allocations", "exceeds_amount")
		if err := v.Err(); err != nil {
			return err
		}
	} else if auto {
		items := make([]money.OpenItem, len(open))
		for i, o := range open {
			items[i] = money.OpenItem{ID: o.ID, Date: o.Date.String(), Outstanding: o.Outstanding}
		}
		allocs, _ = money.AllocateFIFO(amount, items)
	}
	for _, a := range allocs {
		if err := addAllocation(ctx, q, companyID, a.ID, txID, nil, checkID, a.Amount); err != nil {
			return err
		}
	}
	return nil
}

// ReleasePayment removes a cancelled transaction's allocations.
func ReleasePayment(ctx context.Context, q db.DBTX, txID int64) error {
	return release(ctx, q, `DELETE FROM payment_allocations WHERE transaction_id = $1 RETURNING invoice_id, amount`, txID)
}

// ReleaseCheck removes the allocations of a returned or cancelled check.
func ReleaseCheck(ctx context.Context, q db.DBTX, checkID int64) error {
	return release(ctx, q, `DELETE FROM payment_allocations WHERE check_id = $1 RETURNING invoice_id, amount`, checkID)
}

func release(ctx context.Context, q db.DBTX, sql string, id int64) error {
	rows, err := q.Query(ctx, sql, id)
	if err != nil {
		return err
	}
	type alloc struct {
		invoice int64
		amount  decimal.Decimal
	}
	var removed []alloc
	for rows.Next() {
		var a alloc
		if err := rows.Scan(&a.invoice, &a.amount); err != nil {
			rows.Close()
			return err
		}
		removed = append(removed, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range removed {
		if _, err := q.Exec(ctx, `UPDATE invoices SET paid_amount = paid_amount - $2 WHERE id = $1`, a.invoice, a.amount); err != nil {
			return err
		}
	}
	return nil
}
