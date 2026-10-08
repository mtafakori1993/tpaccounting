package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// UIStep is one action for the widget's UI agent. Targets are language-independent
// data-ai-* tags emitted by the app's shared components (see CHANGES_TO_CORE.md):
//
//	page   → data-ai-page, action → data-ai-action, field → data-ai-field (the i18n key of the label),
//	dialog → data-ai-dialog, tab → data-ai-tab, option → data-ai-option-id.
type UIStep struct {
	Op     string `json:"op"`               // navigate | click | fill | select | combo | date | tab | check | submit | wait_closed
	Page   string `json:"page,omitempty"`   // expected data-ai-page after navigate
	Dialog string `json:"dialog,omitempty"` // dialog scope (data-ai-dialog)
	Target string `json:"target,omitempty"` // tag value
	Value  string `json:"value,omitempty"`
	Search string `json:"search,omitempty"` // text typed into a ComboBox
	Label  string `json:"label"`            // human-readable status, e.g. "Filling amount…"
}

type UIPlan struct {
	TaskID string   `json:"task_id"`
	Steps  []UIStep `json:"steps"`
}

// UIResult is reported by the widget after running a plan.
type UIResult struct {
	OK          bool   `json:"ok"`
	FailedStep  int    `json:"failed_step"`
	Error       string `json:"error"`
	FallbackAPI bool   `json:"fallback_api"` // user explicitly chose "register via API instead"
}

func uiPlanIfRunning(st *State) *UIPlan {
	if st.Stage != StageUIRunning || st.Proposal == nil {
		return nil
	}
	p := BuildUIPlan(st)
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
	if reason := uiUnsupported(st.Proposal, st.Lang); reason != "" {
		// The form cannot express this exact payload: register it directly and say so.
		e.Emit("message", map[string]any{"text": reason})
		a.audit(ctx, u, t.ID, "ui_skipped", map[string]any{"reason": reason})
		st.ExecMode = "api"
		mode := "human"
		if st.Decision != nil && st.Decision.Mode == "auto" {
			mode = "auto"
		}
		a.execute(ctx, u, t, st, &tools.Authorization{PayloadHash: tools.PayloadHash(st.Proposal.Action, st.ArgsJSON), Mode: mode, ApprovedBy: st.ApprovedBy, TaskID: t.ID}, e)
		return
	}
	st.Stage = StageUIRunning
	a.save(ctx, t, st)
	plan := BuildUIPlan(st)
	plan.TaskID = t.ID
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
	if st.Stage != StageUIRunning && st.Stage != StageUIFailed {
		e.Emit("error", map[string]any{"message": tr(st.Lang, "This task is not running on screen.", "این کار روی صفحه در حال اجرا نیست.")})
		return
	}
	if !st.Approved {
		e.Emit("error", map[string]any{"message": "not approved"})
		return
	}
	a.audit(ctx, u, t.ID, "ui_result", req)
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
		st.ExecMode = "api"
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
		return tr(lang, "The receipt form can't book a ledger account and a party on the same line, so I'm registering this directly (same approved details).",
			"فرم دریافت نمی‌تواند حساب معین و طرف حساب را هم‌زمان ثبت کند؛ پس همین جزئیات تاییدشده را مستقیم ثبت می‌کنم.")
	case p.Tx != nil && p.Tx.BudgetConfirm, p.Expense != nil && p.Expense.BudgetConfirm:
		return tr(lang, "This posting needs a budget confirmation, so I'm registering it directly with your confirmation.",
			"این ثبت نیاز به تایید بودجه دارد؛ پس با تایید شما مستقیم ثبت می‌کنم.")
	case p.Inventory != nil:
		return tr(lang, "Stock receipts are registered directly (same approved details).", "رسید انبار مستقیم ثبت می‌شود (همان جزئیات تاییدشده).")
	case p.Expense != nil && p.Expense.PaymentMethod == "credit":
		return tr(lang, "The expense form only lists suppliers for credit expenses, so I'm registering this directly.",
			"فرم هزینه برای هزینه نسیه فقط تأمین‌کننده‌ها را نشان می‌دهد؛ پس مستقیم ثبت می‌کنم.")
	}
	return ""
}
