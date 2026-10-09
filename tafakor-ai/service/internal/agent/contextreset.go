package agent

// Fresh-context recovery (D62). Before a message is processed in its conversation the
// deterministic detector (package ctxconflict) checks whether the conversation's state
// is unsafe for it. On a strong conflict the request — exact text, same attachments,
// same request_id — is submitted once to a NEW conversation, as if the user had opened
// a new chat and typed it. Nothing is executed again: the old task is closed (or left
// alone when its outcome is unknown), and the fresh context starts at interpretation,
// then follows the normal approval/execution pipeline. Global context (user, company,
// permissions, settings, language) is per user and stays; conversation/task state
// (plan, pending questions, entities, execution mode, failures) does not.
//
// Identities: request_id (one user request, across contexts) · conversation_id ·
// task_id (one operation) · execution (the task's Exec record).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/ctxconflict"
	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/intent"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func reqKey(id string) string { return "req:" + id }

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// snapshot reads the conversation's current task for the detector.
func (a *Agent) snapshot(ctx context.Context, u *User, c *conversation) (*ctxconflict.Task, *store.Task, *State) {
	if c == nil || c.state.ActiveTask == "" {
		return nil, nil, nil
	}
	t, st, err := a.load(ctx, u, c.state.ActiveTask)
	if err != nil {
		return nil, nil, nil
	}
	s := &ctxconflict.Task{ID: t.ID, Stage: st.Stage, UserText: t.Request, Entities: map[string]string{}}
	if st.Intent != nil {
		s.Domain = st.Intent.Domain
	}
	if st.Exec != nil {
		s.FailureCode = st.Exec.FailureCode
	}
	if p := st.Plan; p != nil {
		s.PlanStatus = p.Status
		if p.Domain != "" {
			s.Domain = p.Domain
		}
		for k, v := range p.Entities {
			if sv, ok := v.(string); ok && sv != "" {
				s.Entities[k] = sv
			}
		}
		for _, x := range p.Steps {
			if x.Status == "done" {
				s.DoneSteps++
			}
			if x.Target != nil && strings.HasPrefix(x.Tool, "cost_centers.") {
				s.Entities["cost_center"] = x.Target.Name
			}
		}
	}
	if f := st.Frame; f != nil {
		if f.Event.Project != "" {
			s.Entities["project"] = f.Event.Project
		}
		if f.Event.Counterparty != "" {
			s.Entities["counterparty"] = f.Event.Counterparty
		}
	}
	return s, t, st
}

// failures lists this conversation's requests that ended stopped (the planner/context
// could not find a way) — the requests a retry in place would reproduce.
func (a *Agent) failures(ctx context.Context, u *User, c *conversation) []ctxconflict.Failure {
	if c == nil {
		return nil
	}
	ts, err := a.Store.ListTasks(ctx, u.Scope, 60)
	if err != nil {
		return nil
	}
	var out []ctxconflict.Failure
	for _, t := range ts {
		var st State
		if json.Unmarshal(t.State, &st) != nil || st.ConversationID != c.row.ID || st.Stage != StageStopped {
			continue
		}
		f := ctxconflict.Failure{TaskID: t.ID, Normalized: ctxconflict.Normalize(t.Request), Outcome: st.Stage, At: t.UpdatedAt, ContextResets: st.ContextResets}
		if st.Plan != nil {
			f.Domain = st.Plan.Domain
			if len(st.Plan.Steps) > 0 {
				f.Tool = st.Plan.Steps[0].Tool
			}
		}
		out = append(out, f)
	}
	return out
}

// isAnswer: the message answers or steers the open operation rather than starting one.
func isAnswer(text string, cur *ctxconflict.Task) bool {
	if _, only := execmode.OnlyInstruction(text); only {
		return true
	}
	if cur == nil || !followUpStages[cur.Stage] {
		return false // nothing is waiting for an answer
	}
	if isRetry(text) && (cur.Stage == StageStopped || cur.Stage == StageUIFailed) {
		return true
	}
	if classify.DetectConfirm(text) && (cur.Stage == StageApproval || cur.Stage == StageUIPreview) && !ctxconflict.IsCommand(text) {
		return true
	}
	return (cur.Stage == StageQuestion || cur.Stage == StageApproval) && !ctxconflict.IsCommand(text)
}

// priorResets: how often this request (by id) was already moved to a fresh context.
func (a *Agent) priorResets(ctx context.Context, u *User, requestID string) int {
	rows, err := a.Store.ListAudit(ctx, u.Scope, reqKey(requestID), 200)
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range rows {
		if r.Kind == "context_reset" {
			n++
		}
	}
	return n
}

