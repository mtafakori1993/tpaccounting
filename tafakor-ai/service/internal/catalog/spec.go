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
	// ExplicitDirectSkipsConfirm is POLICY, not capability: whether the user's explicit
	// «مستقیم ثبت کن» may skip the confirmation card. Whether a direct executor exists is
	// the capability registry's answer (agent.capabilityFor, D59) — never this field.
	ExplicitDirectSkipsConfirm bool `json:"explicit_direct_skips_confirmation"`
	SupportsUIPreview          bool `json:"supports_ui_preview"`

	// UIPrefillComplete: the form has a control for every request field (no lines,
	// nothing to type by hand after the preview).
	UIPrefillComplete bool `json:"ui_prefill_complete,omitempty"`

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
	// enum discriminators the app validates as "invalid" when absent
	"treasury.accounts.create":     {"kind"},
	"treasury.transactions.create": {"kind"},
	"checks.create":                {"check_type"},
	"expenses.create":              {"payment_method"},
	"payroll.components.create":    {"kind", "method"},
}

// notRequired corrects fields the generator marks required because several actions share
// one input type: payroll period actions all decode ActionInput, but only Pay validates
// treasury_account_id (backend/internal/payroll/periods.go). Covered by a catalog test.
var notRequired = map[string][]string{}

func init() {
	for _, a := range []string{"approve", "calculate", "close", "post", "reopen", "unpay", "unpost"} {
		notRequired["payroll.periods."+a] = []string{"treasury_account_id"}
		notRequired["payroll."+a] = []string{"treasury_account_id"}
	}
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
	// Since the generator treats checks inside if/case branches as conditional
	// (they were wrongly unconditional before), the app's real per-variant rules:
	"checks.create": {
		// "a|b": one of them (the app: BankID != nil || BankName != "")
		{discriminator: "check_type", value: "received", require: []string{"received_date", "bank_id|bank_name"}},
		{discriminator: "check_type", value: "issued", require: []string{"treasury_account_id"}},
	},
	"checks.update": {
		{discriminator: "check_type", value: "received", require: []string{"received_date"}},
		{discriminator: "check_type", value: "issued", require: []string{"treasury_account_id"}},
	},
	"expenses.create": {
		{discriminator: "payment_method", value: "credit", require: []string{"party_id"}},
		{discriminator: "payment_method", value: "cash", require: []string{"treasury_account_id"}},
		{discriminator: "payment_method", value: "bank", require: []string{"treasury_account_id"}},
	},
	"payroll.components.create": {
		{discriminator: "method", value: "daily_base", require: []string{"quantity_field"}},
		{discriminator: "method", value: "hourly_base", require: []string{"quantity_field"}},
		{discriminator: "method", value: "per_unit", require: []string{"quantity_field"}},
		{discriminator: "method", value: "tax_brackets", require: []string{"brackets"}},
		{discriminator: "method", value: "insurance_rule", require: []string{"insurance_share"}},
		{discriminator: "method", value: "formula", require: []string{"formula"}},
	},
	"treasury.transactions.create": {
		{discriminator: "kind", value: "transfer", require: []string{"to_treasury_account_id"}, drop: []string{"party_id"}},
		{discriminator: "kind", value: "receipt", require: []string{"party_id"}, drop: []string{"to_treasury_account_id"}},
		{discriminator: "kind", value: "payment", require: []string{"party_id"}, drop: []string{"to_treasury_account_id"}},
	},
}

// uiMeta is a curated correction of the generated form (Operation.UI) for what the
// frontend source cannot show the generator. Kept small; every entry is checked
// against the operation's own body by a test (D34, D41).
type uiMeta struct {
	Route  string
	Open   []uiOpen
	Fields map[string]string
}

// uiOpen is a control (a data-ai-action tag) that opens the form; Field/Value name
// the variant it opens (kind=bank → "New bank account").
type uiOpen struct {
	Tag   string
	Field string
	Value string
}

// uiMetaByOp: no corrections are needed now that forms are generated (D41); the
// table stays as the place for them.
var uiMetaByOp = map[string]uiMeta{}

// Spec derives the operation's contract.
func (o *Operation) Spec() Spec {
	req, opt := o.requiredAndOptional()
	s := Spec{
		ID:       o.ID,
		Name:     o.ID,
		Method:   o.Method,
		Path:     o.Path,
		Endpoint: o.Method + " " + o.Path,

		RequiredFields:             req,
		OptionalFields:             opt,
		ConditionalRequired:        o.conditional(),
		ConfirmationRequired:       o.Classification == NeedsConfirmation || o.Classification == NeedsApproval,
		ApprovalRequired:           o.Classification == NeedsApproval,
		ExplicitDirectSkipsConfirm: o.Executable() && !o.IsRead() && o.Classification != NeedsApproval,
	}
	if f := o.UI; f != nil && s.ExplicitDirectSkipsConfirm {
		s.UIRoute = f.Route
		s.UIFields = map[string]string{}
		for name, x := range f.Fields {
			s.UIFields[name] = x.Tag
		}
		for _, c := range f.Open {
			s.UIOpen = append(s.UIOpen, c.Tag)
		}
		// "Show me on screen" opens the real form and fills what it can hold; values it
		// cannot hold (line items, fields without a control) are reported to the user,
		// never silently dropped. Submitting through the UI is a different question (D16).
		s.SupportsUIPreview = f.Route != "" && len(f.Fields) > 0
		s.UIPrefillComplete = len(f.Missing) == 0 && len(f.Lines) == 0 && o.formCoversRequired(s.UIFields)
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
	optional := map[string]bool{}
	for _, n := range notRequired[o.ID] {
		optional[n] = true
	}
	for _, f := range o.InputFields() {
		switch {
		case optional[f.Name]:
			opt = append(opt, f.Name)
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
		if strings.Contains(f, "|") {
			// one of several fields
			any := false
			for _, alt := range strings.Split(f, "|") {
				if t := strings.TrimSpace(string(body[alt])); t != "" && t != "null" && t != `""` {
					any = true
				}
			}
			if !any {
				missing = append(missing, f)
			}
			continue
		}
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

// UIOpenFor returns the controls that open the form for a variant value (kind=bank
// → "New bank account"); when no variant matches, all candidates in order.
func (o *Operation) UIOpenFor(value string) []string {
	if o.UI == nil {
		return nil
	}
	var all []string
	for _, c := range o.UI.Open {
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

// SpecRequired is the operation's required body fields (curated corrections applied).
func (o *Operation) SpecRequired() ([]string, []string) { return o.requiredAndOptional() }
