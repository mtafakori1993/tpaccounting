package policy

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

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
