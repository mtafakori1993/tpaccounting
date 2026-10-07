package sheet

import (
	"bytes"
	"strings"
	"testing"
)

func TestXLSXRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	err := WriteXLSX(&buf, Sheet{Name: "گزارش: چک‌ها", RTL: true, FreezeRow: 1, Rows: [][]Cell{
		{Bold("کد"), Bold("مبلغ")},
		{Text("E001"), Number("1250000")},
		{Text("a & <b>"), Number("12.5")},
		{Text(""), BoldNumber("1250012.5")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ReadXLSX(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0][0] != "کد" || rows[1][1] != "1250000" || rows[2][0] != "a & <b>" || rows[3][1] != "1250012.5" {
		t.Fatalf("round trip: %q", rows)
	}
}

func TestReadCSVDetectsSeparatorAndBOM(t *testing.T) {
	rows, err := ReadCSV([]byte("\ufeffcode;date;hours\r\nE1;1405/07/01;8\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "code" || rows[1][2] != "8" {
		t.Fatalf("rows: %q", rows)
	}
	if _, err := ReadCSV([]byte{0xff, 0xfe, 0x00}); err == nil {
		t.Fatal("binary data accepted as CSV")
	}
}

func TestWriteCSVQuotes(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCSV(&buf, [][]Cell{{Text("a,b"), Number("1")}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"a,b",1`) {
		t.Fatalf("csv: %q", buf.String())
	}
}

func TestSerialDate(t *testing.T) {
	if d := SerialDate(45658).Format("2006-01-02"); d != "2025-01-01" {
		t.Fatalf("serial: %s", d)
	}
}

func TestReadXLSXRejectsGarbage(t *testing.T) {
	if _, err := ReadXLSX([]byte("not a zip")); err == nil {
		t.Fatal("garbage accepted")
	}
}
