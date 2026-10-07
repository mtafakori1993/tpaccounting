// Package jalali converts between the Gregorian and Jalali (Persian solar hijri)
// calendars. It is a port of the well-known jalaali-js algorithm.
package jalali

import (
	"fmt"
	"time"
)

var breaks = []int{-61, 9, 38, 199, 426, 686, 756, 818, 1111, 1181, 1210, 1635, 2060, 2097, 2192, 2262, 2324, 2394, 2456, 3178}

// MonthNames are the Persian month names, 1-indexed via [m-1].
var MonthNames = []string{"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور", "مهر", "آبان", "آذر", "دی", "بهمن", "اسفند"}

// MonthNamesEn are the transliterated month names.
var MonthNamesEn = []string{"Farvardin", "Ordibehesht", "Khordad", "Tir", "Mordad", "Shahrivar", "Mehr", "Aban", "Azar", "Dey", "Bahman", "Esfand"}

type calInfo struct{ leap, gy, march int }

func jalCal(jy int) calInfo {
	bl := len(breaks)
	gy := jy + 621
	leapJ := -14
	jp := breaks[0]
	jump := 0
	if jy < jp || jy >= breaks[bl-1] {
		panic(fmt.Sprintf("jalali: year %d out of range", jy))
	}
	for i := 1; i < bl; i++ {
		jm := breaks[i]
		jump = jm - jp
		if jy < jm {
			break
		}
		leapJ = leapJ + jump/33*8 + (jump%33)/4
		jp = jm
	}
	n := jy - jp
	leapJ = leapJ + n/33*8 + (n%33+3)/4
	if jump%33 == 4 && jump-n == 4 {
		leapJ++
	}
	leapG := gy/4 - (gy/100+1)*3/4 - 150
	march := 20 + leapJ - leapG
	if jump-n < 6 {
		n = n - jump + (jump+4)/33*33
	}
	leap := ((n+1)%33 - 1) % 4
	if leap == -1 {
		leap = 4
	}
	return calInfo{leap: leap, gy: gy, march: march}
}

func g2d(gy, gm, gd int) int {
	d := (gy+(gm-8)/6+100100)*1461/4 + (153*((gm+9)%12)+2)/5 + gd - 34840408
	return d - (gy+100100+(gm-8)/6)/100*3/4 + 752
}

func d2g(jdn int) (int, int, int) {
	j := 4*jdn + 139361631
	j = j + (4*jdn+183187720)/146097*3/4*4 - 3908
	i := (j%1461)/4*5 + 308
	gd := (i%153)/5 + 1
	gm := (i/153)%12 + 1
	gy := j/1461 - 100100 + (8-gm)/6
	return gy, gm, gd
}

func j2d(jy, jm, jd int) int {
	r := jalCal(jy)
	return g2d(r.gy, 3, r.march) + (jm-1)*31 - jm/7*(jm-7) + jd - 1
}

func d2j(jdn int) (int, int, int) {
	gy, _, _ := d2g(jdn)
	jy := gy - 621
	r := jalCal(jy)
	k := jdn - g2d(gy, 3, r.march)
	if k >= 0 {
		if k <= 185 {
			return jy, 1 + k/31, k%31 + 1
		}
		k -= 186
	} else {
		jy--
		k += 179
		if r.leap == 1 {
			k++
		}
	}
	return jy, 7 + k/30, k%30 + 1
}

// FromGregorian converts a Gregorian date to Jalali.
func FromGregorian(t time.Time) (jy, jm, jd int) {
	return d2j(g2d(t.Year(), int(t.Month()), t.Day()))
}

// ToGregorian converts a Jalali date to a Gregorian date at UTC midnight.
func ToGregorian(jy, jm, jd int) time.Time {
	gy, gm, gd := d2g(j2d(jy, jm, jd))
	return time.Date(gy, time.Month(gm), gd, 0, 0, 0, 0, time.UTC)
}

func IsLeap(jy int) bool { return jalCal(jy).leap == 0 }

func MonthLength(jy, jm int) int {
	switch {
	case jm <= 6:
		return 31
	case jm <= 11:
		return 30
	case IsLeap(jy):
		return 30
	default:
		return 29
	}
}
