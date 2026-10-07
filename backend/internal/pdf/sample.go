package pdf

import "fmt"

// SampleDocument is a representative document used for the letterhead preview
// and the test print. pages controls roughly how many pages it fills.
func SampleDocument(lang string, pages int) Document {
	if pages < 1 {
		pages = 1
	}
	if pages > 10 {
		pages = 10
	}
	fa := lang != "en"
	t := func(f, e string) string {
		if fa {
			return f
		}
		return e
	}
	digits := func(s string) string {
		if !fa {
			return s
		}
		out := []rune{}
		for _, r := range s {
			if r >= '0' && r <= '9' {
				r = '۰' + (r - '0')
			}
			out = append(out, r)
		}
		return string(out)
	}
	rows := []Row{}
	n := 6 + (pages-1)*34
	for i := 1; i <= n; i++ {
		rows = append(rows, Row{Cells: []string{
			digits(fmt.Sprint(i)),
			t("نمونه کالا یا خدمت", "Sample product or service") + " " + digits(fmt.Sprint(i)),
			digits("2"),
			digits("1,250,000"),
			digits("2,500,000"),
		}})
	}
	return Document{
		Lang:   map[bool]string{true: "fa", false: "en"}[fa],
		Title:  t("پیش‌نمایش سربرگ", "Letterhead preview"),
		Number: digits("1405-001"), Date: digits("1405/07/15"),
		Subtitle: t("این سند نمونه نشان می‌دهد اسناد چاپی شما چگونه دیده می‌شوند.", "This sample shows how your printed documents look."),
		Meta:     []KV{{t("مشتری", "Customer"), t("شرکت نمونه", "Sample Co.")}, {t("تاریخ", "Date"), digits("1405/07/15")}, {t("ارز", "Currency"), t("ریال", "IRR")}},
		Tables: []Table{{
			Columns: []Column{{"#", "center"}, {t("شرح", "Description"), "start"}, {t("تعداد", "Qty"), "end"}, {t("فی", "Unit price"), "end"}, {t("مبلغ", "Amount"), "end"}},
			Rows:    rows,
		}},
		Totals:     []Total{{t("جمع", "Subtotal"), digits("15,000,000"), false}, {t("مبلغ کل", "Total"), digits("16,500,000"), true}},
		Notes:      t("متن یادداشت نمونه — Mixed فارسی and English 123.", "Sample note — متن فارسی mixed with English 123."),
		Signatures: []string{t("امضای فروشنده", "Seller signature"), t("امضای خریدار", "Buyer signature")},
	}
}
