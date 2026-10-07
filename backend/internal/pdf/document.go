package pdf

import (
	"fmt"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
)

// Document is the content of any printable document. It is plain data — never
// HTML — so every PDF is laid out by the same templates and styles.
type Document struct {
	Title       string   `json:"title"`
	Subtitle    string   `json:"subtitle"`
	Number      string   `json:"number"`      // shown in the letterhead "شماره" field
	Date        string   `json:"date"`        // shown in the letterhead "تاریخ" field (already formatted)
	Attachment  string   `json:"attachment"`  // shown in the letterhead "پیوست" field
	Lang        string   `json:"lang"`        // fa | en
	Orientation string   `json:"orientation"` // optional override: portrait | landscape
	Purpose     string   `json:"purpose"`     // export (default) | print
	Meta        []KV     `json:"meta"`
	Parties     []Party  `json:"parties"`
	Paragraphs  []string `json:"paragraphs"` // letter / contract body text
	Tables      []Table  `json:"tables"`
	Totals      []Total  `json:"totals"`
	Notes       string   `json:"notes"`
	Signatures  []string `json:"signatures"`
}

type KV struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type Party struct {
	Title string `json:"title"`
	Name  string `json:"name"`
	Lines []KV   `json:"lines"`
}

type Column struct {
	Label string `json:"label"`
	Align string `json:"align"` // start | end | center
}

type Row struct {
	Cells []string `json:"cells"`
	Style string   `json:"style"` // "" | group | subtotal | total | muted
}

type Table struct {
	Title   string   `json:"title"`
	Columns []Column `json:"columns"`
	Rows    []Row    `json:"rows"`
	Footer  []string `json:"footer"`
}

type Total struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Strong bool   `json:"strong"`
}

const (
	maxRows    = 50000
	maxColumns = 30
	maxText    = 20000
)

// Validate bounds the document so a request cannot exhaust the renderer.
func (d *Document) Validate() error {
	v := apperr.NewValidator()
	if d.Lang != "en" {
		d.Lang = "fa"
	}
	v.Check(d.Purpose == "" || d.Purpose == "export" || d.Purpose == "print", "purpose", "invalid")
	v.Check(d.Orientation == "" || d.Orientation == "portrait" || d.Orientation == "landscape", "orientation", "invalid")
	v.MaxLen("title", d.Title, 300)
	v.MaxLen("notes", d.Notes, maxText)
	total := 0
	for i, t := range d.Tables {
		v.Check(len(t.Columns) > 0 && len(t.Columns) <= maxColumns, fmt.Sprintf("tables.%d.columns", i), "invalid")
		total += len(t.Rows)
		for j, r := range t.Rows {
			v.Check(len(r.Cells) <= len(t.Columns), fmt.Sprintf("tables.%d.rows.%d", i, j), "invalid")
		}
	}
	v.Check(total <= maxRows, "tables", "too_many")
	v.Check(len(d.Paragraphs) <= 500, "paragraphs", "too_many")
	for i, p := range d.Paragraphs {
		v.MaxLen(fmt.Sprintf("paragraphs.%d", i), p, maxText)
	}
	return v.Err()
}
