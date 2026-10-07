package pdf

import (
	"bytes"
	"html/template"
)

// bodyTemplate lays out every Document the same way. The letterhead header and
// footer are added around it by the renderer; in "first page only" mode the
// header is the first element of the body.
var bodyTemplate = template.Must(template.New("body").Parse(`<!doctype html>
<html lang="{{.Doc.Lang}}" dir="{{.Dir}}">
<head><meta charset="utf-8"><title>{{.Doc.Title}}</title>
<style>
@page { size: {{.PageSize}}; }
*{box-sizing:border-box;-webkit-print-color-adjust:exact;print-color-adjust:exact}
html,body{margin:0;padding:0;background:#fff}
body{font-family:{{.FamilyFA}};font-size:9.5pt;line-height:1.6;color:#0e2a2e}
body.en{font-family:{{.FamilyEN}}}
.first-header{margin:-{{.FirstHeaderPull}} -{{.MarginRight}} {{.FirstHeaderGap}} -{{.MarginLeft}}}
h1{font-size:15pt;margin:0 0 1mm;color:#165a54;font-weight:700}
.subtitle{color:#4e6a6a;margin-bottom:4mm}
.meta{display:grid;grid-template-columns:repeat(3,1fr);gap:1.5mm 6mm;margin:3mm 0 5mm}
.meta div{display:flex;gap:2mm}
.meta .l{color:#4e6a6a}
.meta .v{font-weight:700}
.parties{display:grid;grid-template-columns:1fr 1fr;gap:4mm;margin-bottom:5mm}
.party{border:0.3mm solid #c9d6d4;border-radius:2mm;padding:3mm 4mm;break-inside:avoid}
.party .t{font-size:8pt;color:#4e6a6a}
.party .n{font-weight:700;font-size:11pt;margin-bottom:1mm}
.party .ls{display:grid;grid-template-columns:1fr 1fr;gap:0.5mm 4mm;font-size:8.5pt}
p.par{margin:0 0 3mm;text-align:justify}
.tt{font-weight:700;margin:4mm 0 2mm;color:#165a54}
table{width:100%;border-collapse:collapse;font-size:8.6pt;margin-bottom:4mm}
thead{display:table-header-group}
tfoot{display:table-row-group}
tr{break-inside:avoid}
th{background:#e9f1ef;color:#0e3338;font-weight:700;padding:1.8mm 1.6mm;border:0.25mm solid #c9d6d4;text-align:start}
td{padding:1.4mm 1.6mm;border:0.25mm solid #dbe4e2;vertical-align:top}
.a-end{text-align:end;font-variant-numeric:tabular-nums;white-space:nowrap}
.a-center{text-align:center}
tr.group td{background:#f1f6f5;font-weight:700}
tr.subtotal td{font-weight:700}
tr.total td,tfoot td{font-weight:700;background:#e9f1ef;border-top:0.4mm solid #165a54}
tr.muted td{color:#6b8583}
.totals{display:grid;grid-template-columns:auto minmax(38mm,auto);gap:1.2mm 8mm;justify-content:end;margin:2mm 0 5mm;break-inside:avoid}
.totals .l{color:#4e6a6a}
.totals .v{text-align:end;font-variant-numeric:tabular-nums}
.totals .strong{font-weight:700;font-size:11pt;color:#0e2a2e;border-top:0.3mm solid #165a54;padding-top:1.5mm}
.notes{border-inline-start:0.8mm solid #91da73;background:#f4f9f6;padding:2.5mm 4mm;margin:2mm 0 5mm;white-space:pre-wrap;break-inside:avoid}
.signs{display:flex;gap:12mm;margin-top:14mm;break-inside:avoid}
.signs div{flex:1;border-top:0.3mm dashed #88a09d;padding-top:2mm;text-align:center;color:#4e6a6a}
</style></head>
<body class="{{.Doc.Lang}}">
{{if .FirstHeader}}<div class="first-header">{{.FirstHeader}}</div>{{end}}
{{if .Doc.Title}}<h1>{{.Doc.Title}}</h1>{{end}}
{{if .Doc.Subtitle}}<div class="subtitle">{{.Doc.Subtitle}}</div>{{end}}
{{if .Doc.Meta}}<div class="meta">{{range .Doc.Meta}}<div><span class="l">{{.Label}}</span><span class="v">{{.Value}}</span></div>{{end}}</div>{{end}}
{{if .Doc.Parties}}<div class="parties">{{range .Doc.Parties}}<div class="party"><div class="t">{{.Title}}</div><div class="n">{{.Name}}</div>
<div class="ls">{{range .Lines}}<span>{{.Label}}: <b>{{.Value}}</b></span>{{end}}</div></div>{{end}}</div>{{end}}
{{range .Doc.Paragraphs}}<p class="par">{{.}}</p>{{end}}
{{range .Doc.Tables}}{{$cols := .Columns}}
{{if .Title}}<div class="tt">{{.Title}}</div>{{end}}
<table><thead><tr>{{range .Columns}}<th class="a-{{.Align}}">{{.Label}}</th>{{end}}</tr></thead>
<tbody>{{range .Rows}}<tr class="{{.Style}}">{{range $i, $c := .Cells}}<td class="a-{{(index $cols $i).Align}}">{{$c}}</td>{{end}}</tr>{{end}}</tbody>
{{if .Footer}}<tfoot><tr>{{range $i, $c := .Footer}}<td class="a-{{(index $cols $i).Align}}">{{$c}}</td>{{end}}</tr></tfoot>{{end}}
</table>{{end}}
{{if .Doc.Totals}}<div class="totals">{{range .Doc.Totals}}<span class="l{{if .Strong}} strong{{end}}">{{.Label}}</span><span class="v{{if .Strong}} strong{{end}}">{{.Value}}</span>{{end}}</div>{{end}}
{{if .Doc.Notes}}<div class="notes">{{.Doc.Notes}}</div>{{end}}
{{if .Doc.Signatures}}<div class="signs">{{range .Doc.Signatures}}<div>{{.}}</div>{{end}}</div>{{end}}
</body></html>`))

func bodyHTML(l layout, doc Document, c Context) (string, error) {
	dir := "rtl"
	if doc.Lang == "en" {
		dir = "ltr"
	}
	data := map[string]any{
		"Doc":             doc,
		"Dir":             dir,
		"FamilyFA":        template.CSS(familyPersianDigits),
		"FamilyEN":        template.CSS(familyLatinDigits),
		"PageSize":        template.CSS(pageSizeCSS(l)),
		"MarginLeft":      template.CSS(mm(l.S.Page.MarginLeftMM)),
		"MarginRight":     template.CSS(mm(l.S.Page.MarginRightMM)),
		"FirstHeaderPull": template.CSS(mm(l.S.Page.MarginTopMM)),
		"FirstHeaderGap":  template.CSS(mm(l.S.Page.MarginTopMM)),
	}
	if l.S.Header.Mode == "first" && l.HeaderArea > 0 {
		data["FirstHeader"] = headerHTML(l, c, doc.Lang)
	}
	var b bytes.Buffer
	if err := bodyTemplate.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func pageSizeCSS(l layout) string {
	if l.PageW > l.PageH {
		return "A4 landscape"
	}
	return "A4 portrait"
}
