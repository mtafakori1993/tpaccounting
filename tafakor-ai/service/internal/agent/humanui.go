package agent

// HUMAN_UI: the assistant operates the application's real screens like an employee —
// visible pointer, real clicks and typing, real submission — and then reads the result
// back through the application. This file turns an operation into the steps the widget's
// single interaction engine (widget/src/agent/engine.ts) performs, and handles what the
// widget reports back. It never calls an accounting write itself: that is DIRECT
// (execute.go / planner.go executePlan), chosen only by package execmode.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// ---------- execution mode ----------

// humanUI reports whether this operation is carried out on the real screen.
func (st *State) humanUI() bool {
	m, ok := execmode.Parse(st.ExecMode)
	if !ok {
		return execmode.Default == execmode.HumanUI
	}
	return m == execmode.HumanUI
}

// resolveExecMode decides HOW the operation runs (the one place this is decided):
// the user's words, then an explicit choice already attached to the operation, then
// their persistent preference, then the application default (HUMAN_UI). The mere
// existence of an API never makes it DIRECT.
func (a *Agent) resolveExecMode(ctx context.Context, u *User, st *State, text, explicit string) {
	pref := ""
	if s, err := a.Store.GetUserSettings(ctx, u.Scope); err == nil {
		pref = s.ExecPreference
	}
	var pending execmode.Mode
	if m, ok := execmode.Parse(explicit); ok {
		pending = m
	} else if m, ok := execmode.Parse(st.ExecMode); ok && (st.ExecSource == string(execmode.SourceRequest) || st.ExecSource == string(execmode.SourceExplicit)) {
		pending = m // an explicit choice stays with the operation across turns
	}
	r := execmode.Resolve(execmode.Input{Request: text, Explicit: pending, Preference: pref})
	st.ExecMode, st.ExecSource = string(r.Mode), string(r.Source)
}

// chooseMode records an explicit per-operation choice (a button on the card).
func (st *State) chooseMode(m execmode.Mode) {
	st.ExecMode, st.ExecSource = string(m), string(execmode.SourceExplicit)
}

func (st *State) via() string {
	if st.humanUI() {
		return string(execmode.HumanUI)
	}
	return string(execmode.Direct)
}

// dispatchPlan carries out an approved plan in the resolved mode. The executor is
// checked again here (a card button or a typed instruction may have changed the mode
// since the plan was presented): a mode without an executor never starts (D59).
func (a *Agent) dispatchPlan(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	m := execmode.Direct
	if st.humanUI() {
		m = execmode.HumanUI
	}
	if ok, why := modeSupported(st, m); !ok {
		other := execmode.Direct
		if m == execmode.Direct {
			other = execmode.HumanUI
		}
		if ok2, _ := modeSupported(st, other); !ok2 || why == FailNoExecutor {
			a.noExecutor(ctx, u, t, st, e)
			return
		}
		// the chosen mode cannot carry it out: say so and offer the one that can — never
		// start the impossible path, never switch silently after approval
		a.execBlocked(ctx, u, t, st, e, m, why)
		return
	}
	if st.Exec == nil {
		st.Exec = &ExecRecord{Requested: string(m)}
	}
	if p := st.Plan.Pending(); len(p) > 0 {
		st.Exec.Operation, st.Exec.Step = p[0].Tool, p[0].ID
	}
	st.Exec.Selected, st.Exec.Executor, st.Exec.Status = string(m), executorName(m), "EXECUTING"
	st.Exec.FailureCode, st.Exec.FailureReason = "", ""
	st.Exec.Attempts++
	a.audit(ctx, u, t.ID, "executor_selected", map[string]any{"mode": m, "executor": st.Exec.Executor, "attempt": st.Exec.Attempts, "operation": st.Exec.Operation})
	if m == execmode.HumanUI {
		a.planOnScreen(ctx, u, t, st, e)
		return
	}
	a.executePlan(ctx, u, t, st, e)
}

// noExecutor ends a task whose work no executor can carry out — before anything is
// presented or changed. Persisted as NO_EXECUTOR; repeating the request gets the same
// honest answer, not the same failed attempt.
func (a *Agent) noExecutor(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if st.Exec == nil {
		st.Exec = &ExecRecord{Requested: st.ExecMode}
	}
	st.Exec.Status, st.Exec.FailureCode = "NO_EXECUTOR", FailNoExecutor
	if st.Exec.FailureReason == "" {
		st.Exec.FailureReason = "no executor for the planned operation"
	}
	if st.Plan != nil {
		st.Plan.Status = "failed"
	}
	op := st.Exec.Operation
	st.Stage, st.Message = StageFailed, st.Exec.FailureReason
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "no_executor", map[string]any{"operation": op, "reason": st.Exec.FailureReason, "requested_mode": st.Exec.Requested})
	e.Emit("message", map[string]any{"text": tr(st.Lang, "The assistant cannot carry out “"+op+"” (neither directly nor on screen). Nothing was changed. You can do it yourself in the application.",
		"دستیار نمی‌تواند «"+op+"» را انجام دهد (نه مستقیم و نه روی صفحه). چیزی تغییر نکرد؛ می‌توانید آن را خودتان در نرم‌افزار انجام دهید.")})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// execBlocked: the approved work cannot run in mode m but can in the other mode. The
// task waits (approval kept) for the user's explicit choice of the mode that works.
func (a *Agent) execBlocked(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, m execmode.Mode, why string) {
	code := FailNoUIExecutor
	if m == execmode.Direct {
		code = FailNoDirect
	}
	if st.Exec == nil {
		st.Exec = &ExecRecord{}
	}
	st.Exec.Requested, st.Exec.Status, st.Exec.FailureCode, st.Exec.FailureReason = string(m), "BLOCKED", code, why
	st.Stage, st.Message = StageUIFailed, why
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "exec_blocked", map[string]any{"mode": m, "code": code, "reason": why})
	msg := tr(st.Lang, "This can't be done on screen ("+why+"). Nothing was changed. Choose “Run directly” (or say «مستقیم انجام بده») to do it through the application.",
		"این کار روی صفحه قابل انجام نیست ("+uiReasonFa(why)+"). چیزی تغییر نکرد. برای انجامش از طریق برنامه «اجرای مستقیم» را بزنید (یا بگویید «مستقیم انجام بده»).")
	if m == execmode.Direct {
		msg = tr(st.Lang, "This can't be done directly. Nothing was changed; choose “Run on screen”.", "این کار مستقیم قابل انجام نیست. چیزی تغییر نکرد؛ «اجرا روی صفحه» را انتخاب کنید.")
	}
	e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": m == execmode.HumanUI, "can_retry": false, "failure_code": code})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// uiUnavailable reports that the real screen cannot carry out (a part of) the work.
