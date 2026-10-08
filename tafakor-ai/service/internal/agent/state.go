// Package agent runs assistant tasks: understand → resolve → check → decide →
// (approve) → execute → verify → report. Each step is persisted so a page refresh
// can resume or cancel a task. Every success message is built from state read
// back from the accounting API after the write.
package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/extract"
	"github.com/tafakorpardazan/tafakor-ai/internal/learning"
	"github.com/tafakorpardazan/tafakor-ai/internal/policy"
)

// Stages of a task.
const (
	StageNew       = "new"
	StageQuestion  = "awaiting_answer"
	StageApproval  = "awaiting_approval"
	StageUIRunning = "ui_running"
	StageUIPreview = "ui_preview" // the real form is open, pre-filled, waiting to be submitted by hand
	StageUIFailed  = "ui_failed"
	StageDone      = "done"
	StageCancelled = "cancelled"
	StageFailed    = "failed"
	StageStopped   = "stopped" // needs the user to do something in the app first
)

type Upload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

// Result is the verified outcome, read back from the accounting API.
type Result struct {
	EntityType    string   `json:"entity_type"`
	EntityID      int64    `json:"entity_id"`
	Type          string   `json:"type,omitempty"` // canonical type, e.g. sales_quotation
	Side          string   `json:"side,omitempty"` // sales | purchase
	Kind          string   `json:"kind,omitempty"` // quotation | invoice | return | receipt | payment | …
	Number        int64    `json:"number"`
	JournalNumber *int64   `json:"journal_number,omitempty"`
	Amount        string   `json:"amount"`
	Date          string   `json:"date"`
	Party         string   `json:"party,omitempty"`
	Status        string   `json:"status"`
	Route         string   `json:"route"`
	Attachments   []string `json:"attachments,omitempty"`
	Mode          string   `json:"mode"` // human | auto
	Verified      bool     `json:"verified"`
	Mismatches    []string `json:"mismatches,omitempty"`
	BudgetWarning bool     `json:"budget_warning,omitempty"`
	Undone        bool     `json:"undone,omitempty"`
	PDF           string   `json:"pdf,omitempty"`
}

type State struct {
	Stage          string                   `json:"stage"`
	Lang           string                   `json:"lang"`
	UserText       string                   `json:"user_text"`
	Uploads        []Upload                 `json:"uploads,omitempty"`
	Extractions    []*extract.Extraction    `json:"extractions,omitempty"`
	Interp         *classify.Interpretation `json:"interpretation,omitempty"`
	Answers        map[string]string        `json:"answers,omitempty"`
	Proposal       *classify.Proposal       `json:"proposal,omitempty"`
	Decision       *policy.Decision         `json:"decision,omitempty"`
	PayloadHash    string                   `json:"payload_hash,omitempty"`
	ArgsJSON       json.RawMessage          `json:"args,omitempty"`
	Duplicates     json.RawMessage          `json:"duplicates,omitempty"`
	Offers         []learning.Offer         `json:"offers,omitempty"`
	Result         *Result                  `json:"result,omitempty"`
	AskThisTime    bool                     `json:"ask_this_time,omitempty"`
	ExecMode       string                   `json:"exec_mode,omitempty"` // api | ui
	Approved       bool                     `json:"approved,omitempty"`
	ApprovedBy     int64                    `json:"approved_by,omitempty"`
	Overrides      map[string]string        `json:"overrides,omitempty"` // description/reference edits
	Message        string                   `json:"message,omitempty"`
	UndoOf         string                   `json:"undo_of,omitempty"`
	Target         *Target                  `json:"target,omitempty"`
	Frame          *classify.Frame          `json:"frame,omitempty"` // structured state of the operation (merged across turns)
	ConversationID string                   `json:"conversation_id,omitempty"`
	Plan           *PlanState               `json:"plan,omitempty"` // capability-driven multi-step operation
	Model          string                   `json:"model,omitempty"`

	// Execution instruction, from the user's own words. PendingDirect persists across
	// the follow-up turns of one operation so multi-turn fills stay direct.
	ExecDirect    bool `json:"exec_direct,omitempty"`
	ExecPreview   bool `json:"exec_preview,omitempty"`
	PendingDirect bool `json:"pending_direct,omitempty"`
	// Active is the document this task is about (set when a reference was resolved).
	Active *Artifact `json:"active,omitempty"`
}

