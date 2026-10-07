// Package sheet reads and writes simple tabular files: CSV and single-sheet
// XLSX (Office Open XML). It has no dependencies beyond the standard library;
// it supports what exports and imports need (text, numbers, bold rows, RTL)
// and nothing else.
package sheet

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Cell is one value of a row. Numeric cells are written as numbers so
// spreadsheet formulas work on them; Value then holds a plain decimal string.
type Cell struct {
	Value   string
	Numeric bool
	Bold    bool
}

func Text(s string) Cell       { return Cell{Value: s} }
func Bold(s string) Cell       { return Cell{Value: s, Bold: true} }
func Number(s string) Cell     { return Cell{Value: s, Numeric: true} }
func BoldNumber(s string) Cell { return Cell{Value: s, Numeric: true, Bold: true} }

// Sheet is a worksheet to write.
type Sheet struct {
	Name      string
	RTL       bool
	Rows      [][]Cell
	ColWidths []float64
	// FreezeRow freezes the rows above it (0 = none), e.g. the header row.
	FreezeRow int
}

// WriteCSV writes rows as UTF-8 CSV with a BOM so Excel detects the encoding.
func WriteCSV(w io.Writer, rows [][]Cell) error {
	if _, err := w.Write([]byte("\ufeff")); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.UseCRLF = true
	for _, r := range rows {
		rec := make([]string, len(r))
		for i, c := range r {
			rec[i] = c.Value
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func colName(i int) string {
	s := ""
	for i >= 0 {
		s = string(rune('A'+i%26)) + s
		i = i/26 - 1
	}
	return s
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	for _, r := range s {
		// Characters XML 1.0 cannot carry are dropped.
		if r == 0x9 || r == 0xA || r == 0xD || (r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF) {
			_ = xml.EscapeText(&b, []byte(string(r)))
		}
	}
	return b.String()
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>
<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>
</Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
</Relationships>`

const workbookRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>`

// Styles: 0 default, 1 bold, 2 number with grouping, 3 bold number.
const styles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<numFmts count="1"><numFmt numFmtId="164" formatCode="#,##0.##"/></numFmts>
<fonts count="2"><font><sz val="11"/><name val="Tahoma"/></font><font><b/><sz val="11"/><name val="Tahoma"/></font></fonts>
<fills count="2"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill></fills>
<borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="4">
<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
<xf numFmtId="0" fontId="1" fillId="0" borderId="0" xfId="0" applyFont="1"/>
<xf numFmtId="164" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/>
<xf numFmtId="164" fontId="1" fillId="0" borderId="0" xfId="0" applyNumberFormat="1" applyFont="1"/>
</cellXfs>
</styleSheet>`

// WriteXLSX writes a single-sheet workbook.
func WriteXLSX(w io.Writer, s Sheet) error {
	zw := zip.NewWriter(w)
	put := func(name, body string) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, body)
		return err
	}
	name := s.Name
	if name == "" {
		name = "Sheet1"
	}
	// Sheet names are limited to 31 characters and cannot contain []:*?/\.
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`[]:*?/\`, r) {
			return '-'
		}
		return r
	}, name)
	if utf8.RuneCountInString(name) > 31 {
		name = string([]rune(name)[:31])
	}
	workbook := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<sheets><sheet name="` + xmlEscape(name) + `" sheetId="1" r:id="rId1"/></sheets></workbook>`
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetViews><sheetView workbookViewId="0"`)
	if s.RTL {
		b.WriteString(` rightToLeft="1"`)
	}
	if s.FreezeRow > 0 {
		fmt.Fprintf(&b, `><pane ySplit="%d" topLeftCell="A%d" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews>`, s.FreezeRow, s.FreezeRow+1)
	} else {
		b.WriteString(`/></sheetViews>`)
	}
	if len(s.ColWidths) > 0 {
		b.WriteString(`<cols>`)
		for i, wd := range s.ColWidths {
			fmt.Fprintf(&b, `<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, wd)
		}
		b.WriteString(`</cols>`)
	}
	b.WriteString(`<sheetData>`)
	for ri, row := range s.Rows {
		fmt.Fprintf(&b, `<row r="%d">`, ri+1)
		for ci, c := range row {
			ref := colName(ci) + strconv.Itoa(ri+1)
			style := 0
			if c.Bold {
				style = 1
			}
			if c.Numeric && c.Value != "" {
				if _, err := strconv.ParseFloat(c.Value, 64); err == nil {
					fmt.Fprintf(&b, `<c r="%s" s="%d"><v>%s</v></c>`, ref, style+2, c.Value)
					continue
				}
			}
			if c.Value == "" {
				continue
			}
			fmt.Fprintf(&b, `<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, style, xmlEscape(c.Value))
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	for _, p := range [][2]string{
		{"[Content_Types].xml", contentTypes}, {"_rels/.rels", rootRels}, {"xl/workbook.xml", workbook},
		{"xl/_rels/workbook.xml.rels", workbookRels}, {"xl/styles.xml", styles}, {"xl/worksheets/sheet1.xml", b.String()},
	} {
		if err := put(p[0], p[1]); err != nil {
			return err
		}
	}
	return zw.Close()
}

// ErrFormat is returned for files that are not valid CSV/XLSX.
var ErrFormat = fmt.Errorf("unsupported or corrupt file")

// ReadCSV reads every record of a CSV file (comma, semicolon or tab separated).
func ReadCSV(data []byte) ([][]string, error) {
	data = bytes.TrimPrefix(data, []byte("\ufeff"))
	if !utf8.Valid(data) {
		return nil, ErrFormat
	}
	first := data
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		first = data[:i]
	}
	sep := ','
	if bytes.Count(first, []byte(";")) > bytes.Count(first, []byte(",")) {
		sep = ';'
	}
	if bytes.Count(first, []byte("\t")) > bytes.Count(first, []byte(string(sep))) {
		sep = '\t'
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = sep
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, ErrFormat
	}
	return rows, nil
}

type xlsxRels struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

type xlsxWorkbook struct {
	Sheets []struct {
		RID string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
	} `xml:"sheets>sheet"`
}

type xlsxRich struct {
	T string `xml:"t"`
	R []struct {
		T string `xml:"t"`
	} `xml:"r"`
}

func (x xlsxRich) text() string {
	if len(x.R) == 0 {
		return x.T
	}
	var b strings.Builder
	for _, r := range x.R {
		b.WriteString(r.T)
	}
	return b.String()
}

type xlsxSheetData struct {
	Rows []struct {
		R     int `xml:"r,attr"`
		Cells []struct {
			Ref string   `xml:"r,attr"`
			T   string   `xml:"t,attr"`
			S   int      `xml:"s,attr"`
			V   string   `xml:"v"`
			Is  xlsxRich `xml:"is"`
		} `xml:"c"`
	} `xml:"sheetData>row"`
}

type xlsxStyles struct {
	NumFmts []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Xfs []struct {
		NumFmtID int `xml:"numFmtId,attr"`
	} `xml:"cellXfs>xf"`
}

const maxXLSXPart = 64 << 20

func readPart(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if strings.EqualFold(strings.TrimPrefix(f.Name, "/"), name) {
			if f.UncompressedSize64 > maxXLSXPart {
				return nil, ErrFormat
			}
			rc, err := f.Open()
			if err != nil {
				return nil, ErrFormat
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxXLSXPart))
		}
	}
	return nil, nil
}

func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		n = n*26 + int(r-'A'+1)
	}
	return n - 1
}

// isDateFormat reports whether an Excel number format displays a date.
func isDateFormat(id int, code string) bool {
	if (id >= 14 && id <= 22) || (id >= 45 && id <= 47) {
		return true
	}
	c := strings.ToLower(code)
	return strings.Contains(c, "yy") || (strings.Contains(c, "d") && strings.Contains(c, "m"))
}

// ReadXLSX returns the cell texts of the first worksheet. Cells formatted as
// dates are returned as ISO dates (YYYY-MM-DD).
func ReadXLSX(data []byte) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrFormat
	}
	sheetPath := "xl/worksheets/sheet1.xml"
	if wb, _ := readPart(zr, "xl/workbook.xml"); wb != nil {
		var w xlsxWorkbook
		var rels xlsxRels
		relsRaw, _ := readPart(zr, "xl/_rels/workbook.xml.rels")
		if xml.Unmarshal(wb, &w) == nil && len(w.Sheets) > 0 && xml.Unmarshal(relsRaw, &rels) == nil {
			for _, r := range rels.Rels {
				if r.ID == w.Sheets[0].RID {
					t := strings.TrimPrefix(r.Target, "/")
					if !strings.HasPrefix(t, "xl/") {
						t = "xl/" + t
					}
					sheetPath = t
				}
			}
		}
	}
	var shared []string
	if raw, _ := readPart(zr, "xl/sharedStrings.xml"); raw != nil {
		var sst struct {
			SI []xlsxRich `xml:"si"`
		}
		if err := xml.Unmarshal(raw, &sst); err != nil {
			return nil, ErrFormat
		}
		for _, si := range sst.SI {
			shared = append(shared, si.text())
		}
	}
	dateStyle := map[int]bool{}
	if raw, _ := readPart(zr, "xl/styles.xml"); raw != nil {
		var st xlsxStyles
		if xml.Unmarshal(raw, &st) == nil {
			codes := map[int]string{}
			for _, f := range st.NumFmts {
				codes[f.ID] = f.Code
			}
			for i, x := range st.Xfs {
				dateStyle[i] = isDateFormat(x.NumFmtID, codes[x.NumFmtID])
			}
		}
	}
	raw, err := readPart(zr, sheetPath)
	if err != nil || raw == nil {
		return nil, ErrFormat
	}
	var sd xlsxSheetData
	if err := xml.Unmarshal(raw, &sd); err != nil {
		return nil, ErrFormat
	}
	var out [][]string
	for ri, row := range sd.Rows {
		idx := row.R - 1
		if idx < 0 {
			idx = ri
		}
		if idx > 1_000_000 {
			return nil, ErrFormat
		}
		for len(out) <= idx {
			out = append(out, nil)
		}
		var cells []string
		for ci, c := range row.Cells {
			col := ci
			if c.Ref != "" {
				col = colIndex(c.Ref)
			}
			if col < 0 || col > 16384 {
				return nil, ErrFormat
			}
			for len(cells) <= col {
				cells = append(cells, "")
			}
			var v string
			switch c.T {
			case "s":
				i, err := strconv.Atoi(strings.TrimSpace(c.V))
				if err != nil || i < 0 || i >= len(shared) {
					return nil, ErrFormat
				}
				v = shared[i]
			case "inlineStr":
				v = c.Is.text()
			case "b":
				v = map[string]string{"1": "TRUE", "0": "FALSE"}[c.V]
			default:
				v = c.V
				if c.T == "" && dateStyle[c.S] {
					if f, err := strconv.ParseFloat(v, 64); err == nil {
						v = SerialDate(f).Format("2006-01-02")
					}
				}
			}
			cells[col] = v
		}
		out[idx] = cells
	}
	return out, nil
}

// SerialDate converts an Excel serial day number (1900 date system) to a date.
func SerialDate(f float64) time.Time {
	base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	return base.AddDate(0, 0, int(math.Floor(f)))
}