// It never switches to DIRECT by itself: nothing was changed, and the user can choose
// to run it directly (button or «مستقیم انجام بده»).
// errNoScreen is the plain statement when the real screen cannot be operated.
func errNoScreen(lang string) string {
	return tr(lang, "Operating the real screen is not available here.", "اجرای UI واقعی در دسترس نیست.")
}

func (a *Agent) uiUnavailable(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, reason string) {
	st.Stage, st.Message = StageUIFailed, reason
	if st.Exec != nil {
		st.Exec.Status, st.Exec.FailureCode, st.Exec.FailureReason = "BLOCKED", FailNoUIExecutor, reason
	}
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "ui_unavailable", map[string]any{"reason": reason, "exec_mode": st.ExecMode, "failure_code": FailNoUIExecutor})
	msg := reason + tr(st.Lang, " Nothing was changed, and I did not switch to running it directly on my own. If you want that, choose “Run directly” (or say «مستقیم انجام بده»).",
		" چیزی تغییر نکرد و خودم به‌تنهایی به حالت مستقیم نرفتم. اگر می‌خواهید، «اجرای مستقیم» را بزنید (یا بگویید «مستقیم انجام بده»).")
	e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": true, "can_retry": false})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// ---------- building steps from an operation's real form ----------

func (st *State) label(en, fa string) string { return tr(st.Lang, en, fa) }

// buildStepPlan turns one catalog step into on-screen steps. submit=false stops before
// the submit (UI_PREVIEW: the user reviews and saves the form themselves). The second
// result is the reason in the user's language when the screen cannot express the step.
func buildStepPlan(st *State, s PlanStep, submit bool) (UIPlan, string) {
	op, ok := catalog.Load().Get(s.Tool)
	if !ok {
		return UIPlan{}, st.label("Unknown operation.", "عملیات ناشناس است.")
	}
	args, err := resolveRefs(st.Plan, s.Args)
	if err == nil {
		err = tools.ValidateOpArgs(op, &args)
	}
	if err != nil {
		return UIPlan{}, err.Error()
	}
	// the same predicate the capability registry reports (D59): the screen is never
	// promised for something the screen executor cannot do
	if ok, why := uiCapability(op); !ok {
		return UIPlan{}, st.label("I can't do “"+op.ID+"” on screen: "+why+".", "«"+op.ID+"» روی صفحه قابل انجام نیست: "+uiReasonFa(why)+".")
	}
	var steps []UIStep
	var why string
	switch op.Kind {
	case "create", "update":
		steps, why = formSteps(st.Lang, op, args, st.Plan, submit)
	default:
		steps, why = recordActionSteps(st.Lang, op, args)
	}
	if why != "" {
		return UIPlan{}, why
	}
	p := UIPlan{Steps: steps, ExecMode: string(execmode.HumanUI), Step: s.ID}
	if !submit {
		p.Mode = "preview"
	}
	return p, ""
}

