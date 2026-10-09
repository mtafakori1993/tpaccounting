package policy

import "github.com/tafakorpardazan/tafakor-ai/internal/catalog"

// StepFacts are the policy-relevant facts of one pending step of a plan.
type StepFacts struct {
	Tool           string `json:"tool"`
	Classification string `json:"classification"`
	NeverAuto      bool   `json:"never_auto"`
	// Routine: an ordinary non-financial create/update (catalog.Operation.RoutineChange).
	Routine bool `json:"routine"`
}

// PlanFacts are the facts a plan decision is made from.
type PlanFacts struct {
	Steps           []StepFacts `json:"steps"`
	HasPermission   bool        `json:"has_permission"`
	Requested       bool        `json:"requested"`        // the user explicitly asked for this change
	FromDocument    bool        `json:"from_document"`    // values came from an uploaded document
	Duplicate       bool        `json:"duplicate"`        // a record like the one to create already exists
	ExplicitDirect  bool        `json:"explicit_direct"`  // «مستقیم ثبت کن»
	ExplicitPreview bool        `json:"explicit_preview"` // «روی صفحه نشانم بده»
	SupportsPreview bool        `json:"supports_preview"`
	AskThisTime     bool        `json:"ask_this_time"`
}

// DecidePlan decides how a multi-step operation is carried out (D40). Changing data
// is not by itself a reason to ask: the decision follows each step's risk, the
// operation's approval class, permission and the user's own words.
//
//	no permission                         → BLOCKED
//	"show me on screen" (form exists)     → UI_PREVIEW
//	any step REQUIRES_APPROVAL / never-auto → APPROVAL_REQUIRED (an explicit "directly" cannot skip it)
//	duplicate, from a document, not requested, "ask me this time" → CONFIRM
//	explicit "directly"                   → DIRECT
//	every step routine (non-financial)    → DIRECT
//	otherwise (money moves / entries post) → CONFIRM
func DecidePlan(f PlanFacts) Decision {
	d := Decision{Reasons: []string{}}
	if !f.HasPermission {
		d.Reasons = append(d.Reasons, "no_permission")
		d.Action, d.Mode = ActionBlocked, Blocked
		return d
	}
	approval, routine := false, len(f.Steps) > 0
	for _, s := range f.Steps {
		if s.Classification == catalog.NeedsApproval || s.NeverAuto {
			approval = true
		}
		if !s.Routine {
			routine = false
		}
	}
	if f.ExplicitPreview && f.SupportsPreview {
		d.Action, d.Mode = ActionUIPreview, Human
		return d
	}
	if approval {
		d.Reasons = append(d.Reasons, "action_requires_approval")
		d.Action, d.Mode = ActionApproval, Human
		return d
	}
	soft := map[string]bool{"not_duplicate": f.Duplicate, "not_from_document_text": f.FromDocument,
		"explicitly_requested": !f.Requested, "not_asked_this_time": f.AskThisTime}
	for _, r := range []string{"not_duplicate", "not_from_document_text", "explicitly_requested", "not_asked_this_time"} {
		if soft[r] {
			d.Reasons = append(d.Reasons, r)
		}
	}
	switch {
	case len(d.Reasons) > 0:
		d.Action, d.Mode = ActionConfirm, Human
	case f.ExplicitDirect, routine:
		d.Action, d.Mode = ActionDirect, Auto
	default:
		d.Reasons = append(d.Reasons, "plan_changes_financial_data")
		d.Action, d.Mode = ActionConfirm, Human
	}
	return d
}
