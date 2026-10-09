package agent

// Execution reliability (audit 2026-10-08): a mutation is never sent twice by
// accident and never reported as done without reading it back.
//
//   - In-flight marker: before a write is sent, the step's idempotency key gets a
//     marker with the highest existing record id (baseline) and the time. If the
//     outcome is unknown — the connection broke, the request timed out, the
//     server answered 5xx, the service restarted mid-call — the step is
//     RECONCILED: the application is searched for a record created after the
//     baseline that carries the step's values. Found → the step is adopted as
//     done (no second write). Not found → it is reported as not saved, and only
//     the user's explicit retry sends it again (which reconciles once more first).
//     Accounting mutations are never retried blindly.
//   - Value verification: after a write the record is read back (by id, or from
//     its list when the app has no read-by-id) and every scalar value the step
//     sent is compared with what the application stored. A mismatch is reported,
//     never hidden behind a success message.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// inflight is the marker stored before a write is sent.
type inflight struct {
	Baseline int64     `json:"baseline"`
	At       time.Time `json:"at"`
	// Before: the record a change/action works on, as it was before the first attempt
	// (a workflow's "unchanged since" check needs its status).
	Before map[string]any `json:"before,omitempty"`
}

// uncertainOutcome reports whether a failed write may nevertheless have been
// applied by the application: anything but a definite answer from it (4xx).
func uncertainOutcome(err error) bool {
	if err == nil {
		return false
	}
	if ae, ok := accounting.AsAPIError(err); ok {
		return ae.Status >= 500 || ae.Status == 0
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return strings.Contains(err.Error(), "accounting unreachable") || strings.Contains(err.Error(), "EOF")
}

// markInflight records that a write is about to be sent (first attempt wins).
func (a *Agent) markInflight(ctx context.Context, u *User, key string, op *catalog.Operation, args tools.OpArgs, before json.RawMessage) inflight {
	if prev, ok, _ := a.Store.Recall(ctx, u.Scope, key+":inflight"); ok {
		var m inflight
		if json.Unmarshal(prev, &m) == nil {
			return m
		}
	}
	m := inflight{At: a.now()}
	_ = json.Unmarshal(before, &m.Before)
	if op.Kind == "create" {
		m.Baseline = a.maxRecordID(ctx, u, op, args)
	}
	b, _ := json.Marshal(m)
	_ = a.Store.Remember(ctx, u.Scope, key+":inflight", b)
	return m
}

// wasInflight returns the marker of an earlier attempt of the same step, if any.
func (a *Agent) wasInflight(ctx context.Context, u *User, key string) (inflight, bool) {
	prev, ok, _ := a.Store.Recall(ctx, u.Scope, key+":inflight")
	if !ok {
		return inflight{}, false
	}
	var m inflight
	if json.Unmarshal(prev, &m) != nil {
		return inflight{}, false
	}
	return m, true
}

// reconcile looks for the record an uncertain write may have created or changed.
// It returns the record when the application shows the step's values, else nil.
func (a *Agent) reconcile(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs, m inflight) json.RawMessage {
	rec, _ := a.reconcileOutcome(ctx, u, op, args, m)
	return rec
}

// Outcomes of reconciling an uncertain write against the application (D65).
const (
	OutcomeDone    = "done"    // the application shows the effect: adopt it, never resend
	OutcomeAbsent  = "absent"  // verified not applied: it may be sent (again)
	OutcomeUnknown = "unknown" // could not be established: never resent automatically
)

// reconcileOutcome establishes from application state whether a write whose response was
// lost took effect — for every kind of mutation.
func (a *Agent) reconcileOutcome(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs, m inflight) (json.RawMessage, string) {
	var body map[string]json.RawMessage
	_ = json.Unmarshal(args.Body, &body)
	switch op.Kind {
	case "create":
		readable := false
		for _, narrow := range []bool{true, false} {
			var pick map[string]any
			var pickID int64
			items, ok := a.listRecordsOK(ctx, u, op, args, narrow)
			readable = readable || ok
			for _, it := range items {
				id := int64Of(it["id"])
				if id <= m.Baseline || id <= pickID || !looselyMatches(body, it) || createdBefore(it, m.At) {
					continue
				}
				pick, pickID = it, id
			}
			if pick != nil {
				b, _ := json.Marshal(pick)
				return b, OutcomeDone
			}
		}
		if readable {
			return nil, OutcomeAbsent
		}
	case "update":
		rec, raw := a.readRecord(ctx, u, op, args.Path["id"], args)
		if rec != nil {
			if len(compareBody(body, rec)) == 0 {
				return raw, OutcomeDone
			}
			return nil, OutcomeAbsent
		}
	case "delete":
		gone, after, problems := a.verifyGone(ctx, u, op, args)
		switch {
		case gone:
			return json.RawMessage(`{"already_completed":true}`), OutcomeDone
		case len(after) > 0:
			return nil, OutcomeAbsent // the record is still there
		default:
			_ = problems
		}
	case "workflow":
		rb := readBackOp(op)
		if rb == nil || args.Path["id"] == "" {
			break
		}
		ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": args.Path["id"], "kind": args.Path["kind"]}})
		v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil)
		if err != nil {
			break
		}
		b, _ := json.Marshal(v)
		var rec map[string]any
		_ = json.Unmarshal(b, &rec)
		want, _ := expectedStatus(op, args, m.Before)
		if want == "" {
			break // the action leaves no status to read: unknown
		}
		if len(checkTransition(op, args, nil, rec)) == 0 {
			return b, OutcomeDone
		}
		if fmt.Sprint(rec["status"]) == fmt.Sprint(m.Before["status"]) {
			return nil, OutcomeAbsent // unchanged since before the call
		}
	}
	return nil, OutcomeUnknown
}

