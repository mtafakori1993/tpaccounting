package export

import (
	"strings"
	"testing"
	"time"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/sheet"
)

func sample(lang string, plain bool) *Report {
	return &Report{Title: "Checks", Lang: lang, Calendar: "jalali", Company: "Tafakor", GeneratedAt: time.Date(2026, 10, 6, 9, 5, 0, 0, time.UTC), Plain: plain,
		Columns: []Column{{Label: "No", Kind: KindID}, {Label: "Due", Kind: KindDate}, {Label: "Amount", Kind: KindMoney}},
		Rows:    [][]string{{"000123", "2026-10-06", "1250000000"}, {"000124", "2026-10-07", "-1500.5"}},
		Totals:  []KV{{Label: "Total", Value: "1249998499.5"}}, Filters: []KV{{Label: "Type", Value: "Received"}}}
}

func TestMoneyAndDatesPerLanguage(t *testing.T) {
	fa, en := sample("fa", false), sample("en", false)
	if got := en.Money("1250000000"); got != "1,250,000,000" {
		t.Errorf("en money: %s", got)
	}
	if got := en.Money("-1500.5"); got != "-1,500.5" {
		t.Errorf("en negative: %s", got)
	}
	if got := fa.Money("1250000000"); got != "۱٬۲۵۰٬۰۰۰٬۰۰۰" {
		t.Errorf("fa money: %s", got)
	}
	if got := fa.Date("2026-10-06"); got != "۱۴۰۵/۰۷/۱۴" {
		t.Errorf("fa date: %s", got)
	}
	if got := en.Date("2026-10-06"); got != "1405/07/14" {
		t.Errorf("en date (company calendar): %s", got)
	}
}

func TestPDFFormatsButSpreadsheetsKeepRawNumbers(t *testing.T) {
	r := sample("fa", false)
	doc := r.PDF()
	cells := doc.Tables[0].Rows[0].Cells
	// Identifiers keep their leading zeros and are never grouped or localised.
	if cells[0] != "000123" || cells[2] != "۱٬۲۵۰٬۰۰۰٬۰۰۰" {
		t.Errorf("pdf cells: %q", cells)
	}
	if doc.Totals[len(doc.Totals)-1].Value != "۲" {
		t.Errorf("record count: %v", doc.Totals)
	}
	b, err := r.XLSX()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := sheet.ReadXLSX(b)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range rows {
		if len(row) == 3 && row[0] == "000123" {
			found = row[2] == "1250000000"
		}
	}
	if !found {
		t.Fatalf("excel must hold the raw amount as a number: %q", rows)
	}
	csv, _ := r.CSV()
	for _, want := range []string{"Tafakor", "Checks", "Type,Received", "000123,۱۴۰۵/۰۷/۱۴,1250000000", "Total,1249998499.5"} {
		if !strings.Contains(string(csv), want) {
			t.Errorf("csv lacks %q:\n%s", want, csv)
		}
	}
}

func TestPlainLayoutHasOnlyHeaderAndRows(t *testing.T) {
	csv, _ := sample("en", true).CSV()
	lines := strings.Split(strings.TrimSpace(strings.TrimPrefix(string(csv), "\ufeff")), "\r\n")
	if len(lines) != 3 || lines[0] != "No,Due,Amount" {
		t.Fatalf("plain csv: %q", lines)
	}
}
