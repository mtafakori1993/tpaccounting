package agent

// Postconditions of a mutation (D63), checked against application state after the call:
//
//	identity      the record acted on is still the one the user approved (name read before
//	              the call equals the name on the card) — otherwise nothing is sent
//	values        every value sent reads back (verifyValues, D51)
//	transition    a workflow action produced the status the application's own state
//	              machine defines (curated from backend/internal/*: periodMachine, budgets,
//	              checks machine, post/cancel/close/reopen)
//	collateral    the record's list before vs after: no OTHER record changed or vanished,
//	              and a create added exactly one record (no duplicate)
//
// An HTTP success, a tool result or the assistant's words are never verification.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// transition is one action of an application state machine.
type transition struct {
	from []string
	to   string
}

// payroll/periods.go periodMachine
var payrollMachine = map[string]transition{
	"calculate": {[]string{"draft", "calculated"}, "calculated"},
	"approve":   {[]string{"calculated"}, "approved"},
	"reopen":    {[]string{"calculated", "approved"}, "draft"},
	"post":      {[]string{"approved"}, "posted"},
	"unpost":    {[]string{"posted"}, "approved"},
	"pay":       {[]string{"posted"}, "paid"},
	"unpay":     {[]string{"paid"}, "posted"},
	"close":     {[]string{"paid"}, "closed"},
}

// budgets/service.go actions ("revise" creates a new revision: no status change on the source)
var budgetMachine = map[string]transition{
	"submit":   {[]string{"draft"}, "submitted"},
	"approve":  {[]string{"submitted"}, "approved"},
	"reject":   {[]string{"submitted"}, "draft"},
	"activate": {[]string{"approved"}, "active"},
	"close":    {[]string{"active"}, "closed"},
	"archive":  {[]string{"approved", "closed"}, "archived"},
}

// checks/machine.go, per check type
var checkMachine = map[string]map[string]string{
	"received": {"custody": "in_custody", "deposit": "deposited", "clear": "cleared", "cash": "cleared", "transfer": "transferred",
		"return": "returned", "lost": "lost", "cancel": "cancelled"},
	"issued": {"issue": "issued", "deliver": "delivered", "clear": "cleared", "return": "returned", "cancel": "cancelled", "replace": "replaced"},
}

// expectedStatus is the status a workflow action must leave the record in ("" when the
// action defines none, e.g. allocations or a conversion that creates another document),
// plus the statuses it may start from (nil = any).
func expectedStatus(op *catalog.Operation, args tools.OpArgs, before map[string]any) (string, []string) {
	action := op.ID[strings.LastIndex(op.ID, ".")+1:]
	switch {
	case strings.HasPrefix(op.ID, "payroll.") && payrollMachine[action].to != "" && !strings.Contains(op.ID, "liabilities"):
		t := payrollMachine[action]
		return t.to, t.from
	case strings.HasPrefix(op.ID, "budgets.") && budgetMachine[action].to != "":
		t := budgetMachine[action]
		return t.to, t.from
	case strings.HasPrefix(op.ID, "company.fiscal_years."):
		return map[string]string{"close": "closed", "reopen": "open"}[action], nil
	case op.ID == "checks.transitions":
		var body struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(args.Body, &body)
		typ, _ := before["check_type"].(string)
		return checkMachine[typ][body.Action], nil
	case op.ID == "payroll.liabilities.pay":
		return "paid|partial", nil
	case action == "post":
		return "posted", nil
	case action == "cancel":
		return "cancelled", nil
	}
	return "", nil
}

// checkTransition compares the status before and after a workflow action.
func checkTransition(op *catalog.Operation, args tools.OpArgs, before, after map[string]any) []string {
	want, from := expectedStatus(op, args, before)
	if want == "" {
		return nil
	}
	if after == nil {
		return []string{"status: the record could not be read after the action"}
	}
	got := fmt.Sprint(after["status"])
	ok := false
	for _, w := range strings.Split(want, "|") {
		ok = ok || got == w
	}
	if !ok {
		was := fmt.Sprint(before["status"])
		return []string{fmt.Sprintf("status: expected %s → %s, stored %s", was, want, got)}
	}
	if len(from) > 0 && before != nil {
		was := fmt.Sprint(before["status"])
		if !containsStr(from, was) && was != want {
			return []string{fmt.Sprintf("status: %s → %s is not a transition the application defines", was, want)}
		}
	}
	return nil
}