// Target is an existing document a task acts on (undo, attach, pdf).
type Target struct {
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	Label      string `json:"label,omitempty"`
	Side       string `json:"side,omitempty"`
	Kind       string `json:"kind,omitempty"`
}

// Artifact is a document the assistant created, changed or was told about. It is
// remembered in the conversation so a later pronoun (این / همین / رسیدش / pdf این)
// resolves to the exact record without searching the database.
type Artifact struct {
	Type   string `json:"type"` // canonical, e.g. sales_quotation, treasury_receipt, expense
	ID     int64  `json:"id"`
	Number int64  `json:"number,omitempty"`
	Side   string `json:"side,omitempty"` // sales | purchase
	Kind   string `json:"kind,omitempty"` // quotation | invoice | return | receipt | payment | …
	Label  string `json:"label,omitempty"`
	At     int64  `json:"at,omitempty"` // unix seconds, for "the last one"
}

// Emitter streams server-sent events to the widget.
type Emitter interface {
	Emit(event string, data any)
}

type sse struct {
	mu sync.Mutex
	w  http.ResponseWriter
	f  http.Flusher
}

func NewSSE(w http.ResponseWriter) Emitter {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	s := &sse{w: w, f: f}
	_, _ = fmt.Fprint(w, ": stream open\n\n")
	if f != nil {
		f.Flush()
	}
	return s
}

func (s *sse) Emit(event string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b)
	if s.f != nil {
		s.f.Flush()
	}
}

// Recorder collects events (tests, JSON responses).
type Recorder struct {
	mu     sync.Mutex
	Events []Ev
}

type Ev struct {
	Name string
	Data any
}

func (r *Recorder) Emit(event string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, Ev{event, data})
}

func (r *Recorder) Last(name string) any {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.Events) - 1; i >= 0; i-- {
		if r.Events[i].Name == name {
			return r.Events[i].Data
		}
	}
	return nil
}

// status texts shown in the panel (never chain-of-thought).
var statusText = map[string][2]string{
	"reading":       {"Reading the document…", "در حال خواندن سند…"},
	"understanding": {"Understanding your request…", "در حال فهم درخواست…"},
	"looking_up":    {"Looking up parties, accounts and projects…", "در حال یافتن طرف حساب، حساب و پروژه…"},
	"checking":      {"Checking for duplicates and period locks…", "بررسی تکراری بودن و قفل دوره…"},
	"waiting":       {"Waiting for your approval…", "در انتظار تایید شما…"},
	"question":      {"I need one detail from you…", "یک نکته را باید از شما بپرسم…"},
	"registering":   {"Registering…", "در حال ثبت…"},
	"attaching":     {"Attaching the original document…", "در حال پیوست سند اصلی…"},
	"verifying":     {"Verifying…", "در حال بررسی نتیجه…"},
	"completed":     {"Completed", "انجام شد"},
	"searching":     {"Searching…", "در حال جستجو…"},
	"thinking":      {"Thinking…", "در حال فکر کردن…"},
	"pdf":           {"Generating the PDF…", "در حال ساخت PDF…"},
	"cancelled":     {"Cancelled", "لغو شد"},
	"stopped":       {"Stopped safely", "به‌صورت ایمن متوقف شد"},
}

func status(e Emitter, lang, key string) {
	t := statusText[key]
	txt := t[0]
	if lang == "fa" {
		txt = t[1]
	}
	e.Emit("status", map[string]string{"key": key, "text": txt})
}

func tr(lang, en, fa string) string {
	if lang == "fa" {
		return fa
	}
	return en
}
