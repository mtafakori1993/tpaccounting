package inventory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/money"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

type DocLine struct {
	ID          int64           `json:"id"`
	ProductID   int64           `json:"product_id"`
	ProductName string          `json:"product_name"`
	ProductSKU  string          `json:"product_sku"`
	Unit        string          `json:"unit"`
	Quantity    decimal.Decimal `json:"quantity"`
	UnitCost    decimal.Decimal `json:"unit_cost"`
	Description string          `json:"description"`
}

type Document struct {
	ID                 int64      `json:"id"`
	Kind               string     `json:"kind"`
	Number             int64      `json:"number"`
	Date               civil.Date `json:"date"`
	WarehouseID        int64      `json:"warehouse_id"`
	WarehouseName      string     `json:"warehouse_name"`
	ToWarehouseID      *int64     `json:"to_warehouse_id"`
	ToWarehouseName    *string    `json:"to_warehouse_name"`
	CounterAccountID   *int64     `json:"counter_account_id"`
	CounterAccountName *string    `json:"counter_account_name"`
	Description        string     `json:"description"`
	Reference          string     `json:"reference"`
	Status             string     `json:"status"`
	JournalEntryID     *int64     `json:"journal_entry_id"`
	CreatedAt          time.Time  `json:"created_at"`
	Lines              []DocLine  `json:"lines,omitempty"`
}

type DocInput struct {
	Kind             string     `json:"kind"`
	Date             civil.Date `json:"date"`
	WarehouseID      int64      `json:"warehouse_id"`
	ToWarehouseID    *int64     `json:"to_warehouse_id"`
	CounterAccountID *int64     `json:"counter_account_id"`
	Description      string     `json:"description"`
	Reference        string     `json:"reference"`
	Lines            []DocLine  `json:"lines"`
	Post             bool       `json:"post"`
}

var docKinds = map[string]bool{"receipt": true, "issue": true, "transfer": true, "adjustment": true}

func validateDoc(in *DocInput) error {
	in.Description, in.Reference = strings.TrimSpace(in.Description), strings.TrimSpace(in.Reference)
	v := apperr.NewValidator()
	v.Check(docKinds[in.Kind], "kind", "invalid")
	v.Check(!in.Date.IsZero(), "date", "required")
	v.RequiredID("warehouse_id", in.WarehouseID)
	if in.Kind == "transfer" {
		v.Check(in.ToWarehouseID != nil && *in.ToWarehouseID != in.WarehouseID, "to_warehouse_id", "required")
	} else {
		in.ToWarehouseID = nil
	}
	v.Check(len(in.Lines) > 0, "lines", "min_lines")
	for i, l := range in.Lines {
		f := fmt.Sprintf("lines.%d.", i)
		v.RequiredID(f+"product_id", l.ProductID)
		if in.Kind == "adjustment" {
			v.Check(!l.Quantity.IsZero(), f+"quantity", "required")
		} else {
			v.Check(l.Quantity.IsPositive(), f+"quantity", "positive")
		}
		v.Check(!l.UnitCost.IsNegative(), f+"unit_cost", "negative")
	}
	return v.Err()
}

func getDocument(ctx context.Context, q db.DBTX, companyID, id int64) (*Document, error) {
	var d Document
	err := q.QueryRow(ctx, `
		SELECT d.id, d.kind, d.number, d.date, d.warehouse_id, w.name, d.to_warehouse_id, tw.name, d.counter_account_id,
		       a.code || ' - ' || a.name, d.description, d.reference, d.status, d.journal_entry_id, d.created_at
		FROM stock_documents d JOIN warehouses w ON w.id = d.warehouse_id
		LEFT JOIN warehouses tw ON tw.id = d.to_warehouse_id
		LEFT JOIN accounts a ON a.id = d.counter_account_id
		WHERE d.company_id = $1 AND d.id = $2`, companyID, id).
		Scan(&d.ID, &d.Kind, &d.Number, &d.Date, &d.WarehouseID, &d.WarehouseName, &d.ToWarehouseID, &d.ToWarehouseName,
			&d.CounterAccountID, &d.CounterAccountName, &d.Description, &d.Reference, &d.Status, &d.JournalEntryID, &d.CreatedAt)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("stock_document")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT l.id, l.product_id, p.name, p.sku, p.unit, l.quantity, l.unit_cost, l.description
		FROM stock_document_lines l JOIN products p ON p.id = l.product_id WHERE l.document_id = $1 ORDER BY l.line_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.Lines = []DocLine{}
	for rows.Next() {
		var l DocLine
		if err := rows.Scan(&l.ID, &l.ProductID, &l.ProductName, &l.ProductSKU, &l.Unit, &l.Quantity, &l.UnitCost, &l.Description); err != nil {
			return nil, err
		}
		d.Lines = append(d.Lines, l)
	}
	return &d, rows.Err()
}