// createdBefore: the record existed before the write was sent (2s clock slack).
func createdBefore(rec map[string]any, at time.Time) bool {
	s, _ := rec["created_at"].(string)
	if s == "" || at.IsZero() {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return false
	}
	return t.Before(at.Add(-2 * time.Second))
}

// listOpFor is the list operation of the resource an operation works on
// (/payroll/components/{id} → GET /payroll/components).
func listOpFor(op *catalog.Operation) *catalog.Operation {
	c := catalog.Load()
	path := op.Path
	if op.Kind == "update" || op.Kind == "delete" || op.Kind == "workflow" {
		if i := strings.Index(path, "/{id}"); i > 0 {
			path = path[:i]
		}
	}
	if alt, ok := listAlias[path]; ok {
		path = alt.Path
	}
	for i := range c.Operations {
		x := &c.Operations[i]
		if x.Method == "GET" && x.Path == path && x.Kind != "export" {
			return x
		}
	}
	return nil
}

// listItemsFor reads the records out of op's list response (a curated key when the
// records sit inside a larger response, D59).
func listItemsFor(op *catalog.Operation, out any) []map[string]any {
	path := op.Path
	if i := strings.Index(path, "/{id}"); i > 0 {
		path = path[:i]
	}
	if alt, ok := listAlias[path]; ok && alt.Key != "" {
		b, _ := json.Marshal(out)
		var obj map[string]json.RawMessage
		var arr []map[string]any
		if json.Unmarshal(b, &obj) == nil && json.Unmarshal(obj[alt.Key], &arr) == nil {
			return arr
		}
		return nil
	}
	return recordItems(out)
}

