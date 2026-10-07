// Package duplicates detects likely duplicate registrations before anything is
// written: same amount and type within a few days, same reference number, same
// party and account all add to a score.
package duplicates

import (
	"time"

	"github.com/shopspring/decimal"
)

// Candidate is an existing document (receipt, payment or expense) to compare against.
type Candidate struct {
	Kind      string // receipt | payment | expense
	ID        int64
	Number    int64
	Date      string
	Amount    decimal.Decimal
	Reference string
	PartyID   int64
	AccountID int64 // treasury account or expense category
	Status    string
}

type Proposed struct {
	Kind      string
	Date      string
	Amount    decimal.Decimal
	Reference string
	PartyID   int64
	AccountID int64
}

type Match struct {
	Candidate Candidate `json:"candidate"`
	Score     int       `json:"score"`
	Reasons   []string  `json:"reasons"`
}

// Threshold at or above which a match is a "likely duplicate".
const Threshold = 60

func days(a, b string) int {
	ta, e1 := time.Parse("2006-01-02", a[:min(10, len(a))])
	tb, e2 := time.Parse("2006-01-02", b[:min(10, len(b))])
	if e1 != nil || e2 != nil {
		return 999
	}
	d := int(ta.Sub(tb).Hours() / 24)
	if d < 0 {
		d = -d
	}
	return d
}

// Find scores every candidate and returns likely duplicates (best first).
func Find(p Proposed, cands []Candidate) []Match {
	var out []Match
	for _, c := range cands {
		if c.Status == "cancelled" || c.Kind != p.Kind {
			continue
		}
		m := Match{Candidate: c}
		if p.Reference != "" && c.Reference != "" && p.Reference == c.Reference {
			m.Score += 70
			m.Reasons = append(m.Reasons, "same_reference")
		}
		if c.Amount.Equal(p.Amount) {
			m.Score += 30
			m.Reasons = append(m.Reasons, "same_amount")
			switch d := days(p.Date, c.Date); {
			case d == 0:
				m.Score += 25
				m.Reasons = append(m.Reasons, "same_date")
			case d <= 3:
				m.Score += 15
				m.Reasons = append(m.Reasons, "date_within_3_days")
			}
			if p.PartyID != 0 && p.PartyID == c.PartyID {
				m.Score += 15
				m.Reasons = append(m.Reasons, "same_party")
			}
			if p.AccountID != 0 && p.AccountID == c.AccountID {
				m.Score += 10
				m.Reasons = append(m.Reasons, "same_account")
			}
		}
		if m.Score >= Threshold {
			out = append(out, m)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
