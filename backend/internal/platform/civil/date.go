// Package civil provides a calendar date type (no time, no zone) used for accounting dates.
package civil

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const layout = "2006-01-02"

type Date struct{ time.Time }

func New(y int, m time.Month, d int) Date { return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)} }

func Today() Date {
	t := time.Now()
	return New(t.Year(), t.Month(), t.Day())
}

func Parse(s string) (Date, error) {
	t, err := time.Parse(layout, strings.TrimSpace(s))
	if err != nil {
		return Date{}, err
	}
	return Date{t}, nil
}

func (d Date) String() string { return d.Format(layout) }

func (d Date) AddDays(n int) Date { return Date{d.Time.AddDate(0, 0, n)} }

func (d Date) Before(o Date) bool { return d.Time.Before(o.Time) }
func (d Date) After(o Date) bool  { return d.Time.After(o.Time) }

func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*d = Date{}
		return nil
	}
	p, err := Parse(s)
	if err != nil {
		return fmt.Errorf("invalid date %q", s)
	}
	*d = p
	return nil
}

func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
	case time.Time:
		*d = New(v.Year(), v.Month(), v.Day())
	case string:
		p, err := Parse(v)
		if err != nil {
			return err
		}
		*d = p
	default:
		return fmt.Errorf("civil.Date: cannot scan %T", src)
	}
	return nil
}

func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}
