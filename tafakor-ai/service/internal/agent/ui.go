package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// UIStep is one action for the widget's UI agent. Targets are language-independent
// data-ai-* tags emitted by the app's shared components (see CHANGES_TO_CORE.md):
//
//	page   → data-ai-page, action → data-ai-action, field → data-ai-field (the i18n key of the label),
//	dialog → data-ai-dialog, tab → data-ai-tab, option → data-ai-option-id.
type UIStep struct {
	Op     string `json:"op"`               // navigate | click | fill | select | combo | date | tab | check | submit | wait_closed | wait_leave | open_row | ensure_rows
	Page   string `json:"page,omitempty"`   // expected data-ai-page after navigate
	Dialog string `json:"dialog,omitempty"` // dialog scope (data-ai-dialog)
	Target string `json:"target,omitempty"` // tag value
	// Alts are alternative tags for the same target or scope, tried in order by the
	// widget. Used when the operation's variant is unknown: several controls can open
	// the form, and each opens a differently-named dialog.
	Alts   []string `json:"alts,omitempty"`
	Value  string   `json:"value,omitempty"`
	Search string   `json:"search,omitempty"` // text typed into a ComboBox
	Label  string   `json:"label"`            // human-readable status, e.g. "Filling amount…"
	// Dialog "*" scopes the step to the topmost open dialog (whatever its title).
	// Row/Col address a cell of a line grid (1-based row, 0-based cell).
	Row int `json:"row,omitempty"`
	Col int `json:"col,omitempty"`
	// Expect is what a click must cause: "dialog" (a dialog opens), "route:<prefix>"
	// (the location moves there) or "none". Empty keeps the legacy per-op expectation.
	Expect string `json:"expect,omitempty"`
	// Optional: the value only restates the form's default (0 / false); forms show some
	// controls only for other variants (a salary component's "exemption" exists only
	// for tax methods), so a missing control is skipped instead of failing the task.
	// The record read back after saving is still compared with every value.
	Optional bool `json:"optional,omitempty"`
}

type UIPlan struct {
	TaskID string   `json:"task_id"`
	Steps  []UIStep `json:"steps"`
	// Mode is "preview" when the form must be filled but not submitted (the user
	// reviews and submits it by hand). Empty means the normal auto-submit plan.
	Mode string `json:"mode,omitempty"`
	// ExecMode is always "human_ui": the plan is the assistant operating the real screen.
	ExecMode string `json:"exec_mode,omitempty"`
	// Step is the plan step this belongs to (multi-step operations run one step at a time).
	Step string `json:"step,omitempty"`
	// Watch (preview only): the save steps left to the user. The widget never runs them;
	// it watches for the user's own save so the server can find the record (D46).
	Watch []UIStep `json:"watch,omitempty"`
}

func isSaveStep(s UIStep) bool {
	return s.Op == "submit" || s.Op == "wait_closed" || s.Op == "wait_leave"
}

// UIResult is reported by the widget after running a plan.
type UIResult struct {
	OK          bool   `json:"ok"`
	FailedStep  int    `json:"failed_step"`
	Error       string `json:"error"`
	FallbackAPI bool   `json:"fallback_api"` // user explicitly chose "register via API instead"
	Preview     bool   `json:"preview"`      // the preview plan was filled; nothing was submitted
	// UserSaved: after a preview, the user saved the form themselves (the form closed or
	// the page left it). The server looks for the record; the widget's word is not proof.
	UserSaved bool `json:"user_saved"`
	Closed    bool `json:"closed"` // after a preview, the form was closed (saved or not)
}

