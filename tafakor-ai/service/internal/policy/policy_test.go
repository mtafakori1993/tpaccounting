package policy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

func autoSettings() store.UserSettings {
	s := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1})
	s.AutoMode = true
	return s
}

func cleanFacts() Facts {
	return Facts{Action: "create_receipt", Amount: decimal.NewFromInt(5_000_000), Currency: "IRR", HasPermission: true}
}

func TestDefaultIsHumanApproval(t *testing.T) {
	s := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1})
	d := Decide(Input{Facts: cleanFacts(), Settings: s})
	if d.Mode != Human || d.Reasons[0] != "auto_switch_on" {
		t.Fatalf("default must ask the human: %+v", d)
	}
}

func TestCleanCaseIsAutoWhenSwitchOn(t *testing.T) {
	d := Decide(Input{Facts: cleanFacts(), Settings: autoSettings()})
	if d.Mode != Auto {
		t.Fatalf("expected auto, got %+v", d.Reasons)
	}
}

// Each guardrail individually forces human approval in auto mode.
func TestEachGuardrailForcesHuman(t *testing.T) {
	cases := map[string]func(*Facts, *Input){
		"not_duplicate":            func(f *Facts, _ *Input) { f.Duplicate = true },
		"no_conflict":              func(f *Facts, _ *Input) { f.Conflict = true },
		"not_ambiguous":            func(f *Facts, _ *Input) { f.Ambiguous = true },
		"confident_fields":         func(f *Facts, _ *Input) { f.LowConfidence = true },
		"open_period":              func(f *Facts, _ *Input) { f.PeriodBlocked = true },
		"no_budget_event":          func(f *Facts, _ *Input) { f.BudgetEvent = true },
		"no_validation_issue":      func(f *Facts, _ *Input) { f.ValidationIssue = true },
		"explicitly_requested":     func(f *Facts, _ *Input) { f.NotRequested = true },
		"not_from_document_text":   func(f *Facts, _ *Input) { f.FromDocumentText = true },
		"base_currency":            func(f *Facts, _ *Input) { f.ForeignCurrency = true },
		"rule_trusted":             func(f *Facts, _ *Input) { f.LearnedSuggest = true },
		"within_amount_limit":      func(f *Facts, _ *Input) { f.Amount = decimal.NewFromInt(100_000_001) },
		"within_daily_cap":         func(_ *Facts, in *Input) { in.AutoToday = 10 },
		"company_allows_auto":      func(_ *Facts, in *Input) { in.CompanyLock = true },
		"not_asked_this_time":      func(_ *Facts, in *Input) { in.AskThisTime = true },
		"action_never_automatic":   func(f *Facts, _ *Input) { f.Action = "cancel_document" },
		"action_allowed_automatic": func(f *Facts, _ *Input) { f.Action = "create_payment" },
	}
	for name, mut := range cases {
		f := cleanFacts()
		in := Input{Settings: autoSettings()}
		mut(&f, &in)
		in.Facts = f
		d := Decide(in)
		if d.Mode == Auto {
			t.Errorf("%s: guardrail did not force human approval", name)
		}
		found := false
		for _, r := range d.Reasons {
			if r == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: reason missing, got %v", name, d.Reasons)
		}
	}
}

func TestNeverAutoActions(t *testing.T) {
	for a := range NeverAuto {
		f := cleanFacts()
		f.Action = a
		s := autoSettings()
		s.AutoActions = append(s.AutoActions, a) // even if someone stored it
		if Decide(Input{Facts: f, Settings: s}).Mode == Auto {
			t.Errorf("%s must never be automatic", a)
		}
	}
}

func TestUnknownCurrencyNeverAuto(t *testing.T) {
	f := cleanFacts()
	f.Currency = "GBP"
	if Decide(Input{Facts: f, Settings: autoSettings()}).Mode == Auto {
		t.Fatal("currency without a limit must not auto-register")
	}
}

func TestNoPermissionBlocks(t *testing.T) {
	f := cleanFacts()
	f.HasPermission = false
	if Decide(Input{Facts: f, Settings: autoSettings()}).Mode != Blocked {
		t.Fatal("must block")
	}
}

func TestSimulation(t *testing.T) {
	s := autoSettings()
	s.Simulation = true
	d := Decide(Input{Facts: cleanFacts(), Settings: s})
	if d.Mode != Human || !d.Simulated || !d.WouldAuto {
		t.Fatalf("simulation must not execute: %+v", d)
	}
}

func TestSwitchRequiresRiskConfirmationAndRespectsLock(t *testing.T) {
	s := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1})
	if err := ApplySwitch(&s, true, false, false, time.Now()); err == nil || s.AutoMode {
		t.Fatal("auto ON needs a risk confirmation")
	}
	if err := ApplySwitch(&s, true, true, true, time.Now()); err == nil || s.AutoMode {
		t.Fatal("company lock must forbid auto")
	}
	if err := ApplySwitch(&s, true, true, false, time.Now()); err != nil || !s.AutoMode {
		t.Fatal("confirmed switch failed")
	}
	if err := ApplySwitch(&s, false, false, true, time.Now()); err != nil || s.AutoMode {
		t.Fatal("turning OFF must be instant and always allowed")
	}
}

