package catalog

import "strings"

// Helpers for operating an operation's real screen (HUMAN_UI). The form itself
// (Operation.UI) is discovered from the frontend source by cmd/capmap; this file only
// adds what the generator cannot see: the cell order of the line grids (curated from the
// frontend pages, like the variants in spec.go) and a few lookups.

// LineCol is one column of a document's line grid.
type LineCol struct {
	Tag     string // i18n key of the column header
	Control string // fill | combo | select
	Col     int    // 0-based cell index in the row (the line-number cell is 0)
}

// LineGrid is the editable line-item table of a page form.
type LineGrid struct {
	Field string             // request field holding the lines: items | lines
	Add   string             // data-ai-action of the "add line" button
	Cols  map[string]LineCol // line field → column
}

var invoiceGrid = LineGrid{Field: "items", Add: "invoices.addItem", Cols: map[string]LineCol{
	"product_id": {"invoices.item", "combo", 1}, "description": {"common.description", "fill", 2}, "quantity": {"invoices.quantity", "fill", 3},
	"unit": {"invoices.unit", "fill", 4}, "unit_price": {"invoices.unitPrice", "fill", 5}, "discount": {"invoices.discount", "fill", 6},
	"tax_rate": {"invoices.taxRate", "fill", 7},
}}

// lineGrids: InvoiceFormPage.tsx and JournalFormPage.tsx table columns.
var lineGrids = map[string]LineGrid{
	"sales.kind.create":     invoiceGrid,
	"purchases.kind.create": invoiceGrid,
	"journal_entries.create": {Field: "lines", Add: "journal.addLine", Cols: map[string]LineCol{
		"account_id": {"journal.account", "combo", 1}, "party_id": {"journal.party", "combo", 2}, "cost_center_id": {"journal.costCenter", "select", 3},
		"description": {"common.description", "fill", 4}, "debit": {"common.debit", "fill", 5}, "credit": {"common.credit", "fill", 6},
	}},
}

// Grid returns the line grid of the operation's form, if it has one.
func (o *Operation) Grid() *LineGrid {
	if g, ok := lineGrids[o.ID]; ok {
		return &g
	}
	return nil
}

// Discriminator is the field whose value selects which button opens the form (kind=bank …).
func (o *Operation) Discriminator() string {
	if o.UI != nil {
		for _, c := range o.UI.Open {
			if c.Field != "" {
				return c.Field
			}
		}
	}
	return ""
}

// ListOp is the GET operation that lists the records this operation creates.
func (c *Catalog) ListOp(o *Operation) *Operation {
	for i := range c.Operations {
		x := &c.Operations[i]
		if x.Method == "GET" && x.Path == o.Path && x.Kind != "export" {
			return x
		}
	}
	return nil
}

// SideOf is the :side segment (sales | purchases) of a document route for the operation.
func SideOf(o *Operation) string {
	if i := strings.Index(o.ID, "."); i > 0 {
		return o.ID[:i]
	}
	return ""
}