// readRecord reads one record back through the application: by id when the app has
// a read-by-id, otherwise from its list.
func (a *Agent) readRecord(ctx context.Context, u *User, op *catalog.Operation, id string, args tools.OpArgs) (map[string]any, json.RawMessage) {
	if id == "" {
		return nil, nil
	}
	if rb := readBackOp(op); rb != nil {
		ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": id, "kind": args.Path["kind"]}})
		v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil)
		if err != nil {
			return nil, nil
		}
		b, _ := json.Marshal(v)
		var obj map[string]any
		_ = json.Unmarshal(b, &obj)
		return obj, b
	}
	lo := listOpFor(op)
	if lo == nil {
		return nil, nil
	}
	la := tools.OpArgs{Path: tools.Scalars{}, Query: tools.Scalars{}}
	for _, p := range lo.PathParams {
		la.Path[p] = args.Path[p]
	}
	for _, q := range lo.QueryParams {
		if q == "page_size" {
			la.Query["page_size"] = "200"
		}
	}
	raw, _ := json.Marshal(la)
	out, err := a.Tools.Call(ctx, a.env(u), lo.ID, pruneEmptyPath(lo, raw), nil)
	if err != nil {
		return nil, nil
	}
	for _, it := range listItemsFor(op, out) {
		if strconv.FormatInt(int64Of(it["id"]), 10) == id {
			b, _ := json.Marshal(it)
			return it, b
		}
	}
	return nil, nil
}

// verifyValues reads the written record back and compares every scalar the step
// sent. verified is true only when the record exists and shows those values.
func (a *Agent) verifyValues(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs, res json.RawMessage) (verified bool, after json.RawMessage, mismatches []string) {
	if op.Kind == "delete" {
		return a.verifyGone(ctx, u, op, args)
	}
	var obj map[string]any
	_ = json.Unmarshal(res, &obj)
	id := args.Path["id"]
	if v, ok := obj["id"]; ok && op.Kind == "create" {
		id = strconv.FormatInt(int64Of(v), 10)
	}
	if op.Kind == "workflow" {
		// post/approve/pay: the record is re-read; its own fields are not in the body
		ok, b := a.verifyStep(ctx, u, op, args, res)
		return ok, b, nil
	}
	rec, raw := a.readRecord(ctx, u, op, id, args)
	if rec == nil {
		// nothing to read back: only the application's acceptance is known
		if readBackOp(op) == nil && listOpFor(op) == nil {
			return len(res) > 0, nil, nil
		}
		return false, nil, []string{"record not found after the write"}
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(args.Body, &body)
	mismatches = compareBody(body, rec)
	return len(mismatches) == 0, raw, mismatches
}

// adoptRecord marks a step done with a record found by reconciliation.
func (a *Agent) adoptRecord(ctx context.Context, u *User, op *catalog.Operation, s *PlanStep, args tools.OpArgs, rec json.RawMessage, key string) {
	s.Result, s.Status, s.Args = rec, "done", args
	verified, _, mism := a.verifyValues(ctx, u, op, args, rec)
	s.Verified = verified
	s.Mismatches = mism
	if b, err := json.Marshal(map[string]any{"result": rec, "verified": verified}); err == nil {
		_ = a.Store.Remember(ctx, u.Scope, key, b)
	}
}

func notSavedText(lang string, s *PlanStep) string {
	return tr(lang, fmt.Sprintf("The accounting app did not answer for step %s (%s). I checked: it was NOT saved, so nothing was duplicated. Say «retry» to send it again.", s.ID, s.Purpose),
		fmt.Sprintf("نرم‌افزار حسابداری برای مرحله %s (%s) پاسخی نداد. بررسی کردم: ثبت نشده است و چیزی تکراری ثبت نشد. برای ارسال دوباره بگویید «دوباره امتحان کن».", s.ID, s.Purpose))
}

// Trace is the developer's view of one task: the pre-tool decision, the plan, every
// step with its tool, arguments, result, verification and timing, recoveries, and
// the outcome — in order, with offsets from the start. Built from the audit trail
// (which never holds credentials or generated passwords) and the task state.
func (a *Agent) Trace(ctx context.Context, u *User, id string) (map[string]any, error) {
	t, st, err := a.load(ctx, u, id)
	if err != nil {
		return nil, err
	}
	rows, err := a.Store.ListAudit(ctx, u.Scope, id, 300)
	if err != nil {
		return nil, err
	}
	// oldest first
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	start := t.CreatedAt
	var timeline []map[string]any
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Kind]++
		timeline = append(timeline, map[string]any{"at": r.CreatedAt, "t_ms": r.CreatedAt.Sub(start).Milliseconds(), "kind": r.Kind, "data": r.Data})
	}
	out := map[string]any{"task_id": t.ID, "request": t.Request, "stage": st.Stage, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
		"duration_ms": t.UpdatedAt.Sub(t.CreatedAt).Milliseconds(), "decision": st.Intent, "interpretation": st.Interp, "exec_mode": st.ExecMode,
		"policy": st.Decision, "result": st.Result, "model": st.Model, "timeline": timeline,
		"request_id": st.RequestID, "context_resets": st.ContextResets, "conversation_id": st.ConversationID, "execution": st.Exec, "uploads": st.Uploads,
		"counters": map[string]int{"planner_rounds": counts["plan_round"] + counts["plan_round_invalid"], "plan_rejections": counts["plan_rejected"],
			"tool_failures": counts["tool_call"], "recoveries": counts["step_recovered"], "repairs": counts["plan_repair"], "retries": counts["plan_retry"]}}
	if p := st.Plan; p != nil {
		var steps []map[string]any
		for _, s := range p.Steps {
			steps = append(steps, map[string]any{"id": s.ID, "tool": s.Tool, "purpose": s.Purpose, "status": s.Status, "verified": s.Verified,
				"mismatches": s.Mismatches, "error": s.Error, "args": s.Args, "record": recordRef(s.Result)})
		}
		out["plan"] = map[string]any{"status": p.Status, "domain": p.Domain, "skill": p.Skill, "summary": p.Summary, "steps": steps, "rounds": p.Rounds}
	}
	return out, nil
}

