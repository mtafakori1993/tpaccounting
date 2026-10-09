package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// A form saved by the user (D46). "Show me on screen" fills the real form and leaves it to
// the user. When they press Save themselves, the widget reports it (user_saved), and a
// later chat message checks too: the new record is looked for through the application
// (only records newer than the baseline taken when the form was opened), verified by
// reading it back, becomes the conversation's document, and the pending source file is
// attached to it exactly as if the assistant had saved it. Nothing is registered by the
// assistant here; if no new record exists, nothing is claimed.

// previewPending is the plan step whose form the preview opened (the first pending one).
func previewPending(st *State) (*PlanStep, *catalog.Operation, tools.OpArgs, bool) {
	if st.Plan == nil {
		return nil, nil, tools.OpArgs{}, false
	}
	for i := range st.Plan.Steps {
		s := &st.Plan.Steps[i]
		if s.Status == "done" || isSourceAttach(st, *s) {
			continue
		}
		op, ok := catalog.Load().Get(s.Tool)
		if !ok {
			return nil, nil, tools.OpArgs{}, false
		}
		args, err := resolveRefs(st.Plan, s.Args)
		if err != nil {
			return nil, nil, tools.OpArgs{}, false
		}
		return s, op, args, true
	}
	return nil, nil, tools.OpArgs{}, false
}

// markPreviewBaseline records the newest existing record id before the form is shown,
// so a record the user then saves can be told apart from older ones.
func (a *Agent) markPreviewBaseline(ctx context.Context, u *User, st *State) {
	st.UIBaseline = 0
	if _, op, args, ok := previewPending(st); ok {
		if op.Kind == "create" {
			for _, it := range a.listRecordsBy(ctx, u, op, args, false) {
				if id := int64Of(it["id"]); id > st.UIBaseline {
					st.UIBaseline = id
				}
			}
		}
		return
	}
	if st.Proposal == nil {
		return
	}
	for _, x := range a.recentBuiltin(ctx, u, st) {
		if x.id > st.UIBaseline {
			st.UIBaseline = x.id
		}
	}
}

type builtinRecord struct {
	id  int64
	out any
}

// recentBuiltin lists the newest records of the built-in proposal's own type.
func (a *Agent) recentBuiltin(ctx context.Context, u *User, st *State) []builtinRecord {
	p := st.Proposal
	env := a.env(u)
	var out []builtinRecord
	switch {
	case p.Tx != nil:
		args, _ := json.Marshal(map[string]any{"kind": strings.TrimPrefix(p.Action, "create_"), "limit": 50})
		if res, err := a.Tools.Call(ctx, env, "search_transactions", args, nil); err == nil {
			for _, x := range res.([]tools.Transaction) {
				xx := x
				out = append(out, builtinRecord{x.ID, &xx})
			}
		}
	case p.Expense != nil:
		args, _ := json.Marshal(map[string]any{"limit": 50})
		if res, err := a.Tools.Call(ctx, env, "search_expenses", args, nil); err == nil {
			for _, x := range res.([]tools.Expense) {
				xx := x
				out = append(out, builtinRecord{x.ID, &xx})
			}
		}
	}
	return out
}

// handSaved finds the record the user saved from the preview form. For a plan step it
// returns the record JSON; for a built-in proposal the typed record. diffs lists values
// the user changed on the form (information, not a failure: they are the user's own).
func (a *Agent) handSaved(ctx context.Context, u *User, t *store.Task, st *State) (rec json.RawMessage, typed any, diffs []string) {
	if _, op, args, ok := previewPending(st); ok {
		if rec, diffs = a.recordAfterUI(ctx, u, st, op, args); rec != nil || op.Kind != "create" {
			if op.Kind != "create" && len(diffs) > 0 {
				return nil, nil, nil // an edit form whose values are not saved yet
			}
			return rec, nil, diffs
		}
		// the user changed values on the form: the only new record is theirs
		var fresh []map[string]any
		for _, it := range a.listRecordsBy(ctx, u, op, args, false) {
			if int64Of(it["id"]) > st.UIBaseline {
				fresh = append(fresh, it)
			}
		}
		if len(fresh) != 1 || st.UIBaseline == 0 {
			return nil, nil, nil
		}
		id := int64Of(fresh[0]["id"])
		b, _ := json.Marshal(fresh[0])
		if rb := readBackOp(op); rb != nil {
			ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": strconv.FormatInt(id, 10), "kind": args.Path["kind"]}})
			if v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil); err == nil {
				b, _ = json.Marshal(v)
			}
		}
		var got map[string]any
		_ = json.Unmarshal(b, &got)
		raw := map[string]json.RawMessage{}
		_ = json.Unmarshal(args.Body, &raw)
		return b, nil, compareBody(raw, got)
	}
	if st.Proposal == nil {
		return nil, nil, nil
	}
	if found, mism := a.findCreated(ctx, u, t, st); found != nil {
		return nil, found, mism
	}
	var fresh []builtinRecord
	for _, x := range a.recentBuiltin(ctx, u, st) {
		if x.id > st.UIBaseline {
			fresh = append(fresh, x)
		}
	}
	if len(fresh) != 1 || st.UIBaseline == 0 {
		return nil, nil, nil
	}
	return nil, fresh[0].out, []string{"edited on the form"}
}

