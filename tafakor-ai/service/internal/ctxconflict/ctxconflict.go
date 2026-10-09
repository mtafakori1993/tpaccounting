// Package ctxconflict decides, deterministically, whether a new user request can be
// processed safely in the current conversation (D62). It never asks a model: every
// signal is read from persisted task state and the user's own words.
//
// Only a strong, specific conflict yields NEW_CONTEXT (the request is re-submitted,
// unchanged, in a fresh conversation). Changing the subject, a follow-up, another
// entity, a corrected value, another language or a long conversation are NOT conflicts.
//
// A reset is a recovery, not a fix: every NEW_CONTEXT carries the state that caused it,
// the decision it would have affected and whether that is an architecture bug, so
// repeated patterns surface in the reliability metrics.
package ctxconflict

import (
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// Actions.
const (
	Continue   = "CONTINUE"
	Ask        = "ASK"
	NewContext = "NEW_CONTEXT"
)

// Severities.
const (
	SevNone     = "none"
	SevLow      = "low"
	SevMedium   = "medium"
	SevHigh     = "high"
	SevCritical = "critical"
)

// Reason codes.
const (
	SameRequestFailed     = "SAME_REQUEST_FAILED_IN_CONTEXT"
	UnrecoverableTask     = "UNRECOVERABLE_TASK_STATE"
	UnknownExecutionState = "UNKNOWN_EXECUTION_STATE"
	PlannerInconsistent   = "REPEATED_PLANNER_INCONSISTENCY"
	DomainConflict        = "DOMAIN_CONFLICT_WITH_OPEN_TASK"
	StaleEntity           = "STALE_ENTITY_ASSUMPTION"
	NoMoreReset           = "NO_MORE_AUTOMATIC_RESET"
	FollowUpReference     = "FOLLOW_UP_REFERENCE"
)

// Task is the persisted state of the conversation's current task.
type Task struct {
	ID          string            `json:"id"`
	Stage       string            `json:"stage"`
	Domain      string            `json:"domain,omitempty"`
	UserText    string            `json:"user_text"`
	PlanStatus  string            `json:"plan_status,omitempty"`
	FailureCode string            `json:"failure_code,omitempty"`
	Entities    map[string]string `json:"entities,omitempty"` // entity type → value the task assumed
	DoneSteps   int               `json:"done_steps"`
}

// Failure is an earlier request of this conversation that did not complete.
type Failure struct {
	TaskID     string    `json:"task_id"`
	Normalized string    `json:"normalized"`
	Domain     string    `json:"domain,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Outcome    string    `json:"outcome"` // stopped | failed | no_executor | …
	At         time.Time `json:"at"`
	// ContextResets > 0: that attempt already ran in a fresh context for this request.
	ContextResets int `json:"context_resets,omitempty"`
}

type Input struct {
	ConversationID string            `json:"conversation_id"`
	Current        *Task             `json:"current_task,omitempty"`
	History        []string          `json:"-"`
	UserRequest    string            `json:"user_request"`
	Normalized     string            `json:"normalized_request"`
	NewDomain      string            `json:"new_domain,omitempty"`
	NewEntities    map[string]string `json:"new_entities,omitempty"` // entities named explicitly in the request
	IsAnswer       bool              `json:"is_answer"`              // an answer, confirmation, retry or mode instruction
	Anaphoric      bool              `json:"anaphoric"`              // refers to the conversation («بهش», «همین», "it"): only it can resolve that
	Command        bool              `json:"command"`                // a self-contained imperative request
	Failures       []Failure         `json:"previous_failures,omitempty"`
	ResetsSoFar    int               `json:"context_reset_count"`
	Now            time.Time         `json:"-"`
}

type Reason struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// Result is the detector's decision plus what is recorded for reliability work.
type Result struct {
	Conflict   bool     `json:"conflict"`
	Confidence float64  `json:"confidence"`
	Severity   string   `json:"severity"`
	Reasons    []Reason `json:"reasons"`
	Action     string   `json:"recommended_action"`
	// Why a reset happened: the state that caused it, the decision it would have
	// affected, and whether the original context should have handled it (a bug).
	CausingState     string `json:"causing_state,omitempty"`
	AffectedDecision string `json:"affected_decision,omitempty"`
	ArchitectureBug  bool   `json:"architecture_bug"`
}

// Normalize is the comparison form of a request (digits, letters, spacing).
func Normalize(s string) string {
	return strings.Join(strings.Fields(normalize.Text(s)), " ")
}

// failedStages: a task in one of these cannot simply continue with a new request.
// ("failed"/"cancelled"/"done" tasks are closed: a new message never continues them.)
var failedStages = map[string]bool{"stopped": true, "ui_failed": true}

// window: earlier failures older than this do not count as "the same retry".
const window = 2 * time.Hour

// Detect applies the rules in order of strength.
func Detect(in Input) Result {
	r := Result{Severity: SevNone, Action: Continue, Reasons: []Reason{}}
	if in.IsAnswer || strings.TrimSpace(in.UserRequest) == "" {
		return r
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	recent := func(f Failure) bool { return f.At.IsZero() || now.Sub(f.At) <= window }

	// B/D. The same request already failed in this conversation: retrying in place
	// would reproduce the failure (the antenna receipt: stuck in one chat, fine in a new one).
	var same []Failure
	for _, f := range in.Failures {
		if f.Normalized == in.Normalized && recent(f) {
			same = append(same, f)
		}
	}
	if len(same) > 0 {
		r.add(SameRequestFailed, "the same request ended "+same[0].Outcome+" in this conversation (task "+same[0].TaskID+")")
		r.set(SevCritical, 0.95, NewContext, "task "+same[0].TaskID+" ("+same[0].Outcome+")", "interpretation/planning of the same request", true)
		domains, tools := map[string]bool{}, map[string]bool{}
		for _, f := range same {
			if f.Domain != "" {
				domains[f.Domain] = true
			}
			if f.Tool != "" {
				tools[f.Tool] = true
			}
		}
		if len(same) >= 2 && (len(domains) > 1 || len(tools) > 1) {
			r.add(PlannerInconsistent, "the same request was planned in different domains/tools")
		}
	}

	if c := in.Current; c != nil && in.Command && c.ID != "" {
		currentNorm := Normalize(c.UserText)
		switch {
		// A. The open task failed/stopped and cannot resume; the new self-contained
		// request must not be merged into it.
		case failedStages[c.Stage] && currentNorm != in.Normalized:
			detail := "the current task is " + c.Stage
			if c.FailureCode != "" {
				detail += " (" + c.FailureCode + ")"
			}
			r.add(UnrecoverableTask, detail+"; the new request is a separate operation")
			r.set(SevHigh, 0.9, NewContext, "task "+c.ID+" "+c.Stage, "the new request would be merged into a failed plan", true)
		// F. The open task's execution state is unknown (operating on screen).
		case c.Stage == "ui_running":
			r.add(UnknownExecutionState, "the current task is still being carried out on screen; its outcome is unknown")
			r.set(SevHigh, 0.85, NewContext, "task "+c.ID+" ui_running", "the new request would inherit an executing task", false)
		// C/E. An open question/approval of another domain: a new task in the same
		// conversation is enough (the superseding path) — not a reset.
		case (c.Stage == "awaiting_answer" || c.Stage == "awaiting_approval") && c.Domain != "" && in.NewDomain != "" && c.Domain != in.NewDomain:
			r.add(DomainConflict, "open "+c.Domain+" task vs a new "+in.NewDomain+" request: started as a new task")
			r.set(SevMedium, 0.7, Continue, "", "", false)
		}
		for typ, v := range in.NewEntities {
			if old, ok := c.Entities[typ]; ok && old != "" && Normalize(old) != Normalize(v) {
				r.add(StaleEntity, typ+": the request names «"+v+"», the current task assumed «"+old+"»")
				if r.Severity == SevNone || r.Severity == SevLow {
					r.set(SevMedium, 0.6, Continue, "", "", false)
				}
			}
		}
	}

	// a fresh context for this same request already failed: never a second reset
	// (re-sending creates a new request id, so the count is also kept per request text)
	for _, f := range same {
		if f.ContextResets > 0 && in.ResetsSoFar == 0 {
			in.ResetsSoFar = f.ContextResets
		}
	}
	if r.Action == NewContext && in.Anaphoric {
		// «حالا بهش کمک هزینه سفر هم اضافه کن»: a fresh context could not know who "him" is
		r.add(FollowUpReference, "the request refers to the conversation; a fresh context could not resolve it")
		r.Action = Continue
	}
	if r.Action == NewContext && in.ResetsSoFar >= 1 {
		// one automatic reset per request, ever
		r.add(NoMoreReset, "this request was already moved to a fresh context once")
		r.Action = Continue
	}
	r.Conflict = len(r.Reasons) > 0
	return r
}

func (r *Result) add(code, detail string) { r.Reasons = append(r.Reasons, Reason{code, detail}) }

func (r *Result) set(sev string, conf float64, action, state, decision string, bug bool) {
	rank := map[string]int{SevNone: 0, SevLow: 1, SevMedium: 2, SevHigh: 3, SevCritical: 4}
	if rank[sev] < rank[r.Severity] {
		return
	}
	r.Severity, r.Confidence, r.Action = sev, conf, action
	if state != "" {
		r.CausingState, r.AffectedDecision, r.ArchitectureBug = state, decision, bug
	}
}

// commandVerbs: a self-contained imperative request (not an answer or a fact).
var commandVerbs = []string{"بزن", "ثبت کن", "بساز", "ایجاد کن", "اضافه کن", "حذف کن", "پاک کن", "تغییر بده", "عوض کن", "ویرایش کن", "لیست کن", "فهرست کن",
	"نشان بده", "نشون بده", "نمایش بده", "بگو", "پرداخت کن", "واریز کن", "صادر کن", "بفرست", "محاسبه کن", "تایید کن", "تأیید کن", "منتقل کن", "انتقال بده", "باز کن",
	"create", "add", "delete", "remove", "record", "register", "list", "show", "pay", "issue", "send", "make", "change", "update"}

// IsCommand reports whether text is a complete imperative request.
func IsCommand(text string) bool {
	t := " " + normalize.Text(text) + " "
	if len(strings.Fields(t)) < 3 {
		return false
	}
	for _, v := range commandVerbs {
		if strings.Contains(t, " "+normalize.Text(v)) {
			return true
		}
	}
	return false
}

// references: words that only the conversation can resolve.
var references = []string{"بهش", "ازش", "براش", "برایش", "باهاش", "روش رو", "واسش", "بهشون", "ازشون", "همین", "همینو", "همون", "همان", "اونو", "اینو", "این یکی", "اون یکی",
	"قبلی", "بالایی", "it", "him", "her", "them", "that one", "this one", "the same"}

// possessive: a noun with the pronoun suffix ش («حقوقش», «مبلغش») refers back; these
// common words merely end in ش.
var notPossessive = map[string]bool{"فروش": true, "آموزش": true, "پوشش": true, "کوشش": true, "ارزش": true, "گزارش": true, "بخش": true, "دانش": true,
	"تلاش": true, "روش": true, "نقش": true, "خواهش": true, "سفارش": true, "پرورش": true, "نمایش": true, "بینش": true, "گردش": true, "پذیرش": true, "سنجش": true,
	"کاهش": true, "افزایش": true, "پیش": true, "بیش": true, "خویش": true, "جوش": true, "هوش": true, "گوش": true, "کش": true, "خوش": true, "تراکنش": true, "واکنش": true, "کنش": true}

// Anaphoric reports whether text refers to something only the conversation knows.
func Anaphoric(text string) bool {
	t := " " + normalize.Text(text) + " "
	for _, r := range references {
		if strings.Contains(t, " "+normalize.Text(r)+" ") {
			return true
		}
	}
	for _, w := range strings.Fields(t) {
		w = strings.Trim(w, "،,.!؟?")
		if r := []rune(w); len(r) >= 4 && strings.HasSuffix(w, "ش") && !notPossessive[w] && !notPossessive[strings.TrimSuffix(w, "ها")] {
			return true
		}
	}
	return false
}
