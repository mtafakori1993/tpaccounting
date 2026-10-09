package catalog

import (
	"encoding/json"
	"strings"
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
				for _, alt := range strings.Split(f, "|") { // "a|b": one of them
					if !known[alt] {
						t.Fatalf("variants %s: unknown field %s", id, alt)
					}
				}
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
	// bank_name is required for a bank account only (the generator used to mark it
	// unconditional, which also blocked cash boxes); the variant rule carries it.
	if !has(s.RequiredFields, "name") || !has(s.RequiredFields, "kind") || has(s.RequiredFields, "bank_name") {
		t.Fatalf("required fields %v", s.RequiredFields)
	}
	if bank := s.ConditionalRequired["kind"]["bank"]; !has(bank, "bank_name") || !has(bank, "name") {
		t.Fatalf("bank variant %v", bank)
	}
	if !s.ConfirmationRequired || !s.ExplicitDirectSkipsConfirm || s.ApprovalRequired {
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

	if got := s.MissingRequired(path, rawBody(t, `{"kind":"bank","name":"Parsian","bank_name":"Parsian"}`)); len(got) != 0 {
		t.Fatalf("complete body reported missing %v", got)
	}
	if got := s.MissingRequired(path, rawBody(t, `{"kind":"bank","opening_balance":"100000000","bank_name":"Parsian"}`)); len(got) != 1 || got[0] != "name" {
		t.Fatalf("missing name not detected: %v", got)
	}
	// the exact reported case: opening_balance present, bank_name absent
	got := s.MissingRequired(path, rawBody(t, `{"kind":"bank","opening_balance":"100000000","name":"Current"}`))
	if len(got) != 1 || got[0] != "bank_name" {
		t.Fatalf("missing bank_name not detected before confirmation: %v", got)
	}
	// a cash box has no bank
	if got := s.MissingRequired(path, rawBody(t, `{"kind":"cash","name":"Cash"}`)); len(got) != 0 {
		t.Fatalf("cash box reported missing %v", got)
	}
	// an explicit "none" satisfies a required field (the app may not enforce it)
	if got := s.MissingRequired(path, rawBody(t, `{"kind":"bank","name":"Cash","bank_name":null}`)); len(got) != 0 {
		t.Fatalf("explicit none must satisfy: %v", got)
	}
	if got := s.MissingRequired(path, rawBody(t, `{"kind":"bank","name":"Cash","bank_name":""}`)); len(got) != 1 {
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

// Regression (audit 2026-10-08): «به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان».
// The generator marked quantity_field, brackets, insurance_share and (from a helper
// of another type) effective_from as always required, so the planner's correct
// fixed-amount component was rejected seven times and the task stopped with
// "I couldn't work out a safe way". Conditional rules apply only to their method.
func TestSalaryComponentConditionalRequired(t *testing.T) {
	op, _ := Load().Get("payroll.components.create")
	s := op.Spec()
	for _, f := range []string{"quantity_field", "brackets", "insurance_share", "effective_from"} {
		if has(s.RequiredFields, f) {
			t.Fatalf("%s must not be unconditionally required: %v", f, s.RequiredFields)
		}
	}
	fixed := `{"code":"travel_allowance","name":"کمک هزینه سفر","kind":"earning","category":"allowance","method":"fixed","rate":"30000000","quantity_field":"","brackets":[]}`
	if got := s.MissingRequired(nil, rawBody(t, fixed)); len(got) != 0 {
		t.Fatalf("fixed allowance reported missing %v", got)
	}
	daily := `{"code":"trip","name":"سفر","kind":"earning","method":"daily_base","rate":"100"}`
	if got := s.MissingRequired(nil, rawBody(t, daily)); len(got) != 1 || got[0] != "quantity_field" {
		t.Fatalf("daily_base needs quantity_field: %v", got)
	}
}

// Every check transition used to require party_id, reason and treasury_account_id
// (branch-local checks of individual transitions); and checks/expenses required
// fields of the other variant.
func TestBranchLocalChecksAreNotRequired(t *testing.T) {
	for id, not := range map[string][]string{
		"checks.transitions": {"party_id", "reason", "treasury_account_id"},
		"checks.create":      {"received_date", "treasury_account_id", "bank_id"},
		"expenses.create":    {"party_id", "treasury_account_id"},
	} {
		op, _ := Load().Get(id)
		for _, f := range not {
			if has(op.Spec().RequiredFields, f) {
				t.Fatalf("%s: %s must be conditional", id, f)
			}
		}
	}
	op, _ := Load().Get("checks.create")
	if got := op.Spec().MissingRequired(nil, rawBody(t, `{"check_type":"issued","party_id":1,"amount":"5","issue_date":"2026-10-01","due_date":"2026-11-01"}`)); !has(got, "treasury_account_id") {
		t.Fatalf("issued check needs its bank account: %v", got)
	}
}

// A received check needs the bank by id OR by name (the app: BankID != nil || BankName != "").
func TestOneOfRequiredField(t *testing.T) {
	op, _ := Load().Get("checks.create")
	s := op.Spec()
	body := func(js string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		_ = json.Unmarshal([]byte(js), &m)
		return m
	}
	base := `"check_type":"received","check_number":"1","party_id":2,"amount":"10","issue_date":"2025-10-01","due_date":"2025-11-01","received_date":"2025-10-01"`
	if m := s.MissingRequired(nil, body("{"+base+"}")); len(m) != 1 || m[0] != "bank_id|bank_name" {
		t.Fatalf("missing %v", m)
	}
	for _, extra := range []string{`,"bank_id":3`, `,"bank_name":"ملت"`} {
		if m := s.MissingRequired(nil, body("{"+base+extra+"}")); len(m) != 0 {
			t.Fatalf("%s: missing %v", extra, m)
		}
	}
}

// Only paying a payroll period needs the bank account (the other actions share its input type).
func TestPayrollActionsNeedNoTreasuryExceptPay(t *testing.T) {
	for _, id := range []string{"payroll.periods.approve", "payroll.post", "payroll.periods.calculate"} {
		op, _ := Load().Get(id)
		for _, f := range op.Spec().RequiredFields {
			if f == "treasury_account_id" {
				t.Errorf("%s must not require treasury_account_id", id)
			}
		}
	}
	op, _ := Load().Get("payroll.periods.pay")
	if !strings.Contains(strings.Join(op.Spec().RequiredFields, ","), "treasury_account_id") {
		t.Error("pay requires the treasury account")
	}
}
