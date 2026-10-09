package catalog

import (
	"encoding/json"
	"strings"

	"github.com/shopspring/decimal"
)

// RoutineChange reports whether a call is an ordinary edit of the company's master
// data — a customer, employee, product, warehouse, cost centre, bank account without
// an opening balance … Such a change needs no confirmation card when the user asked
// for it and may do it (D40): it writes no accounting entry and is not an accounting
// document.
//
// Every input comes from the generated contract, never from a list of names:
//   - classified as needing (only) the user's confirmation — never REQUIRES_APPROVAL,
//     never critical, never a delete or a workflow transition;
//   - not a financial document (no posting lifecycle on the record, D15);
//   - this call writes no entry: the handler never reaches the ledger, or reaches it
//     only when a trigger field is set (opening_balance, post …) and the body leaves
//     every trigger empty.
//
// Attributes that happen to be amounts (a product's price, an employee's salary)
// are not money movements and do not by themselves require a confirmation.
func (o *Operation) RoutineChange(body json.RawMessage) bool {
	if o == nil || o.Classification != NeedsConfirmation || o.Critical || o.FinancialDocument {
		return false
	}
	if o.Kind != "create" && o.Kind != "update" {
		return false
	}
	if o.PostingAlways {
		return false
	}
	var m map[string]any
	if len(body) > 0 && json.Unmarshal(body, &m) != nil {
		return false
	}
	for _, t := range o.PostingTriggers {
		if set(m[t]) {
			return false
		}
	}
	return true
}

// set: a trigger value that makes the call post (true, a non-zero amount, any text).
func set(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		if strings.TrimSpace(x) == "" {
			return false
		}
		d, err := decimal.NewFromString(strings.ReplaceAll(x, ",", ""))
		return err != nil || !d.IsZero()
	}
	return true
}

// SemanticName is the operation as the user thinks of it — create_customer,
// create_sales_quotation, update_product — derived from the operation's action, its
// resource and (for parties) the role in the body. Endpoints stay internal.
func (o *Operation) SemanticName(kind string, body json.RawMessage) string {
	if o == nil {
		return ""
	}
	var segs []string
	for _, s := range strings.Split(strings.Trim(o.Path, "/"), "/") {
		if s != "" && !strings.HasPrefix(s, "{") {
			segs = append(segs, s)
		}
	}
	entity := ""
	if len(segs) > 0 {
		entity = singular(segs[len(segs)-1])
	}
	if strings.Contains(o.Path, "{kind}") && kind != "" {
		entity = singular(segs[0]) + "_" + singular(kind) // sales_quotation, purchase_invoice
	}
	if entity == "party" {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		switch {
		case m["is_customer"] == true && m["is_supplier"] != true:
			entity = "customer"
		case m["is_supplier"] == true && m["is_customer"] != true:
			entity = "supplier"
		}
	}
	action := o.Kind // create | update | delete | read …
	if o.Kind == "workflow" && o.Action != "" {
		action = o.Action // post, convert, cancel …
	}
	return action + "_" + entity
}

func singular(s string) string {
	switch {
	case s == "sales" || s == "news":
		return s
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "s"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}
