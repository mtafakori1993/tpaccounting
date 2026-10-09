package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"os"
	"testing"
)

// The committed catalog must be what the application's current source produces: an
// operation, form, permission or posting rule added to the app without regenerating
// would otherwise be invisible to the assistant (D28, D42).
//
//	go run ./cmd/capmap -repo ../.. -out internal/catalog/capabilities.json -md ../CAPABILITIES.md \
//	  -audit ../CAPABILITY_AUDIT.md -audit-json internal/catalog/audit.json
func TestCommittedCatalogIsCurrent(t *testing.T) {
	if _, err := os.Stat("../../../../backend/internal/server"); err != nil {
		t.Skip("application source not available")
	}
	cat, err := generate("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.MarshalIndent(cat, "", "  ")
	want, err := os.ReadFile("../../internal/catalog/capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatal("internal/catalog/capabilities.json is stale: regenerate it with cmd/capmap (see the command in this test)")
	}
	var rows []AuditRow
	for _, o := range cat.Operations {
		rows = append(rows, auditOf(o))
	}
	committed, err := os.ReadFile("../../internal/catalog/audit.json")
	if err != nil || !bytes.Equal(bytes.TrimSpace(auditJSON(rows)), bytes.TrimSpace(committed)) {
		t.Fatal("internal/catalog/audit.json is stale: regenerate it with cmd/capmap")
	}
}

// Posting is read from the call graph, not guessed from the URL.
func TestPostingAnalysis(t *testing.T) {
	if _, err := os.Stat("../../../../backend/internal/server"); err != nil {
		t.Skip("application source not available")
	}
	cat, err := generate("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Operation{}
	for _, o := range cat.Operations {
		by[o.ID] = o
	}
	for id, want := range map[string]string{
		"treasury.transactions.create": "always",
		"journal_entries.post":         "always",
		"sales.kind.post":              "always",
		"sales.kind.create":            "post",            // a draft unless post=true
		"treasury.accounts.create":     "opening_balance", // the opening entry
		"parties.create":               "",
		"employees.create":             "",
		"products.create":              "",
		"expenses.categories.create":   "", // was wrongly marked as posting by the URL rule
	} {
		o := by[id]
		got := ""
		switch {
		case o.PostingAlways:
			got = "always"
		case o.PostsEntries && len(o.PostingTriggers) > 0:
			got = o.PostingTriggers[0]
		case o.PostsEntries:
			got = "?"
		}
		if got != want {
			t.Errorf("%s posts %q, want %q", id, got, want)
		}
	}
	if o := by["employees.create"]; o.Classification == "REQUIRES_APPROVAL" {
		t.Error("employees are master data, not an approval-only operation")
	}
}

// Audit 2026-10-08: only UNCONDITIONAL checks make a field required. Checks inside
// if/switch branches, disjunctions (method != "x" || field != "") and helpers called
// by another input type made valid plans impossible (salary components).
func TestRequiredOnlyFromUnconditionalChecks(t *testing.T) {
	src := `package p
type Component struct{ Name, Method, QuantityField string }
type TaxTable struct{ Name string }
func validateComponent(c *Component) error {
	v.Required("name", c.Name)
	v.Check(c.Method != "daily" || c.QuantityField != "", "quantity_field", "required")
	if c.Method == "bank" {
		v.Required("bank_name", "")
	}
	switch c.Method {
	case "x":
		v.Check(false, "formula", "required")
	}
	return nil
}
func SaveTaxTable(in TaxTable) error {
	checkEffective(v)
	return nil
}
func checkEffective(v any) { v.Check(false, "effective_from", "required") }
`
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	p := &pkg{reqs: map[string]bool{}, typeReqs: map[string]map[string]bool{}, enums: map[string][]string{}}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			p.validators = append(p.validators, scanValidator(p, fd))
		}
	}
	p.resolveRequired()
	comp := p.typeReqs["Component"]
	if !comp["name"] || comp["quantity_field"] || comp["bank_name"] || comp["formula"] || comp["effective_from"] {
		t.Fatalf("Component required: %v", comp)
	}
	if !p.typeReqs["TaxTable"]["effective_from"] || p.reqs["effective_from"] {
		t.Fatalf("helper requirement must apply to its caller's type only: tax=%v pkg=%v", p.typeReqs["TaxTable"], p.reqs)
	}
}
