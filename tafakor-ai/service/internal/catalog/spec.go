package catalog

import (
	"encoding/json"
	"sort"
	"strings"
)

// Spec is the machine-readable contract of one operation: what it needs, whether
// it may run without confirmation, and how to reproduce it in the real UI. It is
// derived from the generated catalog (never hand-invented, D28) plus two small
// curated tables (D34) for the few rules the generator cannot see: conditional
// required fields and the frontend controls that open a form.
type Spec struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Endpoint string `json:"endpoint"`

	RequiredFields      []string                       `json:"required_fields"`
	OptionalFields      []string                       `json:"optional_fields"`
	ConditionalRequired map[string]map[string][]string `json:"conditional_required,omitempty"`

	ConfirmationRequired bool `json:"confirmation_required"`
	ApprovalRequired     bool `json:"approval_required"`
	SupportsDirect       bool `json:"supports_direct_execution"`
	SupportsUIPreview    bool `json:"supports_ui_preview"`

	UIRoute  string            `json:"ui_route,omitempty"`
	UIOpen   []string          `json:"ui_open,omitempty"`
	UIFields map[string]string `json:"ui_fields,omitempty"`
}

// mustHave are body fields the application requires but the generated markers miss
// (notably the []objects item list, which is validated element-wise). Kept small
// and covered by a catalog test.
var mustHave = map[string][]string{
	"sales.kind.create":     {"items"},
	"purchases.kind.create": {"items"},
}

// variant is one conditional-required rule: when the discriminator (a path
// parameter or a body field) has Value, the operation requires Require and does
// not require Drop.
type variant struct {
	discriminator string
	value         string
	require       []string
	drop          []string
}

// variants are the conditional rules the app validates but the generator cannot
// express (an enum discriminator decides which fields apply). Kept small and
// covered by a catalog test; the app's 422 is still the authority (D29, D36).
var variants = map[string][]variant{
	// The generator sees v.Required("bank_name", …) inside the `kind == "bank"`
	// branch and marks it unconditionally required; a cash box does not need it.
	"treasury.accounts.create": {
		{discriminator: "kind", value: "bank", require: []string{"name", "bank_name"}},
		{discriminator: "kind", value: "cash", drop: []string{"bank_name"}},
	},
	"treasury.transactions.create": {
		{discriminator: "kind", value: "transfer", require: []string{"to_treasury_account_id"}, drop: []string{"party_id"}},
		{discriminator: "kind", value: "receipt", drop: []string{"to_treasury_account_id"}},
		{discriminator: "kind", value: "payment", drop: []string{"to_treasury_account_id"}},
	},
}

// uiMeta describes how to open an operation's real form and which control holds
// each body field. Curated (the generator emits file paths, not DOM tags); every
// entry is checked against the operation's own body by a test (D34).
type uiMeta struct {
	Route  string
	Open   []uiOpen
	Fields map[string]string
}

// uiOpen is a candidate control (a data-ai-action tag) that opens the form. When
// Field/Value are set, the control is the one for that discriminator value
// (e.g. kind=bank → the "New bank account" button).
type uiOpen struct {
	Tag   string
	Field string
	Value string
}

var uiMetaByOp = map[string]uiMeta{
	"treasury.accounts.create": {
		Route: "/treasury",
		Open: []uiOpen{
			{Tag: "treasury.newBank", Field: "kind", Value: "bank"},
			{Tag: "treasury.newCash", Field: "kind", Value: "cash"},
		},
		Fields: map[string]string{
			"name": "common.name", "bank_name": "treasury.bankName", "branch": "treasury.branch",
			"account_number": "treasury.accountNumber", "iban": "treasury.iban",
			"card_number": "treasury.cardNumber", "account_holder": "treasury.accountHolder",
			"opening_balance": "treasury.openingBalance", "opening_date": "treasury.openingDate",
			"description": "common.description",
		},
	},
}

