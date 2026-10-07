package pdf

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
)

func TestDefaultsAreValidAndMatchReference(t *testing.T) {
	s := Defaults()
	if err := s.Normalize(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if !s.Enabled || !s.Header.Enabled || !s.Footer.Enabled {
		t.Fatal("letterhead must be enabled by default")
	}
	if !strings.Contains(s.Footer.Text, "شماره ثبت: ۸۲۱") || !strings.Contains(s.Footer.Text, "۱۴۰۱۵۵۸۹۰۳۸") {
		t.Fatalf("default footer must come from the reference letterhead: %q", s.Footer.Text)
	}
	if s.Page.Size != "A4" || s.Header.HeightMM != 43.9 {
		t.Fatalf("unexpected page defaults: %+v %+v", s.Page, s.Header)
	}
}

func TestNormalizeRejectsBadValues(t *testing.T) {
	s := Defaults()
	s.Footer.Color = "green"
	s.Footer.Align = "justify"
	s.Footer.SizePt = 40
	s.Page.Orientation = "diagonal"
	err := s.Normalize()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("expected validation error, got %v", err)
	}
	for _, f := range []string{"footer.color", "footer.align", "footer.size_pt", "page.orientation"} {
		if e.Fields[f] == "" {
			t.Errorf("expected error on %s: %v", f, e.Fields)
		}
	}
}

func TestStoredSettingsMergeOverDefaults(t *testing.T) {
	// Older stored JSON without newer keys must keep defaults for them.
	s := Defaults()
	if err := json.Unmarshal([]byte(`{"footer":{"text":"x","enabled":false}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Footer.Text != "x" || s.Footer.Enabled || s.Footer.SizePt != Defaults().Footer.SizePt || !s.Header.Enabled {
		t.Fatalf("merge failed: %+v", s.Footer)
	}
}

func TestFooterVariables(t *testing.T) {
	c := Context{CompanyName: "تفکر <پردازان>", CompanyRegistration: "821", DocumentNumber: "INV-7", DocumentDate: "1405/07/15"}
	got := string(c.Expand("{company_name} | ثبت: {company_registration} | {document_number} {document_date} | صفحه {page_number} از {total_pages} | {unknown}"))
	for _, want := range []string{"تفکر &lt;پردازان&gt;", "ثبت: 821", "INV-7 1405/07/15", `<span class="pageNumber"></span>`, `<span class="totalPages"></span>`, "{unknown}"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestComposeUsesOneLetterheadForEveryDocument(t *testing.T) {
	s := Defaults()
	for _, doc := range representativeDocuments() {
		job := Compose(s, doc, Context{CompanyName: "تفکر پردازان"})
		if !strings.Contains(job.Header, s.Header.Title) || !strings.Contains(job.Header, "lh-header") {
			t.Errorf("%s: header missing", doc.Title)
		}
		if !strings.Contains(job.Footer, "شماره ثبت:") || !strings.Contains(job.Footer, "pageNumber") {
			t.Errorf("%s: footer missing", doc.Title)
		}
		if job.WidthMM != 210 || job.HeightMM != 297 {
			t.Errorf("%s: not A4 portrait", doc.Title)
		}
		// Content never overlaps the header or footer bands.
		if job.MarginTop < s.Header.HeightMM || job.MarginBottom < s.Footer.HeightMM {
			t.Errorf("%s: content area overlaps letterhead: top %.1f bottom %.1f", doc.Title, job.MarginTop, job.MarginBottom)
		}
	}
}

func TestComposeOptions(t *testing.T) {
	s := Defaults()
	s.Footer.Enabled = false
	job := Compose(s, Document{Title: "x"}, Context{})
	if job.Footer != "<span></span>" {
		t.Fatal("disabled footer must not render")
	}
	s = Defaults()
	s.Header.Mode = "first"
	job = Compose(s, Document{Title: "x"}, Context{})
	if job.Header != "<span></span>" || !strings.Contains(job.HTML, "lh-header") {
		t.Fatal("first-page header must be part of the body, not the page template")
	}
	job = Compose(Defaults(), Document{Title: "x", Orientation: "landscape"}, Context{})
	if job.WidthMM != 297 || job.HeightMM != 210 {
		t.Fatal("landscape must swap the A4 dimensions")
	}
	s = Defaults()
	s.Enabled = false
	job = Compose(s, Document{Title: "x"}, Context{})
	if job.Header != "<span></span>" || job.Footer != "<span></span>" {
		t.Fatal("disabled letterhead must not draw header or footer")
	}
}

func TestDocumentContentIsEscaped(t *testing.T) {
	job := Compose(Defaults(), Document{Title: `<script>alert(1)</script>`, Tables: []Table{{Columns: []Column{{"a", "start"}}, Rows: []Row{{Cells: []string{`<img src=x onerror=1>`}}}}}}, Context{})
	if strings.Contains(job.HTML, "<script>alert") || strings.Contains(job.HTML, "<img src=x") {
		t.Fatal("document content must be HTML-escaped")
	}
}

// representativeDocuments mirrors the documents the application prints.
func representativeDocuments() []Document {
	table := func(cols ...string) Table {
		t := Table{}
		for i, c := range cols {
			a := "start"
			if i > 0 {
				a = "end"
			}
			t.Columns = append(t.Columns, Column{Label: c, Align: a})
		}
		return t
	}
	inv := table("کالا", "تعداد", "فی", "جمع")
	inv.Rows = []Row{{Cells: []string{"لپ‌تاپ ایسوس", "۲", "۴۶۰٬۰۰۰٬۰۰۰", "۹۲۰٬۰۰۰٬۰۰۰"}}}
	rcp := table("فاکتور", "مبلغ")
	rcp.Rows = []Row{{Cells: []string{"فاکتور فروش ۱۲", "۵۰٬۰۰۰٬۰۰۰"}}}
	tb := table("حساب", "بدهکار", "بستانکار")
	tb.Rows = []Row{{Cells: []string{"بانک‌ها", "۵٬۰۶۰٬۰۰۰٬۰۰۰", "—"}}, {Cells: []string{"جمع", "۱۰٬۱۱۰٬۰۰۰٬۰۰۰", "۱۰٬۱۱۰٬۰۰۰٬۰۰۰"}, Style: "total"}}
	st := table("شرح", "بدهکار", "بستانکار", "مانده")
	st.Rows = []Row{{Cells: []string{"فاکتور فروش ۱", "۱۱۰٬۰۰۰٬۰۰۰", "", "۱۱۰٬۰۰۰٬۰۰۰"}}}
	gen := table("Name", "Amount")
	gen.Rows = []Row{{Cells: []string{"Mixed فارسی English", "1,234"}}}
	return []Document{
		{Title: "فاکتور فروش", Number: "۱۲", Date: "۱۴۰۵/۰۷/۱۵", Parties: []Party{{Title: "فروشنده", Name: "تفکر پردازان"}, {Title: "خریدار", Name: "فروشگاه آریا"}}, Tables: []Table{inv}, Totals: []Total{{"مبلغ کل", "۱٬۰۱۲٬۰۰۰٬۰۰۰", true}}, Signatures: []string{"فروشنده", "خریدار"}},
		{Title: "رسید دریافت", Number: "۴", Tables: []Table{rcp}},
		{Title: "تراز آزمایشی", Tables: []Table{tb}},
		{Title: "صورت‌حساب مشتری", Tables: []Table{st}},
		{Title: "Generic report", Lang: "en", Tables: []Table{gen}},
	}
}
