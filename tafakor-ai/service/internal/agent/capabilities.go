package agent

// Execution capability registry (D59): the ONE place that says how an operation can
// actually be carried out. It is derived from the real executors — never hand-listed:
//
//	direct  the tool registry has a runner for it (tools.Registry, the direct executor)
//	ui      the screen executor can build its steps (uiCapability, shared with buildStepPlan)
//	verify  the result can be read back from the application (read-by-id, its list, or
//	        for a delete: the record is gone)
//
// The planner is only offered operations that have an executor AND a verification path
// (plannerTools); a plan step is only accepted when its tool is planner-exposed
// (validatePlan); and before a plan is presented the executor for the requested mode is
// selected (selectExecutor) — so "planned, approved, no executor" cannot be reached.
// cmd/capmap and the capability tests read the same registry (CapabilityMatrix).

import (
	"sort"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/policy"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// Verification strategies.
const (
	VerifyReadBack = "read_back"  // the record is read by id and every sent value compared
	VerifyList     = "list_check" // the record is found in its list and compared
	VerifyGone     = "gone_check" // delete: reading the record by id must fail with 404 (or show it deactivated)
	VerifyListGone = "list_gone"  // delete: the record must no longer be in its list
	VerifyResult   = "result"     // reads: the answer is the application's own response
	VerifyNone     = "none"
)

// Failure codes of execution (persisted on the task, shown in the trace).
const (
	FailNoExecutor     = "NO_EXECUTOR"
	FailNoUIExecutor   = "NO_UI_EXECUTOR"
	FailNoDirect       = "NO_DIRECT_EXECUTOR"
	FailNoVerification = "NO_VERIFICATION"
	FailNotOffered     = "NOT_OFFERED"
	FailVerification   = "VERIFICATION_FAILED"
	FailApplication    = "APPLICATION_REJECTED"
)

// ExecCapability is one row of the registry.
type ExecCapability struct {
	Tool        string `json:"tool"`
	Kind        string `json:"kind"`
	Planner     bool   `json:"planner"`
	Direct      bool   `json:"direct"`
	UI          bool   `json:"ui"`
	UIReason    string `json:"ui_reason,omitempty"`
	Verify      string `json:"verify"`
	Approval    string `json:"approval"` // read | confirm | approval | not_offered
	Destructive bool   `json:"destructive"`
	Idempotency string `json:"idempotency"` // read | task_step_key (+ in-flight reconciliation)
	// Blocked is the failure code that keeps the operation away from the planner.
	Blocked string `json:"blocked,omitempty"`
	// Postconditions verified after a mutation (D63) and how an uncertain outcome is
	// reconciled before anything is sent again (D65).
	Postconditions []string `json:"postconditions,omitempty"`
	Retry          string   `json:"retry,omitempty"`
}

// uiCapability is the operation-level answer to "can the screen executor carry this
// out end to end (navigate, open, fill, submit, detect the result)?". buildStepPlan
// uses exactly this predicate; values a particular call carries can still make one
// step inexpressible on screen, which selectExecutor checks per step.
func uiCapability(op *catalog.Operation) (bool, string) {
	switch op.Kind {
	case "create", "update":
		f := op.UI
		if f == nil || f.Route == "" {
			return false, "the application has no screen for it"
		}
		route := strings.NewReplacer(":side", "x", ":kind", "x", ":id", "x").Replace(f.Route)
		if strings.Contains(route, ":") || strings.Contains(route, "{") {
			return false, "its screen's address cannot be worked out"
		}
		if len(f.Dialogs) > 0 {
			if op.Kind == "update" && !f.Row {
				return false, "the record is not edited from a list row on screen"
			}
			if op.Kind == "create" && len(f.Open) == 0 {
				return false, "no button opens its form"
			}
		}
		return true, ""
	case "delete", "workflow":
		if uiCancelTarget(op) != "" {
			return true, ""
		}
		return false, "the screen executor has no steps for this action"
	}
	return false, "it is not operated on screen"
}

// uiCancelTarget names the record type whose list screen has the application's own
// Cancel button and confirmation dialog for op ("" when there is none).
func uiCancelTarget(op *catalog.Operation) string {
	if !strings.HasSuffix(op.ID, ".cancel") {
		return ""
	}
	switch {
	case strings.HasPrefix(op.ID, "treasury.transactions"):
		return "treasury_transaction"
	case strings.HasPrefix(op.ID, "expenses"):
		return "expense"
	}
	return ""
}

// verifyStrategy is how the result of op is established from application state.
func verifyStrategy(op *catalog.Operation) string {
	if op.IsRead() {
		return VerifyResult
	}
	switch op.Kind {
	case "delete":
		if readBackOp(op) != nil && strings.Contains(op.Path, "{id}") {
			return VerifyGone
		}
		if listOpFor(op) != nil {
			return VerifyListGone
		}
	case "workflow":
		if readBackOp(op) != nil {
			return VerifyReadBack
		}
	default:
		if readBackOp(op) != nil {
			return VerifyReadBack
		}
		if listOpFor(op) != nil {
			return VerifyList
		}
	}
	return VerifyNone
}

func capabilityOf(reg *tools.Registry, op *catalog.Operation) ExecCapability {
	c := ExecCapability{Tool: op.ID, Kind: op.Kind, Verify: verifyStrategy(op), Idempotency: "task_step_key",
		Destructive: op.Kind == "delete" || strings.HasSuffix(op.ID, ".cancel") || strings.HasSuffix(op.ID, ".void") || strings.HasSuffix(op.ID, ".reverse")}
	switch op.Classification {
	case catalog.ReadOnly:
		c.Approval, c.Idempotency = "read", "read"
	case catalog.NeedsApproval:
		c.Approval = "approval"
	case catalog.NeedsConfirmation:
		c.Approval = "confirm"
	default:
		c.Approval = "not_offered"
	}
	if c.Approval == "confirm" && (policy.NeverAuto[op.ID] || policy.NeverAutoTools[op.ID]) {
		c.Approval = "approval" // never-automatic documents (D52)
	}
	if !op.Executable() {
		c.Blocked = FailNotOffered
		return c
	}
	if reg != nil {
		_, c.Direct = reg.Get(op.ID)
	}
	if !op.IsRead() {
		c.UI, c.UIReason = uiCapability(op)
	} else {
		c.UIReason = "reads are answered from the application"
	}
	if !op.IsRead() {
		c.Postconditions, c.Retry = postconditionsOf(op, c.Verify)
	}
	switch {
	case !c.Direct && !c.UI:
		c.Blocked = FailNoExecutor
	case c.Verify == VerifyNone:
		c.Blocked = FailNoVerification
	default:
		c.Planner = true
	}
	return c
}

var capReg = tools.NewRegistry()

// capabilityFor returns the registry row for a tool id (ok=false: unknown tool).
func capabilityFor(id string) (ExecCapability, bool) {
	op, ok := catalog.Load().Get(id)
	if !ok {
		return ExecCapability{Tool: id, Blocked: FailNoExecutor}, false
	}
	return capabilityOf(capReg, op), true
}

// CapabilityMatrix is the whole registry (audit, CI, cmd/capmap).
func CapabilityMatrix() []ExecCapability {
	c := catalog.Load()
	out := make([]ExecCapability, 0, len(c.Operations))
	for i := range c.Operations {
		out = append(out, capabilityOf(capReg, &c.Operations[i]))
	}
	return out
}

// plannerTools renders the tools the planner may use: permitted, and carried out by a
// real executor with a verification path. Nothing else is ever shown to the model.
func plannerTools(can func(string) bool) string {
	c := catalog.Load()
	var lines []string
	for i := range c.Operations {
		o := &c.Operations[i]
		if cap := capabilityOf(capReg, o); !cap.Planner {
			continue
		}
		if ok, _ := o.Allowed(can); !ok {
			continue
		}
		lines = append(lines, o.Line())
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// ---------- executor selection (before anything is presented) ----------

// ExecRecord is the persisted execution state of a task's operation.
type ExecRecord struct {
	Operation     string `json:"operation,omitempty"`
	Step          string `json:"step,omitempty"`
	Requested     string `json:"requested_mode"`
	Selected      string `json:"selected_mode,omitempty"`
	Executor      string `json:"executor,omitempty"` // direct_api | ui_pointer
	Status        string `json:"status"`             // SELECTED | EXECUTING | COMPLETED | VERIFICATION_FAILED | FAILED | NO_EXECUTOR | BLOCKED
	FailureCode   string `json:"failure_code,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	Attempts      int    `json:"attempt_count"`
	Note          string `json:"note,omitempty"` // why the selected mode differs from the requested one
}

func executorName(m execmode.Mode) string {
	if m == execmode.HumanUI {
		return "ui_pointer"
	}
	return "direct_api"
}

// planExecutors reports which executors can carry out ALL pending steps of the plan.
// The UI answer is per step: the operation must be operable on screen and this exact
// call must be expressible on its form (dry run of the same builder, references stubbed).
func planExecutors(st *State) (direct, ui bool, uiWhy string, missing []string) {
	direct, ui = true, true
	for _, s := range st.Plan.Pending() {
		if isSourceAttach(st, s) {
			continue // the system attaches the user's own file in either mode (D46)
		}
		c, ok := capabilityFor(s.Tool)
		if !ok || (!c.Direct && !c.UI) {
			missing = append(missing, s.Tool)
			direct, ui = false, false
			continue
		}
		direct = direct && c.Direct
		if !c.UI {
			if ui {
				uiWhy = c.UIReason
			}
			ui = false
			continue
		}
		probe := s
		probe.Args = stubRefs(s.Args)
		if _, why := buildStepPlan(st, probe, true); why != "" {
			if ui {
				uiWhy = why
			}
			ui = false
		}
	}
	return direct, ui, uiWhy, missing
}

// stubRefs replaces "$s1.id"-style references with a placeholder id (dry runs only).
func stubRefs(a tools.OpArgs) tools.OpArgs {
	out := tools.OpArgs{Path: tools.Scalars{}, Query: a.Query, Body: a.Body}
	for k, v := range a.Path {
		out.Path[k] = refRe.ReplaceAllString(v, "1")
	}
	if len(a.Body) > 0 {
		out.Body = []byte(refRe.ReplaceAllString(string(a.Body), "1"))
	}
	return out
}

// selectExecutor picks the executor for the pending steps BEFORE the plan is shown or
// run. The requested mode is kept when it can carry the work out; otherwise the other
// real executor is selected and the reason recorded (the card says so — the user still
// approves it, and an explicit request for the missing mode always gets a card). When
// no executor can carry a step, nothing is presented: the task is NO_EXECUTOR.
// explicitSwitch reports that the user's own explicit choice could not be honoured.
func selectExecutor(st *State) (ok, explicitSwitch bool) {
	m, valid := execmode.Parse(st.ExecMode)
	if !valid {
		m = execmode.Default
	}
	rec := &ExecRecord{Requested: string(m), Status: "SELECTED"}
	if prev := st.Exec; prev != nil {
		rec.Attempts = prev.Attempts
	}
	if p := st.Plan.Pending(); len(p) > 0 {
		rec.Operation, rec.Step = p[0].Tool, p[0].ID
	}
	st.Exec = rec
	direct, ui, uiWhy, missing := planExecutors(st)
	if len(missing) > 0 {
		rec.Status, rec.FailureCode = "NO_EXECUTOR", FailNoExecutor
		rec.FailureReason = "no direct or screen executor for " + strings.Join(missing, ", ")
		return false, false
	}
	explicit := st.ExecSource == string(execmode.SourceRequest) || st.ExecSource == string(execmode.SourceExplicit)
	switch {
	case m == execmode.HumanUI && !ui && direct:
		m = execmode.Direct
		rec.Note = tr(st.Lang, "This can't be done on screen ("+uiWhy+"), so I'll do it directly through the application.",
			"این کار روی صفحه قابل انجام نیست ("+uiReasonFa(uiWhy)+")؛ مستقیم از طریق برنامه انجام می‌دهم.")
		explicitSwitch = explicit
	case m == execmode.Direct && !direct && ui:
		m = execmode.HumanUI
		rec.Note = tr(st.Lang, "This can't be done directly, so I'll do it on screen.", "این کار مستقیم قابل انجام نیست؛ روی صفحه انجامش می‌دهم.")
		explicitSwitch = explicit
	case m == execmode.HumanUI && !ui, m == execmode.Direct && !direct:
		rec.Status, rec.FailureCode = "NO_EXECUTOR", FailNoExecutor
		rec.FailureReason = "the requested mode has no executor and no other executor exists"
		return false, false
	}
	if string(m) != st.ExecMode {
		st.ExecMode, st.ExecSource = string(m), "capability"
	}
	rec.Selected, rec.Executor = string(m), executorName(m)
	return true, explicitSwitch
}

// modeSupported reports whether the pending steps can run in mode m (card buttons,
// a typed «مستقیم انجام بده», a fallback from the screen).
func modeSupported(st *State, m execmode.Mode) (bool, string) {
	if st.Plan == nil {
		return true, ""
	}
	direct, ui, uiWhy, missing := planExecutors(st)
	switch {
	case len(missing) > 0:
		return false, FailNoExecutor
	case m == execmode.HumanUI && !ui:
		return false, uiWhy
	case m == execmode.Direct && !direct:
		return false, FailNoDirect
	}
	return true, ""
}

// uiReasonFa translates the registry's screen reasons for the Persian card.
func uiReasonFa(why string) string {
	switch why {
	case "the application has no screen for it":
		return "نرم‌افزار برای آن صفحه‌ای ندارد"
	case "the screen executor has no steps for this action":
		return "صفحه نرم‌افزار دکمه یا مسیر قابل‌استفاده‌ای برای آن ندارد"
	case "the record is not edited from a list row on screen":
		return "این رکورد از ردیف فهرست ویرایش نمی‌شود"
	case "no button opens its form":
		return "دکمه‌ای فرم آن را باز نمی‌کند"
	case "its screen's address cannot be worked out":
		return "آدرس صفحه آن مشخص نیست"
	}
	return why
}

// selectLegacyExecutor is selectExecutor for a single built-in action (receipt, payment,
// expense, …): the screen is only kept when its real form can carry this exact payload.
func selectLegacyExecutor(st *State) (uiOK, explicitSwitch bool) {
	uiOK = !st.NoUI && st.Proposal != nil && uiUnsupported(st.Proposal, st.Lang) == "" && len(BuildUIPlan(st).Steps) > 0
	m, valid := execmode.Parse(st.ExecMode)
	if !valid {
		m = execmode.Default
	}
	rec := &ExecRecord{Requested: string(m), Status: "SELECTED"}
	if st.Proposal != nil {
		rec.Operation = st.Proposal.Action
	}
	if m == execmode.HumanUI && !uiOK && !st.NoUI {
		why := uiUnsupported(st.Proposal, st.Lang)
		if why == "" {
			why = tr(st.Lang, "no screen path for this operation", "مسیر صفحه‌ای برای این عملیات وجود ندارد")
		}
		m = execmode.Direct
		rec.Note = tr(st.Lang, "This can't be done on screen ("+why+"), so I'll do it directly through the application.",
			"این کار روی صفحه قابل انجام نیست ("+why+")؛ مستقیم از طریق برنامه انجام می‌دهم.")
		explicitSwitch = st.ExecSource == string(execmode.SourceRequest) || st.ExecSource == string(execmode.SourceExplicit)
		st.ExecMode, st.ExecSource = string(m), "capability"
	}
	rec.Selected, rec.Executor = string(m), executorName(m)
	st.Exec = rec
	return uiOK, explicitSwitch
}

// postconditionsOf lists what is checked after a mutation and how a lost response is
// reconciled — the same functions executePlan and reconcileOutcome run.
func postconditionsOf(op *catalog.Operation, verify string) ([]string, string) {
	pc := []string{}
	switch op.Kind {
	case "create":
		pc = append(pc, "values_read_back", "exactly_one_new_record", "no_other_record_changed")
		return pc, "find the record created after the attempt (list, matching values); absent only when the list was read completely, else stop"
	case "update":
		pc = append(pc, "target_identity", "values_read_back", "untouched_fields_unchanged", "no_other_record_changed")
		return pc, "read the record: values applied → done; readable but not applied → resend allowed; unreadable → stop"
	case "delete":
		pc = append(pc, "target_identity", "record_gone_or_inactive", "no_other_record_changed")
		return pc, "read the record: gone → already completed; still present → resend allowed; unreadable → stop"
	case "workflow":
		pc = append(pc, "target_identity")
		if want, _ := expectedStatus(op, tools.OpArgs{}, map[string]any{"check_type": "received"}); want != "" || op.ID == "checks.transitions" {
			pc = append(pc, "status_transition", "no_other_record_changed")
			return pc, "read the status: target reached → done (never resent); unchanged → resend allowed; else stop"
		}
		pc = append(pc, "record_readable_after", "no_other_record_changed")
		return pc, "the action leaves no status to compare: an uncertain outcome stops and is never resent automatically"
	}
	return pc, ""
}
