package catalog

import (
	"strings"
	"testing"
)

func all(string) bool { return true }

func TestCatalogLoadsAndClassifies(t *testing.T) {
	c := Load()
	if len(c.Operations) < 150 {
		t.Fatalf("catalog too small: %d", len(c.Operations))
	}
	must := map[string]string{
		"parties.create":               NeedsConfirmation,
		"parties.list":                 ReadOnly,
		"treasury.transactions.create": NeedsConfirmation,
		"treasury.transactions.cancel": NeedsApproval,
		"users.create":                 NotSafe,
		"auth.login.create":            NotSupported,
		"reports.trial_balance.list":   ReadOnly,
	}
	for id, cl := range must {
		o, ok := c.Get(id)
		if !ok {
			t.Errorf("missing %s", id)
			continue
		}
		if o.Classification != cl {
			t.Errorf("%s: %s want %s", id, o.Classification, cl)
		}
	}
	tx, _ := c.Get("treasury.transactions.create")
	if strings.Join(tx.Permissions(), ",") != "treasury.create,treasury.post" {
		t.Fatalf("both route permissions must be required: %v", tx.Permissions())
	}
	ok, missing := tx.Allowed(func(p string) bool { return p == "treasury.create" })
	if ok || missing != "treasury.post" {
		t.Fatal("permission check must match the app's route middleware")
	}
	s := c.Summary(all)
	if strings.Contains(s, "users.create") || strings.Contains(s, "auth.login") {
		t.Fatal("restricted operations must not be offered to the planner")
	}
	t.Logf("planner catalog: %d chars, %d lines", len(s), strings.Count(s, "\n")+1)
}
