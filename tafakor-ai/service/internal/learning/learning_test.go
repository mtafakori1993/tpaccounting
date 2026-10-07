package learning

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

func svc(t *testing.T) (*Service, *store.SQLite) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Service{Store: st}, st
}

func TestCreateSuggestThenApply(t *testing.T) {
	s, _ := svc(t)
	ctx := context.Background()
	sc := store.Scope{CompanyID: 1, UserID: 7}
	o := Offer{Kind: ExpenseCategory, Pattern: Pattern(ExpenseCategory, "VPN"), Result: Result{ID: 12, Name: "Software & Subscriptions", Field: "category_id"}}
	r, err := s.Accept(ctx, sc, o)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Lookup(ctx, sc, ExpenseCategory, "", "Purchased a vpn for the company")
	if err != nil || m == nil || m.Result.ID != 12 || m.Stage != "suggest" {
		t.Fatalf("first time must only suggest: %+v %v", m, err)
	}
	_ = s.Confirm(ctx, sc, r.ID)
	_ = s.Confirm(ctx, sc, r.ID)
	m, _ = s.Lookup(ctx, sc, ExpenseCategory, "", "خرید VPN")
	if m.Stage != "apply" {
		t.Fatalf("after repeated confirmations the rule applies by default: %+v", m)
	}
	// scope: another user doesn't see a personal rule
	if m, _ := s.Lookup(ctx, store.Scope{CompanyID: 1, UserID: 8}, ExpenseCategory, "", "vpn"); m != nil {
		t.Fatal("personal rule leaked to another user")
	}
	if m, _ := s.Lookup(ctx, store.Scope{CompanyID: 2, UserID: 7}, ExpenseCategory, "", "vpn"); m != nil {
		t.Fatal("rule leaked to another company")
	}
}

func TestConflictingRulesAsk(t *testing.T) {
	s, st := svc(t)
	ctx := context.Background()
	sc := store.Scope{CompanyID: 1, UserID: 7}
	_, _ = s.Accept(ctx, sc, Offer{Kind: ExpenseCategory, Pattern: Pattern(ExpenseCategory, "vpn"), Result: Result{ID: 12, Field: "category_id"}})
	res, _ := json.Marshal(Result{ID: 13, Field: "category_id"})
	_ = st.SaveRule(ctx, &store.Rule{CompanyID: 1, UserID: 9, Scope: "company", Kind: ExpenseCategory, Pattern: Pattern(ExpenseCategory, "vpn"), Result: res, Source: "promoted", Status: "active", TimesConfirmed: 5})
	m, err := s.Lookup(ctx, sc, ExpenseCategory, "", "vpn")
	if !errors.Is(err, ErrConflict) || len(m.Conflict) != 2 {
		t.Fatalf("conflict expected: %+v %v", m, err)
	}
}

func TestPromotionNeedsPermissionAndDelete(t *testing.T) {
	s, _ := svc(t)
	ctx := context.Background()
	sc := store.Scope{CompanyID: 1, UserID: 7}
	r, _ := s.Accept(ctx, sc, Offer{Kind: PaymentDefault, Pattern: Pattern(PaymentDefault, "company_card"), Result: Result{ID: 3, Field: "treasury_account_id"}})
	if _, err := s.Update(ctx, sc, r.ID, "company", "", "", false); err == nil {
		t.Fatal("promotion without permission")
	}
	if _, err := s.Update(ctx, sc, r.ID, "company", "", "", true); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, store.Scope{CompanyID: 1, UserID: 8}, r.ID, false); err == nil {
		t.Fatal("company rule deleted without permission")
	}
	if err := s.Delete(ctx, sc, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.Lookup(ctx, sc, PaymentDefault, "company_card", ""); m != nil {
		t.Fatal("deleted rule still applies")
	}
}

func TestCorrectionOfOwnRuleResetsTrust(t *testing.T) {
	s, _ := svc(t)
	ctx := context.Background()
	sc := store.Scope{CompanyID: 1, UserID: 7}
	p := Pattern(ExpenseCategory, "hosting")
	r, _ := s.Accept(ctx, sc, Offer{Kind: ExpenseCategory, Pattern: p, Result: Result{ID: 1, Field: "category_id"}})
	_ = s.Confirm(ctx, sc, r.ID)
	_ = s.Confirm(ctx, sc, r.ID)
	r2, _ := s.Accept(ctx, sc, Offer{Kind: ExpenseCategory, Pattern: p, Result: Result{ID: 2, Field: "category_id"}})
	if r2.ID != r.ID || r2.TimesConfirmed != 1 || decodeResult(*r2).ID != 2 {
		t.Fatalf("correction should replace and reset: %+v", r2)
	}
}
