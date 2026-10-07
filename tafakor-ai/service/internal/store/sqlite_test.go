package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *SQLite {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUserSettingsDefaultAndPersistence(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sc := Scope{CompanyID: 1, UserID: 7}
	u, err := s.GetUserSettings(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if u.AutoMode {
		t.Fatal("auto mode must default to off (ask me before registering)")
	}
	u.AutoMode = true
	if err := s.SaveUserSettings(ctx, u); err != nil {
		t.Fatal(err)
	}
	u2, _ := s.GetUserSettings(ctx, sc)
	if !u2.AutoMode {
		t.Fatal("setting not persisted")
	}
	other, _ := s.GetUserSettings(ctx, Scope{CompanyID: 1, UserID: 8})
	if other.AutoMode {
		t.Fatal("settings leaked to another user")
	}
	otherCo, _ := s.GetUserSettings(ctx, Scope{CompanyID: 2, UserID: 7})
	if otherCo.AutoMode {
		t.Fatal("settings leaked to another company")
	}
}

func TestRuleScoping(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mine := Rule{CompanyID: 1, UserID: 7, Scope: "personal", Kind: "expense_category", Pattern: "vpn", Result: json.RawMessage(`{"id":3}`), Source: "correction", Status: "active", TimesConfirmed: 1}
	if err := s.SaveRule(ctx, &mine); err != nil {
		t.Fatal(err)
	}
	co := Rule{CompanyID: 1, UserID: 9, Scope: "company", Kind: "expense_category", Pattern: "hosting", Result: json.RawMessage(`{"id":4}`), Source: "promoted", Status: "active", TimesConfirmed: 3}
	_ = s.SaveRule(ctx, &co)
	theirs := Rule{CompanyID: 1, UserID: 9, Scope: "personal", Kind: "expense_category", Pattern: "taxi", Result: json.RawMessage(`{"id":5}`), Source: "correction", Status: "active"}
	_ = s.SaveRule(ctx, &theirs)
	otherCo := Rule{CompanyID: 2, UserID: 7, Scope: "company", Kind: "expense_category", Pattern: "x", Result: json.RawMessage(`{}`), Source: "manual", Status: "active"}
	_ = s.SaveRule(ctx, &otherCo)

	rules, err := s.ListRules(ctx, Scope{1, 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("user 7 must see own personal + company rules, got %d", len(rules))
	}
	if _, err := s.GetRule(ctx, Scope{1, 7}, theirs.ID); err != ErrNotFound {
		t.Fatal("another user's personal rule must be invisible")
	}
	if err := s.DeleteRule(ctx, Scope{1, 7}, theirs.ID); err != ErrNotFound {
		t.Fatal("must not delete another user's personal rule")
	}
	if err := s.DeleteRule(ctx, Scope{1, 7}, mine.ID); err != nil {
		t.Fatal(err)
	}
	rules, _ = s.ListRules(ctx, Scope{1, 7})
	if len(rules) != 1 {
		t.Fatal("rule not deleted")
	}
}

func TestApprovalSingleUseExpiryAndBinding(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	now := time.Now()
	tok := ApprovalToken{TokenHash: "h1", CompanyID: 1, UserID: 7, TaskID: "t1", PayloadHash: "p1", ExpiresAt: now.Add(time.Minute)}
	if err := s.SaveApproval(ctx, tok); err != nil {
		t.Fatal(err)
	}
	tampered := tok
	tampered.PayloadHash = "p2"
	if err := s.ConsumeApproval(ctx, tampered, now); err != ErrApprovalInvalid {
		t.Fatal("token must be bound to the payload")
	}
	otherUser := tok
	otherUser.UserID = 8
	if err := s.ConsumeApproval(ctx, otherUser, now); err != ErrApprovalInvalid {
		t.Fatal("token must be bound to the user")
	}
	if err := s.ConsumeApproval(ctx, tok, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeApproval(ctx, tok, now); err != ErrApprovalInvalid {
		t.Fatal("token must be single-use")
	}
	exp := ApprovalToken{TokenHash: "h2", CompanyID: 1, UserID: 7, TaskID: "t1", PayloadHash: "p1", ExpiresAt: now.Add(-time.Second)}
	_ = s.SaveApproval(ctx, exp)
	if err := s.ConsumeApproval(ctx, exp, now); err != ErrApprovalInvalid {
		t.Fatal("expired token accepted")
	}
}

func TestAuditRetentionAndAutoCount(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	sc := Scope{1, 7}
	_ = s.AppendAudit(ctx, AuditEntry{CompanyID: 1, UserID: 7, TaskID: "a", Kind: "auto_registered"})
	_ = s.AppendAudit(ctx, AuditEntry{CompanyID: 1, UserID: 7, TaskID: "a", Kind: "request"})
	n, _ := s.CountAutoToday(ctx, sc, time.Now().Add(-time.Hour))
	if n != 1 {
		t.Fatalf("auto count %d", n)
	}
	purged, err := s.PurgeAudit(ctx, time.Now().Add(time.Hour))
	if err != nil || purged != 2 {
		t.Fatalf("purge %d %v", purged, err)
	}
}

func TestTaskIsolation(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	tk := Task{ID: "x", CompanyID: 1, UserID: 7, Status: "running", Request: "hi"}
	_ = s.SaveTask(ctx, &tk)
	if _, err := s.GetTask(ctx, Scope{1, 8}, "x"); err != ErrNotFound {
		t.Fatal("task visible to another user")
	}
	// another user cannot overwrite the task by reusing its id
	evil := Task{ID: "x", CompanyID: 1, UserID: 8, Status: "done", Request: "evil"}
	_ = s.SaveTask(ctx, &evil)
	got, _ := s.GetTask(ctx, Scope{1, 7}, "x")
	if got.Status != "running" {
		t.Fatal("task overwritten by another user")
	}
}
