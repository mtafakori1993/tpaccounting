// Package checks manages received and issued (payable) checks: registration,
// a status state machine and the accounting of every status change through the
// ledger. Returned, lost and cancelled checks are undone with reversal entries.
package checks

import "github.com/tafakorpardazan/tpaccounting/internal/apperr"

const (
	TypeReceived = "received"
	TypeIssued   = "issued"

	StatusDraft       = "draft"
	StatusReceived    = "received"
	StatusInCustody   = "in_custody"
	StatusDeposited   = "deposited"
	StatusCleared     = "cleared"
	StatusTransferred = "transferred"
	StatusReturned    = "returned"
	StatusCancelled   = "cancelled"
	StatusLost        = "lost"
	StatusIssued      = "issued"
	StatusDelivered   = "delivered"
	StatusReplaced    = "replaced"
)

// Accounting effect of an action.
const (
	effectNone    = iota // status change only
	effectPost           // posts a new journal entry
	effectReverse        // reverses every live entry of the check
)

type rule struct {
	to     string
	from   []string
	effect int
	// cancelPerm marks actions that undo a check (need checks.cancel instead of checks.post).
	cancelPerm bool
}

// machine lists, per check type, every action with the statuses it may start from.
var machine = map[string]map[string]rule{
	TypeReceived: {
		"custody":  {StatusInCustody, []string{StatusReceived}, effectNone, false},
		"deposit":  {StatusDeposited, []string{StatusReceived, StatusInCustody}, effectPost, false},
		"clear":    {StatusCleared, []string{StatusDeposited}, effectPost, false},
		"cash":     {StatusCleared, []string{StatusReceived, StatusInCustody}, effectPost, false},
		"transfer": {StatusTransferred, []string{StatusReceived, StatusInCustody}, effectPost, false},
		"return":   {StatusReturned, []string{StatusReceived, StatusInCustody, StatusDeposited, StatusTransferred}, effectReverse, true},
		"lost":     {StatusLost, []string{StatusReceived, StatusInCustody}, effectReverse, true},
		"cancel":   {StatusCancelled, []string{StatusReceived, StatusInCustody}, effectReverse, true},
	},
	TypeIssued: {
		"issue":   {StatusIssued, []string{StatusDraft}, effectPost, false},
		"deliver": {StatusDelivered, []string{StatusIssued}, effectNone, false},
		"clear":   {StatusCleared, []string{StatusIssued, StatusDelivered}, effectPost, false},
		"return":  {StatusReturned, []string{StatusIssued, StatusDelivered}, effectReverse, true},
		"cancel":  {StatusCancelled, []string{StatusDraft, StatusIssued, StatusDelivered}, effectReverse, true},
		"replace": {StatusReplaced, []string{StatusIssued, StatusDelivered}, effectReverse, true},
	},
}

// actionOrder is the display order of actions.
var actionOrder = []string{"custody", "issue", "deliver", "deposit", "clear", "cash", "transfer", "return", "lost", "replace", "cancel"}

// LiveStatuses are statuses in which a check is still outstanding.
var LiveStatuses = map[string][]string{
	TypeReceived: {StatusReceived, StatusInCustody, StatusDeposited},
	TypeIssued:   {StatusIssued, StatusDelivered},
}

// Next validates a transition and returns the resulting status.
func Next(checkType, status, action string) (rule, error) {
	actions, ok := machine[checkType]
	if !ok {
		return rule{}, apperr.Rule("invalid_check_type", "unknown check type")
	}
	r, ok := actions[action]
	if !ok {
		return rule{}, apperr.Rule("invalid_transition", "this action does not apply to this check").WithParams("action", action, "status", status)
	}
	for _, f := range r.from {
		if f == status {
			return r, nil
		}
	}
	return rule{}, apperr.Rule("invalid_transition", "this action does not apply to this check").WithParams("action", action, "status", status)
}

// AllowedActions lists the actions available from a status, in display order.
func AllowedActions(checkType, status string) []string {
	out := []string{}
	for _, a := range actionOrder {
		if _, err := Next(checkType, status, a); err == nil {
			out = append(out, a)
		}
	}
	return out
}

// IsLive reports whether a check in status is still outstanding.
func IsLive(checkType, status string) bool {
	for _, s := range LiveStatuses[checkType] {
		if s == status {
			return true
		}
	}
	return false
}

// PermissionFor returns the permission needed to perform action.
func PermissionFor(checkType, action string) string {
	if r, ok := machine[checkType][action]; ok && r.cancelPerm {
		return "checks.cancel"
	}
	return "checks.post"
}
