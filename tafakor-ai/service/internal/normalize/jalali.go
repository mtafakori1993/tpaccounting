package normalize

import "fmt"

// Jalali (Solar Hijri) ↔ Gregorian conversion using the well-known arithmetic
// algorithm (valid for 1300–1500 AP). Re-implemented here so the AI service never
// imports accounting packages.

func div(a, b int) int { return a / b }

// JalaliToGregorian converts a Jalali date to Gregorian.
func JalaliToGregorian(jy, jm, jd int) (int, int, int) {
	jy += 1595
	days := -355668 + (365 * jy) + (div(jy, 33) * 8) + div((jy%33)+3, 4) + jd
	if jm < 7 {
		days += (jm - 1) * 31
	} else {
		days += ((jm - 7) * 30) + 186
	}
	gy := 400 * div(days, 146097)
	days %= 146097
	if days > 36524 {
		days--
		gy += 100 * div(days, 36524)
		days %= 36524
		if days >= 365 {
			days++
		}
	}
	gy += 4 * div(days, 1461)
	days %= 1461
	if days > 365 {
		gy += div(days-1, 365)
		days = (days - 1) % 365
	}
	gd := days + 1
	leap := (gy%4 == 0 && gy%100 != 0) || gy%400 == 0
	sal := []int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if leap {
		sal[2] = 29
	}
	gm := 1
	for gm = 1; gm <= 12 && gd > sal[gm]; gm++ {
		gd -= sal[gm]
	}
	return gy, gm, gd
}

// GregorianToJalali converts a Gregorian date to Jalali.
func GregorianToJalali(gy, gm, gd int) (int, int, int) {
	gdm := []int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + (365 * gy) + div(gy2+3, 4) - div(gy2+99, 100) + div(gy2+399, 400) + gd + gdm[gm-1]
	jy := -1595 + (33 * div(days, 12053))
	days %= 12053
	jy += 4 * div(days, 1461)
	days %= 1461
	if days > 365 {
		jy += div(days-1, 365)
		days = (days - 1) % 365
	}
	var jm, jd int
	if days < 186 {
		jm = 1 + div(days, 31)
		jd = 1 + (days % 31)
	} else {
		jm = 7 + div(days-186, 30)
		jd = 1 + ((days - 186) % 30)
	}
	return jy, jm, jd
}

// JalaliLeap reports whether a Jalali year is leap (33-year cycle approximation used by the algorithm).
func JalaliLeap(jy int) bool {
	gy, gm, gd := JalaliToGregorian(jy, 12, 30)
	y, m, _ := GregorianToJalali(gy, gm, gd)
	return y == jy && m == 12
}

// JalaliMonthDays returns the number of days in a Jalali month.
func JalaliMonthDays(jy, jm int) int {
	switch {
	case jm <= 6:
		return 31
	case jm <= 11:
		return 30
	case JalaliLeap(jy):
		return 30
	}
	return 29
}

// FormatJalali renders a Gregorian date as YYYY/MM/DD in the Jalali calendar.
func FormatJalali(gy, gm, gd int) string {
	y, m, d := GregorianToJalali(gy, gm, gd)
	return fmt.Sprintf("%04d/%02d/%02d", y, m, d)
}
