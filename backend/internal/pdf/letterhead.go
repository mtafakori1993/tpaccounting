package pdf

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

//go:embed assets/*
var assets embed.FS

func dataURI(name, mime string) string {
	b, err := assets.ReadFile("assets/" + name)
	if err != nil {
		panic(err)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

var letterheadLogo = dataURI("letterhead-logo.png", "image/png")

// Brand colours measured from the official letterhead.
const (
	colorBand      = "#021A25"
	colorLabel     = "#B7D8C5"
	colorAccent    = "#91DA73"
	colorDots      = "#4A8A78"
	colorDivider   = "#165A54"
	gradientHeader = "linear-gradient(to right, #165A54, #4D9641 50%, #91DA72)"
	gradientFooter = "linear-gradient(to right, #91DA72, #4D9641 50%, #165A54)"
	headerBarMM    = 1.7
	footerBarMM    = 1.1
)

// Fonts: the renderer installs the embedded Vazir TTFs into Chromium's private
// fontconfig (see fonts.go), so they are real system fonts in the body and in
// the per-page header/footer templates alike. "Vazir FD-WOL" draws Persian digits.
const (
	familyPersianDigits = `'Vazir FD-WOL', Vazir, sans-serif`
	familyLatinDigits   = `Vazir, sans-serif`
)

// Context carries the values that letterhead variables are resolved from.
type Context struct {
	CompanyName         string
	CompanyRegistration string
	CompanyNationalID   string
	CompanyEconomicCode string
	CompanyPhone        string
	CompanyEmail        string
	CompanyWebsite      string
	CompanyAddress      string
	CompanyLogo         string // data URI of the uploaded company logo, if any
	DocumentNumber      string
	DocumentDate        string
	DocumentTitle       string
}

var varPattern = regexp.MustCompile(`\{([a-z_]+)\}`)

// Expand resolves {variables}. Page variables become Chromium placeholders
// that are filled in on every page.
func (c Context) Expand(text string) template.HTML {
	parts := varPattern.Split(text, -1)
	names := varPattern.FindAllStringSubmatch(text, -1)
	var b strings.Builder
	for i, p := range parts {
		b.WriteString(template.HTMLEscapeString(p))
		if i < len(names) {
			b.WriteString(c.value(names[i][1], names[i][0]))
		}
	}
	return template.HTML(b.String())
}

func (c Context) value(name, raw string) string {
	esc := template.HTMLEscapeString
	switch name {
	case "page_number":
		return `<span class="pageNumber"></span>`
	case "total_pages":
		return `<span class="totalPages"></span>`
	case "company_name":
		return esc(c.CompanyName)
	case "company_registration":
		return esc(c.CompanyRegistration)
	case "company_national_id":
		return esc(c.CompanyNationalID)
	case "company_economic_code":
		return esc(c.CompanyEconomicCode)
	case "company_phone":
		return esc(c.CompanyPhone)
	case "company_email":
		return esc(c.CompanyEmail)
	case "company_website":
		return esc(c.CompanyWebsite)
	case "company_address":
		return esc(c.CompanyAddress)
	case "document_number":
		return esc(c.DocumentNumber)
	case "document_date":
		return esc(c.DocumentDate)
	case "document_title":
		return esc(c.DocumentTitle)
	}
	return esc(raw) // unknown variables are shown as typed
}

// Variables lists the supported footer variables (for the settings UI).
var Variables = []string{"company_name", "company_registration", "company_national_id", "company_economic_code", "company_phone",
	"company_email", "company_website", "company_address", "page_number", "total_pages", "document_number", "document_date", "document_title"}

// layout holds the resolved geometry of one rendering, in millimetres.
type layout struct {
	S            Settings
	PageW, PageH float64
	HeaderArea   float64 // height reserved at the top of every page (0 = none)
	FooterArea   float64 // height reserved at the bottom of every page
	ContentTop   float64 // printToPDF top margin
	ContentBot   float64 // printToPDF bottom margin
}

func newLayout(s Settings, orientation string) layout {
	l := layout{S: s, PageW: 210, PageH: 297}
	if orientation == "landscape" {
		l.PageW, l.PageH = 297, 210
	}
	if s.Enabled && s.Header.Enabled {
		l.HeaderArea = s.Header.HeightMM + headerBarMM
	}
	if s.Enabled && s.Footer.Enabled {
		l.FooterArea = s.Footer.HeightMM
		if s.Footer.Separator {
			l.FooterArea += footerBarMM
		}
	}
	l.ContentTop = s.Page.MarginTopMM
	if s.Header.Mode == "every" {
		l.ContentTop += l.HeaderArea
	}
	l.ContentBot = s.Page.MarginBottom + l.FooterArea
	return l
}

func mm(v float64) string { return fmt.Sprintf("%.2fmm", v) }

const templateBase = `<style>
html,body{margin:0;padding:0}
*{box-sizing:border-box;-webkit-print-color-adjust:exact;print-color-adjust:exact}
</style>`

// headerHTML renders the letterhead band. It is used both as Chromium's
// per-page header template and, in "first page only" mode, inside the body.
func headerHTML(l layout, c Context, lang string) template.HTML {
	s := l.S.Header
	logo := letterheadLogo
	if s.LogoSource == "company" && c.CompanyLogo != "" {
		logo = c.CompanyLogo
	}
	title := s.Title
	if title == "" {
		title = c.CompanyName
	}
	logoSide, fieldSide := "right", "left"
	if s.LogoPosition == "left" {
		logoSide, fieldSide = "left", "right"
	}
	// The logo occupies its side; texts sit next to it; the fields box sits on the other side.
	logoInset := 17.7
	textInset := logoInset + s.LogoWidthMM + 8.4
	labels := map[string][3]string{"fa": {"شماره:", "تاریخ:", "پیوست:"}, "en": {"No.:", "Date:", "Attachment:"}}[lang]
	values := [3]string{c.DocumentNumber, c.DocumentDate, ""}
	var fields strings.Builder
	if s.ShowFields {
		fmt.Fprintf(&fields, `<div class="lh-fields" style="%s:16.1mm;top:%s">`, fieldSide, mm(s.TopMarginMM-0.6))
		for i, lbl := range labels {
			fmt.Fprintf(&fields, `<div class="lh-field"><span class="lh-label">%s</span><span class="lh-dots">%s</span></div>`,
				template.HTMLEscapeString(lbl), template.HTMLEscapeString(values[i]))
		}
		fields.WriteString(`</div>`)
	}
	dir := "rtl"
	if lang == "en" {
		dir = "ltr"
	}
	html := fmt.Sprintf(`<div class="lh-header" dir="%[1]s" style="width:%[2]s;height:%[3]s">
<style>
.lh-header{position:relative;font-family:%[22]s;background:%[4]s;overflow:hidden}
.lh-header .lh-bar{position:absolute;left:0;right:0;bottom:0;height:%[5]s;background:%[6]s}
.lh-header .lh-logo{position:absolute;top:%[7]s;%[8]s:%[9]s;width:%[10]s}
.lh-header .lh-texts{position:absolute;top:%[11]s;%[8]s:%[12]s;text-align:%[8]s}
.lh-header .lh-title{color:#fff;font-weight:700;font-size:12.3pt;line-height:1.35;white-space:nowrap}
.lh-header .lh-sub{color:%[13]s;font-weight:700;font-size:7.2pt;letter-spacing:0.32em;margin-top:2.2mm;direction:ltr;unicode-bidi:isolate}
.lh-header .lh-fields{position:absolute;width:45.7mm;height:26.6mm;border-%[14]s:0.3mm solid %[15]s;padding:2.6mm 0 0;padding-%[14]s:6.9mm;display:flex;flex-direction:column;gap:2.75mm}
.lh-header .lh-field{display:flex;align-items:flex-end;gap:2.4mm;height:4.9mm;font-size:8.6pt;font-weight:700;color:%[16]s}
.lh-header .lh-label{white-space:nowrap;flex:none}
.lh-header .lh-dots{flex:1;min-width:0;border-bottom:0.35mm dotted %[17]s;color:#fff;min-height:4.4mm;font-size:8.6pt;padding:0 1mm;white-space:nowrap;overflow:hidden}
</style>
<img class="lh-logo" src="%[18]s" alt="">
<div class="lh-texts"><div class="lh-title">%[19]s</div><div class="lh-sub">%[20]s</div></div>
%[21]s
<div class="lh-bar"></div>
</div>`,
		dir, mm(l.PageW), mm(l.HeaderArea), colorBand, mm(headerBarMM), gradientHeader,
		mm(s.TopMarginMM-0.6), logoSide, mm(logoInset), mm(s.LogoWidthMM),
		mm(s.TopMarginMM+6.4), mm(textInset), colorAccent,
		fieldSide, colorDivider, colorLabel, colorDots,
		logo, template.HTMLEscapeString(title), template.HTMLEscapeString(s.Subtitle), fields.String(), template.CSS(familyPersianDigits))
	return template.HTML(html)
}

// footerHTML renders the editable footer band (Chromium per-page footer template).
func footerHTML(l layout, c Context, lang string) template.HTML {
	f := l.S.Footer
	family := familyLatinDigits
	if f.Font == "vazir-fa-digits" {
		family = familyPersianDigits
	}
	var items []string
	for _, part := range strings.Split(f.Text, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// "Label: value" — the label in the footer colour, the value highlighted.
		if i := strings.Index(part, ":"); i > 0 && i < len(part)-1 {
			items = append(items, fmt.Sprintf(`<span class="lf-item"><span>%s</span> <b>%s</b></span>`,
				c.Expand(part[:i+1]), c.Expand(strings.TrimSpace(part[i+1:]))))
		} else {
			items = append(items, fmt.Sprintf(`<span class="lf-item">%s</span>`, c.Expand(part)))
		}
	}
	justify := map[string]string{"center": "center", "right": "flex-end", "left": "flex-start"}[f.Align]
	dir := "rtl"
	if lang == "en" {
		dir = "ltr"
		// In LTR the start is on the left; keep the visual meaning of right/left.
	}
	pageNo := ""
	if f.PageNumbers && f.PageNumberTx != "" {
		side := "left"
		if f.Align == "left" {
			side = "right"
		}
		pageNo = fmt.Sprintf(`<span class="lf-page" style="%s:16mm">%s</span>`, side, c.Expand(f.PageNumberTx))
	}
	bar := ""
	if f.Separator {
		bar = fmt.Sprintf(`<div style="height:%s;background:%s"></div>`, mm(footerBarMM), gradientFooter)
	}
	html := fmt.Sprintf(`<div class="lh-footer" style="width:%[1]s">
<style>
.lh-footer{font-family:%[2]s}
.lh-footer .lf-band{position:relative;height:%[3]s;background:%[4]s;display:flex;align-items:center;justify-content:%[5]s;padding:0 16mm %[6]s;direction:ltr}
.lh-footer .lf-items{display:flex;align-items:center;gap:0;direction:%[7]s;font-size:%.1[8]fpt;font-weight:700;color:%[9]s}
.lh-footer .lf-items b{color:%[10]s}
.lh-footer .lf-item+.lf-item::before{content:"";display:inline-block;width:1.7mm;height:1.7mm;border-radius:50%%;background:%[11]s;margin:0 12.7mm;vertical-align:middle}
.lh-footer .lf-page,.lh-footer .lf-page span{font-family:%[2]s !important}
.lh-footer .lf-page{position:absolute;top:50%%;transform:translateY(-50%%);font-size:%.1[12]fpt;color:%[9]s;direction:%[7]s}
</style>
%[13]s<div class="lf-band"><div class="lf-items" dir="%[7]s">%[14]s</div>%[15]s</div>
</div>`,
		mm(l.PageW), family, mm(f.HeightMM), colorBand, justify, mm(maxF(0, f.OffsetMM)), dir, f.SizePt, f.Color, f.ValueColor, colorAccent,
		f.SizePt-1.5, bar, strings.Join(items, ""), pageNo)
	return template.HTML(html)
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// pageTemplate wraps a header or footer for Chromium's per-page templates.
// Chromium centres template content in the margin box; pinning it to the page
// edge keeps the letterhead geometry exact.
func pageTemplate(inner template.HTML, edge string) string {
	var b bytes.Buffer
	_ = template.Must(template.New("t").Parse(templateBase)).Execute(&b, nil)
	return b.String() + `<div style="position:fixed;` + edge + `:0;left:0;right:0;margin:0;padding:0">` + string(inner) + `</div>`
}
