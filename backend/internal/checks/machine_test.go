package checks

import (
	"slices"
	"testing"
)

func TestReceivedLifecycle(t *testing.T) {
	steps := []struct{ from, action, to string }{
		{StatusReceived, "custody", StatusInCustody},
		{StatusInCustody, "deposit", StatusDeposited},
		{StatusDeposited, "clear", StatusCleared},
		{StatusReceived, "transfer", StatusTransferred},
		{StatusInCustody, "return", StatusReturned},
		{StatusDeposited, "return", StatusReturned},
		{StatusTransferred, "return", StatusReturned},
		{StatusReceived, "cash", StatusCleared},
		{StatusInCustody, "lost", StatusLost},
		{StatusReceived, "cancel", StatusCancelled},
	}
	for _, s := range steps {
		r, err := Next(TypeReceived, s.from, s.action)
		if err != nil || r.to != s.to {
			t.Errorf("%s --%s--> got %q, %v; want %s", s.from, s.action, r.to, err, s.to)
		}
	}
}

func TestIssuedLifecycle(t *testing.T) {
	steps := []struct{ from, action, to string }{
		{StatusDraft, "issue", StatusIssued},
		{StatusIssued, "deliver", StatusDelivered},
		{StatusDelivered, "clear", StatusCleared},
		{StatusIssued, "return", StatusReturned},
		{StatusDraft, "cancel", StatusCancelled},
		{StatusIssued, "replace", StatusReplaced},
	}
	for _, s := range steps {
		r, err := Next(TypeIssued, s.from, s.action)
		if err != nil || r.to != s.to {
			t.Errorf("%s --%s--> got %q, %v; want %s", s.from, s.action, r.to, err, s.to)
		}
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	bad := []struct{ typ, from, action string }{
		{TypeReceived, StatusCleared, "deposit"},
		{TypeReceived, StatusReturned, "clear"},
		{TypeReceived, StatusInCustody, "clear"}, // must be deposited first
		{TypeReceived, StatusCancelled, "return"},
		{TypeReceived, StatusDeposited, "transfer"},
		{TypeReceived, StatusReceived, "issue"},
		{TypeIssued, StatusDraft, "clear"},
		{TypeIssued, StatusCleared, "return"},
		{TypeIssued, StatusCancelled, "issue"},
		{TypeIssued, StatusIssued, "deposit"},
		{"other", StatusIssued, "clear"},
	}
	for _, b := range bad {
		if _, err := Next(b.typ, b.from, b.action); err == nil {
			t.Errorf("%s %s --%s--> should be rejected", b.typ, b.from, b.action)
		}
	}
}

func TestAllowedActionsAndPermissions(t *testing.T) {
	got := AllowedActions(TypeReceived, StatusInCustody)
	for _, want := range []string{"deposit", "cash", "transfer", "return", "lost", "cancel"} {
		if !slices.Contains(got, want) {
			t.Errorf("in_custody actions %v missing %s", got, want)
		}
	}
	if slices.Contains(got, "clear") || slices.Contains(got, "custody") {
		t.Errorf("unexpected actions %v", got)
	}
	if len(AllowedActions(TypeIssued, StatusCleared)) != 0 {
		t.Error("cleared issued checks are final")
	}
	if PermissionFor(TypeReceived, "deposit") != "checks.post" || PermissionFor(TypeReceived, "return") != "checks.cancel" {
		t.Error("wrong permissions")
	}
	if !IsLive(TypeReceived, StatusDeposited) || IsLive(TypeReceived, StatusCleared) || IsLive(TypeIssued, StatusDraft) {
		t.Error("wrong live statuses")
	}
}
