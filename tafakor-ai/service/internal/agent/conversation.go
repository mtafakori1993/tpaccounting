package agent

// Conversation state. A conversation is one continuous exchange with the assistant:
// its transcript (user turns, assistant messages/questions, tool calls and results) and
// the id of the operation in progress live in the AI store under optimistic locking.
// A new message in a conversation whose operation is still open is a FOLLOW-UP: it is
// interpreted against the existing frame and merged into it — the operation is never
// re-detected from scratch and nothing already answered is asked again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/ctxconflict"
	"github.com/tafakorpardazan/tafakor-ai/internal/intent"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

type ConvMessage struct {
	Role   string          `json:"role"` // user | assistant | tool
	Kind   string          `json:"kind"` // text | question | approval | result | error | tool
	Text   string          `json:"text"`
	TaskID string          `json:"task_id,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	At     time.Time       `json:"at"`
}

type ConvState struct {
	Messages   []ConvMessage `json:"messages"`
	ActiveTask string        `json:"active_task,omitempty"`
	// Active is the document the conversation is about right now: the last one the
	// assistant created, changed or was told about. A later pronoun (این / همین / رسیدش /
	// pdf این / that one) binds to it instead of searching the database.
	Active *Artifact `json:"active,omitempty"`
	// Artifacts is the recent document stack (newest last) and Entities the latest
	// artifact per canonical type, so "همین فاکتور" and "رسیدش" can both resolve.
	Artifacts []Artifact          `json:"artifacts,omitempty"`
	Entities  map[string]Artifact `json:"entities,omitempty"`
	// Pending: files sent in a turn that registered nothing, inherited by the next request.
	Pending *PendingUploads `json:"pending_uploads,omitempty"`
}

type conversation struct {
	row   store.Conversation
	state ConvState
}

// remember records a document the assistant produced (or was told about) as the
// conversation's active artifact. Called from the recorder, so every path that emits
// an artifact event persists it, including across a page refresh.
func (c *conversation) remember(a Artifact) {
	if a.Type == "" || a.ID == 0 {
		return
	}
	if a.At == 0 {
		a.At = time.Now().UTC().Unix()
	}
	c.state.Active = &a
	// a document referenced again moves to the top instead of appearing twice
	kept := c.state.Artifacts[:0]
	for _, x := range c.state.Artifacts {
		if x.Type != a.Type || x.ID != a.ID {
			kept = append(kept, x)
		}
	}
	c.state.Artifacts = append(kept, a)
	if n := len(c.state.Artifacts); n > 20 {
		c.state.Artifacts = c.state.Artifacts[n-20:]
	}
	if c.state.Entities == nil {
		c.state.Entities = map[string]Artifact{}
	}
	c.state.Entities[a.Type] = a
}

func (c *conversation) add(m ConvMessage) {
	m.At = time.Now().UTC()
	c.state.Messages = append(c.state.Messages, m)
	if n := len(c.state.Messages); n > 400 { // bounded transcript; the frame holds the structured facts
		c.state.Messages = c.state.Messages[n-400:]
	}
}

// history returns the last turns as plain text for the model.
func (c *conversation) history(max int) []classify.Turn {
	var out []classify.Turn
	for _, m := range c.state.Messages {
		if m.Role == "tool" || m.Text == "" {
			continue
		}
		out = append(out, classify.Turn{Role: m.Role, Text: m.Text})
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// per-conversation lock: requests of one conversation are processed one at a time.
var convLocks sync.Map

func lockConversation(id string) func() {
	v, _ := convLocks.LoadOrStore(id, &sync.Mutex{})
	m := v.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

var ErrConversationChanged = errors.New("the conversation changed in another window; reload it and try again")

func (a *Agent) openConversation(ctx context.Context, u *User, id string) (*conversation, error) {
	if id != "" {
		row, err := a.Store.GetConversation(ctx, u.Scope, id)
		if err == nil {
			c := &conversation{row: row}
			_ = json.Unmarshal(row.State, &c.state)
			return c, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return &conversation{row: store.Conversation{ID: newTaskID(), CompanyID: u.Scope.CompanyID, UserID: u.Scope.UserID}}, nil
}

// saveConversation never overwrites newer state: a stale version is rejected.
func (a *Agent) saveConversation(ctx context.Context, c *conversation) error {
	b, err := json.Marshal(c.state)
	if err != nil {
		return err
	}
	c.row.State = b
	if err := a.Store.SaveConversation(ctx, &c.row); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ErrConversationChanged
		}
		return err
	}
	return nil
}

// recorder copies what the user sees into the transcript.
type recorder struct {
	base  Emitter
	conv  *conversation
	mu    sync.Mutex
	delta strings.Builder
	task  string
}

func (r *recorder) Emit(event string, data any) {
	r.base.Emit(event, data)
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := json.Marshal(data)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	str := func(k string) string { s, _ := m[k].(string); return s }
	switch event {
	case "task":
		r.task = str("task_id")
		r.conv.state.ActiveTask = r.task
	case "question":
		text := str("text")
		if opts, ok := m["options"].([]any); ok && len(opts) > 0 {
			var labels []string
			for _, o := range opts {
				if om, ok := o.(map[string]any); ok {
					labels = append(labels, fmt.Sprint(om["label"]))
				}
			}
			text += " [" + strings.Join(labels, " | ") + "]"
		}
		r.conv.add(ConvMessage{Role: "assistant", Kind: "question", Text: text, TaskID: r.task, Data: raw})
	case "message":
		r.conv.add(ConvMessage{Role: "assistant", Kind: "text", Text: str("text"), TaskID: r.task})
	case "error":
		r.conv.add(ConvMessage{Role: "assistant", Kind: "error", Text: str("message"), TaskID: r.task})
	case "approval":
		r.conv.add(ConvMessage{Role: "assistant", Kind: "approval", Text: "Waiting for approval", TaskID: r.task, Data: raw})
	case "artifact":
		var a Artifact
		if json.Unmarshal(raw, &a) == nil {
			r.conv.remember(a)
		}
	case "preview":
		r.conv.add(ConvMessage{Role: "assistant", Kind: "preview", Text: "", TaskID: r.task, Data: raw})
	case "result":
		r.conv.add(ConvMessage{Role: "assistant", Kind: "result", Text: str("summary"), TaskID: r.task, Data: raw})
	case "tool":
		r.conv.add(ConvMessage{Role: "tool", Kind: "tool", Text: str("tool"), TaskID: r.task, Data: raw})
	case "delta":
		r.delta.WriteString(str("text"))
	case "done":
		if r.delta.Len() > 0 {
			r.conv.add(ConvMessage{Role: "assistant", Kind: "text", Text: r.delta.String(), TaskID: r.task})
			r.delta.Reset()
		}
	}
}

// withConversation runs fn for one request of a conversation: serialized, recorded, persisted.
func (a *Agent) withConversation(ctx context.Context, u *User, convID string, e Emitter, fn func(c *conversation, e Emitter)) {
	c, err := a.openConversation(ctx, u, convID)
	if err != nil {
		e.Emit("error", map[string]any{"message": a.plain(lang(u), err)})
		return
	}
	unlock := lockConversation(c.row.ID)
	defer unlock()
	// re-read under the lock so we always build on the newest state
	if c.row.Version > 0 {
		if fresh, err := a.openConversation(ctx, u, c.row.ID); err == nil {
			c = fresh
		}
	}
	e.Emit("conversation", map[string]any{"conversation_id": c.row.ID})
	rec := &recorder{base: e, conv: c, task: c.state.ActiveTask}
	fn(c, rec)
	if err := a.saveConversation(ctx, c); err != nil {
		e.Emit("error", map[string]any{"message": tr(lang(u), "This conversation was changed in another window. Nothing was overwritten; please reload it.",
			"این گفتگو در پنجره دیگری تغییر کرده است. چیزی بازنویسی نشد؛ لطفاً دوباره بارگذاری کنید.")})
	}
}

// followUpStages: the operation is still open and a new message continues it.
// StageUIPreview is included: the form is open for the user to save by hand, but they
// may equally change their mind and ask the assistant to register it after all.
var followUpStages = map[string]bool{StageQuestion: true, StageApproval: true, StageUIFailed: true, StageStopped: true, StageUIPreview: true}

// activeOperation returns the open register task of the conversation, if any.
func (a *Agent) activeOperation(ctx context.Context, u *User, c *conversation) (*store.Task, *State) {
	if c.state.ActiveTask == "" {
		return nil, nil
	}
	t, st, err := a.load(ctx, u, c.state.ActiveTask)
	if err != nil || (st.Frame == nil && st.Plan == nil) || !followUpStages[st.Stage] {
		return nil, nil
	}
	return t, st
}

// answerKeys: card answers that a newly stated value supersedes.
var answerKeys = map[string][]string{
	"amount": {"amount", "amount_conflict"}, "date": {"date", "date_conflict"}, "counterparty": {"party"}, "project": {"project"},
	"warehouse": {"warehouse"}, "product_name": {"product"}, "paid_by": {"paid_by"}, "fx_rate": {"fx_rate"},
}

// followUp merges a new user message into the open operation and continues it.
func (a *Agent) followUp(ctx context.Context, u *User, t *store.Task, st *State, text string, c *conversation, e Emitter) {
	// «تأیید و ثبت» after "show me on screen": register the exact payload the user
	// reviewed on the real form — never re-plan or re-interpret it.
	// The user may already have saved the open form themselves: never register it a
	// second time; finish with their record and attach the file to it (D46).
	if st.Stage == StageUIPreview && a.completeHandSaved(ctx, u, t, st, e, true) {
		return
	}
	if st.Stage == StageUIPreview && classify.DetectConfirm(text) {
		a.confirmPreview(ctx, u, t, st, e)
		return
	}
	// A self-contained NEW request (the domain layer recognises a complete operation
	// in it) is not an answer to the open one: merging it lost the second request. The
	// open operation is closed — the user is told it was not registered — and the new
	// one starts.
	// A stopped / screen-failed task is not reopened by a different complete request
	// (D62: the antenna receipt was merged into a stuck plan and failed again).
	newRequest := ctxconflict.IsCommand(text) && !ctxconflict.Anaphoric(text) && !isRetry(text) && ctxconflict.Normalize(text) != ctxconflict.Normalize(st.UserText)
	// On an approval card a different complete request is not an edit of it (D62: «برای
	// پروژه وب‌سایت یه هزینه ثبت کن» was merged into a pending cost-centre delete).
	if st.Stage == StageApproval && st.Plan != nil && newRequest && !classify.DetectConfirm(text) {
		prev := st.UserText
		st.Stage, st.Message = StageCancelled, "superseded"
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "superseded", map[string]any{"by": text, "from_stage": StageApproval})
		e.Emit("message", map[string]any{"text": tr(st.Lang, "The earlier request («"+truncate(prev, 80)+"») was not registered; I'm doing the new one.",
			"درخواست قبلی («"+truncate(prev, 80)+"») ثبت نشد و کنار گذاشته شد؛ درخواست جدید را انجام می‌دهم.")})
		a.startTask(ctx, u, StartRequest{Message: text, ExecMode: st.ExecMode, RequestID: st.RequestID}, c, e)
		return
	}
	if (st.Stage == StageStopped || st.Stage == StageUIFailed) && newRequest {
		st.Stage, st.Message = StageCancelled, "superseded"
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "superseded", map[string]any{"by": text, "from_stage": "stopped_or_ui_failed"})
		a.startTask(ctx, u, StartRequest{Message: text, ExecMode: st.ExecMode, RequestID: st.RequestID}, c, e)
		return
	}
	if st.Stage == StageApproval || st.Stage == StageQuestion {
		if d := intent.Detect(text); d.Domain != intent.General && d.Domain != intent.Memory && d.Domain != intent.Documents {
			prev := st.UserText
			st.Stage, st.Message = StageCancelled, "superseded"
			a.save(ctx, t, st)
			a.audit(ctx, u, t.ID, "superseded", map[string]any{"by": text, "domain": d.Domain})
			e.Emit("message", map[string]any{"text": tr(st.Lang, "The earlier request («"+truncate(prev, 80)+"») was not registered; I'm doing the new one.",
				"درخواست قبلی («"+truncate(prev, 80)+"») ثبت نشد و کنار گذاشته شد؛ درخواست جدید را انجام می‌دهم.")})
			a.startTask(ctx, u, StartRequest{Message: text, ExecMode: st.ExecMode}, c, e)
			return
		}
	}
	// a follow-up may add the execution instruction ("مستقیم ثبت کن", "نشانم بده")
	if d, p := classify.DetectExecutionIntent(text); d || p {
		st.ExecDirect, st.ExecPreview = d, p
	}
	// «مستقیم انجام بده» / «با موس انجام بده» in a follow-up changes HOW the same operation runs
	a.resolveExecMode(ctx, u, st, text, "")
	if st.Plan != nil {
		a.planFollowUp(ctx, u, t, st, text, c, e)
		return
	}
	status(e, st.Lang, "understanding")
	cfg, err := tools.LoadCompanyConfig(ctx, a.env(u))
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	ai, model := a.Router.For(provider.PurposeClassify, "")
	var hist []classify.Turn
	if c != nil {
		hist = c.history(12)
		if n := len(hist); n > 0 && hist[n-1].Role == "user" && hist[n-1].Text == text {
			hist = hist[:n-1] // the current message is passed separately
		}
	}
	it, res, err := classify.Interpret(ctx, ai, model, classify.InterpretInput{UserText: text, History: hist, Frame: st.Frame, Config: cfg,
		Hints: a.hints(ctx, u), Knowledge: a.Knowledge, Today: a.today(), Lang: st.Lang, Documents: c.referenceContext()})
	a.cost(ctx, u, t.ID, res)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	a.audit(ctx, u, t.ID, "follow_up", map[string]any{"text": text, "interpretation": it, "frame_before": st.Frame})
	if it.Cancel {
		st.Stage = StageCancelled
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "cancelled", map[string]any{"by": u.Me.Email, "via": "follow_up"})
		e.Emit("message", map[string]any{"text": tr(st.Lang, "OK, cancelled. Nothing was registered.", "باشه، لغو شد. چیزی ثبت نشد.")})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	newType := it.Event.Type
	if it.IntentChanged && (it.Intent != "register" || (newType != "" && newType != classify.EvOther && newType != st.Frame.Event.Type)) {
		// the user explicitly switched to something else: close this operation, start the new one
		st.Stage = StageCancelled
		st.Message = "superseded"
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "superseded", map[string]any{"new_intent": it.Intent, "new_type": newType})
		it.IntentChanged = false
		a.startTask(ctx, u, StartRequest{Message: text, ExecMode: st.ExecMode, pre: it}, c, e)
		return
	}
	// MERGE: existing state + new information → merged state (nothing is reset)
	st.Frame.Merge(it.Event, it.ExplicitNull, text)
	for field, keys := range answerKeys {
		if st.Frame.Status[field] == classify.Known && fieldIn(it.Event, field) {
			for _, k := range keys {
				delete(st.Answers, k)
			}
		}
	}
	st.Interp.Event = st.Frame.Event
	st.UserText = st.Frame.UserText()
	st.Stage = StageNew
	a.save(ctx, t, st)
	e.Emit("task", map[string]any{"task_id": t.ID})
	a.register(ctx, u, t, st, cfg, e)
}

// fieldIn reports whether the update itself carried a value for field.
func fieldIn(ev classify.Event, field string) bool {
	b, _ := json.Marshal(ev)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	v, ok := m[field]
	if !ok || v == nil {
		return false
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// ConversationView is what the widget needs to restore a conversation after a refresh.
func (a *Agent) ConversationView(ctx context.Context, u *User, id string) (map[string]any, error) {
	row, err := a.Store.GetConversation(ctx, u.Scope, id)
	if err != nil {
		return nil, err
	}
	var st ConvState
	_ = json.Unmarshal(row.State, &st)
	out := map[string]any{"id": row.ID, "version": row.Version, "messages": st.Messages, "active_task": st.ActiveTask}
	if st.Active != nil {
		out["active"] = st.Active
	}
	if len(st.Artifacts) > 0 {
		out["artifacts"] = st.Artifacts
	}
	if st.ActiveTask != "" {
		if t, err := a.Store.GetTask(ctx, u.Scope, st.ActiveTask); err == nil {
			out["task"] = taskView(t)
		}
	}
	return out, nil
}

// convOf returns the conversation id a task belongs to ("" for legacy tasks).
func (a *Agent) convOf(ctx context.Context, u *User, taskID string) string {
	if _, st, err := a.load(ctx, u, taskID); err == nil {
		return st.ConversationID
	}
	return ""
}

// The task endpoints run inside the task's conversation: serialized, recorded and persisted.

func (a *Agent) Answer(ctx context.Context, u *User, id string, req AnswerRequest, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) {
		if label := answerLabel(ctx, a, u, id, req.Value); label != "" {
			c.add(ConvMessage{Role: "user", Kind: "answer", Text: label, TaskID: id})
		}
		a.answerIn(ctx, u, id, req, c, e)
	})
}

func answerLabel(ctx context.Context, a *Agent, u *User, id, value string) string {
	_, st, err := a.load(ctx, u, id)
	if err != nil || st.Proposal == nil || st.Proposal.Question == nil || st.Proposal.Question.Field == "details" {
		return ""
	}
	for _, o := range st.Proposal.Question.Options {
		if o.Value == value {
			return o.Label
		}
	}
	return value
}

func (a *Agent) Approve(ctx context.Context, u *User, id string, req ApproveRequest, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) {
		c.add(ConvMessage{Role: "user", Kind: "approval", Text: "approved", TaskID: id})
		a.approveIn(ctx, u, id, req, e)
	})
}

func (a *Agent) Edit(ctx context.Context, u *User, id string, req EditRequest, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) {
		b, _ := json.Marshal(req)
		c.add(ConvMessage{Role: "user", Kind: "edit", Text: "edited the proposal", TaskID: id, Data: b})
		a.editIn(ctx, u, id, req, e)
	})
}

func (a *Agent) UIResult(ctx context.Context, u *User, id string, req UIResult, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) {
		e.Emit("task", map[string]any{"task_id": id})
		a.uiResultIn(ctx, u, id, req, e)
	})
}

func (a *Agent) Undo(ctx context.Context, u *User, id string, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) {
		c.add(ConvMessage{Role: "user", Kind: "undo", Text: "undo", TaskID: id})
		a.undoIn(ctx, u, id, e)
	})
}

// Reissue shows the approval card again after a page refresh: a NEW single-use token for the
// unchanged payload (the old one stays bound to the same payload; execution is idempotent).
func (a *Agent) Reissue(ctx context.Context, u *User, id string, e Emitter) {
	t, st, err := a.load(ctx, u, id)
	if err != nil || st.Stage != StageApproval || st.Proposal == nil {
		e.Emit("error", map[string]any{"message": "nothing to approve"})
		return
	}
	a.reissueIn(ctx, u, t, st, e)
}