// formSteps opens the operation's real form and fills every value the call carries.
func formSteps(lang string, op *catalog.Operation, args tools.OpArgs, plan *PlanState, submit bool) ([]UIStep, string) {
	L := func(en, fa string) string { return tr(lang, en, fa) }
	form := op.UI
	if form == nil {
		return nil, L("The application has no screen for “"+op.ID+"”.", "نرم‌افزار برای «"+op.ID+"» صفحه‌ای ندارد.")
	}
	var body map[string]json.RawMessage
	if len(args.Body) > 0 {
		_ = json.Unmarshal(args.Body, &body)
	}
	route := strings.NewReplacer(":side", catalog.SideOf(op), ":kind", args.Path["kind"], ":id", args.Path["id"]).Replace(form.Route)
	if strings.Contains(route, ":") || strings.Contains(route, "{") {
		return nil, L("I could not work out which screen holds this record.", "نتوانستم صفحه این رکورد را پیدا کنم.")
	}
	dialog := len(form.Dialogs) > 0
	disc := op.Discriminator()
	flat := map[string]string{}
	for k, v := range body {
		flat[k] = scalarValue(v)
	}
	for k, v := range args.Path {
		if flat[k] == "" {
			flat[k] = v
		}
	}
	steps := []UIStep{{Op: "navigate", Value: route, Label: L("Opening the page…", "باز کردن صفحه…")}}
	scope := ""
	if dialog {
		scope = "*" // the topmost open dialog
		if op.Kind == "update" {
			if !form.Row || args.Path["id"] == "" {
				return nil, L("This record is not edited from a list row on screen.", "این رکورد از ردیف فهرست روی صفحه ویرایش نمی‌شود.")
			}
			steps = append(steps, UIStep{Op: "open_row", Value: args.Path["id"], Expect: "dialog", Label: L("Opening the record…", "باز کردن رکورد…")})
		} else {
			open := op.UIOpenFor(flat[disc])
			if len(open) == 0 {
				return nil, L("I could not find the button that opens this form.", "دکمه‌ای که این فرم را باز می‌کند پیدا نشد.")
			}
			c := UIStep{Op: "click", Target: open[0], Expect: "dialog", Label: L("Opening the form…", "باز کردن فرم…")}
			if len(open) > 1 {
				c.Alts = open
			}
			steps = append(steps, c)
		}
	}
	grid := op.Grid()
	post := false
	var unsupported []string
	curTab := ""
	label := func(f string) string { return L("Filling "+f+"…", "وارد کردن "+f+"…") }
	for _, f := range op.InputFields() {
		raw, present := body[f.Name]
		if !present || isNull(raw) {
			continue
		}
		switch {
		case f.Name == "post":
			post = scalarValue(raw) == "true"
			continue
		case grid != nil && f.Name == grid.Field:
			ls, bad := lineSteps(lang, grid, raw, plan)
			steps = append(steps, ls...)
			unsupported = append(unsupported, bad...)
			continue
		case f.Name == disc && dialog && op.Kind == "create":
			continue // chosen by the button that opened the form
		}
		v := scalarValue(raw)
		if v == "" {
			if strings.TrimSpace(string(raw)) == `""` || strings.TrimSpace(string(raw)) == "[]" {
				continue
			}
			unsupported = append(unsupported, f.Name)
			continue
		}
		if v == tools.SecretGenerate || refRe.MatchString(v) {
			unsupported = append(unsupported, f.Name)
			continue
		}
		ff, ok := form.Fields[f.Name]
		if !ok {
			if op.Kind == "create" && isCreateDefault(f.Name, v) {
				continue // the application creates it that way anyway (new records are active, balances are zero)
			}
			unsupported = append(unsupported, f.Name)
			continue
		}
		if ff.Tab != "" && ff.Tab != curTab {
			// the control lives on another tab of the form: click the tab first, as a person would
			steps = append(steps, UIStep{Op: "tab", Dialog: scope, Target: ff.Tab, Label: L("Switching tab…", "تغییر زبانه…")})
			curTab = ff.Tab
		}
		s := controlStep(ff.Control, ff.Tag, v, planLabel(plan, f.Name, v), label(f.Name))
		s.Dialog, s.Alts = scope, ff.Alts
		s.Optional = isCreateDefault(f.Name, v) && f.Name != "is_active"
		steps = append(steps, s)
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, L("The screen has no control for: "+strings.Join(unsupported, ", ")+".", "صفحه برای این موارد کنترلی ندارد: "+strings.Join(unsupported, "، ")+".")
	}
	if !submit {
		return steps, ""
	}
	save := []string{"common.save", "common.saveDraft"}
	if post {
		save = []string{"common.saveAndPost", "common.save"}
	}
	steps = append(steps, UIStep{Op: "submit", Dialog: scope, Target: save[0], Alts: save, Label: L("Saving…", "ذخیره…")})
	if dialog {
		steps = append(steps, UIStep{Op: "wait_closed", Dialog: scope, Label: L("Verifying…", "بررسی…")})
		// the result is read from the screen itself: the saved record must show up in the list
		for _, k := range []string{"name", "full_name", "last_name", "title", "code"} {
			if v := scalarValue(body[k]); v != "" && op.Kind == "create" {
				steps = append(steps, UIStep{Op: "verify_text", Value: v, Label: L("Checking the list…", "بررسی فهرست…")})
				break
			}
		}
	} else {
		steps = append(steps, UIStep{Op: "wait_leave", Label: L("Verifying…", "بررسی…")})
	}
	return steps, ""
}

// isCreateDefault: a value the application gives a new record anyway, so a form that has no
// control for it loses nothing when it is left alone.
func isCreateDefault(field, v string) bool {
	switch field {
	case "is_active":
		return v == "true"
	}
	return v == "false" || v == "0" || v == "0.00"
}

func isNull(raw json.RawMessage) bool { return strings.TrimSpace(string(raw)) == "null" }

func controlStep(control, tag, value, search, label string) UIStep {
	s := UIStep{Target: tag, Value: value, Label: label}
	switch control {
	case "date":
		s.Op = "date"
	case "select":
		s.Op = "select"
	case "combo":
		s.Op, s.Search = "combo", search
	case "check":
		s.Op = "check"
	default:
		s.Op = "fill"
	}
	return s
}

// lineSteps fills a document's line grid, one row per line.
func lineSteps(lang string, g *catalog.LineGrid, raw json.RawMessage, plan *PlanState) ([]UIStep, []string) {
	L := func(en, fa string) string { return tr(lang, en, fa) }
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil, []string{g.Field}
	}
	if len(items) == 0 {
		return nil, nil
	}
	steps := []UIStep{{Op: "ensure_rows", Target: g.Add, Value: strconv.Itoa(len(items)), Label: L("Adding lines…", "افزودن ردیف‌ها…")}}
	var bad []string
	names := make([]string, 0, len(g.Cols))
	for n := range g.Cols {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return g.Cols[names[i]].Col < g.Cols[names[j]].Col })
	for i, it := range items {
		for k := range it {
			if _, ok := g.Cols[k]; !ok && !isNull(it[k]) && scalarValue(it[k]) != "" {
				bad = append(bad, g.Field+"."+k)
			}
		}
		for _, n := range names {
			raw, ok := it[n]
			v := scalarValue(raw)
			if !ok || v == "" || isNull(raw) {
				continue
			}
			c := g.Cols[n]
			s := controlStep(c.Control, c.Tag, v, planLabel(plan, n, v), L(fmt.Sprintf("Line %d: %s…", i+1, n), fmt.Sprintf("ردیف %d: %s…", i+1, n)))
			s.Row, s.Col = i+1, c.Col
			steps = append(steps, s)
		}
	}
	return steps, bad
}

var labelRe = regexp.MustCompile(`"(?:name|full_name|title|label)":"([^"]+)"`)