func listDocuments(ctx context.Context, q db.DBTX, companyID int64, kind, status string, lp httpx.ListParams) (httpx.Page[Document], error) {
	f := db.NewFilter("d.company_id = $1", companyID)
	if kind != "" {
		f.Add("d.kind = $N", kind)
	}
	if status != "" {
		f.Add("d.status = $N", status)
	}
	if lp.Q != "" {
		f.Add("(d.description ILIKE $N OR d.reference ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM stock_documents d WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return httpx.Page[Document]{}, err
	}
	order := lp.OrderBy(map[string]string{"date": "d.date", "number": "d.number", "kind": "d.kind"}, "d.date")
	rows, err := q.Query(ctx, `
		SELECT d.id, d.kind, d.number, d.date, d.warehouse_id, w.name, d.to_warehouse_id, tw.name, d.description, d.reference,
		       d.status, d.journal_entry_id, d.created_at
		FROM stock_documents d JOIN warehouses w ON w.id = d.warehouse_id LEFT JOIN warehouses tw ON tw.id = d.to_warehouse_id
		WHERE `+f.Where()+` ORDER BY `+order+`, d.id DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return httpx.Page[Document]{}, err
	}
	defer rows.Close()
	var items []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Kind, &d.Number, &d.Date, &d.WarehouseID, &d.WarehouseName, &d.ToWarehouseID,
			&d.ToWarehouseName, &d.Description, &d.Reference, &d.Status, &d.JournalEntryID, &d.CreatedAt); err != nil {
			return httpx.Page[Document]{}, err
		}
		items = append(items, d)
	}
	return httpx.NewPage(items, total, lp), rows.Err()
}

func saveLines(ctx context.Context, q db.DBTX, docID int64, lines []DocLine) error {
	if _, err := q.Exec(ctx, `DELETE FROM stock_document_lines WHERE document_id = $1`, docID); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := q.Exec(ctx, `INSERT INTO stock_document_lines (document_id, line_no, product_id, quantity, unit_cost, description)
			VALUES ($1, $2, $3, $4, $5, $6)`, docID, i+1, l.ProductID, l.Quantity, l.UnitCost, strings.TrimSpace(l.Description)); err != nil {
			return err
		}
	}
	return nil
}

func createDocument(ctx context.Context, q db.DBTX, companyID, userID int64, in DocInput) (int64, error) {
	if err := validateDoc(&in); err != nil {
		return 0, err
	}
	n, err := db.NextNumber(ctx, q, companyID, "stock_"+in.Kind, 0)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO stock_documents (company_id, kind, number, date, warehouse_id, to_warehouse_id, counter_account_id, description, reference, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		companyID, in.Kind, n, in.Date, in.WarehouseID, in.ToWarehouseID, in.CounterAccountID, in.Description, in.Reference, userID).Scan(&id); err != nil {
		return 0, err
	}
	if err := saveLines(ctx, q, id, in.Lines); err != nil {
		return 0, err
	}
	if in.Post {
		return id, postDocument(ctx, q, companyID, userID, id)
	}
	return id, nil
}

func updateDocument(ctx context.Context, q db.DBTX, companyID, userID, id int64, in DocInput) error {
	var status, kind string
	if err := q.QueryRow(ctx, `SELECT status, kind FROM stock_documents WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&status, &kind); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("stock_document")
		}
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be edited")
	}
	in.Kind = kind
	if err := validateDoc(&in); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE stock_documents SET date = $3, warehouse_id = $4, to_warehouse_id = $5, counter_account_id = $6,
		description = $7, reference = $8 WHERE company_id = $1 AND id = $2`,
		companyID, id, in.Date, in.WarehouseID, in.ToWarehouseID, in.CounterAccountID, in.Description, in.Reference); err != nil {
		return err
	}
	if err := saveLines(ctx, q, id, in.Lines); err != nil {
		return err
	}
	if in.Post {
		return postDocument(ctx, q, companyID, userID, id)
	}
	return nil
}

// postDocument applies a stock document to the stock ledger and the general
// ledger in the caller's transaction.
func postDocument(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM stock_documents WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&status); err != nil {
		return err
	}
	if status != "draft" {
		return apperr.Rule("not_draft", "only drafts can be posted")
	}
	d, err := getDocument(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if _, err := ledger.ResolveFiscalYear(ctx, q, companyID, d.Date); err != nil {
		return err
	}
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	src := Source{Type: "stock_document", ID: id}
	desc := d.Description
	net := decimal.Zero // value added to (+) or removed from (−) inventory
	for _, l := range d.Lines {
		switch d.Kind {
		case "receipt":
			v := money.Round(l.Quantity.Mul(l.UnitCost), places)
			if err := Receive(ctx, q, companyID, l.ProductID, d.WarehouseID, d.Date, l.Quantity, v, desc, src); err != nil {
				return err
			}
			net = net.Add(v)
		case "issue":
			v, err := Issue(ctx, q, companyID, l.ProductID, d.WarehouseID, d.Date, l.Quantity, desc, src)
			if err != nil {
				return err
			}
			net = net.Sub(v)
		case "transfer":
			v, err := Issue(ctx, q, companyID, l.ProductID, d.WarehouseID, d.Date, l.Quantity, desc, src)
			if err != nil {
				return err
			}
			if err := Receive(ctx, q, companyID, l.ProductID, *d.ToWarehouseID, d.Date, l.Quantity, v, desc, src); err != nil {
				return err
			}
		case "adjustment":
			if l.Quantity.IsPositive() {
				unit := l.UnitCost
				if unit.IsZero() {
					b, err := lockBalance(ctx, q, l.ProductID, d.WarehouseID)
					if err != nil {
						return err
					}
					if unit = money.AverageCost(b.qty, b.value); unit.IsZero() {
						if err := q.QueryRow(ctx, `SELECT purchase_price FROM products WHERE id = $1`, l.ProductID).Scan(&unit); err != nil {
							return err
						}
					}
				}
				v := money.Round(l.Quantity.Mul(unit), places)
				if err := Receive(ctx, q, companyID, l.ProductID, d.WarehouseID, d.Date, l.Quantity, v, desc, src); err != nil {
					return err
				}
				net = net.Add(v)
			} else {
				v, err := Issue(ctx, q, companyID, l.ProductID, d.WarehouseID, d.Date, l.Quantity.Neg(), desc, src)
				if err != nil {
					return err
				}
				net = net.Sub(v)
			}
		}
	}
	var entryID *int64
	if !net.IsZero() {
		maps, err := ledger.Mappings(ctx, q, companyID)
		if err != nil {
			return err
		}
		inv, err := ledger.Require(maps, ledger.MapInventory)
		if err != nil {
			return err
		}
		counter := int64(0)
		if d.CounterAccountID != nil {
			counter = *d.CounterAccountID
		} else if counter, err = ledger.Require(maps, ledger.MapInventoryAdjustment); err != nil {
			return err
		}
		var b ledger.Builder
		b.Debit(inv, nil, nil, desc, net)
		b.Credit(counter, nil, nil, desc, net)
		eid, err := ledger.PostAuto(ctx, q, companyID, userID, d.Date, stockEntryDesc(d), stockRef(d), b.Lines(),
			ledger.Source{Type: "stock_document", ID: id})
		if err != nil {
			return err
		}
		entryID = &eid
	}
	_, err = q.Exec(ctx, `UPDATE stock_documents SET status = 'posted', posted_at = now(), journal_entry_id = $2 WHERE id = $1`, id, entryID)
	return err
}

func stockEntryDesc(d *Document) string {
	if d.Description != "" {
		return d.Description
	}
	return fmt.Sprintf("Stock %s #%d", d.Kind, d.Number)
}

func stockRef(d *Document) string {
	return fmt.Sprintf("STK-%s-%d", strings.ToUpper(d.Kind[:3]), d.Number)
}

func cancelDocument(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	var entryID *int64
	var date civil.Date
	if err := q.QueryRow(ctx, `SELECT status, journal_entry_id, date FROM stock_documents WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).
		Scan(&status, &entryID, &date); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("stock_document")
		}
		return err
	}
	switch status {
	case "cancelled":
		return apperr.Rule("already_cancelled", "the document is already cancelled")
	case "posted":
		if err := Reverse(ctx, q, companyID, Source{"stock_document", id}, date, Source{"stock_document_cancel", id}); err != nil {
			return err
		}
		if entryID != nil {
			if _, err := ledger.Cancel(ctx, q, companyID, userID, *entryID, civil.Date{}, true); err != nil {
				return err
			}
		}
	}
	_, err := q.Exec(ctx, `UPDATE stock_documents SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, id)
	return err
}