func TestSettingsValidation(t *testing.T) {
	s := autoSettings()
	s.AutoActions = []string{"create_journal_entry"}
	if ValidateSettings(&s) == nil {
		t.Fatal("journal entries cannot be enabled for auto")
	}
	s = autoSettings()
	s.MaxAmount["IRR"] = "-1"
	if ValidateSettings(&s) == nil {
		t.Fatal("negative limit accepted")
	}
}

// The user's own switches, caps and confidence are overridden by an explicit
// "register directly" instruction; the hard risks are not.
func TestExplicitDirectOverridesOnlySoftReasons(t *testing.T) {
	off := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1}) // auto switch OFF
	direct := cleanFacts()
	direct.ExplicitDirect, direct.SupportsDirect = true, true

	d := Decide(Input{Facts: direct, Settings: off})
	if d.Action != ActionDirect || d.Mode != Auto {
		t.Fatalf("explicit direct must execute: %+v", d)
	}
	if !hasReason(d.Warnings, "auto_switch_on") || len(d.Reasons) != 0 {
		t.Fatalf("override must be reported as a warning: %+v", d)
	}

	// over the amount cap directly: runs, and says so
	f := direct
	f.Amount = decimal.NewFromInt(100_000_001)
	d = Decide(Input{Facts: f, Settings: off})
	if d.Action != ActionDirect || !hasReason(d.Warnings, "within_amount_limit") {
		t.Fatalf("explicit direct must beat the configured cap and warn: %+v", d)
	}

	// hard risks still keep the confirmation
	for name, mut := range map[string]func(*Facts){
		"no_conflict":            func(f *Facts) { f.Conflict = true },
		"not_ambiguous":          func(f *Facts) { f.Ambiguous = true },
		"open_period":            func(f *Facts) { f.PeriodBlocked = true },
		"no_budget_event":        func(f *Facts) { f.BudgetEvent = true },
		"no_validation_issue":    func(f *Facts) { f.ValidationIssue = true },
		"not_from_document_text": func(f *Facts) { f.FromDocumentText = true },
		"base_currency":          func(f *Facts) { f.ForeignCurrency = true },
		"company_allows_auto":    func(*Facts) {}, // set through the input below
	} {
		f := direct
		mut(&f)
		in := Input{Facts: f, Settings: off}
		if name == "company_allows_auto" {
			in.CompanyLock = true
		}
		d := Decide(in)
		if d.Mode != Human || d.Action == ActionDirect {
			t.Fatalf("%s must still confirm despite the direct instruction: %+v", name, d)
		}
	}

	// a never-auto action or a REQUIRES_APPROVAL operation is never direct
	f = direct
	f.Action = "cancel_document"
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionApproval {
		t.Fatalf("never-auto must keep approval: %+v", d)
	}
	f = direct
	f.Classification = catalog.NeedsApproval
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionApproval {
		t.Fatalf("REQUIRES_APPROVAL must keep approval: %+v", d)
	}

	// without permission nothing runs
	f = direct
	f.HasPermission = false
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionBlocked || d.Mode != Blocked {
		t.Fatalf("no permission must block even a direct instruction: %+v", d)
	}

	// an operation that cannot be run directly falls back to the card
	f = direct
	f.SupportsDirect = false
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionConfirm {
		t.Fatalf("unsupported direct must confirm: %+v", d)
	}
}

func TestExplicitPreviewSelectsUIPreview(t *testing.T) {
	off := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1})
	f := cleanFacts()
	f.ExplicitPreview, f.SupportsPreview = true, true
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionUIPreview || d.Mode != Human {
		t.Fatalf("preview must open the form and never execute: %+v", d)
	}
	// a form that cannot express the payload falls back to a normal confirmation
	f.SupportsPreview = false
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionConfirm {
		t.Fatalf("unexpressible payload must confirm: %+v", d)
	}
	// preview wins over a direct instruction in the same turn only if preview was asked;
	// when both are set, seeing it on screen is the safer reading
	f.SupportsPreview, f.ExplicitDirect, f.SupportsDirect = true, true, true
	if d := Decide(Input{Facts: f, Settings: off}); d.Action != ActionUIPreview {
		t.Fatalf("preview must be honoured: %+v", d)
	}
}

// Existing callers that set none of the new facts keep the old behaviour.
func TestActionModesAreDerivedForPlainRequests(t *testing.T) {
	off := store.DefaultUserSettings(store.Scope{CompanyID: 1, UserID: 1})
	if d := Decide(Input{Facts: cleanFacts(), Settings: off}); d.Action != ActionConfirm {
		t.Fatalf("a plain request confirms: %+v", d)
	}
	if d := Decide(Input{Facts: cleanFacts(), Settings: autoSettings()}); d.Action != ActionDirect || d.Mode != Auto {
		t.Fatalf("the switch alone still runs directly: %+v", d)
	}
	f := cleanFacts()
	f.Classification = catalog.NeedsApproval
	if d := Decide(Input{Facts: f, Settings: autoSettings()}); d.Action != ActionApproval {
		t.Fatalf("REQUIRES_APPROVAL always needs approval: %+v", d)
	}
}