// planLabel is the visible name of a record picked by id (for a combo box's search text),
// taken from what the plan already read or created — no extra calls.
func planLabel(p *PlanState, field, id string) string {
	if p == nil || !strings.HasSuffix(field, "_id") {
		return ""
	}
	for _, s := range p.Steps {
		if s.Status != "done" || len(s.Result) == 0 {
			continue
		}
		var r struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
		}
		if json.Unmarshal(s.Result, &r) == nil && r.ID.String() == id && r.Name != "" {
			return r.Name
		}
	}
	needle := `"id":` + id
	for i := len(p.Reads) - 1; i >= 0; i-- {
		res := p.Reads[i].Result
		for off := 0; ; {
			k := strings.Index(res[off:], needle)
			if k < 0 {
				break
			}
			k += off
			off = k + len(needle)
			if off < len(res) && res[off] >= '0' && res[off] <= '9' {
				continue
			}
			lo := strings.LastIndex(res[:k], "{")
			hi := strings.Index(res[k:], "}")
			if lo < 0 || hi < 0 {
				continue
			}
			if m := labelRe.FindStringSubmatch(res[lo : k+hi]); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// ---------- actions on an existing record (open, print, cancel) ----------

// recordScreen says where a document lives on screen: the list, the route its row opens
// to (a page) or "" (a dialog), and the document's id.
type recordScreen struct {
	list   string
	detail string // route prefix of the page the row opens ("" = a dialog)
	id     int64
}

func screenOf(t Target) (recordScreen, bool) {
	r := recordScreen{id: t.EntityID}
	seg := func() (string, string) {
		side := "sales"
		if t.Side == "purchase" || t.Side == "purchases" {
			side = "purchases"
		}
		kind := map[string]string{"quotation": "quotations", "invoice": "invoices", "return": "returns"}[t.Kind]
		return side, kind
	}
	switch {
	case t.EntityType == "invoice" || (t.Side != "" && t.Kind != ""):
		side, kind := seg()
		if kind == "" {
			return r, false
		}
		r.list = "/" + side + "/" + kind
		r.detail = r.list + "/"
	case t.EntityType == "treasury_transaction" || strings.HasPrefix(t.EntityType, "treasury_"):
		r.list = "/treasury/transactions"
	case t.EntityType == "expense":
		r.list = "/expenses"
	case t.EntityType == "journal_entry":
		r.list, r.detail = "/journal", "/journal/"
	case t.EntityType == "stock_document":
		r.list, r.detail = "/inventory/documents", "/inventory/documents/"
	case t.EntityType == "party":
		r.list, r.detail = "/parties", "/parties/"
	default:
		return r, false
	}
	return r, true
}

// openSteps navigates to the list and opens the record's row, as a person would.
func openSteps(lang string, r recordScreen) []UIStep {
	L := func(en, fa string) string { return tr(lang, en, fa) }
	expect := "dialog"
	if r.detail != "" {
		expect = "route:" + r.detail + strconv.FormatInt(r.id, 10)
	}
	return []UIStep{
		{Op: "navigate", Value: r.list, Label: L("Opening the list…", "باز کردن فهرست…")},
		{Op: "open_row", Value: strconv.FormatInt(r.id, 10), Expect: expect, Label: L("Opening the document…", "باز کردن سند…")},
	}
}

func openPlan(lang string, t Target) (UIPlan, string) {
	r, ok := screenOf(t)
	if !ok {
		return UIPlan{}, tr(lang, "I don't know which screen shows this kind of record.", "نمی‌دانم این نوع رکورد در کدام صفحه نمایش داده می‌شود.")
	}
	return UIPlan{Steps: openSteps(lang, r), ExecMode: string(execmode.HumanUI)}, ""
}

// printPlan opens the document and presses the application's own print/PDF button.
func printPlan(lang string, t Target) (UIPlan, string) {
	r, ok := screenOf(t)
	if !ok {
		return UIPlan{}, tr(lang, "I don't know which screen shows this kind of document.", "نمی‌دانم این نوع سند در کدام صفحه نمایش داده می‌شود.")
	}
	steps := openSteps(lang, r)
	b := UIStep{Op: "click", Target: "common.printDoc", Expect: "none", Label: tr(lang, "Printing the document…", "چاپ سند…")}
	if r.detail == "" {
		b.Dialog = "*"
	}
	return UIPlan{Steps: append(steps, b), ExecMode: string(execmode.HumanUI)}, ""
}

// cancelPlan opens the document and cancels it with the application's own button and
// confirmation dialog (the reversal entry is the application's business).
func cancelPlan(lang string, t Target) (UIPlan, string) {
	r, ok := screenOf(t)
	if !ok || r.detail != "" || (t.EntityType != "treasury_transaction" && t.EntityType != "expense") {
		return UIPlan{}, tr(lang, "Cancelling this kind of document is not done from a list screen.", "لغو این نوع سند از صفحه فهرست انجام نمی‌شود.")
	}
	steps := openSteps(lang, r)
	steps = append(steps,
		UIStep{Op: "click", Target: "common.cancel", Dialog: "*", Expect: "dialog", Label: tr(lang, "Pressing Cancel on the document…", "زدن لغو روی سند…")},
		UIStep{Op: "submit", Target: "common.confirm", Dialog: "*", Label: tr(lang, "Confirming…", "تأیید…")},
		UIStep{Op: "wait_closed", Dialog: "*", Label: tr(lang, "Verifying…", "بررسی…")})
	return UIPlan{Steps: steps, ExecMode: string(execmode.HumanUI)}, ""
}

// recordActionSteps: workflow/delete operations done from the record's own screen
// (only what uiCancelTarget names: the list screens with the app's Cancel button).
func recordActionSteps(lang string, op *catalog.Operation, args tools.OpArgs) ([]UIStep, string) {
	id, _ := strconv.ParseInt(args.Path["id"], 10, 64)
	kind := uiCancelTarget(op)
	if id == 0 || kind == "" {
		return nil, tr(lang, "I don't have a way to do “"+op.ID+"” on screen.", "روشی برای انجام «"+op.ID+"» روی صفحه ندارم.")
	}
	p, reason := cancelPlan(lang, Target{EntityType: kind, EntityID: id})
	return p.Steps, reason
}

// ---------- running a plan on screen ----------

// planOnScreen operates the next pending step of the plan on the real screen. Each step
// is one UI plan; the widget reports back and the next one follows (uiPlanResult).
func (a *Agent) planOnScreen(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if st.NoUI {
		a.uiUnavailable(ctx, u, t, st, e, errNoScreen(st.Lang))
		return
	}
	p := st.Plan
	p.Status = "executing"
	for i := range p.Steps {
		s := &p.Steps[i]
		if s.Status == "done" {
			continue
		}
		if isSourceAttach(st, *s) {
			// attaching the user's own file is not a form: the system does it (D46)
			if err := a.runSourceAttach(ctx, u, t, st, e, s, "human"); err != nil {
				a.planFailed(ctx, u, t, st, s, e, err)
				return
			}
			a.save(ctx, t, st)
			continue
		}
		plan, why := buildStepPlan(st, *s, true)
		if why != "" {
			a.uiUnavailable(ctx, u, t, st, e, why)
			return
		}
		op, _ := catalog.Load().Get(s.Tool)
		if args, err := resolveRefs(p, s.Args); err == nil && op.Kind == "create" {
			st.UIBaseline = a.maxRecordID(ctx, u, op, args)
		}
		st.UIStep, st.UIKind, st.Stage = s.ID, "", StageUIRunning
		a.save(ctx, t, st)
		plan.TaskID = t.ID
		status(e, st.Lang, "registering")
		a.audit(ctx, u, t.ID, "ui_plan", map[string]any{"step": s.ID, "tool": s.Tool, "exec_mode": st.ExecMode, "steps": len(plan.Steps)})
		e.Emit("ui_plan", plan)
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	a.finishPlan(ctx, u, t, st, e)
}

// planUIPlan rebuilds the current step's plan (task reload / retry).
func planUIPlan(st *State) *UIPlan {
	if st.Plan == nil || st.UIStep == "" {
		return nil
	}
	for _, s := range st.Plan.Steps {
		if s.ID == st.UIStep && s.Status != "done" {
			if p, why := buildStepPlan(st, s, true); why == "" {
				return &p
			}
		}
	}
	return nil
}

// planUIResult handles the widget's report for a plan step: whatever it says, the record
// is looked for through the application and only verified state is reported.
func (a *Agent) planUIResult(ctx context.Context, u *User, t *store.Task, st *State, req UIResult, e Emitter) {
	p := st.Plan
	var s *PlanStep
	for i := range p.Steps {
		if p.Steps[i].ID == st.UIStep {
			s = &p.Steps[i]
		}
	}
	if s == nil && req.FallbackAPI && st.Stage == StageUIFailed {
		// «به‌جای آن مستقیم اجرا کن» when the screen never started (no step was operated):
		// the same approved plan runs directly — never the failed screen path again.
		st.chooseMode(execmode.Direct)
		a.audit(ctx, u, t.ID, "mode_changed", map[string]any{"to": "direct", "by": "user_choice", "ui_step": ""})
		a.save(ctx, t, st)
		a.dispatchPlan(ctx, u, t, st, e)
		return
	}
	if s == nil {
		// A late or duplicate report (a second tab, a report after a reload): the
		// persisted plan, not the report, says where the work is — continue from it.
		a.audit(ctx, u, t.ID, "ui_result_stale", map[string]any{"ui_step": st.UIStep, "ok": req.OK})
		st.UIStep = ""
		a.save(ctx, t, st)
		a.planOnScreen(ctx, u, t, st, e)
		return
	}
	op, _ := catalog.Load().Get(s.Tool)
	args, _ := resolveRefs(p, s.Args)
	status(e, st.Lang, "verifying")
	rec, mism := a.recordAfterUI(ctx, u, st, op, args)
	if rec == nil {
		if req.FallbackAPI {
			// The user explicitly chose “run directly”; the screen attempt created nothing.
			st.chooseMode(execmode.Direct)
			st.UIStep = ""
			a.save(ctx, t, st)
			a.audit(ctx, u, t.ID, "mode_changed", map[string]any{"to": "direct", "by": "user_choice"})
			a.dispatchPlan(ctx, u, t, st, e)
			return
		}
		st.Stage = StageUIFailed
		msg := tr(st.Lang, "I stopped: the screen did not behave as expected", "متوقف شدم: صفحه آن‌طور که انتظار داشتم رفتار نکرد")
		if req.Error != "" {
			msg += " (" + req.Error + ")"
		} else if req.OK {
			msg += tr(st.Lang, " (the form closed but I could not find the new record)", " (فرم بسته شد اما رکورد جدید را پیدا نکردم)")
		}
		msg += tr(st.Lang, ". Nothing was registered. You can retry, or choose “Run directly”.", ". چیزی ثبت نشد. می‌توانید دوباره امتحان کنید یا «اجرای مستقیم» را بزنید.")
		st.Message = msg
		a.save(ctx, t, st)
		e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": true, "can_retry": true})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	s.Result, s.Status, s.Verified, s.Args = rec, "done", len(mism) == 0, args
	a.audit(ctx, u, t.ID, "record_change", map[string]any{"tool": s.Tool, "step": s.ID, "purpose": s.Purpose, "args": args, "after": json.RawMessage(rec),
		"verified": s.Verified, "mismatches": mism, "user": u.Me.Email, "actor": "tafakor_ai", "via": "human_ui", "decision": st.Decision, "approved_by": st.ApprovedBy})
	e.Emit("tool", map[string]any{"tool": "verify", "step": s.ID, "ok": s.Verified})
	if op.Kind != "delete" {
		s.Artifact = artifactFromRecord(op, rec)
		emitArtifact(e, s.Artifact)
	}
	for _, m := range mism {
		e.Emit("message", map[string]any{"text": tr(st.Lang, "The saved record differs from what I entered: ", "رکورد ذخیره‌شده با آنچه وارد کردم تفاوت دارد: ") + m})
	}
	st.UIStep = ""
	a.save(ctx, t, st)
	a.planOnScreen(ctx, u, t, st, e)
}

// finishPlan reports a completed plan (shared by DIRECT and HUMAN_UI).
func (a *Agent) finishPlan(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	p := st.Plan
	var done []map[string]any
	for _, s := range p.Steps {
		if s.Status != "done" {
			continue
		}
		op, _ := catalog.Load().Get(s.Tool)
		done = append(done, map[string]any{"id": s.ID, "tool": s.Tool, "purpose": s.Purpose, "verified": s.Verified, "record": recordRef(s.Result),
			"operation": op.SemanticName(s.Args.Path["kind"], s.Args.Body)})
	}
	p.Status = "done"
	st.Stage = StageDone
	st.Result = &Result{EntityType: "plan", Verified: allVerified(done), Mode: "human", Status: "done", Via: st.via()}
	if st.Exec != nil {
		st.Exec.Status = "COMPLETED"
		if !st.Result.Verified {
			st.Exec.Status, st.Exec.FailureCode = "VERIFICATION_FAILED", FailVerification
		}
	}
	if st.Decision != nil && st.Decision.Mode == "auto" {
		st.Result.Mode = "auto"
	}
	// the user's original files become attachments of the record the plan registered
	if files := a.linkPlanUploads(ctx, u, t, st, e, st.Result.Mode); len(files) > 0 {
		st.Result.Files = files
		for _, f := range files {
			st.Result.Attachments = append(st.Result.Attachments, f.Name)
		}
	}
	a.save(ctx, t, st)
	summary := planSummary(st.Lang, done)
	if len(st.Result.Attachments) > 0 {
		summary += tr(st.Lang, " — attached: ", " — پیوست: ") + strings.Join(st.Result.Attachments, ", ")
	}
	a.audit(ctx, u, t.ID, "result", map[string]any{"steps": done, "exec_mode": st.ExecMode, "verified": st.Result.Verified})
	e.Emit("result", map[string]any{"result": st.Result, "summary": summary, "steps": done, "undo_available": false, "exec_mode": st.ExecMode})
	for _, s := range p.Steps {
		if len(s.Mismatches) > 0 {
			// never hidden behind the success line
			e.Emit("message", map[string]any{"text": tr(st.Lang, "Check: the saved record shows different values in step "+s.ID+": "+strings.Join(s.Mismatches, "; "),
				"بررسی: رکورد ذخیره‌شده در مرحله "+s.ID+" مقادیر متفاوتی نشان می‌دهد: "+strings.Join(s.Mismatches, "؛ "))})
		}
	}
	a.memoryAfterTask(ctx, u, t, st, e)
	status(e, st.Lang, "completed")
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// ---------- reading the result back ----------

// recordAfterUI looks the record up through the application after the widget finished:
// a created record is found by matching the entered values among records newer than the
// baseline; a changed record is read back by id. mismatches lists entered values the
// saved record does not show.
func (a *Agent) recordAfterUI(ctx context.Context, u *User, st *State, op *catalog.Operation, args tools.OpArgs) (json.RawMessage, []string) {
	var body map[string]json.RawMessage
	_ = json.Unmarshal(args.Body, &body)
	switch op.Kind {
	case "update":
		rb := readBackOp(op)
		if rb == nil || args.Path["id"] == "" {
			return nil, nil
		}
		ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": args.Path["id"], "kind": args.Path["kind"]}})
		v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil)
		if err != nil {
			return nil, nil
		}
		b, _ := json.Marshal(v)
		var obj map[string]any
		_ = json.Unmarshal(b, &obj)
		return b, compareBody(body, obj)
	case "create":
		best := a.listRecords(ctx, u, op, args)
		var pick map[string]any
		var pickID int64
		for _, it := range best {
			id := int64Of(it["id"])
			if id <= st.UIBaseline || id <= pickID {
				continue
			}
			if !looselyMatches(body, it) {
				continue
			}
			pick, pickID = it, id
		}
		if pick == nil {
			return nil, nil
		}
		// duplicate check (D63): the screen must have created exactly one such record
		var extra []string
		for _, it := range best {
			if id := int64Of(it["id"]); id > st.UIBaseline && id != pickID && looselyMatches(body, it) {
				extra = append(extra, strconv.FormatInt(id, 10))
			}
		}
		b, _ := json.Marshal(pick)
		// prefer the full record when the application has a read-back for it
		if rb := readBackOp(op); rb != nil {
			ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": strconv.FormatInt(pickID, 10), "kind": args.Path["kind"]}})
			if v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil); err == nil {
				if fb, err := json.Marshal(v); err == nil {
					b = fb
					_ = json.Unmarshal(fb, &pick)
				}
			}
		}
		m := compareBody(body, pick)
		if len(extra) > 0 {
			m = append(m, "more than one record was created: "+strings.Join(extra, ", "))
		}
		return b, m
	case "workflow":
		// a record action on screen (the application's Cancel button): done only when the
		// record reads back in the status the application's machine defines (D63)
		rb := readBackOp(op)
		if rb == nil || args.Path["id"] == "" {
			return nil, nil
		}
		ra, _ := json.Marshal(tools.OpArgs{Path: tools.Scalars{"id": args.Path["id"], "kind": args.Path["kind"]}})
		v, err := a.Tools.Call(ctx, a.env(u), rb.ID, pruneEmptyPath(rb, ra), nil)
		if err != nil {
			return nil, nil
		}
		b, _ := json.Marshal(v)
		var obj map[string]any
		_ = json.Unmarshal(b, &obj)
		if len(checkTransition(op, args, nil, obj)) > 0 {
			return nil, nil // the action did not happen
		}
		return b, nil
	}
	return nil, nil
}

// looselyMatches: at least one entered scalar equals the record's value (the list view
// may not carry every field), and none of the shared ones differ.
func looselyMatches(body map[string]json.RawMessage, rec map[string]any) bool {
	same := 0
	for k, raw := range body {
		want := scalarValue(raw)
		got, ok := rec[k]
		if want == "" || !ok {
			continue
		}
		if !sameValue(want, got) {
			return false
		}
		same++
	}
	return same > 0
}

func sameValue(want string, got any) bool {
	g := fmt.Sprint(got)
	if f, ok := got.(float64); ok {
		g = strconv.FormatFloat(f, 'f', -1, 64)
	}
	if want == g || normalize.Text(want) == normalize.Text(g) {
		return true
	}
	// numbers: 1000000 == 1000000.00
	wf, e1 := strconv.ParseFloat(strings.ReplaceAll(want, ",", ""), 64)
	gf, e2 := strconv.ParseFloat(g, 64)
	if e1 == nil && e2 == nil {
		return wf == gf
	}
	// dates: the record may carry a timestamp
	return len(g) >= 10 && len(want) == 10 && g[:10] == want
}

// compareBody lists entered scalar values the record shows differently.
func compareBody(body map[string]json.RawMessage, rec map[string]any) []string {
	var out []string
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if missing := missingElements(body[k], rec[k]); missing > 0 {
			out = append(out, fmt.Sprintf("%s: %d item(s) not found", k, missing))
			continue
		}
		want := scalarValue(body[k])
		got, ok := rec[k]
		if want == "" || !ok || got == nil || k == "post" || tools.SecretFields[k] {
			continue
		}
		if !sameValue(want, got) {
			out = append(out, fmt.Sprintf("%s: %s ≠ %v", k, want, got))
		}
	}
	return out
}

// listRecords reads the operation's list, narrowed by the entered name where the list takes q.
func (a *Agent) listRecords(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs) []map[string]any {
	return a.listRecordsBy(ctx, u, op, args, true)
}

// listRecordsBy: narrow=false lists the newest records of the type regardless of the
// entered values (a record the user saved after editing the form must still show).
func (a *Agent) listRecordsBy(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs, narrow bool) []map[string]any {
	items, _ := a.listRecordsOK(ctx, u, op, args, narrow)
	return items
}

// listRecordsOK also reports whether the list was read completely (an absence in it is
// conclusive only then).
func (a *Agent) listRecordsOK(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs, narrow bool) ([]map[string]any, bool) {
	lo := catalog.Load().ListOp(op)
	if lo == nil {
		return nil, false
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(args.Body, &body)
	la := tools.OpArgs{Path: tools.Scalars{}, Query: tools.Scalars{}}
	for _, p := range lo.PathParams {
		la.Path[p] = args.Path[p]
	}
	has := map[string]bool{}
	for _, q := range lo.QueryParams {
		has[q] = true
	}
	if has["q"] && narrow {
		for _, k := range []string{"name", "full_name", "last_name", "code", "number", "reference", "description"} {
			if v := scalarValue(body[k]); v != "" {
				la.Query["q"] = v
				break
			}
		}
	}
	if has["page_size"] {
		la.Query["page_size"] = "100"
	}
	raw, _ := json.Marshal(la)
	out, err := a.Tools.Call(ctx, a.env(u), lo.ID, pruneEmptyPath(lo, raw), nil)
	if err != nil {
		return nil, false
	}
	items := recordItems(out)
	return items, !has["page_size"] || len(items) < 100 // an unpaged list is complete
}

func recordItems(out any) []map[string]any {
	b, _ := json.Marshal(out)
	var arr []map[string]any
	if json.Unmarshal(b, &arr) == nil {
		return arr
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) == nil {
		for _, k := range []string{"items", "data", "rows", "results"} {
			if raw, ok := obj[k]; ok && json.Unmarshal(raw, &arr) == nil {
				return arr
			}
		}
	}
	return nil
}

// maxRecordID is the highest id the list shows before a create is operated on screen, so
// the new record is the one above it.
func (a *Agent) maxRecordID(ctx context.Context, u *User, op *catalog.Operation, args tools.OpArgs) int64 {
	var max int64
	for _, it := range a.listRecords(ctx, u, op, args) {
		if id := int64Of(it["id"]); id > max {
			max = id
		}
	}
	return max
}

// ---------- screen-only tasks: open a document, print/PDF, navigate, cancel ----------

// startScreenTask hands a screen-only plan to the widget. kind routes the widget's report.
func (a *Agent) startScreenTask(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, kind string, plan UIPlan) {
	if st.NoUI {
		a.uiUnavailable(ctx, u, t, st, e, errNoScreen(st.Lang))
		return
	}
	st.UIKind, st.Stage = kind, StageUIRunning
	a.save(ctx, t, st)
	plan.TaskID, plan.ExecMode = t.ID, string(execmode.HumanUI)
	a.audit(ctx, u, t.ID, "ui_plan", map[string]any{"kind": kind, "exec_mode": st.ExecMode, "steps": len(plan.Steps)})
	e.Emit("ui_plan", plan)
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// screenPlan rebuilds a screen-only task's plan (reload / retry).
func screenPlan(st *State) *UIPlan {
	var p UIPlan
	var why string
	switch st.UIKind {
	case "open":
		if st.Target != nil {
			p, why = openPlan(st.Lang, *st.Target)
		}
	case "pdf":
		if st.Target != nil {
			p, why = printPlan(st.Lang, *st.Target)
		}
	case "undo":
		if st.Target != nil {
			p, why = cancelPlan(st.Lang, *st.Target)
		}
	case "navigate":
		p = UIPlan{Steps: []UIStep{{Op: "navigate", Value: st.UIRoute, Label: tr(st.Lang, "Opening…", "در حال باز کردن…")}}}
	default:
		return nil
	}
	if why != "" || len(p.Steps) == 0 {
		return nil
	}
	p.ExecMode = string(execmode.HumanUI)
	return &p
}

// navigateOnScreen opens a screen with the pointer (menu click), or sends the route
// silently in DIRECT mode.
func (a *Agent) navigateOnScreen(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, route string) {
	if !st.humanUI() {
		e.Emit("navigate", map[string]any{"route": route})
		return
	}
	st.UIRoute = route
	a.startScreenTask(ctx, u, t, st, e, "navigate", *screenPlan(&State{Lang: st.Lang, UIKind: "navigate", UIRoute: route}))
}

// openDocument opens an existing document: list → row, with the pointer (HUMAN_UI);
// DIRECT just routes to it.
func (a *Agent) openDocument(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, art Artifact) {
	tgt := art.target()
	st.Target = tgt
	r, ok := screenOf(*tgt)
	if !st.humanUI() {
		route := r.list
		if ok && r.detail != "" {
			route = r.detail + strconv.FormatInt(r.id, 10)
		}
		if !ok {
			a.fail(ctx, u, t, st, e, tr(st.Lang, "I don't know which screen shows that.", "نمی‌دانم آن را کجا نشان بدهم."), nil)
			return
		}
		st.Stage = StageDone
		a.save(ctx, t, st)
		e.Emit("navigate", map[string]any{"route": route})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	plan, why := openPlan(st.Lang, *tgt)
	if why != "" {
		st.UIKind = "open"
		a.uiUnavailable(ctx, u, t, st, e, why)
		return
	}
	emitArtifact(e, &art)
	a.startScreenTask(ctx, u, t, st, e, "open", plan)
}

// screenTaskResult handles the widget's report for open / pdf / navigate / undo.
func (a *Agent) screenTaskResult(ctx context.Context, u *User, t *store.Task, st *State, req UIResult, e Emitter) {
	kind := st.UIKind
	if req.FallbackAPI {
		// explicit user choice: run it directly instead
		st.chooseMode(execmode.Direct)
		a.audit(ctx, u, t.ID, "mode_changed", map[string]any{"to": "direct", "by": "user_choice", "kind": kind})
		switch kind {
		case "pdf":
			st.UIKind = ""
			a.generatePDF(ctx, u, t, st, e)
		case "undo":
			st.UIKind = ""
			a.executeUndo(ctx, u, t, st, e)
		default:
			st.Stage, st.UIKind = StageDone, ""
			a.save(ctx, t, st)
			if st.UIRoute != "" {
				e.Emit("navigate", map[string]any{"route": st.UIRoute})
			}
			e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		}
		return
	}
	if !req.OK {
		st.Stage = StageUIFailed
		msg := tr(st.Lang, "I stopped: the screen did not behave as expected", "متوقف شدم: صفحه آن‌طور که انتظار داشتم رفتار نکرد")
		if req.Error != "" {
			msg += " (" + req.Error + ")"
		}
		msg += tr(st.Lang, ". Nothing was changed. You can retry, or choose “Run directly”.", ". چیزی تغییر نکرد. می‌توانید دوباره امتحان کنید یا «اجرای مستقیم» را بزنید.")
		st.Message = msg
		a.save(ctx, t, st)
		e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": true, "can_retry": true})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	st.UIKind = ""
	if kind == "undo" {
		a.verifyUndo(ctx, u, t, st, e, "human_ui")
		return
	}
	st.Stage = StageDone
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "ui_done", map[string]any{"kind": kind})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// missingElements counts the objects of a sent array (e.g. an employee's component
// overrides) that the record's array does not show with the same scalar values.
func missingElements(raw json.RawMessage, got any) int {
	var want []map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &want) != nil || len(want) == 0 {
		return 0
	}
	have, ok := got.([]any)
	if !ok {
		return 0 // the record does not show the list: nothing to compare
	}
	missing := 0
	for _, w := range want {
		found := false
		for _, h := range have {
			if hm, ok := h.(map[string]any); ok && looselyMatches(w, hm) && len(compareBody(w, hm)) == 0 {
				found = true
				break
			}
		}
		if !found {
			missing++
		}
	}
	return missing
}

// ResumeUI continues a screen task after a reload, a closed tab or a crash. The
// screen plan is NEVER replayed blindly: if the form was already saved before the
// interruption, the record is found through the application and the step is
// completed with it (a replay would register it a second time).
func (a *Agent) ResumeUI(ctx context.Context, u *User, id string, e Emitter) {
	a.withConversation(ctx, u, a.convOf(ctx, u, id), e, func(c *conversation, e Emitter) { a.resumeUIIn(ctx, u, id, e) })
}

func (a *Agent) resumeUIIn(ctx context.Context, u *User, id string, e Emitter) {
	t, st, err := a.load(ctx, u, id)
	if err != nil {
		e.Emit("error", map[string]any{"message": "task not found"})
		return
	}
	if st.Stage != StageUIRunning && st.Stage != StageUIFailed {
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	e.Emit("task", map[string]any{"task_id": t.ID})
	status(e, st.Lang, "verifying")
	switch {
	case st.UIKind != "":
		// open / print / navigate: nothing is registered, replaying is safe
	case st.Plan != nil && st.UIStep != "":
		for _, s := range st.Plan.Steps {
			if s.ID != st.UIStep || s.Status == "done" {
				continue
			}
			op, _ := catalog.Load().Get(s.Tool)
			args, _ := resolveRefs(st.Plan, s.Args)
			if rec, _ := a.recordAfterUI(ctx, u, st, op, args); rec != nil {
				a.audit(ctx, u, t.ID, "step_recovered", map[string]any{"step": s.ID, "tool": s.Tool, "how": "saved_before_interruption", "record": recordRef(rec)})
				a.planUIResult(ctx, u, t, st, UIResult{OK: true}, e)
				return
			}
		}
	case st.Proposal != nil && st.Plan == nil:
		if rec, _ := a.findCreated(ctx, u, t, st); rec != nil {
			a.audit(ctx, u, t.ID, "step_recovered", map[string]any{"how": "saved_before_interruption"})
			a.uiResultIn(ctx, u, t.ID, UIResult{OK: true}, e)
			return
		}
	}
	plan := uiPlanIfRunning(st)
	if plan == nil {
		a.fail(ctx, u, t, st, e, tr(st.Lang, "This step can no longer be shown on screen. Nothing was registered; you can run it directly.",
			"این مرحله دیگر روی صفحه قابل اجرا نیست. چیزی ثبت نشد؛ می‌توانید آن را مستقیم اجرا کنید."), nil)
		return
	}
	plan.TaskID = t.ID
	a.audit(ctx, u, t.ID, "ui_resumed", map[string]any{"step": st.UIStep, "kind": st.UIKind})
	e.Emit("ui_plan", plan)
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}
