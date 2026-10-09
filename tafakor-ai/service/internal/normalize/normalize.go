// Package normalize turns noisy text (OCR output, speech transcripts, user input)
// into canonical values: ASCII digits, decimal amounts, Gregorian dates, masked
// card/account numbers. Nothing here trusts its input.
package normalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"
)

// Digits converts Persian (۰-۹) and Arabic-Indic (٠-٩) digits to ASCII, and the
// Persian/Arabic decimal and thousands separators to '.' and ','.
func Digits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			b.WriteRune('0' + (r - '۰'))
		case r >= '٠' && r <= '٩':
			b.WriteRune('0' + (r - '٠'))
		case r == '٫': // Arabic decimal separator
			b.WriteRune('.')
		case r == '٬': // Arabic thousands separator
			b.WriteRune(',')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Text normalises Persian text for matching: Arabic yeh/kaf → Persian, removes
// diacritics, tatweel and ZWNJ, collapses spaces, lower-cases Latin.
func Text(s string) string {
	s = Digits(s)
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case 'ي', 'ى':
			r = 'ی'
		case 'ك':
			r = 'ک'
		case 'ة':
			r = 'ه'
		case 'أ', 'إ', 'آ':
			r = 'ا'
		case 'ۀ':
			r = 'ه'
		case 'ـ': // tatweel
			continue
		case '‌', '‍', '‏', '‎':
			r = ' '
		}
		if unicode.Is(unicode.Mn, r) { // diacritics (harakat)
			continue
		}
		if unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' || r == ',' {
			if !space && b.Len() > 0 {
				b.WriteRune(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(unicode.ToLower(r))
	}
	return strings.TrimSpace(b.String())
}

var amountRe = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// Amount parses a money string with any digits and separators ("۱۲٬۵۰۰٫۵۰",
// "12,500.50", "12 500"). It rejects ambiguous or non-numeric input.
func Amount(s string) (decimal.Decimal, error) {
	s = Digits(strings.TrimSpace(s))
	for _, cur := range []string{"ریال", "تومان", "rial", "rials", "irr", "irt", "toman", "usd", "eur", "$", "€", "﷼"} {
		s = strings.TrimSpace(strings.ReplaceAll(strings.ToLower(s), cur, ""))
	}
	s = strings.NewReplacer(",", "", "،", "", " ", "", " ", "", "'", "").Replace(s)
	if !amountRe.MatchString(s) {
		return decimal.Zero, fmt.Errorf("not an amount: %q", s)
	}
	return decimal.NewFromString(s)
}

// Card masks a card or account number, keeping the first 6 and last 4 digits of
// a 16-digit card, otherwise only the last 4 characters.
func Card(s string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		if r == '*' || r == 'x' || r == 'X' {
			return '*'
		}
		return -1
	}, Digits(s))
	if len(d) == 0 {
		return ""
	}
	if len(d) == 16 {
		return d[:6] + "******" + d[12:]
	}
	if len(d) <= 4 {
		return d
	}
	return strings.Repeat("*", len(d)-4) + d[len(d)-4:]
}

var cardLike = regexp.MustCompile(`\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{4}\b|\bIR\d{24}\b|\b\d{10,}\b`)

// MaskSensitive masks card numbers, IBANs and long account numbers inside free text
// (used before storing audit data).
func MaskSensitive(s string) string {
	return cardLike.ReplaceAllStringFunc(Digits(s), func(m string) string {
		if strings.HasPrefix(m, "IR") {
			return "IR" + strings.Repeat("*", 20) + m[len(m)-4:]
		}
		return Card(m)
	})
}

// amountKey: fields that hold money or quantities — never masked (a 1,200,000,000 rial
// amount is not a card number).
var amountKey = regexp.MustCompile(`(?i)(amount|total|price|balance|salary|upto|ceiling|rate|exemption|quantity|debit|credit|limit|wage|cost|value|_id$|^id$|number$)`)

// MaskJSON masks sensitive numbers inside a JSON document's string values. Numbers and
// amount-like fields are left alone and the result stays valid JSON (masking the text
// of the document turned a large amount into ******0000 and broke the whole audit
// trace). Input that is not JSON is masked as text.
func MaskJSON(b []byte) []byte {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []byte(MaskSensitive(string(b)))
	}
	var walk func(key string, x any) any
	walk = func(key string, x any) any {
		switch t := x.(type) {
		case map[string]any:
			for k, y := range t {
				t[k] = walk(k, y)
			}
			return t
		case []any:
			for i, y := range t {
				t[i] = walk(key, y)
			}
			return t
		case string:
			if amountKey.MatchString(key) {
				return t
			}
			return MaskSensitive(t)
		}
		return x
	}
	out, err := json.Marshal(walk("", v))
	if err != nil {
		return []byte(MaskSensitive(string(b)))
	}
	return out
}

var (
	isoDate  = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})$`)
	dmyDate  = regexp.MustCompile(`^(\d{1,2})[-/.](\d{1,2})[-/.](\d{4})$`)
	timeOnly = regexp.MustCompile(`^(\d{1,2}):(\d{2})(?::(\d{2}))?$`)
)

// Date parses a Jalali or Gregorian date written with any digits. Years 1300–1500
// are Jalali, 1900–2200 Gregorian. Returns the Gregorian date and whether the input was Jalali.
func Date(s string) (time.Time, bool, error) {
	s = strings.TrimSpace(Digits(s))
	if i := strings.IndexAny(s, " T"); i > 0 { // drop a trailing time
		s = s[:i]
	}
	var y, m, d int
	if g := isoDate.FindStringSubmatch(s); g != nil {
		y, m, d = atoi(g[1]), atoi(g[2]), atoi(g[3])
	} else if g := dmyDate.FindStringSubmatch(s); g != nil {
		y, m, d = atoi(g[3]), atoi(g[2]), atoi(g[1])
	} else {
		return time.Time{}, false, fmt.Errorf("unrecognised date %q", s)
	}
	switch {
	case y >= 1300 && y <= 1500:
		if m < 1 || m > 12 || d < 1 || d > JalaliMonthDays(y, m) {
			return time.Time{}, true, fmt.Errorf("invalid Jalali date %q", s)
		}
		gy, gm, gd := JalaliToGregorian(y, m, d)
		return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, time.UTC), true, nil
	case y >= 1900 && y <= 2200:
		t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
		if t.Month() != time.Month(m) || t.Day() != d {
			return time.Time{}, false, fmt.Errorf("invalid date %q", s)
		}
		return t, false, nil
	}
	return time.Time{}, false, fmt.Errorf("year out of range in %q", s)
}

// Time parses "HH:MM[:SS]" with any digits.
func Time(s string) (string, error) {
	g := timeOnly.FindStringSubmatch(strings.TrimSpace(Digits(s)))
	if g == nil {
		return "", fmt.Errorf("unrecognised time %q", s)
	}
	h, m := atoi(g[1]), atoi(g[2])
	if h > 23 || m > 59 {
		return "", fmt.Errorf("invalid time %q", s)
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
