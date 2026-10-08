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
}

type conversation struct {
	row   store.Conversation
	state ConvState
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
var followUpStages = map[string]bool{StageQuestion: true, StageApproval: true, StageUIFailed: true, StageStopped: true}

// activeOperation returns the open register task of the conversation, if any.
func (a *Agent) activeOperation(ctx context.Context, u *User, c *conversation) (*store.Task, *State) {
	if c.state.ActiveTask == "" {
		return nil, nil
	}
	t, st, err := a.load(ctx, u, c.state.ActiveTask)
	if err != nil || st.Frame == nil || !followUpStages[st.Stage] {
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
		Hints: a.hints(ctx, u), Knowledge: a.Knowledge, Today: a.today(), Lang: st.Lang})
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
	hash := payloadHash(st.Proposal.Action, st.ArgsJSON, st.Uploads)
	if hash != st.PayloadHash {
		e.Emit("error", map[string]any{"message": "the details changed; review again"})
		return
	}
	tok, exp, err := a.Approvals.Issue(ctx, u.Scope, t.ID, hash)
	if err != nil {
		e.Emit("error", map[string]any{"message": a.plain(st.Lang, err)})
		return
	}
	reasons := []map[string]string{}
	if st.Decision != nil {
		reasons = reasonTexts(st.Lang, st.Decision.Reasons)
	}
	e.Emit("preview", map[string]any{"action": st.Proposal.Action, "display": st.Proposal.Display, "preview": st.Proposal.Preview,
		"notes": st.Proposal.Notes, "flags": st.Proposal.Flags, "decision": st.Decision, "reasons": reasons})
	e.Emit("approval", map[string]any{"task_id": t.ID, "token": tok, "expires_at": exp, "payload_hash": hash, "reasons": reasons})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}