// completeHandSaved finishes the operation when the user saved the form themselves.
// quiet: called from a chat message — say nothing when no saved record exists yet.
// It reports whether a saved record was found.
func (a *Agent) completeHandSaved(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, quiet bool) bool {
	if st.Stage != StageUIPreview {
		return false
	}
	status(e, st.Lang, "verifying")
	rec, typed, diffs := a.handSaved(ctx, u, t, st)
	if rec == nil && typed == nil {
		a.audit(ctx, u, t.ID, "user_save_not_found", map[string]any{"baseline": st.UIBaseline})
		if !quiet {
			e.Emit("message", map[string]any{"text": tr(st.Lang,
				"I don't see a new saved record yet, so nothing was registered and the file is not attached. Save the form, or tell me to register it.",
				"هنوز رکورد ذخیره‌شده جدیدی نمی‌بینم؛ پس چیزی ثبت نشده و فایل هم پیوست نشد. فرم را ذخیره کنید یا بگویید خودم ثبت کنم.")})
			e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		}
		return false
	}
	// the user's own Save is the human act; the assistant registered nothing
	st.Approved, st.ApprovedBy = true, u.Scope.UserID
	if len(diffs) > 0 {
		e.Emit("message", map[string]any{"text": tr(st.Lang, "You changed some values on the form; the saved record is what counts: ",
			"برخی مقادیر را در فرم تغییر دادید؛ ملاک رکورد ذخیره‌شده است: ") + strings.Join(diffs, "، ")})
	}
	if s, op, args, ok := previewPending(st); ok {
		s.Result, s.Status, s.Verified, s.Args = rec, "done", true, args
		a.audit(ctx, u, t.ID, "record_change", map[string]any{"tool": s.Tool, "step": s.ID, "after": json.RawMessage(rec), "verified": true,
			"edited": diffs, "user": u.Me.Email, "actor": "user", "via": "user_saved_form"})
		if op.Kind != "delete" {
			s.Artifact = artifactFromRecord(op, rec)
			emitArtifact(e, s.Artifact)
		}
		// the source document is attached by the system, never shown as a form
		a.runSourceAttaches(ctx, u, t, st, e, "human")
		// more steps: show the next one's form the same way
		if _, _, _, more := previewPending(st); more {
			if plan, err := BuildPreviewPlan(st); err == nil {
				a.markPreviewBaseline(ctx, u, st)
				plan.TaskID = t.ID
				a.save(ctx, t, st)
				a.audit(ctx, u, t.ID, "ui_preview", map[string]any{"after_user_save": s.ID, "steps": len(plan.Steps)})
				e.Emit("message", map[string]any{"text": tr(st.Lang, "Saved. Next form:", "ذخیره شد. فرم بعدی:")})
				e.Emit("ui_plan", plan)
				e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
				return true
			}
		}
		st.ExecMode = "human_ui"
		a.finishPlan(ctx, u, t, st, e) // attaches the source file to the saved record
		return true
	}
	res := a.verify(ctx, u, st, typed)
	res.Mode, res.Via = "human", "human_ui"
	res.Verified = res.EntityID > 0 && !hasReadBackFailure(res.Mismatches)
	st.Result = res
	a.audit(ctx, u, t.ID, "accounting_result", map[string]any{"entity_type": res.EntityType, "entity_id": res.EntityID, "verified": res.Verified, "via": "user_saved_form"})
	a.attachUploads(ctx, u, t, st, e)
	a.finish(ctx, u, t, st, e)
	return true
}

func hasReadBackFailure(m []string) bool {
	for _, x := range m {
		if x == "read-back failed" {
			return true
		}
	}
	return false
}
