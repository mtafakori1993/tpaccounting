package approval

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

func TestTokenSingleUseExpiryTamper(t *testing.T) {
	st, _ := store.OpenSQLite(filepath.Join(t.TempDir(), "a.db"))
	defer st.Close()
	now := time.Now()
	s := &Service{Store: st, TTL: time.Minute, Now: func() time.Time { return now }}
	ctx := context.Background()
	sc := store.Scope{CompanyID: 1, UserID: 2}
	args := []byte(`{"amount":"1000","party_id":5,"date":"2025-10-08"}`)
	h := tools.PayloadHash("create_receipt", args)

	tok, _, err := s.Issue(ctx, sc, "task1", h)
	if err != nil {
		t.Fatal(err)
	}
	// tampered payload (amount changed) voids the approval
	tampered := tools.PayloadHash("create_receipt", []byte(`{"amount":"9000","party_id":5,"date":"2025-10-08"}`))
	if s.Consume(ctx, sc, "task1", tok, tampered) == nil {
		t.Fatal("tampered payload accepted")
	}
	// key order does not matter (canonical JSON)
	same := tools.PayloadHash("create_receipt", []byte(`{"date":"2025-10-08","party_id":5,"amount":"1000"}`))
	if same != h {
		t.Fatal("canonical hash differs for reordered keys")
	}
	if s.Consume(ctx, sc, "task2", tok, h) == nil {
		t.Fatal("token used for another task")
	}
	if s.Consume(ctx, store.Scope{CompanyID: 1, UserID: 3}, "task1", tok, h) == nil {
		t.Fatal("token used by another user")
	}
	if err := s.Consume(ctx, sc, "task1", tok, h); err != nil {
		t.Fatal(err)
	}
	if s.Consume(ctx, sc, "task1", tok, h) == nil {
		t.Fatal("token reused")
	}
	tok2, _, _ := s.Issue(ctx, sc, "task1", h)
	now = now.Add(2 * time.Minute)
	if s.Consume(ctx, sc, "task1", tok2, h) == nil {
		t.Fatal("expired token accepted")
	}
}
