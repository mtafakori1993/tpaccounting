package policy

import (
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
)

func TestDecidePlan(t *testing.T) {
	routine := StepFacts{Tool: "parties.create", Classification: catalog.NeedsConfirmation, Routine: true}
	money := StepFacts{Tool: "treasury.transactions.create", Classification: catalog.NeedsConfirmation}
	approval := StepFacts{Tool: "users.create", Classification: catalog.NeedsApproval}
	base := func(steps ...StepFacts) PlanFacts {
		return PlanFacts{Steps: steps, HasPermission: true, Requested: true, SupportsPreview: true}
	}
	cases := []struct {
		name string
		f    PlanFacts
		want ActionMode
	}{
		{"routine change runs directly", base(routine), ActionDirect},
		{"money asks", base(routine, money), ActionConfirm},
		{"explicit directly runs money", func() PlanFacts { f := base(money); f.ExplicitDirect = true; return f }(), ActionDirect},
		{"approval class can't be skipped", func() PlanFacts { f := base(approval); f.ExplicitDirect = true; return f }(), ActionApproval},
		{"preview wins", func() PlanFacts { f := base(routine); f.ExplicitPreview = true; return f }(), ActionUIPreview},
		{"duplicate asks", func() PlanFacts { f := base(routine); f.Duplicate = true; return f }(), ActionConfirm},
		{"from a document asks", func() PlanFacts { f := base(routine); f.FromDocument = true; return f }(), ActionConfirm},
		{"ask this time", func() PlanFacts { f := base(routine); f.AskThisTime = true; return f }(), ActionConfirm},
		{"no permission", func() PlanFacts { f := base(routine); f.HasPermission = false; return f }(), ActionBlocked},
	}
	for _, tc := range cases {
		if got := DecidePlan(tc.f).Action; got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}

// Audit 2026-10-08: checks, journal entries, invoices and stock documents are never
// automatic, also as plan steps and also when the user says «مستقیم ثبت کن».
func TestNeverAutoToolsNeedApprovalEvenWhenDirect(t *testing.T) {
	for tool := range NeverAutoTools {
		d := DecidePlan(PlanFacts{HasPermission: true, Requested: true, ExplicitDirect: true,
			Steps: []StepFacts{{Tool: tool, Classification: "REQUIRES_USER_CONFIRMATION", NeverAuto: NeverAutoTools[tool]}}})
		if d.Action != ActionApproval {
			t.Fatalf("%s: %s", tool, d.Action)
		}
	}
}

func TestNeverAutoToolsExistInTheCatalog(t *testing.T) {
	for tool := range NeverAutoTools {
		if _, ok := catalog.Load().Get(tool); !ok {
			t.Fatalf("unknown tool %s", tool)
		}
	}
}