// checkContext runs the detector for one message and records the attempt.
func (a *Agent) checkContext(ctx context.Context, u *User, c *conversation, req StartRequest, text string) (ctxconflict.Result, *store.Task, *State) {
	cur, t, st := a.snapshot(ctx, u, c)
	in := ctxconflict.Input{UserRequest: text, Normalized: ctxconflict.Normalize(text), Current: cur, Command: ctxconflict.IsCommand(text),
		IsAnswer: isAnswer(text, cur), Anaphoric: ctxconflict.Anaphoric(text), NewDomain: intent.Detect(text).Domain, Failures: a.failures(ctx, u, c), ResetsSoFar: max(req.resets, a.priorResets(ctx, u, req.RequestID)), Now: a.now()}
	if c != nil {
		in.ConversationID = c.row.ID
	}
	res := ctxconflict.Detect(in)
	if res.Conflict {
		a.audit(ctx, u, reqKey(req.RequestID), "context_conflict_detected", map[string]any{"request_id": req.RequestID, "conversation_id": in.ConversationID,
			"input": in, "result": res})
	}
	return res, t, st
}

// freshContext moves the request to a new conversation (at most once per request).
func (a *Agent) freshContext(ctx context.Context, u *User, c *conversation, req StartRequest, res ctxconflict.Result, old *store.Task, oldSt *State, e Emitter) string {
	l := lang(u)
	// The old task is never executed again. An open one is closed; one whose outcome is
	// unknown (operating on screen) is left for its own verification.
	prior := []map[string]any{}
	if old != nil && oldSt != nil {
		if oldSt.Plan != nil {
			for _, s := range oldSt.Plan.Steps {
				if s.Status == "done" {
					prior = append(prior, map[string]any{"step": s.ID, "tool": s.Tool, "verified": s.Verified, "record": recordRef(s.Result)})
				}
			}
		}
		if followUpStages[oldSt.Stage] {
			oldSt.Stage, oldSt.Message = StageCancelled, "context_reset"
			a.save(ctx, old, oldSt)
		}
		a.audit(ctx, u, old.ID, "context_reset", map[string]any{"request_id": req.RequestID, "reasons": res.Reasons, "stage_after": oldSt.Stage})
	}
	// files: the message's own, or — when it had none — the earlier files this request
	// would have inherited in its conversation (D44). Same stored files, same content.
	uploads := append([]string{}, req.Uploads...)
	inherited := false
	if len(uploads) == 0 && c != nil && c.state.Pending != nil {
		for _, f := range c.state.Pending.Uploads {
			uploads = append(uploads, f.ID)
		}
		inherited = len(uploads) > 0
	}
	from := ""
	if c != nil {
		from = c.row.ID
	}
	a.audit(ctx, u, reqKey(req.RequestID), "context_reset", map[string]any{"request_id": req.RequestID, "from_conversation": from,
		"message_sha256": sha(req.Message), "uploads": uploads, "uploads_inherited": inherited, "reasons": res.Reasons, "severity": res.Severity,
		"confidence": res.Confidence, "causing_state": res.CausingState, "affected_decision": res.AffectedDecision, "architecture_bug": res.ArchitectureBug,
		"prior_mutations": prior, "old_task": taskIDOf(old)})
	a.audit(ctx, u, reqKey(req.RequestID), "request_attempt", map[string]any{"request_id": req.RequestID, "attempt": req.resets + 1, "conversation_id": from,
		"task_id": taskIDOf(old), "action": ctxconflict.NewContext, "result": "CONTEXT_CONFLICT", "reasons": res.Reasons})
	e.Emit("message", map[string]any{"text": tr(l, "To avoid interference with the earlier state of this conversation, I'm continuing your request in a fresh context.",
		"برای جلوگیری از تداخل با وضعیت گفت‌وگوی قبلی، درخواست را در یک زمینه تازه ادامه می‌دهم.")})
	base := e
	if r, ok := e.(*recorder); ok {
		base = r.base
	}
	nreq := req
	nreq.ConversationID, nreq.Uploads, nreq.resets, nreq.pre = "", uploads, req.resets+1, nil
	return a.Start(ctx, u, nreq, base)
}

func taskIDOf(t *store.Task) string {
	if t == nil {
		return ""
	}
	return t.ID
}

// ---------- trace and metrics ----------

// RequestTrace is the lifecycle of one request across contexts.
func (a *Agent) RequestTrace(ctx context.Context, u *User, id string) (map[string]any, error) {
	rows, err := a.Store.ListAudit(ctx, u.Scope, reqKey(id), 200)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("unknown request")
	}
	var attempts, resets, conflicts []map[string]any
	for i := len(rows) - 1; i >= 0; i-- {
		var d map[string]any
		_ = json.Unmarshal(rows[i].Data, &d)
		d["at"] = rows[i].CreatedAt
		switch rows[i].Kind {
		case "request_attempt":
			if tid, _ := d["task_id"].(string); tid != "" && d["result"] == nil {
				d["result"], d["stage"] = a.attemptResult(ctx, u, tid)
			}
			attempts = append(attempts, d)
		case "context_reset":
			resets = append(resets, d)
		case "context_conflict_detected":
			conflicts = append(conflicts, d)
		}
	}
	return map[string]any{"request_id": id, "attempts": attempts, "context_resets": resets, "conflicts": conflicts, "context_reset_count": len(resets)}, nil
}

