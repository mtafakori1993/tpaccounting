// Package invoices implements sales and purchase documents (quotations,
// invoices, returns) and their automatic accounting and stock effects.
package invoices

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/money"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

const (
	SideSales    = "sales"
	SidePurchase = "purchase"

	KindQuotation = "quotation"
	KindInvoice   = "invoice"
	KindReturn    = "return"
)

type Item struct {
	ID             int64           `json:"id"`
	ProductID      *int64          `json:"product_id"`
	ProductName    *string         `json:"product_name"`
	ProductSKU     *string         `json:"product_sku"`
	TrackInventory bool            `json:"track_inventory"`
	Description    string          `json:"description"`
	Quantity       decimal.Decimal `json:"quantity"`
	Unit           string          `json:"unit"`
	UnitPrice      decimal.Decimal `json:"unit_price"`
	Discount       decimal.Decimal `json:"discount"`
	TaxRate        decimal.Decimal `json:"tax_rate"`
	NetAmount      decimal.Decimal `json:"net_amount"`
	TaxAmount      decimal.Decimal `json:"tax_amount"`
	Total          decimal.Decimal `json:"total"`
}

type Settlement struct {
	ID            int64           `json:"id"`
	Amount        decimal.Decimal `json:"amount"`
	TransactionID *int64          `json:"transaction_id"`
	TxKind        *string         `json:"transaction_kind"`
	TxNumber      *int64          `json:"transaction_number"`
	CreditID      *int64          `json:"credit_invoice_id"`
	CreditNumber  *int64          `json:"credit_invoice_number"`
	CheckID       *int64          `json:"check_id"`
	CheckNumber   *string         `json:"check_number"`
	Date          civil.Date      `json:"date"`
}

type Invoice struct {
	ID               int64           `json:"id"`
	Side             string          `json:"side"`
	Kind             string          `json:"kind"`
	Number           int64           `json:"number"`
	Date             civil.Date      `json:"date"`
	DueDate          *civil.Date     `json:"due_date"`
	PartyID          int64           `json:"party_id"`
	PartyName        string          `json:"party_name"`
	PartyCode        string          `json:"party_code"`
	WarehouseID      *int64          `json:"warehouse_id"`
	WarehouseName    *string         `json:"warehouse_name"`
	CostCenterID     *int64          `json:"cost_center_id"`
	CostCenterName   *string         `json:"cost_center_name"`
	RelatedInvoiceID *int64          `json:"related_invoice_id"`
	RelatedNumber    *int64          `json:"related_number"`
	RelatedKind      *string         `json:"related_kind"`
	Reference        string          `json:"reference"`
	Notes            string          `json:"notes"`
	Status           string          `json:"status"`
	Subtotal         decimal.Decimal `json:"subtotal"`
	DiscountTotal    decimal.Decimal `json:"discount_total"`
	TaxTotal         decimal.Decimal `json:"tax_total"`
	Total            decimal.Decimal `json:"total"`
	PaidAmount       decimal.Decimal `json:"paid_amount"`
	Outstanding      decimal.Decimal `json:"outstanding"`
	PaymentStatus    string          `json:"payment_status"`
	JournalEntryID   *int64          `json:"journal_entry_id"`
	JournalNumber    *int64          `json:"journal_number"`
	CreatedAt        time.Time       `json:"created_at"`
	PostedAt         *time.Time      `json:"posted_at"`
	Items            []Item          `json:"items,omitempty"`
	Settlements      []Settlement    `json:"settlements,omitempty"`
}

type ItemInput struct {
	ProductID   *int64          `json:"product_id"`
	Description string          `json:"description"`
	Quantity    decimal.Decimal `json:"quantity"`
	Unit        string          `json:"unit"`
	UnitPrice   decimal.Decimal `json:"unit_price"`
	Discount    decimal.Decimal `json:"discount"`
	TaxRate     decimal.Decimal `json:"tax_rate"`
}

type Input struct {
	Number           int64       `json:"number"`
	Date             civil.Date  `json:"date"`
	DueDate          *civil.Date `json:"due_date"`
	PartyID          int64       `json:"party_id"`
	WarehouseID      *int64      `json:"warehouse_id"`
	CostCenterID     *int64      `json:"cost_center_id"`
	RelatedInvoiceID *int64      `json:"related_invoice_id"`
	Reference        string      `json:"reference"`
	Notes            string      `json:"notes"`
	Items            []ItemInput `json:"items"`
	Post             bool        `json:"post"`
}

