package pdf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// These tests render real PDFs with Chromium and inspect them with poppler
// (pdfinfo/pdftotext). They are skipped when either tool is missing.

var sharedRenderer *Renderer

func renderer(t *testing.T) *Renderer {
	t.Helper()
	if FindChrome() == "" {
		t.Skip("chromium not installed")
	}
	for _, tool := range []string{"pdfinfo", "pdftotext"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	if sharedRenderer == nil {
		sharedRenderer = NewRenderer(FindChrome())
	}
	return sharedRenderer
}

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedRenderer != nil {
		sharedRenderer.Close()
	}
	os.Exit(code)
}

type renderedPDF struct {
	path  string
	pages int
	w, h  float64
}

func render(t *testing.T, s Settings, doc Document) renderedPDF {
	t.Helper()
	r := renderer(t)
	b, err := r.Print(context.Background(), Compose(s, doc, Context{CompanyName: "تفکر پردازان", DocumentNumber: doc.Number, DocumentDate: doc.Date}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := os.Getenv("PDF_SAMPLES"); out != "" {
		_ = os.WriteFile(filepath.Join(out, strings.ReplaceAll(t.Name(), "/", "_")+".pdf"), b, 0o644)
	}
	info, err := exec.Command("pdfinfo", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	p := renderedPDF{path: path}
	if m := regexp.MustCompile(`Pages:\s+(\d+)`).FindSubmatch(info); m != nil {
		p.pages, _ = strconv.Atoi(string(m[1]))
	}
	if m := regexp.MustCompile(`Page size:\s+([\d.]+) x ([\d.]+) pts`).FindSubmatch(info); m != nil {
		p.w, _ = strconv.ParseFloat(string(m[1]), 64)
		p.h, _ = strconv.ParseFloat(string(m[2]), 64)
	}
	return p
}

var bidiMarks = strings.NewReplacer("‪", "", "‫", "", "‬", "", "‭", "", "‮", "", "‎", "", "‏", "")

// text extracts one page's text (1-based), or all pages when page is 0.
func (p renderedPDF) text(t *testing.T, page int) string {
	t.Helper()
	args := []string{}
	if page > 0 {
		args = append(args, "-f", strconv.Itoa(page), "-l", strconv.Itoa(page))
	}
	out, err := exec.Command("pdftotext", append(args, p.path, "-")...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return bidiMarks.Replace(string(out))
}

func mustContain(t *testing.T, where, text string, words ...string) {
	t.Helper()
	for _, w := range words {
		if !strings.Contains(text, w) {
			t.Errorf("%s: expected %q in extracted text:\n%s", where, w, text)
		}
	}
}

func isA4(p renderedPDF, landscape bool) bool {
	w, h := 595.0, 842.0
	if landscape {
		w, h = h, w
	}
	near := func(a, b float64) bool { return a > b-2 && a < b+2 }
	return near(p.w, w) && near(p.h, h)
}

func TestPDFHasHeaderFooterAndA4(t *testing.T) {
	p := render(t, Defaults(), Document{Title: "سند آزمایشی", Number: "۱۲۳", Date: "۱۴۰۵/۰۷/۱۵", Paragraphs: []string{"متن نمونه"}})
	if p.pages != 1 || !isA4(p, false) {
		t.Fatalf("expected one A4 page, got %d pages %.2fx%.2f", p.pages, p.w, p.h)
	}
	txt := p.text(t, 1)
	mustContain(t, "header", txt, "تفکر", "پردازان", "TAFAKOR", "شماره:", "تاریخ:", "پیوست:", "۱۲۳")
	mustContain(t, "footer", txt, "شماره", "ثبت", "۸۲۱", "شناسه", "۱۴۰۱۵۵۸۹۰۳۸", "صفحه")
}

func TestFooterSettingsAreApplied(t *testing.T) {
	s := Defaults()
	s.Footer.Text = "{company_name} | تلفن: ۰۲۱۸۸۷۷۶۶۵۵ | سند {document_number}"
	s.Footer.PageNumberTx = "Page {page_number} / {total_pages}"
	p := render(t, s, Document{Title: "x", Number: "INV-42"})
	txt := p.text(t, 1)
	mustContain(t, "custom footer", txt, "تفکر", "۰۲۱۸۸۷۷۶۶۵۵", "INV-42", "Page 1 / 1")
	if strings.Contains(txt, "۸۲۱") {
		t.Error("old footer text must be gone")
	}
}

func TestFooterCanBeDisabled(t *testing.T) {
	s := Defaults()
	s.Footer.Enabled = false
	txt := render(t, s, Document{Title: "x"}).text(t, 1)
	if strings.Contains(txt, "۸۲۱") || strings.Contains(txt, "صفحه") {
		t.Errorf("footer should be absent:\n%s", txt)
	}
	mustContain(t, "header still present", txt, "TAFAKOR")
}

func TestMultiPageKeepsLetterheadAndNumbers(t *testing.T) {
	p := render(t, Defaults(), SampleDocument("fa", 3))
	if p.pages < 3 {
		t.Fatalf("expected at least 3 pages, got %d", p.pages)
	}
	for i := 1; i <= p.pages; i++ {
		txt := p.text(t, i)
		mustContain(t, "page "+strconv.Itoa(i), txt, "TAFAKOR", "۸۲۱", "۱۴۰۱۵۵۸۹۰۳۸")
		// "صفحه i از n": the Persian-digit font draws Persian numerals, while
		// the text layer keeps the digits Chromium filled in.
		re := regexp.MustCompile(`صفحه\s*` + strconv.Itoa(i) + `\s*از\s*` + strconv.Itoa(p.pages) + `|صفحه\s*` + strconv.Itoa(i) + `\s*` + `|` + strconv.Itoa(i) + `\s*از\s*` + strconv.Itoa(p.pages))
		if !strings.Contains(txt, "صفحه") || !re.MatchString(txt) {
			t.Errorf("page %d: page number missing:\n%s", i, txt)
		}
	}
}

func TestLandscapeIsA4(t *testing.T) {
	p := render(t, Defaults(), Document{Title: "گزارش عریض", Orientation: "landscape"})
	if !isA4(p, true) {
		t.Fatalf("expected A4 landscape, got %.2fx%.2f", p.w, p.h)
	}
}

func TestPersianAndMixedText(t *testing.T) {
	doc := Document{Title: "گزارش فروش", Paragraphs: []string{"فروش ماهانه ۱۲۳٬۴۵۶ ریال", "Mixed text: فاکتور INV-2026 با مبلغ 1,250,000"}}
	txt := render(t, Defaults(), doc).text(t, 1)
	mustContain(t, "rtl text", txt, "گزارش", "فروش", "ماهانه", "۱۲۳٬۴۵۶", "ریال", "INV-2026", "1,250,000", "فاکتور", "Mixed")
}

func TestRepresentativeDocumentsShareTheLetterhead(t *testing.T) {
	for _, doc := range representativeDocuments() {
		t.Run(doc.Title, func(t *testing.T) {
			p := render(t, Defaults(), doc)
			if !isA4(p, false) {
				t.Fatalf("not A4: %.2fx%.2f", p.w, p.h)
			}
			txt := p.text(t, 0)
			mustContain(t, doc.Title, txt, "TAFAKOR", "۸۲۱", "۱۴۰۱۵۵۸۹۰۳۸", strings.Fields(doc.Title)[0])
		})
	}
}