func uiPlanIfRunning(st *State) *UIPlan {
	if st.UIKind != "" && (st.Stage == StageUIRunning || st.Stage == StageUIFailed) {
		return screenPlan(st)
	}
	if st.Stage == StageUIFailed && st.Plan != nil {
		return planUIPlan(st) // retry the step that failed
	}
	if st.Stage == StageUIPreview {
		if plan, err := BuildPreviewPlan(st); err == nil {
			return &plan
		}
		return nil
	}
	if st.Stage != StageUIRunning && st.Stage != StageUIFailed {
		return nil
	}
	if st.Plan != nil {
		return planUIPlan(st)
	}
	if st.Proposal == nil {
		return nil
	}
	p := BuildUIPlan(st)
	p.ExecMode = "human_ui"
	return &p
}

// BuildUIPlan converts the approved proposal into concrete UI steps.
func BuildUIPlan(st *State) UIPlan {
	p := st.Proposal
	lang := st.Lang
	L := func(en, fa string) string { return tr(lang, en, fa) }
	var steps []UIStep
	idStr := func(v *int64) string {
		if v == nil {
			return ""
		}
		return strconv.FormatInt(*v, 10)
	}
	switch {
	case p.Tx != nil:
		dlg := "treasury.newReceipt"
		if p.Action == "create_payment" {
			dlg = "treasury.newPayment"
		}
		x := p.Tx
		steps = append(steps,
			UIStep{Op: "navigate", Value: "/treasury/transactions", Page: "treasury/transactions", Label: L("Opening Receipts & payments…", "باز کردن دریافت و پرداخت…")},
			UIStep{Op: "click", Target: dlg, Label: L("Opening the form…", "باز کردن فرم…")},
			UIStep{Op: "date", Dialog: dlg, Target: "common.date", Value: x.Date, Label: L("Setting the date…", "تنظیم تاریخ…")},
			UIStep{Op: "select", Dialog: dlg, Target: "treasury.treasuryAccount", Value: strconv.FormatInt(x.TreasuryAccountID, 10), Label: L("Choosing the cash/bank account…", "انتخاب حساب نقد/بانک…")},
			UIStep{Op: "fill", Dialog: dlg, Target: "common.amount", Value: x.Amount, Label: L("Filling amount…", "وارد کردن مبلغ…")},
		)
		if x.CounterAccountID != nil {
			steps = append(steps,
				UIStep{Op: "tab", Dialog: dlg, Target: "account", Label: L("Switching to account mode…", "انتخاب حالت حساب…")},
				UIStep{Op: "combo", Dialog: dlg, Target: "journal.account", Value: idStr(x.CounterAccountID), Search: accountCode(p), Label: L("Choosing the account…", "انتخاب حساب…")})
		} else {
			steps = append(steps, UIStep{Op: "combo", Dialog: dlg, Target: "treasury.payee", Value: idStr(x.PartyID), Search: p.Display.Party, Label: L("Choosing the party…", "انتخاب طرف حساب…")})
		}
		if x.CostCenterID != nil {
			steps = append(steps, UIStep{Op: "select", Dialog: dlg, Target: "journal.costCenter", Value: idStr(x.CostCenterID), Label: L("Choosing the project…", "انتخاب پروژه…")})
		}
		if x.Reference != "" {
			steps = append(steps, UIStep{Op: "fill", Dialog: dlg, Target: "common.reference", Value: x.Reference, Label: L("Filling reference…", "وارد کردن شماره پیگیری…")})
		}
		if x.Description != "" {
			steps = append(steps, UIStep{Op: "fill", Dialog: dlg, Target: "common.description", Value: x.Description, Label: L("Filling description…", "وارد کردن شرح…")})
		}
		if x.PartyID != nil && x.CounterAccountID == nil {
			steps = append(steps, UIStep{Op: "check", Dialog: dlg, Target: "treasury.autoAllocate", Value: strconv.FormatBool(x.AutoAllocate), Label: L("Setting allocation…", "تنظیم تخصیص…")})
		}
		steps = append(steps,
			UIStep{Op: "submit", Dialog: dlg, Target: "common.save", Label: L("Saving…", "ذخیره…")},
			UIStep{Op: "wait_closed", Dialog: dlg, Label: L("Verifying…", "بررسی…")})
	case p.Expense != nil:
		dlg := "expenses.newExpense"
		x := p.Expense
		steps = append(steps,
			UIStep{Op: "navigate", Value: "/expenses", Page: "expenses", Label: L("Opening Expenses…", "باز کردن هزینه‌ها…")},
			UIStep{Op: "click", Target: dlg, Label: L("Opening the form…", "باز کردن فرم…")},
			UIStep{Op: "date", Dialog: dlg, Target: "common.date", Value: x.Date, Label: L("Setting the date…", "تنظیم تاریخ…")},
			UIStep{Op: "select", Dialog: dlg, Target: "expenses.category", Value: strconv.FormatInt(x.CategoryID, 10), Label: L("Choosing the category…", "انتخاب دسته هزینه…")},
			UIStep{Op: "fill", Dialog: dlg, Target: "common.amount", Value: x.Amount, Label: L("Filling amount…", "وارد کردن مبلغ…")},
			UIStep{Op: "select", Dialog: dlg, Target: "expenses.paymentMethod", Value: x.PaymentMethod, Label: L("Choosing payment method…", "انتخاب روش پرداخت…")},
		)
		if x.PaymentMethod == "credit" {
			steps = append(steps, UIStep{Op: "combo", Dialog: dlg, Target: "invoices.supplier", Value: idStr(x.PartyID), Search: p.Display.Party, Label: L("Choosing who is owed…", "انتخاب طلبکار…")})
		} else {
			steps = append(steps, UIStep{Op: "select", Dialog: dlg, Target: "treasury.treasuryAccount", Value: idStr(x.TreasuryAccountID), Label: L("Choosing the account…", "انتخاب حساب…")})
		}
		if x.CostCenterID != nil {
			steps = append(steps, UIStep{Op: "select", Dialog: dlg, Target: "journal.costCenter", Value: idStr(x.CostCenterID), Label: L("Choosing the project…", "انتخاب پروژه…")})
		}
		if x.Reference != "" {
			steps = append(steps, UIStep{Op: "fill", Dialog: dlg, Target: "common.reference", Value: x.Reference, Label: L("Filling reference…", "وارد کردن شماره پیگیری…")})
		}
		if x.Description != "" {
			steps = append(steps, UIStep{Op: "fill", Dialog: dlg, Target: "common.description", Value: x.Description, Label: L("Filling description…", "وارد کردن شرح…")})
		}
		steps = append(steps,
			UIStep{Op: "submit", Dialog: dlg, Target: "common.saveAndPost", Label: L("Saving and posting…", "ذخیره و قطعی کردن…")},
			UIStep{Op: "wait_closed", Dialog: dlg, Label: L("Verifying…", "بررسی…")})
	}
	return UIPlan{Steps: steps}
}