// attemptResult reads an attempt's outcome from its task.
func (a *Agent) attemptResult(ctx context.Context, u *User, taskID string) (string, string) {
	_, st, err := a.load(ctx, u, taskID)
	if err != nil {
		return "UNKNOWN", ""
	}
	switch st.Stage {
	case StageDone:
		if st.Result != nil && !st.Result.Verified {
			return "DONE_UNVERIFIED", st.Stage
		}
		return "SUCCESS", st.Stage
	case StageQuestion, StageApproval, StageUIPreview, StageUIRunning:
		return "IN_PROGRESS", st.Stage
	case StageCancelled:
		return "CANCELLED", st.Stage
	}
	return "FAILED", st.Stage
}

// ContextMetrics: whether fresh-context recovery improves reliability, and which
// patterns keep needing it (each repeated cause is an architecture signal).
func (a *Agent) ContextMetrics(ctx context.Context, u *User) (map[string]any, error) {
	conflicts, err := a.Store.ListAuditKind(ctx, u.Scope, "context_conflict_detected", 5000)
	if err != nil {
		return nil, err
	}
	resets, err := a.Store.ListAuditKind(ctx, u.Scope, "context_reset", 5000)
	if err != nil {
		return nil, err
	}
	attempts, _ := a.Store.ListAuditKind(ctx, u.Scope, "request_attempt", 5000)
	m := map[string]any{}
	n, ok, fail, sameFailed, sameOK, attTotal, attOK, dupPrevented := 0, 0, 0, 0, 0, 0, 0, 0
	patterns := map[string]int{}
	for _, r := range resets {
		if !strings.HasPrefix(r.TaskID, "req:") {
			continue // the old task's own note
		}
		n++
		var d struct {
			RequestID string               `json:"request_id"`
			Reasons   []ctxconflict.Reason `json:"reasons"`
			Uploads   []string             `json:"uploads"`
			Prior     []any                `json:"prior_mutations"`
		}
		_ = json.Unmarshal(r.Data, &d)
		same := false
		for _, x := range d.Reasons {
			patterns[x.Code]++
			same = same || x.Code == ctxconflict.SameRequestFailed
		}
		if len(d.Prior) > 0 {
			dupPrevented++
		}
		// the fresh attempt's outcome
		res := "UNKNOWN"
		var freshUploads []Upload
		for _, at := range attempts {
			if at.TaskID != r.TaskID {
				continue
			}
			var ad struct {
				Attempt int    `json:"attempt"`
				TaskID  string `json:"task_id"`
			}
			_ = json.Unmarshal(at.Data, &ad)
			if ad.Attempt >= 2 && ad.TaskID != "" {
				res, _ = a.attemptResult(ctx, u, ad.TaskID)
				if _, st, err := a.load(ctx, u, ad.TaskID); err == nil {
					freshUploads = st.Uploads
				}
			}
		}
		if res == "SUCCESS" || res == "IN_PROGRESS" {
			ok++
			if same {
				sameOK++
			}
		} else {
			fail++
		}
		if same {
			sameFailed++
		}
		if len(d.Uploads) > 0 {
			attTotal++
			if len(freshUploads) == len(d.Uploads) {
				attOK++
			}
		}
	}
	rate := func(a, b int) float64 {
		if b == 0 {
			return 0
		}
		return float64(a) / float64(b)
	}
	var signals []map[string]any
	for code, c := range patterns {
		if c >= 3 {
			signals = append(signals, map[string]any{"pattern": code, "resets": c, "note": "repeated fresh-context recovery: investigate the underlying planner/state bug"})
		}
	}
	m["context_conflict_detected"] = len(conflicts)
	m["context_reset_count"] = n
	m["fresh_context_success_rate"] = rate(ok, n)
	m["fresh_context_failure_rate"] = rate(fail, n)
	m["same_request_failed_in_original_context"] = sameFailed
	m["same_request_succeeded_in_fresh_context"] = sameOK
	m["attachment_preservation_success"] = rate(attOK, attTotal)
	m["duplicate_mutation_prevented"] = dupPrevented
	m["reset_patterns"] = patterns
	m["reliability_signals"] = signals
	m["since"] = a.now().Add(-30 * 24 * time.Hour)
	return m, nil
}