// Spec derives the operation's contract.
func (o *Operation) Spec() Spec {
	req, opt := o.requiredAndOptional()
	s := Spec{
		ID:       o.ID,
		Name:     o.ID,
		Method:   o.Method,
		Path:     o.Path,
		Endpoint: o.Method + " " + o.Path,

		RequiredFields:       req,
		OptionalFields:       opt,
		ConditionalRequired:  o.conditional(),
		ConfirmationRequired: o.Classification == NeedsConfirmation || o.Classification == NeedsApproval,
		ApprovalRequired:     o.Classification == NeedsApproval,
		SupportsDirect:       o.Executable() && !o.IsRead() && o.Classification != NeedsApproval,
	}
	if m, ok := uiMetaByOp[o.ID]; ok {
		s.UIRoute = m.Route
		s.UIFields = m.Fields
		for _, c := range m.Open {
			s.UIOpen = append(s.UIOpen, c.Tag)
		}
		// A preview is possible when the real form has a control for every required
		// field, so a pre-filled form can always be submitted. Optional values the
		// plan carries but the form cannot hold are reported, never silently dropped.
		s.SupportsUIPreview = o.UIExists && m.Route != "" && o.formCoversRequired(m.Fields)
	}
	return s
}

// formCoversRequired reports whether every required field maps to a form control.
func (o *Operation) formCoversRequired(fields map[string]string) bool {
	req, _ := o.requiredAndOptional()
	for _, f := range req {
		if _, ok := fields[f]; !ok {
			return false
		}
	}
	return len(req) > 0
}

// requiredAndOptional splits the writable body fields into required / optional.
func (o *Operation) requiredAndOptional() (req, opt []string) {
	must := map[string]bool{}
	for _, n := range mustHave[o.ID] {
		must[n] = true
	}
	for _, f := range o.InputFields() {
		switch {
		case f.Required || must[f.Name]:
			req = append(req, f.Name)
		default:
			opt = append(opt, f.Name)
		}
	}
	sort.Strings(req)
	sort.Strings(opt)
	return req, opt
}

// conditional groups the variant rules by discriminator and value.
func (o *Operation) conditional() map[string]map[string][]string {
	out := map[string]map[string][]string{}
	for _, v := range variants[o.ID] {
		if out[v.discriminator] == nil {
			out[v.discriminator] = map[string][]string{}
		}
		list := append([]string{}, o.RequiredForVariant(v)...)
		out[v.discriminator][v.value] = list
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RequiredForVariant is the field list a single variant rule yields.
func (o *Operation) RequiredForVariant(v variant) []string {
	req, _ := o.requiredAndOptional()
	drop := map[string]bool{}
	for _, d := range v.drop {
		drop[d] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{req, v.require} {
		for _, f := range list {
			if drop[f] || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// discriminatorValue reads the discriminator from the path (a path parameter) or
// the body, so a rule can key on either.
func discriminatorValue(d string, path map[string]string, body map[string]json.RawMessage) string {
	if v, ok := path[d]; ok && v != "" {
		return v
	}
	if raw, ok := body[d]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return ""
}

// MissingRequired lists the required body fields this invocation does not
// provide. A field counts as provided when it is present and not JSON null —
// null is the user's explicit "none" and satisfies a required field the app
// itself does not enforce for this variant. This is a pre-flight only: the
// application's 422 remains the authority (D29, D36).
func (s Spec) MissingRequired(path map[string]string, body map[string]json.RawMessage) []string {
	required := s.RequiredFields
	for d, byValue := range s.ConditionalRequired {
		if v := discriminatorValue(d, path, body); v != "" {
			if list, ok := byValue[v]; ok {
				required = list
			}
		}
	}
	var missing []string
	for _, f := range required {
		raw, ok := body[f]
		if !ok {
			missing = append(missing, f)
			continue
		}
		t := strings.TrimSpace(string(raw))
		if t == "" || t == "null" {
			continue // explicitly absent: the user said "none"
		}
		if t == `""` || t == "[]" || t == "{}" {
			missing = append(missing, f)
		}
	}
	return missing
}

// UIOpenFor returns the control that opens the form for this invocation, given a
// discriminator value (e.g. kind=bank). When no discriminator matches, the first
// candidate is used.
func (o *Operation) UIOpenFor(value string) []string {
	m, ok := uiMetaByOp[o.ID]
	if !ok {
		return nil
	}
	var all []string
	for _, c := range m.Open {
		if value != "" && c.Field != "" && c.Value == value {
			return []string{c.Tag} // the control for exactly this variant
		}
		all = append(all, c.Tag)
	}
	return all
}

// Specs lists the contracts a user may act on (executable and permitted).
func (c *Catalog) Specs(can func(string) bool) []Spec {
	var out []Spec
	for i := range c.Operations {
		o := &c.Operations[i]
		if !o.Executable() {
			continue
		}
		if ok, _ := o.Allowed(can); !ok {
			continue
		}
		out = append(out, o.Spec())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