// BuildPreviewPlan produces the steps that open an operation's real form and fill
// in everything that is known, stopping before submit. The user reviews the form and
// submits it by hand; the assistant registers nothing.
func BuildPreviewPlan(st *State) (UIPlan, error) {
	L := func(en, fa string) string { return tr(st.Lang, en, fa) }
	if st.Plan != nil {
		return buildCatalogPreview(st, L)
	}
	plan := BuildUIPlan(st)
	steps := make([]UIStep, 0, len(plan.Steps))
	var watch []UIStep
	for _, s := range plan.Steps {
		if isSaveStep(s) {
			watch = append(watch, s)
			continue
		}
		if len(watch) > 0 {
			break // what follows the save is the assistant's own checking
		}
		steps = append(steps, s)
	}
	if len(steps) == 0 {
		return UIPlan{}, fmt.Errorf("no form to show")
	}
	return UIPlan{Steps: steps, Mode: "preview", Watch: watch}, nil
}

// buildCatalogPreview builds a preview plan for a multi-step operation: it opens the first
// pending step's real form and fills the values it knows (stopping before submit). The same
// builder as the on-screen execution (humanui.go), so preview and HUMAN_UI never diverge.
func buildCatalogPreview(st *State, L func(string, string) string) (UIPlan, error) {
	var pending []PlanStep
	for _, s := range st.Plan.Pending() {
		if !isSourceAttach(st, s) { // the system attaches the source document itself
			pending = append(pending, s)
		}
	}
	if len(pending) == 0 {
		return UIPlan{}, fmt.Errorf("nothing is left to do")
	}
	plan, why := buildStepPlan(st, pending[0], false)
	if why != "" {
		return UIPlan{}, fmt.Errorf("%s", why)
	}
	if full, why := buildStepPlan(st, pending[0], true); why == "" {
		for _, s := range full.Steps {
			if isSaveStep(s) {
				plan.Watch = append(plan.Watch, s)
			}
		}
	}
	return plan, nil
}