// Compute calculates every line and the document totals.
func Compute(items []ItemInput, places int32) ([]money.LineResult, money.Totals) {
	res := make([]money.LineResult, len(items))
	for i, it := range items {
		res[i] = money.CalcLine(money.LineInput{Quantity: it.Quantity, UnitPrice: it.UnitPrice, Discount: it.Discount, TaxRate: it.TaxRate}, places)
	}
	return res, money.SumLines(res)
}

// ValidateItems checks the structural rules for invoice lines.
func ValidateItems(v *apperr.Validator, items []ItemInput, places int32) {
	v.Check(len(items) > 0, "items", "min_lines")
	v.Check(len(items) <= 500, "items", "too_many")
	hundred := decimal.NewFromInt(100)
	for i, it := range items {
		f := fmt.Sprintf("items.%d.", i)
		v.Check(it.ProductID != nil || strings.TrimSpace(it.Description) != "", f+"product_id", "required")
		v.Check(it.Quantity.IsPositive(), f+"quantity", "positive")
		v.Check(it.Quantity.Equal(it.Quantity.Round(3)), f+"quantity", "too_many_decimals")
		v.Check(!it.UnitPrice.IsNegative(), f+"unit_price", "negative")
		v.Check(it.UnitPrice.Equal(it.UnitPrice.Round(places)), f+"unit_price", "too_many_decimals")
		v.Check(!it.Discount.IsNegative(), f+"discount", "negative")
		v.Check(it.Discount.LessThanOrEqual(money.Round(it.Quantity.Mul(it.UnitPrice), places)), f+"discount", "discount_exceeds")
		v.Check(!it.TaxRate.IsNegative() && it.TaxRate.LessThanOrEqual(hundred), f+"tax_rate", "invalid_rate")
		v.MaxLen(f+"description", it.Description, 500)
	}
}

const invoiceCols = `
	i.id, i.side, i.kind, i.number, i.date, i.due_date, i.party_id, p.name, p.code, i.warehouse_id, w.name, i.cost_center_id, cc.name,
	i.related_invoice_id, ri.number, ri.kind, i.reference, i.notes, i.status, i.subtotal, i.discount_total, i.tax_total, i.total,
	i.paid_amount, i.journal_entry_id, je.number, i.created_at, i.posted_at`

const invoiceJoins = `
	FROM invoices i JOIN parties p ON p.id = i.party_id
	LEFT JOIN warehouses w ON w.id = i.warehouse_id
	LEFT JOIN cost_centers cc ON cc.id = i.cost_center_id
	LEFT JOIN invoices ri ON ri.id = i.related_invoice_id
	LEFT JOIN journal_entries je ON je.id = i.journal_entry_id`

type rowScanner interface{ Scan(...any) error }

func scanInvoice(s rowScanner, x *Invoice) error {
	err := s.Scan(&x.ID, &x.Side, &x.Kind, &x.Number, &x.Date, &x.DueDate, &x.PartyID, &x.PartyName, &x.PartyCode, &x.WarehouseID,
		&x.WarehouseName, &x.CostCenterID, &x.CostCenterName, &x.RelatedInvoiceID, &x.RelatedNumber, &x.RelatedKind, &x.Reference,
		&x.Notes, &x.Status, &x.Subtotal, &x.DiscountTotal, &x.TaxTotal, &x.Total, &x.PaidAmount, &x.JournalEntryID,
		&x.JournalNumber, &x.CreatedAt, &x.PostedAt)
	x.Outstanding = x.Total.Sub(x.PaidAmount)
	x.PaymentStatus = money.PaymentStatus(x.Total, x.PaidAmount)
	return err
}