// ---------- collateral: nothing else changed, nothing duplicated ----------

// volatile keys change as side effects of legitimate work elsewhere (balances, timestamps).
func volatileKey(k string) bool {
	return strings.HasSuffix(k, "_at") || strings.Contains(k, "balance") || strings.Contains(k, "remaining") || strings.Contains(k, "allocated") ||
		strings.Contains(k, "paid_amount") || k == "due_status" || k == "payment_status"
}

type listSnapshot struct {
	ok   bool              // the list was read completely (fewer rows than the page size)
	rows map[string]string // id → canonical projection
}

const snapshotPage = 500

// listSnapshotOf reads the list the record belongs to (the same list verifyGone/readRecord use).
func (a *Agent) listSnapshotOf(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs) listSnapshot {
	lo := listOpFor(op)
	if lo == nil {
		return listSnapshot{}
	}
	la := tools.OpArgs{Path: tools.Scalars{}, Query: tools.Scalars{}}
	for _, p := range lo.PathParams {
		v := args.Path[p]
		if v == "" || refRe.MatchString(v) {
			return listSnapshot{}
		}
		la.Path[p] = v
	}
	if !containsStr(lo.QueryParams, "page_size") && len(lo.QueryParams) > 0 && containsStr(lo.QueryParams, "page") {
		return listSnapshot{} // paged without a page size: cannot be read completely
	}
	if containsStr(lo.QueryParams, "page_size") {
		la.Query["page_size"] = strconv.Itoa(snapshotPage)
	}
	raw, _ := json.Marshal(la)
	out, err := a.Tools.Call(ctx, a.env(u), lo.ID, pruneEmptyPath(lo, raw), nil)
	if err != nil {
		return listSnapshot{}
	}
	items := listItemsFor(op, out)
	s := listSnapshot{ok: !containsStr(lo.QueryParams, "page_size") || len(items) < snapshotPage, rows: map[string]string{}}
	for _, it := range items {
		id := strconv.FormatInt(int64Of(it["id"]), 10)
		keys := make([]string, 0, len(it))
		for k := range it {
			if !volatileKey(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var b strings.Builder
		for _, k := range keys {
			v, _ := json.Marshal(it[k])
			b.WriteString(k + "=" + string(v) + ";")
		}
		s.rows[id] = b.String()
	}
	return s
}

// collateral compares the record's list before and after the step. target is the record
// the step acted on (for a create: the id it returned). It returns the problems found and
// whether the comparison could be made.
func collateral(op *catalog.Operation, before, after listSnapshot, target string) ([]string, bool) {
	if !before.ok || !after.ok {
		return nil, false
	}
	var problems []string
	var added []string
	for id := range after.rows {
		if _, ok := before.rows[id]; !ok {
			added = append(added, id)
		}
	}
	for id, row := range before.rows {
		if id == target {
			continue
		}
		now, ok := after.rows[id]
		switch {
		case !ok:
			problems = append(problems, "record "+id+" disappeared although the step did not act on it")
		case now != row:
			problems = append(problems, "record "+id+" changed although the step did not act on it")
		}
	}
	sort.Strings(added)
	switch op.Kind {
	case "create":
		if len(added) == 0 {
			problems = append(problems, "no new record appeared in the list")
		} else if len(added) > 1 || (target != "" && added[0] != target) {
			problems = append(problems, "more than one record was created: "+strings.Join(added, ", "))
		}
	case "update", "delete":
		if len(added) > 0 {
			problems = append(problems, "unexpected new record(s): "+strings.Join(added, ", "))
		}
	}
	sort.Strings(problems)
	if len(problems) > 4 {
		problems = append(problems[:4], fmt.Sprintf("… and %d more", len(problems)-4))
	}
	return problems, true
}

// identityChanged reports a record that is no longer the one approved on the card.
func identityChanged(s PlanStep, before json.RawMessage) string {
	if s.Target == nil || s.Target.Name == "" || len(before) == 0 {
		return ""
	}
	var rec map[string]any
	_ = json.Unmarshal(before, &rec)
	if n := recordName(rec); n != "" && n != s.Target.Name {
		return fmt.Sprintf("the record with id %s is now «%s», not «%s» as approved", s.Target.ID, n, s.Target.Name)
	}
	return ""
}
