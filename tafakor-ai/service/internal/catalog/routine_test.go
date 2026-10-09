package catalog

import (
	"encoding/json"
	"testing"
)

// D40: "routine" is derived from each operation's own contract — not from a list of names.
func TestRoutineChangeFollowsTheContract(t *testing.T) {
	c := Load()
	cases := []struct {
		op, body string
		want     bool
	}{
		{"parties.create", `{"name":"شرکت قاسم","kind":"company","is_customer":true,"mobile":"09357578808"}`, true},
		{"parties.update", `{"name":"x","phone":"021"}`, true},
		{"inventory.warehouses.create", `{"name":"W2"}`, true},
		{"treasury.accounts.create", `{"kind":"bank","name":"پارسیان","bank_name":"پارسیان"}`, true},
		{"treasury.accounts.create", `{"kind":"bank","name":"پارسیان","bank_name":"پارسیان","opening_balance":"100000000"}`, false}, // money
		{"sales.kind.create", `{"party_id":1,"items":[{"description":"x","quantity":"1","unit_price":"190000000"}]}`, false},        // money in lines
		{"treasury.transactions.create", `{"kind":"payment","amount":"5"}`, false},                                                  // accounting impact
		{"journal_entries.create", `{}`, false},                                                                                     // accounting impact
		{"sales.kind.post", `{}`, false},                                                                                            // workflow
	}
	for _, tc := range cases {
		op, ok := c.Get(tc.op)
		if !ok {
			t.Fatalf("unknown op %s", tc.op)
		}
		if got := op.RoutineChange(json.RawMessage(tc.body)); got != tc.want {
			t.Errorf("%s %s → %v, want %v", tc.op, tc.body, got, tc.want)
		}
	}
	for _, o := range c.Operations {
		if o.Classification == NeedsApproval && o.RoutineChange(nil) {
			t.Errorf("%s needs approval but was classified routine", o.ID)
		}
	}
}

func TestSemanticName(t *testing.T) {
	c := Load()
	get := func(id string) *Operation { o, _ := c.Get(id); return o }
	for _, tc := range []struct{ op, kind, body, want string }{
		{"parties.create", "", `{"is_customer":true}`, "create_customer"},
		{"parties.create", "", `{"is_supplier":true}`, "create_supplier"},
		{"sales.kind.create", "quotations", `{}`, "create_sales_quotation"},
		{"purchases.kind.create", "invoices", `{}`, "create_purchase_invoice"},
		{"inventory.warehouses.create", "", `{}`, "create_warehouse"},
	} {
		if got := get(tc.op).SemanticName(tc.kind, json.RawMessage(tc.body)); got != tc.want {
			t.Errorf("%s → %s, want %s", tc.op, got, tc.want)
		}
	}
}