// verifyGone establishes a delete from application state (D60): the record must no
// longer be readable (404), or — where the application's delete deactivates — must read
// back deactivated. Accepting the DELETE call is not proof: a delete that removed nothing
// (or another record) was reported "verified" before.
func (a *Agent) verifyGone(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs) (bool, json.RawMessage, []string) {
	id := args.Path["id"]
	if id == "" {
		return false, nil, []string{"no record id to verify the delete"}
	}
	if rb := readBackOp(op); rb != nil && strings.Contains(op.Path, "{id}") {
		ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": id, "kind": args.Path["kind"]}})
		v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil)
		if err != nil {
			if ae, ok := accounting.AsAPIError(err); ok && ae.Status == 404 {
				return true, nil, nil
			}
			return false, nil, []string{"could not read the record back after the delete: " + errString(err)}
		}
		b, _ := json.Marshal(v)
		var rec map[string]any
		_ = json.Unmarshal(b, &rec)
		if deactivated(rec) {
			return true, b, nil
		}
		return false, b, []string{"the record still exists after the delete"}
	}
	lo := listOpFor(op)
	if lo == nil {
		return false, nil, []string{"the application offers no way to confirm the delete"}
	}
	la := tools.OpArgs{Path: tools.Scalars{}, Query: tools.Scalars{}}
	for _, p := range lo.PathParams {
		la.Path[p] = args.Path[p]
	}
	if containsStr(lo.QueryParams, "page_size") {
		la.Query["page_size"] = "500"
	}
	raw, _ := json.Marshal(la)
	out, err := a.Tools.Call(ctx, a.env(u), lo.ID, pruneEmptyPath(lo, raw), nil)
	if err != nil {
		return false, nil, []string{"could not read the list after the delete: " + errString(err)}
	}
	for _, it := range listItemsFor(op, out) {
		if strconv.FormatInt(int64Of(it["id"]), 10) == id && !deactivated(it) {
			b, _ := json.Marshal(it)
			return false, b, []string{"the record is still listed after the delete"}
		}
	}
	return true, nil, nil
}

// deactivated: the record reads back as removed by the application's own semantics.
func deactivated(rec map[string]any) bool {
	if v, ok := rec["is_active"].(bool); ok && !v {
		return true
	}
	if v, ok := rec["deleted_at"]; ok && v != nil {
		return true
	}
	switch fmt.Sprint(rec["status"]) {
	case "cancelled", "canceled", "voided", "void", "archived", "deleted":
		return true
	}
	return false
}
