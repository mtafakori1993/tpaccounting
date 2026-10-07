// Package learning remembers the user's corrections as rules that improve future
// suggestions. Rules are only created from an explicit "remember this" UI action on
// an offer the server generated from the user's own correction — never from text in
// documents or pages. Rules influence suggestions only; they cannot touch the
// approval switch, limits or any guardrail.
package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// Rule kinds.
const (
	ExpenseCategory = "expense_category" // subject keyword → expense category
	EventAccount    = "event_account"    // event type (+party) → counter ledger account
	PaymentDefault  = "payment_default"  // payment method → treasury account
	PartyDefault    = "party_default"    // party → default treasury account / project
	PartyAlias      = "party_alias"      // spoken/written name → party
)

var kinds = map[string]bool{ExpenseCategory: true, EventAccount: true, PaymentDefault: true, PartyDefault: true, PartyAlias: true}

// TrustApply is the number of confirmations after which a rule is applied by default
// (still shown in the preview). Below it the rule is only suggested.
const TrustApply = 3

// Result is what a rule points to.
type Result struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Field string `json:"field"` // category_id | counter_account_id | treasury_account_id | party_id | cost_center_id
}

// Offer is a server-generated "remember this?" proposal from a user correction.
type Offer struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Result  Result `json:"result"`
	Label   string `json:"label"`
}

// Match is the outcome of a lookup.
type Match struct {
	Rule     *store.Rule `json:"rule,omitempty"`
	Result   Result      `json:"result"`
	Stage    string      `json:"stage"` // suggest | apply
	Conflict []Result    `json:"conflict,omitempty"`
}

var ErrConflict = errors.New("conflicting rules")

// Pattern normalises a match key.
func Pattern(kind, s string) string {
	return kind + ":" + normalize.Text(s)
}

type Service struct {
	Store store.Repository
}

func decodeResult(r store.Rule) Result {
	var res Result
	_ = json.Unmarshal(r.Result, &res)
	return res
}

// Lookup finds active rules for kind whose pattern matches key exactly or (for keyword
// kinds) is contained in text. Distinct results → conflict (the caller must ask).
func (s *Service) Lookup(ctx context.Context, sc store.Scope, kind, key, text string) (*Match, error) {
	rules, err := s.Store.ListRules(ctx, sc)
	if err != nil {
		return nil, err
	}
	want := Pattern(kind, key)
	nt := " " + normalize.Text(text) + " "
	var hits []store.Rule
	for _, r := range rules {
		if r.Kind != kind || r.Status != "active" {
			continue
		}
		if r.Pattern == want && key != "" {
			hits = append(hits, r)
			continue
		}
		if kind == ExpenseCategory && text != "" {
			kw := strings.TrimPrefix(r.Pattern, kind+":")
			if kw != "" && strings.Contains(nt, " "+kw+" ") {
				hits = append(hits, r)
			}
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	distinct := map[string]Result{}
	for _, h := range hits {
		res := decodeResult(h)
		distinct[fmt.Sprintf("%s:%d", res.Field, res.ID)] = res
	}
	if len(distinct) > 1 {
		m := &Match{}
		for _, r := range distinct {
			m.Conflict = append(m.Conflict, r)
		}
		return m, ErrConflict
	}
	best := hits[0]
	for _, h := range hits {
		if h.TimesConfirmed > best.TimesConfirmed {
			best = h
		}
	}
	stage := "suggest"
	if best.TimesConfirmed >= TrustApply {
		stage = "apply"
	}
	return &Match{Rule: &best, Result: decodeResult(best), Stage: stage}, nil
}

// Accept stores an offer as a personal rule (or bumps an identical existing rule).
func (s *Service) Accept(ctx context.Context, sc store.Scope, o Offer) (*store.Rule, error) {
	if !kinds[o.Kind] || o.Result.ID <= 0 || o.Pattern == "" {
		return nil, fmt.Errorf("invalid offer")
	}
	rules, err := s.Store.ListRules(ctx, sc)
	if err != nil {
		return nil, err
	}
	resJSON, _ := json.Marshal(o.Result)
	for _, r := range rules {
		if r.Kind == o.Kind && r.Pattern == o.Pattern && r.Status == "active" && (r.Scope == "personal" && r.UserID == sc.UserID) {
			if decodeResult(r) == o.Result {
				r.TimesConfirmed++
			} else {
				// the user corrected their own rule: replace the result, restart trust
				r.Result, r.TimesConfirmed, r.Label = resJSON, 1, o.Label
			}
			r.Source = "correction"
			if err := s.Store.SaveRule(ctx, &r); err != nil {
				return nil, err
			}
			return &r, nil
		}
	}
	r := store.Rule{CompanyID: sc.CompanyID, UserID: sc.UserID, Scope: "personal", Kind: o.Kind, Pattern: o.Pattern, Result: resJSON,
		Label: o.Label, TimesConfirmed: 1, Source: "correction", Status: "active"}
	if err := s.Store.SaveRule(ctx, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Confirm records that the user accepted a proposal that used this rule.
func (s *Service) Confirm(ctx context.Context, sc store.Scope, ruleID int64) error {
	r, err := s.Store.GetRule(ctx, sc, ruleID)
	if err != nil {
		return err
	}
	r.TimesConfirmed++
	return s.Store.SaveRule(ctx, &r)
}

// Update edits a rule's scope/status/label. Promoting to company scope needs canPromote.
func (s *Service) Update(ctx context.Context, sc store.Scope, id int64, scope, status, label string, canPromote bool) (*store.Rule, error) {
	r, err := s.Store.GetRule(ctx, sc, id)
	if err != nil {
		return nil, err
	}
	if r.Scope == "personal" && r.UserID != sc.UserID {
		return nil, store.ErrNotFound
	}
	if r.Scope == "company" && !canPromote {
		return nil, fmt.Errorf("permission_required")
	}
	if scope != "" && scope != r.Scope {
		if scope == "company" && !canPromote {
			return nil, fmt.Errorf("permission_required")
		}
		if scope != "company" && scope != "personal" {
			return nil, fmt.Errorf("invalid scope")
		}
		r.Scope = scope
		if scope == "company" {
			r.Source = "promoted"
		}
	}
	if status != "" {
		if status != "active" && status != "disabled" {
			return nil, fmt.Errorf("invalid status")
		}
		r.Status = status
	}
	if label != "" {
		if len([]rune(label)) > 200 {
			return nil, fmt.Errorf("label too long")
		}
		r.Label = label
	}
	return &r, s.Store.SaveRule(ctx, &r)
}

// Delete removes a rule. Company rules need canPromote.
func (s *Service) Delete(ctx context.Context, sc store.Scope, id int64, canPromote bool) error {
	r, err := s.Store.GetRule(ctx, sc, id)
	if err != nil {
		return err
	}
	if r.Scope == "company" && !canPromote {
		return fmt.Errorf("permission_required")
	}
	return s.Store.DeleteRule(ctx, sc, id)
}
