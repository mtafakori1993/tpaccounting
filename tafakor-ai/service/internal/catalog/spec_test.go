package catalog

import (
	"encoding/json"
	"testing"
)

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func rawBody(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// The curated tables must stay consistent with the generated catalog, otherwise a
// typo silently disables a rule.
func TestCuratedTablesReferenceRealOperationsAndFields(t *testing.T) {
	c := Load()
	for id, fields := range mustHave {
		op, ok := c.Get(id)
		if !ok {
			t.Fatalf("mustHave references unknown operation %s", id)
		}
		known := map[string]bool{}
		for _, f := range op.Body {
			known[f.Name] = true
		}
		for _, f := range fields {
			if !known[f] {
				t.Fatalf("mustHave %s: unknown body field %s", id, f)
			}
		}
	}
	for id, rules := range variants {
		op, ok := c.Get(id)
		if !ok {
			t.Fatalf("variants references unknown operation %s", id)
		}
		known := map[string]bool{}
		for _, f := range op.Body {
			known[f.Name] = true
		}
		for _, p := range op.PathParams {
			known[p] = true
		}
		for _, v := range rules {
			if !known[v.discriminator] {
				t.Fatalf("variants %s: unknown discriminator %s", id, v.discriminator)
			}
			for _, f := range append(append([]string{}, v.require...), v.drop...) {
				if !known[f] {
					t.Fatalf("variants %s: unknown field %s", id, f)
				}
			}
		}
	}
	for id, m := range uiMetaByOp {
		op, ok := c.Get(id)
		if !ok {
			t.Fatalf("uiMeta references unknown operation %s", id)
		}
		known := map[string]bool{}
		for _, f := range op.InputFields() {
			known[f.Name] = true
		}
		for f := range m.Fields {
			if !known[f] {
				t.Fatalf("uiMeta %s: unknown body field %s", id, f)
			}
		}
		for _, c := range m.Open {
			if c.Field == "" {
				continue
			}
			if !known[c.Field] && c.Field != "kind" {
				t.Fatalf("uiMeta %s: open references unknown field %s", id, c.Field)
			}
		}
	}
}

// The reported bug: an account plan was confirmed and only then rejected for a
// missing bank_name. The contract must know about it up front.
func TestBankAccountContractRequiresNameAndBankName(t *testing.T) {
	op, ok := Load().Get("treasury.accounts.create")
	if !ok {
		t.Fatal("operation missing")
	}
	s := op.Spec()
	if !has(s.RequiredFields, "name") || !has(s.RequiredFields, "bank_name") {
		t.Fatalf("required fields %v", s.RequiredFields)
	}
	if !s.ConfirmationRequired || !s.SupportsDirect || s.ApprovalRequired {
		t.Fatalf("contract flags %+v", s)
	}
	if !s.SupportsUIPreview || s.UIRoute != "/treasury" {
		t.Fatalf("ui preview %+v", s)
	}
	if len(s.UIOpen) != 2 {
		t.Fatalf("ui open candidates %v", s.UIOpen)
	}
	if got := op.UIOpenFor("bank"); len(got) != 1 || got[0] != "treasury.newBank" {
		t.Fatalf("open for bank: %v", got)
	}
	if op.UIOpenFor("cash")[0] != "treasury.newCash" {
		t.Fatal("cash variant must open the cash form")
	}
	if s.UIFields["bank_name"] != "treasury.bankName" || s.UIFields["opening_balance"] != "treasury.openingBalance" {
		t.Fatalf("ui fields %v", s.UIFields)
	}
}

func TestMissingRequiredDistinguishesExplicitNone(t *testing.T) {
	op, _ := Load().Get("treasury.accounts.create")
	s := op.Spec()
	path := map[string]string{}

	if got := s.MissingRequired(path, rawBody(t, `{"name":"Parsian","bank_name":"Parsian"}`)); len(got) != 0 {
		t.Fatalf("complete body reported missing %v", got)
	}
	if got := s.MissingRequired(path, rawBody(t, `{"opening_balance":"100000000","bank_name":"Parsian"}`)); len(got) != 1 || got[0] != "name" {
		t.Fatalf("missing name not detected: %v", got)
	}
	// the exact reported case: opening_balance present, bank_name absent
	got := s.MissingRequired(path, rawBody(t, `{"opening_balance":"100000000","name":"Current"}`))
	if len(got) != 1 || got[0] != "bank_name" {
		t.Fatalf("missing bank_name not detected before confirmation: %v", got)
	}
	// an explicit "none" satisfies a required field (the app may not enforce it)
	if got := s.MissingRequired(path, rawBody(t, `{"name":"Cash","bank_name":null}`)); len(got) != 0 {
		t.Fatalf("explicit none must satisfy: %v", got)
	}
	if got := s.MissingRequired(path, rawBody(t, `{"name":"Cash","bank_name":""}`)); len(got) != 1 {
		t.Fatalf("empty string is not a value: %v", got)
	}
}

// The generated markers require party_id and to_treasury_account_id for every
// treasury transaction; a transfer has no party and a receipt has no destination.
func TestTransactionConditionalRequired(t *testing.T) {
	op, _ := Load().Get("treasury.transactions.create")
	s := op.Spec()
	if s.ConditionalRequired["kind"]["transfer"] == nil {
		t.Fatalf("no conditional rule: %+v", s.ConditionalRequired)
	}
	base := `"date":"2025-10-07","treasury_account_id":"2"`
	if got := s.MissingRequired(map[string]string{}, rawBody(t, `{"kind":"transfer",`+base+`,"to_treasury_account_id":"3"}`)); len(got) != 0 {
		t.Fatalf("a transfer without a party must be complete: %v", got)
	}
	if got := s.MissingRequired(map[string]string{}, rawBody(t, `{"kind":"receipt",`+base+`,"party_id":"7"}`)); len(got) != 0 {
		t.Fatalf("a receipt without a destination must be complete: %v", got)
	}
	got := s.MissingRequired(map[string]string{}, rawBody(t, `{"kind":"receipt",`+base+`}`))
	if len(got) != 1 || got[0] != "party_id" {
		t.Fatalf("a receipt needs its party: %v", got)
	}
}

// The catalog JSON is embedded; a broken file must fail loudly, not silently.
func TestSpecsArePermissionFiltered(t *testing.T) {
	all := Load().Specs(func(string) bool { return true })
	if len(all) < 100 {
		t.Fatalf("expected the whole catalog, got %d", len(all))
	}
	none := Load().Specs(func(string) bool { return false })
	if len(none) >= len(all) {
		t.Fatalf("permission filtering removed nothing: %d/%d", len(none), len(all))
	}
	for _, s := range none {
		if s.ID == "treasury.accounts.create" || s.ID == "parties.create" {
			t.Fatalf("a user with no permissions saw %s", s.ID)
		}
	}
}
