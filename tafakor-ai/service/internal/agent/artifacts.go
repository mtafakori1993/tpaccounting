package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// Active document context (D39). Every record the assistant creates, changes or reads
// back one-by-one becomes the conversation's active artifact, taken from the structured
// API result — never from the summary text. A later "این / همین / PDF این / رسیدش /
// تبدیلش کن" binds to it before anything is searched.

// artifactFromResult maps a verified built-in registration to its artifact.
func artifactFromResult(r *Result) *Artifact {
	if r == nil || r.EntityID == 0 || !r.Verified {
		return nil
	}
	a := &Artifact{ID: r.EntityID, Number: r.Number, Side: r.Side, Kind: r.Kind, Label: r.Party}
	switch r.EntityType {
	case "treasury_transaction":
		a.Type = "treasury_transaction"
		if r.Kind != "" {
			a.Type = "treasury_" + r.Kind
		}
	case "invoice":
		if r.Side == "" || r.Kind == "" {
			return nil
		}
		a.Type = r.Side + "_" + r.Kind
	default:
		a.Type = r.EntityType
	}
	return a
}

var invoiceKinds = map[string]bool{"quotation": true, "invoice": true, "return": true}

// artifactFromRecord maps a record returned by a catalog operation to its artifact.
// The type comes from the record itself (side/kind) or the operation's path, so a sales
// quotation stays a sales quotation and is never collapsed into a generic "document".
func artifactFromRecord(op *catalog.Operation, res json.RawMessage) *Artifact {
	var obj map[string]any
	if op == nil || json.Unmarshal(res, &obj) != nil {
		return nil
	}
	id := int64Of(obj["id"])
	if id == 0 {
		return nil
	}
	a := &Artifact{ID: id, Number: int64Of(obj["number"])}
	side, _ := obj["side"].(string)
	kind, _ := obj["kind"].(string)
	path := op.Path
	switch {
	case (side == "sales" || side == "purchase") && invoiceKinds[kind]:
		a.Type, a.Side, a.Kind = side+"_"+kind, side, kind
		a.Label, _ = obj["party_name"].(string)
	case strings.HasPrefix(path, "/treasury/transactions"):
		a.Type, a.Kind = "treasury_transaction", kind
		if kind != "" {
			a.Type = "treasury_" + kind
		}
		a.Label, _ = obj["party_name"].(string)
	case strings.HasPrefix(path, "/expenses"):
		a.Type = "expense"
		a.Label, _ = obj["category_name"].(string)
	case strings.HasPrefix(path, "/inventory/documents"):
		a.Type = "stock_document"
	case strings.HasPrefix(path, "/journal"):
		a.Type = "journal_entry"
	case strings.HasPrefix(path, "/parties"):
		a.Type = "party"
		a.Label, _ = obj["name"].(string)
	default:
		// any other entity keeps its own resource name (products, warehouses, accounts …)
		seg := strings.SplitN(strings.Trim(path, "/"), "/", 3)
		a.Type = seg[0]
		if len(seg) > 1 && !strings.HasPrefix(seg[1], "{") {
			a.Type += "_" + seg[1]
		}
		a.Label, _ = obj["name"].(string)
	}
	return a
}

