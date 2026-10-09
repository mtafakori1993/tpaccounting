package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDigits(t *testing.T) {
	cases := map[string]string{
		"۱۲۳۴۵۶۷۸۹۰": "1234567890",
		"٠١٢٣٤٥٦٧٨٩": "0123456789",
		"۱۲٬۵۰۰٫۵۰":  "12,500.50",
		"abc ۳ def":  "abc 3 def",
		"۱۴۰۳/۰۷/۱۵": "1403/07/15",
	}
	for in, want := range cases {
		if got := Digits(in); got != want {
			t.Errorf("Digits(%q)=%q want %q", in, got, want)
		}
	}
}

func TestAmount(t *testing.T) {
	ok := map[string]string{
		"۱۲٬۵۰۰٫۵۰":    "12500.5",
		"12,500,000":   "12500000",
		"۲۵۰۰۰۰۰ ریال": "2500000",
		"1 250 000":    "1250000",
		"٣٤٥":          "345",
		"$ 9.99":       "9.99",
	}
	for in, want := range ok {
		d, err := Amount(in)
		if err != nil || d.String() != want {
			t.Errorf("Amount(%q)=%v,%v want %s", in, d, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "12a", "1.2.3", "--5"} {
		if _, err := Amount(bad); err == nil {
			t.Errorf("Amount(%q) should fail", bad)
		}
	}
}

func TestJalaliRoundTrip(t *testing.T) {
	cases := []struct{ jy, jm, jd, gy, gm, gd int }{
		{1403, 1, 1, 2024, 3, 20},
		{1402, 12, 29, 2024, 3, 19},
		{1403, 12, 30, 2025, 3, 20}, // 1403 is leap
		{1404, 7, 16, 2025, 10, 8},
		{1399, 10, 11, 2020, 12, 31},
	}
	for _, c := range cases {
		gy, gm, gd := JalaliToGregorian(c.jy, c.jm, c.jd)
		if gy != c.gy || gm != c.gm || gd != c.gd {
			t.Errorf("J2G %d/%d/%d = %d-%d-%d", c.jy, c.jm, c.jd, gy, gm, gd)
		}
		jy, jm, jd := GregorianToJalali(c.gy, c.gm, c.gd)
		if jy != c.jy || jm != c.jm || jd != c.jd {
			t.Errorf("G2J %d-%d-%d = %d/%d/%d", c.gy, c.gm, c.gd, jy, jm, jd)
		}
	}
	if !JalaliLeap(1403) || JalaliLeap(1404) {
		t.Error("leap years wrong")
	}
}

func TestDate(t *testing.T) {
	d, jal, err := Date("۱۴۰۴/۰۷/۱۶")
	if err != nil || !jal || d.Format("2006-01-02") != "2025-10-08" {
		t.Fatalf("jalali: %v %v %v", d, jal, err)
	}
	d, jal, err = Date("2025-10-08")
	if err != nil || jal || d.Format("2006-01-02") != "2025-10-08" {
		t.Fatalf("gregorian: %v %v %v", d, jal, err)
	}
	d, _, err = Date("08/10/2025")
	if err != nil || d.Format("2006-01-02") != "2025-10-08" {
		t.Fatalf("dmy: %v %v", d, err)
	}
	d, _, err = Date("1404-07-16 14:30")
	if err != nil || d.Format("2006-01-02") != "2025-10-08" {
		t.Fatalf("with time: %v %v", d, err)
	}
	for _, bad := range []string{"1404/13/01", "1404/12/31", "2025-02-30", "hello", "0099/01/01", "1403/7"} {
		if _, _, err := Date(bad); err == nil {
			t.Errorf("Date(%q) should fail", bad)
		}
	}
	if tm, err := Time("۱۴:۰۵"); err != nil || tm != "14:05" {
		t.Errorf("time %v %v", tm, err)
	}
	if _, err := Time("25:00"); err == nil {
		t.Error("bad time accepted")
	}
}

func TestCardMasking(t *testing.T) {
	if got := Card("6104-3378-1234-5678"); got != "610433******5678" {
		t.Errorf("card %q", got)
	}
	if got := Card("۰۱۲۳۴۵۶۷۸۹"); got != "******6789" {
		t.Errorf("account %q", got)
	}
	got := MaskSensitive("paid with 6104 3378 1234 5678 from IR123456789012345678901234")
	if got != "paid with 610433******5678 from IR********************1234" {
		t.Errorf("mask %q", got)
	}
}

func TestText(t *testing.T) {
	if Text("علي  رضايي") != Text("علی رضایی") {
		t.Error("arabic yeh not normalised")
	}
	if Text("مي‌خواهم") != "می خواهم" {
		t.Errorf("zwnj: %q", Text("مي‌خواهم"))
	}
}

func TestWordsToNumber(t *testing.T) {
	cases := map[string]string{
		"دو میلیون و پانصد هزار": "2500000",
		"سی و پنج میلیون":        "35000000",
		"یک میلیارد و دویست":     "1000000200",
		"صد و بیست و سه":         "123",
		"هزار":                   "1000",
		"یک و نیم میلیون":        "1500000",
		"۲ میلیون":               "2000000",
		"2.5 میلیون":             "2500000",
		"15 Million":             "15000000",
		"بیست‌و‌پنج هزار":        "25000",
		"سیصد و پنجاه هزار":      "350000",
		"نهصد و نود و نه هزار و نهصد و نود و نه": "999999",
	}
	for in, want := range cases {
		d, ok := WordsToNumber(in)
		if !ok || d.String() != want {
			t.Errorf("WordsToNumber(%q)=%v,%v want %s", in, d, ok, want)
		}
	}
}

func TestReplaceNumberWords(t *testing.T) {
	cases := map[string]string{
		"دو میلیون و پانصد هزار تومان از محمد تفکری قرض گرفتیم": "2500000 تومان از محمد تفکری قرض گرفتیم",
		"نه، این درست نیست":  "نه، این درست نیست",
		"یه وی پی ان خریدیم": "یه وی پی ان خریدیم",
		"نه تومان":           "9 تومان",
		"پرداخت سی و پنج هزار تومان و ده هزار تومان": "پرداخت 35000 تومان و 10000 تومان",
		"مبلغ ۱۲ میلیون": "مبلغ 12000000",
	}
	for in, want := range cases {
		if got := ReplaceNumberWords(in); got != want {
			t.Errorf("ReplaceNumberWords(%q)=%q want %q", in, got, want)
		}
	}
}

func TestMaskJSONKeepsAmountsAndValidity(t *testing.T) {
	in := []byte(`{"brackets":[{"upto":1000000000,"rate":35}],"amount":"1200000000","card":"6037991234567890","notes":"کارت 6037991234567890","iban":"IR123456789012345678901234"}`)
	out := MaskJSON(in)
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("invalid JSON: %s", out)
	}
	s := string(out)
	if !strings.Contains(s, "1000000000") || !strings.Contains(s, `"1200000000"`) || strings.Contains(s, "6037991234567890") || strings.Contains(s, "IR123456789012345678901234") {
		t.Fatalf("masking: %s", s)
	}
}

func TestDatesIn(t *testing.T) {
	ds := DatesIn("چک ... ثبت کن تاریخ ۱۲ آبان ۱۴۰۵ و سررسید 1405/09/15")
	if len(ds) != 2 {
		t.Fatalf("%+v", ds)
	}
	got := map[string]bool{}
	for _, d := range ds {
		got[d.Gregorian] = d.Valid
	}
	// Jalali 1405 is never Gregorian 1405
	if !got["2026-11-03"] || !got["2026-12-06"] { // 1 Farvardin 1405 = 2026-03-21
		t.Fatalf("conversion %+v", ds)
	}
	if d := DatesIn("۱۴۰۵/۱۳/۴۰"); len(d) != 1 || d[0].Valid {
		t.Fatalf("invalid date accepted: %+v", d)
	}
	if d := DatesIn("۳۱ اسفند ۱۴۰۴"); len(d) != 1 || d[0].Valid {
		t.Fatalf("Esfand 1404 has 29 days: %+v", d)
	}
}
