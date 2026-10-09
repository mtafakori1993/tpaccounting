package normalize

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TextDate is a date found in free text.
type TextDate struct {
	Raw       string // as written
	Pos       int    // byte offset in the text (after digit normalization)
	Jalali    bool
	Gregorian string // YYYY-MM-DD (valid dates only)
	Valid     bool
}

var jalaliMonths = map[string]int{"فروردین": 1, "اردیبهشت": 2, "خرداد": 3, "تیر": 4, "مرداد": 5, "امرداد": 5, "شهریور": 6, "مهر": 7, "آبان": 8,
	"آذر": 9, "دی": 10, "بهمن": 11, "اسفند": 12}

var (
	numericDateRe = regexp.MustCompile(`(1[34][0-9]{2}|19[0-9]{2}|2[01][0-9]{2})[/\-.](\d{1,2})[/\-.](\d{1,2})`)
	textDateRe    = regexp.MustCompile(`(\d{1,2})\s*(فروردین|اردیبهشت|خرداد|تیر|امرداد|مرداد|شهریور|مهر|آبان|آذر|دی|بهمن|اسفند)(?:\s*ماه)?\s*(?:سال\s*)?(1[34][0-9]{2})`)
)

// DatesIn finds the dates in text — numeric (1405/08/12, 2026-11-02) and written with a
// Jalali month name («۱۲ آبان ۱۴۰۵») — and converts the valid ones to Gregorian. A Jalali
// year (13xx/14xx) is never read as a Gregorian year.
func DatesIn(text string) []TextDate {
	t := Digits(text)
	var out []TextDate
	add := func(raw string, pos, y, m, d int, jalali bool) {
		td := TextDate{Raw: raw, Pos: pos, Jalali: jalali}
		maxDay := 31
		if m >= 1 && m <= 12 {
			if jalali {
				maxDay = JalaliMonthDays(y, m)
			} else {
				maxDay = time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
			}
		}
		if m >= 1 && m <= 12 && d >= 1 && d <= maxDay {
			td.Valid = true
			gy, gm, gd := y, m, d
			if jalali {
				gy, gm, gd = JalaliToGregorian(y, m, d)
			}
			td.Gregorian = fmt.Sprintf("%04d-%02d-%02d", gy, gm, gd)
		}
		out = append(out, td)
	}
	for _, m := range numericDateRe.FindAllStringSubmatchIndex(t, -1) {
		y, _ := strconv.Atoi(t[m[2]:m[3]])
		mo, _ := strconv.Atoi(t[m[4]:m[5]])
		d, _ := strconv.Atoi(t[m[6]:m[7]])
		add(t[m[0]:m[1]], m[0], y, mo, d, y < 1500)
	}
	for _, m := range textDateRe.FindAllStringSubmatchIndex(t, -1) {
		d, _ := strconv.Atoi(t[m[2]:m[3]])
		mo := jalaliMonths[t[m[4]:m[5]]]
		y, _ := strconv.Atoi(t[m[6]:m[7]])
		add(strings.TrimSpace(t[m[0]:m[1]]), m[0], y, mo, d, true)
	}
	return out
}