// Get loads a document with items and settlements.
func Get(ctx context.Context, q db.DBTX, companyID, id int64) (*Invoice, error) {
	var x Invoice
	err := scanInvoice(q.QueryRow(ctx, `SELECT `+invoiceCols+invoiceJoins+` WHERE i.company_id = $1 AND i.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("invoice")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT it.id, it.product_id, pr.name, pr.sku, COALESCE(pr.track_inventory, FALSE), it.description, it.quantity, it.unit,
		       it.unit_price, it.discount, it.tax_rate, it.net_amount, it.tax_amount, it.total
		FROM invoice_items it LEFT JOIN products pr ON pr.id = it.product_id
		WHERE it.invoice_id = $1 ORDER BY it.line_no`, id)
	if err != nil {
		return nil, err
	}
	x.Items = []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.ProductID, &it.ProductName, &it.ProductSKU, &it.TrackInventory, &it.Description, &it.Quantity,
			&it.Unit, &it.UnitPrice, &it.Discount, &it.TaxRate, &it.NetAmount, &it.TaxAmount, &it.Total); err != nil {
			rows.Close()
			return nil, err
		}
		x.Items = append(x.Items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	srows, err := q.Query(ctx, `
		SELECT a.id, a.amount, a.transaction_id, t.kind, t.number, a.credit_invoice_id, ci.number, a.check_id, ch.check_number,
		       COALESCE(t.date, ci.date, ch.received_date, ch.issue_date)
		FROM payment_allocations a
		LEFT JOIN treasury_transactions t ON t.id = a.transaction_id
		LEFT JOIN invoices ci ON ci.id = a.credit_invoice_id
		LEFT JOIN checks ch ON ch.id = a.check_id
		WHERE a.invoice_id = $1 OR a.credit_invoice_id = $1 ORDER BY a.id`, id)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	x.Settlements = []Settlement{}
	for srows.Next() {
		var s Settlement
		if err := srows.Scan(&s.ID, &s.Amount, &s.TransactionID, &s.TxKind, &s.TxNumber, &s.CreditID, &s.CreditNumber, &s.CheckID, &s.CheckNumber, &s.Date); err != nil {
			return nil, err
		}
		x.Settlements = append(x.Settlements, s)
	}
	return &x, srows.Err()
}

type ListFilter struct {
	Side, Kind, Status, PaymentStatus string
	PartyID                           int64
	From, To                          civil.Date
}

func List(ctx context.Context, q db.DBTX, companyID int64, lf ListFilter, lp httpx.ListParams) (httpx.Page[Invoice], error) {
	f := db.NewFilter("i.company_id = $1", companyID)
	f.Add("i.side = $N", lf.Side)
	f.Add("i.kind = $N", lf.Kind)
	if lf.Status != "" {
		f.Add("i.status = $N", lf.Status)
	}
	if lf.PartyID > 0 {
		f.Add("i.party_id = $N", lf.PartyID)
	}
	if !lf.From.IsZero() {
		f.Add("i.date >= $N", lf.From)
	}
	if !lf.To.IsZero() {
		f.Add("i.date <= $N", lf.To)
	}
	switch lf.PaymentStatus {
	case "unpaid":
		f.AddRaw("i.status = 'posted' AND i.paid_amount = 0")
	case "partial":
		f.AddRaw("i.status = 'posted' AND i.paid_amount > 0 AND i.paid_amount < i.total")
	case "paid":
		f.AddRaw("i.status = 'posted' AND i.paid_amount >= i.total")
	case "open":
		f.AddRaw("i.status = 'posted' AND i.paid_amount < i.total")
	case "overdue":
		f.AddRaw("i.status = 'posted' AND i.paid_amount < i.total AND i.due_date < CURRENT_DATE")
	}
	if lp.Q != "" {
		f.Add("(p.name ILIKE $N OR i.reference ILIKE $N OR i.notes ILIKE $N OR i.number::text = "+f.Arg(lp.Q)+")", httpx.Like(lp.Q))
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM invoices i JOIN parties p ON p.id = i.party_id WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return httpx.Page[Invoice]{}, err
	}
	order := lp.OrderBy(map[string]string{"date": "i.date", "number": "i.number", "total": "i.total", "party": "p.name",
		"due_date": "i.due_date", "status": "i.status"}, "i.date")
	rows, err := q.Query(ctx, `SELECT `+invoiceCols+invoiceJoins+` WHERE `+f.Where()+` ORDER BY `+order+`, i.number DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return httpx.Page[Invoice]{}, err
	}
	defer rows.Close()
	var items []Invoice
	for rows.Next() {
		var x Invoice
		if err := scanInvoice(rows, &x); err != nil {
			return httpx.Page[Invoice]{}, err
		}
		items = append(items, x)
	}
	return httpx.NewPage(items, total, lp), rows.Err()
}
