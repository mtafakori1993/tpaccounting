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
		"parties.create":                    NeedsConfirmation,
		"parties.list":                      ReadOnly,
		"treasury.transactions.create":      NeedsConfirmation,
		"treasury.transactions.cancel":      NeedsApproval,
		"users.create":                      NeedsApproval,
		"treasury.transactions.allocations": NeedsConfirmation,
		"auth.login.create":                 NotSupported,
		"reports.trial_balance.list":        ReadOnly,
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
	if u, _ := c.Get("users.create"); !u.Critical {
		t.Fatal("security operations must be marked critical")
	}
	s := c.Summary(all)
	if !strings.Contains(s, "users.create") || strings.Contains(s, "auth.login") {
		t.Fatal("parity: business/admin operations are offered, session operations are not")
	}
	// Parity invariant: the only operations the assistant can never perform are
	// session/credential ones (sign-in, sign-out, setup, own password).
	for _, o := range c.Operations {
		if !o.Executable() && !strings.HasPrefix(o.Path, "/auth") && !strings.HasPrefix(o.Path, "/setup") {
			t.Errorf("parity broken: %s (%s) is not executable", o.ID, o.Classification)
		}
		if o.Executable() && !strings.Contains(s, o.ID+":") {
			t.Errorf("parity broken: %s is not offered to the planner", o.ID)
		}
	}
	t.Logf("planner catalog: %d chars, %d lines", len(s), strings.Count(s, "\n")+1)
}
