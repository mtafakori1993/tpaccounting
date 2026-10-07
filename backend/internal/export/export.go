// Package export renders tabular reports on the server, so exports always
// contain the complete filtered dataset (never just the page on screen).
// Every export carries the company name, report title, generation time, the
// filters applied, the rows, totals and the record count.
package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/sheet"
)

// Kind tells how a column's raw values are formatted.
type Kind int

const (
	KindText  Kind = iota
	KindMoney      // decimal amount: numeric cell in Excel, grouped in PDF
	KindInt        // integer: numeric cell in Excel
	KindDate       // ISO date: shown in the company calendar
	KindID         // identifier made of digits (IBAN, account number): always text, never localised
)

type Column struct {
	Label string
	Kind  Kind
}

type KV struct {
	Label string
	Value string
}

// Report is a complete tabular report. Row values are raw (ISO dates, plain
// decimals); Format turns them into display text for the chosen language.
type Report struct {
	Name     string // file name without extension
	Title    string
	Subtitle string
	Lang     string // fa | en
	Columns  []Column
	Rows     [][]string
	Totals   []KV // already-formatted label + raw money value
	Filters  []KV // already-formatted label + display value
	// Plain writes only the header row and data rows (no title block, totals
	// or counts) — for files another system imports, such as bank transfers.
	Plain bool
	// Set by Prepare.
	Company     string
	Calendar    string
	GeneratedAt time.Time
}

// Prepare loads the company name and calendar used in the export header.
func (r *Report) Prepare(ctx context.Context, q db.DBTX, companyID int64) error {
	var name, nameEn string
	if err := q.QueryRow(ctx, `SELECT name, name_en, calendar FROM companies WHERE id = $1`, companyID).Scan(&name, &nameEn, &r.Calendar); err != nil {
		return err
	}
	r.Company = name
	if r.Lang == "en" && nameEn != "" {
		r.Company = nameEn
	}
	if r.Lang != "en" {
		r.Lang = "fa"
	}
	r.GeneratedAt = time.Now()
	return nil
}

var persianDigits = strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹")

// Digits localises digits for Persian output.
func (r *Report) Digits(s string) string {
	if r.Lang == "fa" {
		return persianDigits.Replace(s)
	}
	return s
}

// Date formats an ISO date in the company calendar (YYYY/MM/DD).
func (r *Report) Date(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", iso[:min(10, len(iso))])
	if err != nil {
		return iso
	}
	if r.Calendar == "jalali" {
		y, m, d := jalali.FromGregorian(t)
		return r.Digits(fmt.Sprintf("%04d/%02d/%02d", y, m, d))
	}
	return r.Digits(t.Format("2006/01/02"))
}

// CivilDate formats a civil date.
func (r *Report) CivilDate(d civil.Date) string {
	if d.IsZero() {
		return ""
	}
	return r.Date(d.String())
}

// DateTime formats the generation timestamp.
func (r *Report) DateTime(t time.Time) string {
	return r.Date(t.Format("2006-01-02")) + " " + r.Digits(t.Format("15:04"))
}

// Money groups a decimal amount by thousands.
func (r *Report) Money(s string) string {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return s
	}
	neg := d.IsNegative()
	str := d.Abs().String()
	intPart, frac := str, ""
	if i := strings.IndexByte(str, '.'); i >= 0 {
		intPart, frac = str[:i], str[i:]
	}
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String() + frac
	if neg {
		out = "-" + out
	}
	if r.Lang == "fa" {
		out = strings.NewReplacer(",", "٬", ".", "٫").Replace(out)
	}
	return r.Digits(out)
}

func (r *Report) display(c Column, v string) string {
	switch c.Kind {
	case KindMoney:
		return r.Money(v)
	case KindInt:
		return r.Digits(v)
	case KindDate:
		return r.Date(v)
	case KindID:
		return v
	}
	return r.Digits(v)
}

func (r *Report) t(fa, en string) string {
	if r.Lang == "en" {
		return en
	}
	return fa
}

// header rows shared by CSV and XLSX.
func (r *Report) headerRows() [][]sheet.Cell {
	rows := [][]sheet.Cell{
		{sheet.Bold(r.Company)},
		{sheet.Bold(r.Title)},
	}
	if r.Subtitle != "" {
		rows = append(rows, []sheet.Cell{sheet.Text(r.Subtitle)})
	}
	rows = append(rows, []sheet.Cell{sheet.Text(r.t("تاریخ تهیه", "Generated")), sheet.Text(r.DateTime(r.GeneratedAt))})
	for _, f := range r.Filters {
		rows = append(rows, []sheet.Cell{sheet.Text(f.Label), sheet.Text(f.Value)})
	}
	if len(r.Filters) == 0 {
		rows = append(rows, []sheet.Cell{sheet.Text(r.t("فیلترها", "Filters")), sheet.Text(r.t("بدون فیلتر", "None"))})
	}
	return append(rows, nil)
}