func int64Of(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// target is the artifact as something undo / attach / pdf can act on.
func (a Artifact) target() *Target {
	t := &Target{EntityType: a.Type, EntityID: a.ID, Side: a.Side, Kind: a.Kind, Label: a.describe("fa")}
	if et := attachEntity(a); et != "" {
		t.EntityType = et
	}
	return t
}

// attachEntity is the application's entity type for the artifact when the app accepts
// attachments on it (backend attachments.entityTables), or "".
func attachEntity(a Artifact) string {
	switch {
	case strings.HasPrefix(a.Type, "treasury_"):
		return "treasury_transaction"
	case a.Side != "" && invoiceKinds[a.Kind]:
		return "invoice"
	}
	switch a.Type {
	case "expense", "journal_entry", "party", "stock_document":
		return a.Type
	case "checks":
		return "check"
	case "employees":
		return "employee"
	case "payroll", "payroll_periods":
		return "payroll_period"
	case "budgets":
		return "budget"
	}
	return ""
}

// printable: the documents generate_pdf can render.
func printable(a Artifact) bool {
	t := a.target().EntityType
	return t == "treasury_transaction" || t == "expense" || t == "invoice" || t == "journal_entry" || t == "stock_document"
}

// cancellable: the documents prepareUndo can reverse.
func cancellable(a Artifact) bool {
	t := a.target().EntityType
	return t == "treasury_transaction" || t == "expense"
}

// attachable: the entity types the attachments API accepts.
func attachable(a Artifact) bool {
	return attachEntity(a) != ""
}

var artifactNames = map[string][2]string{
	"sales_quotation": {"Sales quotation", "پیش‌فاکتور فروش"}, "sales_invoice": {"Sales invoice", "فاکتور فروش"}, "sales_return": {"Sales return", "برگشت از فروش"},
	"purchase_quotation": {"Purchase quotation", "پیش‌فاکتور خرید"}, "purchase_invoice": {"Purchase invoice", "فاکتور خرید"}, "purchase_return": {"Purchase return", "برگشت از خرید"},
	"treasury_receipt": {"Receipt", "رسید دریافت"}, "treasury_payment": {"Payment", "سند پرداخت"}, "treasury_transfer": {"Transfer", "انتقال"},
	"expense": {"Expense", "هزینه"}, "stock_document": {"Stock document", "سند انبار"}, "journal_entry": {"Journal entry", "سند حسابداری"}, "party": {"Party", "طرف حساب"},
}

// describe is a short human label: "پیش‌فاکتور فروش #1 — jonk".
func (a Artifact) describe(lang string) string {
	name := a.Type
	if n, ok := artifactNames[a.Type]; ok {
		name = pick(lang, n)
	}
	s := name
	if a.Number > 0 {
		s += fmt.Sprintf(" #%d", a.Number)
	} else {
		s += fmt.Sprintf(" (id %d)", a.ID)
	}
	if a.Label != "" {
		s += " — " + a.Label
	}
	return s
}

// typeWords: words naming a document type in the user's message. Checked in order —
// "پیش فاکتور" before "فاکتور". «رسید» alone is deliberately absent: a "رسید" of a
// quotation is its printable copy, not a payment receipt.
var typeWords = []struct {
	words []string
	match func(Artifact) bool
}{
	{[]string{"پیشفاکتور", "پیش فاکتور", "proforma", "pro forma", "quotation", "quote"}, func(a Artifact) bool { return a.Kind == "quotation" && a.Side != "" }},
	{[]string{"برگشت از", "مرجوعی", "return"}, func(a Artifact) bool { return a.Kind == "return" && a.Side != "" }},
	{[]string{"فاکتور", "صورتحساب", "invoice"}, func(a Artifact) bool { return a.Kind == "invoice" && a.Side != "" }},
	{[]string{"هزینه", "expense"}, func(a Artifact) bool { return a.Type == "expense" }},
	{[]string{"رسید دریافت", "دریافتی", "receipt"}, func(a Artifact) bool { return a.Type == "treasury_receipt" }},
	{[]string{"سند پرداخت", "پرداختی", "payment"}, func(a Artifact) bool { return a.Type == "treasury_payment" }},
	{[]string{"انتقال", "transfer"}, func(a Artifact) bool { return a.Type == "treasury_transfer" }},
	{[]string{"سند حسابداری", "journal"}, func(a Artifact) bool { return a.Type == "journal_entry" }},
}

// explicitNumber: "#5", "شماره ۵", "فاکتور ۱۲" — not a bare amount or year.
var explicitNumber = regexp.MustCompile(`(?:#|شماره|no\.?|number|فاکتور|سند|هزینه|رسید|invoice|quotation|receipt|payment|expense)\s*#?(\d+)\b`)

// resolveReference returns the document the message refers to from the conversation's
// own context, or nil when it names something the context does not hold (then the
// caller searches). Priority: an explicit number that matches a known document; a
// document type named in the message (newest of that type); the active document; the
// newest suitable one. ok filters to what the operation can act on.
func (c *conversation) resolveReference(text string, ok func(Artifact) bool) *Artifact {
	if c == nil || len(c.state.Artifacts) == 0 {
		return nil
	}
	norm := strings.ToLower(normalize.Text(text))
	stack := c.state.Artifacts
	newest := func(f func(Artifact) bool) *Artifact {
		if c.state.Active != nil && ok(*c.state.Active) && f(*c.state.Active) {
			a := *c.state.Active
			return &a
		}
		for i := len(stack) - 1; i >= 0; i-- {
			if ok(stack[i]) && f(stack[i]) {
				a := stack[i]
				return &a
			}
		}
		return nil
	}
	var typed func(Artifact) bool
	for _, tw := range typeWords {
		for _, w := range tw.words {
			if strings.Contains(norm, w) {
				typed = tw.match
				break
			}
		}
		if typed != nil {
			break
		}
	}
	side := func(Artifact) bool { return true }
	switch {
	case strings.Contains(norm, "فروش") || strings.Contains(norm, "sales"):
		side = func(a Artifact) bool { return a.Side == "" || a.Side == "sales" }
	case strings.Contains(norm, "خرید") || strings.Contains(norm, "purchase"):
		side = func(a Artifact) bool { return a.Side == "" || a.Side == "purchase" }
	}
	if m := explicitNumber.FindAllStringSubmatch(norm, -1); len(m) > 0 {
		for _, g := range m {
			n, _ := strconv.ParseInt(g[1], 10, 64)
			if hit := newest(func(a Artifact) bool { return a.Number == n && side(a) && (typed == nil || typed(a)) }); hit != nil {
				return hit
			}
		}
		return nil // a number the conversation does not know: search for it
	}
	if typed != nil {
		return newest(func(a Artifact) bool { return typed(a) && side(a) })
	}
	return newest(side)
}

// referenceContext describes the conversation's documents for the models, newest first,
// so pronouns in operations ("تبدیلش کن", "ایمیلش کن", "پرداختش رو ثبت کن") bind to ids.
func (c *conversation) referenceContext() string {
	if c == nil || len(c.state.Artifacts) == 0 {
		return ""
	}
	var b strings.Builder
	if a := c.state.Active; a != nil {
		fmt.Fprintf(&b, "ACTIVE: %s (type=%s id=%d number=%d side=%s kind=%s)\n", a.describe("en"), a.Type, a.ID, a.Number, a.Side, a.Kind)
	}
	n := 0
	for i := len(c.state.Artifacts) - 1; i >= 0 && n < 8; i-- {
		a := c.state.Artifacts[i]
		fmt.Fprintf(&b, "- type=%s id=%d number=%d side=%s kind=%s label=%q\n", a.Type, a.ID, a.Number, a.Side, a.Kind, a.Label)
		n++
	}
	return b.String()
}

// emitArtifact records a document as the conversation's active artifact (the recorder
// persists it) and lets the widget know which document the conversation is about.
func emitArtifact(e Emitter, a *Artifact) {
	if a != nil && a.Type != "" && a.ID > 0 {
		e.Emit("artifact", a)
	}
}
