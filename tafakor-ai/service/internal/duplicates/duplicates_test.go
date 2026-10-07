package duplicates

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestFind(t *testing.T) {
	amt := decimal.NewFromInt(25_000_000)
	p := Proposed{Kind: "receipt", Date: "2025-10-08", Amount: amt, Reference: "123456", PartyID: 4, AccountID: 2}
	cands := []Candidate{
		{Kind: "receipt", ID: 1, Date: "2025-10-08", Amount: amt, PartyID: 4, AccountID: 2, Status: "posted"},              // same everything except ref
		{Kind: "receipt", ID: 2, Date: "2025-05-01", Amount: decimal.NewFromInt(1), Reference: "123456", Status: "posted"}, // same reference
		{Kind: "receipt", ID: 3, Date: "2025-10-08", Amount: amt, PartyID: 4, Status: "cancelled"},                         // cancelled → ignored
		{Kind: "payment", ID: 4, Date: "2025-10-08", Amount: amt, PartyID: 4, Status: "posted"},                            // other kind
		{Kind: "receipt", ID: 5, Date: "2025-09-01", Amount: amt, Status: "posted"},                                        // same amount, far date
	}
	m := Find(p, cands)
	if len(m) != 2 || m[0].Candidate.ID != 1 || m[1].Candidate.ID != 2 {
		t.Fatalf("got %+v", m)
	}
	if len(Find(Proposed{Kind: "receipt", Date: "2025-10-08", Amount: decimal.NewFromInt(7)}, cands)) != 0 {
		t.Fatal("false positive")
	}
}
