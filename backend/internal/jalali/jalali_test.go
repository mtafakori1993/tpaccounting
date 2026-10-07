package jalali

import (
	"testing"
	"time"
)

func TestConversions(t *testing.T) {
	cases := []struct {
		g          time.Time
		jy, jm, jd int
	}{
		{time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC), 1405, 7, 14},
		{time.Date(2025, 3, 21, 0, 0, 0, 0, time.UTC), 1404, 1, 1},
		{time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC), 1405, 1, 1},
		{time.Date(2024, 3, 19, 0, 0, 0, 0, time.UTC), 1402, 12, 29},
		{time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC), 1403, 12, 30}, // 1403 is a leap year
		{time.Date(1979, 2, 11, 0, 0, 0, 0, time.UTC), 1357, 11, 22},
	}
	for _, c := range cases {
		jy, jm, jd := FromGregorian(c.g)
		if jy != c.jy || jm != c.jm || jd != c.jd {
			t.Errorf("FromGregorian(%s) = %d/%d/%d, want %d/%d/%d", c.g.Format("2006-01-02"), jy, jm, jd, c.jy, c.jm, c.jd)
		}
		if g := ToGregorian(c.jy, c.jm, c.jd); !g.Equal(c.g) {
			t.Errorf("ToGregorian(%d/%d/%d) = %s, want %s", c.jy, c.jm, c.jd, g.Format("2006-01-02"), c.g.Format("2006-01-02"))
		}
	}
}

func TestLeapAndMonthLength(t *testing.T) {
	if !IsLeap(1403) || IsLeap(1404) || IsLeap(1405) {
		t.Fatal("unexpected leap-year result")
	}
	if MonthLength(1403, 12) != 30 || MonthLength(1404, 12) != 29 || MonthLength(1404, 7) != 30 || MonthLength(1404, 1) != 31 {
		t.Fatal("unexpected month length")
	}
}

func TestRoundTripRange(t *testing.T) {
	d := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 365*40; i++ {
		jy, jm, jd := FromGregorian(d)
		if back := ToGregorian(jy, jm, jd); !back.Equal(d) {
			t.Fatalf("round trip failed for %s", d.Format("2006-01-02"))
		}
		d = d.AddDate(0, 0, 1)
	}
}