func (r *Report) cells() (rows [][]sheet.Cell, headerRow int) {
	if !r.Plain {
		rows = r.headerRows()
	}
	header := make([]sheet.Cell, len(r.Columns))
	for i, c := range r.Columns {
		header[i] = sheet.Bold(c.Label)
	}
	rows = append(rows, header)
	headerRow = len(rows)
	for _, row := range r.Rows {
		out := make([]sheet.Cell, len(r.Columns))
		for i, c := range r.Columns {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			switch c.Kind {
			case KindMoney, KindInt:
				out[i] = sheet.Number(v)
			case KindDate:
				out[i] = sheet.Text(r.Date(v))
			default:
				out[i] = sheet.Text(v)
			}
		}
		rows = append(rows, out)
	}
	if r.Plain {
		return rows, headerRow
	}
	rows = append(rows, nil)
	for _, t := range r.Totals {
		rows = append(rows, []sheet.Cell{sheet.Bold(t.Label), sheet.BoldNumber(t.Value)})
	}
	rows = append(rows, []sheet.Cell{sheet.Bold(r.t("تعداد رکورد", "Record count")), sheet.BoldNumber(fmt.Sprint(len(r.Rows)))})
	return rows, headerRow
}

func (r *Report) CSV() ([]byte, error) {
	var buf bytes.Buffer
	rows, _ := r.cells()
	err := sheet.WriteCSV(&buf, rows)
	return buf.Bytes(), err
}

func (r *Report) XLSX() ([]byte, error) {
	var buf bytes.Buffer
	rows, header := r.cells()
	widths := make([]float64, len(r.Columns))
	for i, c := range r.Columns {
		widths[i] = 24
		if c.Kind == KindMoney || c.Kind == KindInt || c.Kind == KindDate {
			widths[i] = 16
		}
		if c.Kind == KindID {
			widths[i] = 30
		}
	}
	err := sheet.WriteXLSX(&buf, sheet.Sheet{Name: r.Title, RTL: r.Lang == "fa", Rows: rows, ColWidths: widths, FreezeRow: header})
	return buf.Bytes(), err
}

// PDF builds the letterhead document for the report.
func (r *Report) PDF() pdf.Document {
	doc := pdf.Document{Title: r.Title, Subtitle: r.Subtitle, Lang: r.Lang, Date: r.Date(r.GeneratedAt.Format("2006-01-02")), Purpose: "export"}
	if len(r.Columns) > 7 {
		doc.Orientation = "landscape"
	}
	doc.Meta = append(doc.Meta, pdf.KV{Label: r.t("تاریخ تهیه", "Generated"), Value: r.DateTime(r.GeneratedAt)})
	for _, f := range r.Filters {
		doc.Meta = append(doc.Meta, pdf.KV{Label: f.Label, Value: f.Value})
	}
	t := pdf.Table{}
	for _, c := range r.Columns {
		align := "start"
		if c.Kind == KindMoney || c.Kind == KindInt {
			align = "end"
		}
		t.Columns = append(t.Columns, pdf.Column{Label: c.Label, Align: align})
	}
	for _, row := range r.Rows {
		cells := make([]string, len(r.Columns))
		for i, c := range r.Columns {
			if i < len(row) {
				cells[i] = r.display(c, row[i])
			}
		}
		t.Rows = append(t.Rows, pdf.Row{Cells: cells})
	}
	doc.Tables = []pdf.Table{t}
	for _, tot := range r.Totals {
		doc.Totals = append(doc.Totals, pdf.Total{Label: tot.Label, Value: r.Money(tot.Value)})
	}
	doc.Totals = append(doc.Totals, pdf.Total{Label: r.t("تعداد رکورد", "Record count"), Value: r.Digits(fmt.Sprint(len(r.Rows))), Strong: true})
	return doc
}

// MaxRows bounds a single export.
const MaxRows = 50000

// Write sends the report in the requested format (csv, xlsx or pdf).
func Write(ctx context.Context, w http.ResponseWriter, pdfs *pdf.Service, companyID int64, r *Report, format string) error {
	if len(r.Rows) > MaxRows {
		return apperr.Rule("export_too_large", "too many rows to export; narrow the filters").WithParams("max", fmt.Sprint(MaxRows))
	}
	var (
		body []byte
		err  error
		ct   string
		ext  = format
	)
	switch format {
	case "csv":
		body, err = r.CSV()
		ct = "text/csv; charset=utf-8"
	case "xlsx":
		body, err = r.XLSX()
		ct = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "pdf":
		if pdfs == nil {
			return apperr.New(http.StatusServiceUnavailable, "pdf_unavailable", "the PDF renderer is not installed")
		}
		body, err = pdfs.Render(ctx, companyID, r.PDF(), nil)
		if errors.Is(err, pdf.ErrUnavailable) {
			return apperr.New(http.StatusServiceUnavailable, "pdf_unavailable", "the PDF renderer is not installed")
		}
		ct = "application/pdf"
	default:
		v := apperr.NewValidator()
		v.Check(false, "format", "invalid")
		return v.Err()
	}
	if err != nil {
		return err
	}
	name := r.Name
	if name == "" {
		name = "report"
	}
	w.Header().Set("Content-Type", ct)
	disposition := "attachment"
	if format == "pdf" {
		disposition = "inline"
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s.%s"; filename*=UTF-8''%s.%s`, disposition, "export", ext, url.PathEscape(name), ext))
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	_, err = w.Write(body)
	return err
}
