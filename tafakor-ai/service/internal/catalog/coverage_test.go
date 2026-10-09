package catalog

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// knownUIGaps: operations the assistant executes but whose real screen it cannot open
// and pre-fill yet, each with the reason (D42). A new gap fails this test, so a form
// added to the app without discoverable tags — or a regression in the generator —
// cannot go unnoticed. Shrink this list; never grow it without a reason.
var knownUIGaps = map[string]string{
	"accounts.update":                    "edit opens from the chart tree's selected-account panel (tree selection, not a row/button)",
	"attachments.create":                 "a file upload, not a form",
	"auth.preferences.update":            "language/theme toggles in the shell, not a form",
	"company.account_mappings.update":    "a grid of selects (one row per posting rule)",
	"company.logo.update":                "a file upload",
	"company.periods.update":             "per-period lock switches in a table",
	"expenses.categories.update":         "inline editing inside the categories list",
	"expenses.categories.create":         "the form is nested inside the categories manager dialog (verified live)",
	"payroll.components.defaults.create": "a single button with no fields (nothing to pre-fill)",
	"payroll.periods.inputs.update":      "a spreadsheet-like grid of employee inputs",
	"products.categories.create":         "inline input without a label, so it carries no data-ai-field tag (app tagging)",
	"roles.update":                       "permission matrix panel with a title made of data",
	"treasury.accounts.update":           "opened from the row's ⋯ menu (row-menu step not supported yet)",
	"products.categories.update":         "APPLICATION_LIMITATION: the app has no screen to rename a product category",
}

type auditRow struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Policy string `json:"policy"`
}

func TestCapabilityCoverageDoesNotRegress(t *testing.T) {
	b, err := os.ReadFile("audit.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []auditRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	gaps := map[string]bool{}
	for _, r := range rows {
		switch r.Status {
		case "MISSING_UI_CAPABILITY", "APPLICATION_LIMITATION":
			gaps[r.ID] = true
			if _, ok := knownUIGaps[r.ID]; !ok {
				t.Errorf("new UI gap %s (%s): make its form discoverable, or document why here", r.ID, r.Status)
			}
		case "UNSAFE_TO_AUTOMATE":
			if r.Policy != "NOT_OFFERED" {
				t.Errorf("%s unsafe but offered", r.ID)
			}
		}
	}
	var closed []string
	for id := range knownUIGaps {
		if !gaps[id] {
			closed = append(closed, id)
		}
	}
	sort.Strings(closed)
	if len(closed) > 0 {
		t.Errorf("gaps closed — remove them from knownUIGaps: %v", closed)
	}
}

// Policy parity (D40): ordinary master data the user asked for runs directly;
// money movements and accounting documents ask; approval-class operations always ask.
func TestPolicyClassesComeFromTheContract(t *testing.T) {
	c := Load()
	for _, tc := range []struct {
		op, body string
		want     bool
	}{
		{"employees.create", `{"first_name":"علی","last_name":"رضایی","base_salary":"150000000"}`, true}, // a salary is an attribute, not a posting
		{"products.create", `{"name":"لپ‌تاپ","sales_price":"900000000","kind":"goods"}`, true},
		{"parties.create", `{"name":"شرکت قاسم","kind":"company","is_customer":true}`, true},
		{"treasury.accounts.create", `{"kind":"bank","name":"رفاه","bank_name":"رفاه","opening_balance":"0"}`, true},
		{"treasury.accounts.create", `{"kind":"bank","name":"رفاه","bank_name":"رفاه","opening_balance":"10000000"}`, false},
		{"sales.kind.create", `{"party_id":1}`, false}, // an accounting document (D15)
		{"expenses.categories.create", `{"name":"x"}`, true},
		{"users.create", `{"email":"a@b.c"}`, false}, // security: approval
	} {
		op, ok := c.Get(tc.op)
		if !ok {
			t.Fatalf("unknown %s", tc.op)
		}
		if got := op.RoutineChange(json.RawMessage(tc.body)); got != tc.want {
			t.Errorf("%s %s routine=%v, want %v", tc.op, tc.body, got, tc.want)
		}
	}
	if op, _ := c.Get("employees.update"); op.Classification == NeedsApproval {
		t.Error("employee records must not be approval-only")
	}
}