// previewStep selects the widget step kind for a body field.
func previewStep(name string, f catalog.Field, tag, value string, L func(string, string) string) UIStep {
	label := L("Filling "+name+"…", "وارد کردن "+name+"…")
	switch {
	case f.Type == "date" || strings.HasSuffix(name, "_date"):
		return UIStep{Op: "date", Target: tag, Value: value, Label: label}
	case len(f.Enum) > 0 || f.Type == "bool":
		op := "select"
		if f.Type == "bool" {
			op = "check"
		}
		return UIStep{Op: op, Target: tag, Value: value, Label: label}
	default:
		return UIStep{Op: "fill", Target: tag, Value: value, Label: label}
	}
}

// scalarValue renders a JSON body value as the string a form control takes. Objects
// and arrays (references to other documents) cannot be typed into a simple control.
func scalarValue(raw json.RawMessage) string {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}

func accountCode(p *classify.Proposal) string {
	if p.Display.CounterAccount == "" {
		return ""
	}
	for i, r := range p.Display.CounterAccount {
		if r == ' ' {
			return p.Display.CounterAccount[:i]
		}
	}
	return p.Display.CounterAccount
}

// startUI hands the approved payload to the widget's UI agent. The widget operates
// the real form (the app validates everything as for a human); afterwards the
// server verifies the created document against the approved payload.
func (a *Agent) startUI(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if st.NoUI {
		a.uiUnavailable(ctx, u, t, st, e, errNoScreen(st.Lang))
		return
	}
	if reason := uiUnsupported(st.Proposal, st.Lang); reason != "" {
		// The screen cannot express this exact payload. Say so and stop: HUMAN_UI never
		// switches to DIRECT on its own — the user chooses «اجرای مستقیم».
		a.uiUnavailable(ctx, u, t, st, e, reason)
		return
	}
	plan := BuildUIPlan(st)
	if len(plan.Steps) == 0 {
		a.uiUnavailable(ctx, u, t, st, e, tr(st.Lang, "I don't have a screen path for this kind of operation yet.", "هنوز مسیر صفحه‌ای برای این نوع عملیات ندارم."))
		return
	}
	st.Stage = StageUIRunning
	a.save(ctx, t, st)
	plan.TaskID = t.ID
	plan.ExecMode = "human_ui"
	a.audit(ctx, u, t.ID, "ui_plan", plan)
	e.Emit("ui_plan", plan)
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// UIResult receives the widget's report and verifies the outcome through the API.
func (a *Agent) uiResultIn(ctx context.Context, u *User, id string, req UIResult, e Emitter) {
	t, st, err := a.load(ctx, u, id)
	if err != nil {
		e.Emit("error", map[string]any{"message": "task not found"})
		return
	}
	if (req.UserSaved || req.Closed) && st.Stage != StageUIPreview {
		// the operation already finished another way (registered from the chat): nothing to do
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	if st.Stage != StageUIRunning && st.Stage != StageUIFailed && st.Stage != StageUIPreview {
		e.Emit("error", map[string]any{"message": tr(st.Lang, "This task is not running on screen.", "این کار روی صفحه در حال اجرا نیست.")})
		return
	}
	// Preview: the form was filled in and left for the user to review and submit.
	// Nothing was registered, so there is nothing to verify.
	if st.Stage == StageUIPreview {
		a.audit(ctx, u, t.ID, "ui_preview_result", req)
		if req.UserSaved || req.Closed {
			if !a.completeHandSaved(ctx, u, t, st, e, !req.UserSaved) && !req.UserSaved {
				e.Emit("message", map[string]any{"text": tr(st.Lang,
					"The form was closed without saving; nothing was registered. Tell me if you want me to register it.",
					"فرم بدون ذخیره بسته شد؛ چیزی ثبت نشد. اگر می‌خواهید خودم ثبتش کنم بگویید.")})
				e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
			}
			return
		}
		if !req.OK {
			st.Stage, st.Message = StageUIFailed, req.Error
			a.save(ctx, t, st)
			msg := tr(st.Lang, "I could not fill the form", "نتوانستم فرم را پر کنم")
			if req.Error != "" {
				msg += " (" + req.Error + ")"
			}
			e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": false})
			e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
			return
		}
		// The task stays open: nothing was registered, so the operation is not finished.
		// The user can save the form by hand, or ask the assistant to register it instead
		// (a follow-up continues this same operation and its exact payload).
		st.Stage = StageUIPreview
		a.save(ctx, t, st)
		e.Emit("message", map[string]any{"text": tr(st.Lang,
			"The form is open and filled in with these values — review it and save it when you are happy. I registered nothing; if you would rather I register it, just say so and I will.",
			"فرم باز و با این مقادیر پر شد؛ بررسی کنید و هر وقت مطمئن شدید ذخیره کنید. من چیزی ثبت نکردم؛ اگر ترجیح می‌دهید خودم ثبت کنم فقط بگویید.")})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	if st.UIKind != "" && st.UIKind != "undo" { // open | pdf | navigate: nothing to approve or verify
		a.audit(ctx, u, t.ID, "ui_result", req)
		a.screenTaskResult(ctx, u, t, st, req, e)
		return
	}
	if !st.Approved {
		e.Emit("error", map[string]any{"message": "not approved"})
		return
	}
	a.audit(ctx, u, t.ID, "ui_result", req)
	if st.UIKind == "undo" {
		a.screenTaskResult(ctx, u, t, st, req, e)
		return
	}
	if st.Plan != nil {
		a.planUIResult(ctx, u, t, st, req, e)
		return
	}
	mode := "human"
	if st.Decision != nil && st.Decision.Mode == "auto" {
		mode = "auto"
	}
	status(e, st.Lang, "verifying")
	found, mismatches := a.findCreated(ctx, u, t, st)
	if found != nil {
		// Whatever the widget reported, the document exists: report verified state only.
		res := a.verify(ctx, u, st, found)
		res.Mode = mode
		if len(mismatches) > 0 {
			res.Mismatches = append(res.Mismatches, mismatches...)
			res.Verified = false
		}
		st.Result = res
		a.audit(ctx, u, t.ID, "accounting_result", map[string]any{"entity_type": res.EntityType, "entity_id": res.EntityID, "verified": res.Verified, "via": "ui"})
		a.attachUploads(ctx, u, t, st, e)
		a.finish(ctx, u, t, st, e)
		return
	}
	if req.FallbackAPI && st.Stage == StageUIFailed {
		// The user explicitly asked to register the same approved payload through the API;
		// we checked above that the UI attempt created nothing.
		st.chooseMode(execmode.Direct) // the user explicitly chose to run it directly
		a.audit(ctx, u, t.ID, "mode_changed", map[string]any{"to": "direct", "by": "user_choice"})
		a.save(ctx, t, st)
		a.execute(ctx, u, t, st, &tools.Authorization{PayloadHash: tools.PayloadHash(st.Proposal.Action, st.ArgsJSON), Mode: mode, ApprovedBy: st.ApprovedBy, TaskID: t.ID}, e)
		return
	}
	st.Stage = StageUIFailed
	st.Message = req.Error
	a.save(ctx, t, st)
	msg := tr(st.Lang, "I stopped: the screen did not behave as expected", "متوقف شدم: صفحه آن‌طور که انتظار داشتم رفتار نکرد")
	if req.Error != "" {
		msg += " (" + req.Error + ")"
	}
	msg += tr(st.Lang, ". Nothing was registered. You can retry, or register it directly instead.", ". چیزی ثبت نشد. می‌توانید دوباره امتحان کنید یا مستقیم ثبت کنید.")
	e.Emit("ui_failed", map[string]any{"task_id": t.ID, "message": msg, "can_fallback": true})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// findCreated looks for the document the UI agent should have created: same kind,
// date and amount, created after the task started. Returns a mismatch list when a
// candidate differs from the approved payload.
func (a *Agent) findCreated(ctx context.Context, u *User, t *store.Task, st *State) (any, []string) {
	p := st.Proposal
	env := a.env(u)
	since := t.CreatedAt.Add(-5 * time.Second)
	switch {
	case p.Tx != nil:
		args, _ := json.Marshal(map[string]any{"kind": p.Action[len("create_"):], "from": p.Tx.Date, "to": p.Tx.Date, "limit": 20})
		res, err := a.Tools.Call(ctx, env, "search_transactions", args, nil)
		if err != nil {
			return nil, nil
		}
		want, _ := decimal.NewFromString(p.Tx.Amount)
		for _, x := range res.([]tools.Transaction) {
			ct, _ := time.Parse(time.RFC3339Nano, x.CreatedAt)
			if x.Amount.Equal(want) && x.Status != "cancelled" && ct.After(since) {
				xx := x
				return &xx, compareTx(p.Tx, &xx)
			}
		}
	case p.Expense != nil:
		args, _ := json.Marshal(map[string]any{"from": p.Expense.Date, "to": p.Expense.Date, "limit": 20})
		res, err := a.Tools.Call(ctx, env, "search_expenses", args, nil)
		if err != nil {
			return nil, nil
		}
		want, _ := decimal.NewFromString(p.Expense.Amount)
		for _, x := range res.([]tools.Expense) {
			ct, _ := time.Parse(time.RFC3339Nano, x.CreatedAt)
			if x.Amount.Equal(want) && x.Status != "cancelled" && ct.After(since) {
				xx := x
				return &xx, compareExpense(p.Expense, &xx)
			}
		}
	}
	return nil, nil
}

var _ = fmt.Sprint

// uiUnsupported explains why the app's form cannot represent a payload exactly.
func uiUnsupported(p *classify.Proposal, lang string) string {
	switch {
	case p.Tx != nil && p.Tx.CounterAccountID != nil && p.Tx.PartyID != nil:
		return tr(lang, "The receipt form can't book a ledger account and a party on the same line, so I can't do it on screen.",
			"فرم دریافت نمی‌تواند حساب معین و طرف حساب را هم‌زمان ثبت کند؛ پس روی صفحه نمی‌توانم.")
	case p.Tx != nil && p.Tx.BudgetConfirm, p.Expense != nil && p.Expense.BudgetConfirm:
		return tr(lang, "This posting needs a budget confirmation, so it can't be done on screen.",
			"این ثبت نیاز به تایید بودجه دارد؛ روی صفحه انجام نمی‌شود.")
	case p.Inventory != nil:
		return tr(lang, "Stock receipts have no on-screen path for me yet.", "برای رسید انبار هنوز مسیر صفحه‌ای ندارم.")
	case p.Expense != nil && p.Expense.PaymentMethod == "credit":
		return tr(lang, "The expense form only lists suppliers for credit expenses, so I can't do it on screen.",
			"فرم هزینه برای هزینه نسیه فقط تأمین‌کننده‌ها را نشان می‌دهد؛ پس روی صفحه نمی‌توانم.")
	}
	return ""
}
