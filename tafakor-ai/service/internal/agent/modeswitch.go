package agent

// A bare execution-mode instruction («مستقیم اجرا کن», «به‌جای آن مستقیم اجرا کن», «با
// موس انجام بده») changes HOW the open operation runs — same task, same steps, same
// entity, same approval. It never re-plans and never creates a task (D59). Approval is a
// separate dimension: a card still waiting for approval stays waiting, with the new mode.

import (
	"context"

	"github.com/tafakorpardazan/tafakor-ai/internal/execmode"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

func modeLabel(lang string, m execmode.Mode) string {
	if m == execmode.HumanUI {
		return tr(lang, "on screen", "روی صفحه")
	}
	return tr(lang, "directly", "مستقیم")
}

// switchMode applies a bare mode instruction to the open operation.
func (a *Agent) switchMode(ctx context.Context, u *User, t *store.Task, st *State, m execmode.Mode, e Emitter) {
	e.Emit("task", map[string]any{"task_id": t.ID})
	if ok, why := modeSupported(st, m); !ok {
		code := FailNoUIExecutor
		if m == execmode.Direct {
			code = FailNoDirect
		}
		if why == FailNoExecutor {
			code = why
		}
		a.audit(ctx, u, t.ID, "mode_change_refused", map[string]any{"to": m, "code": code, "reason": why})
		e.Emit("message", map[string]any{"text": tr(st.Lang, "This operation can't be carried out "+modeLabel(st.Lang, m)+" ("+why+"). Nothing was changed; the operation is still open as it was.",
			"این عملیات "+modeLabel(st.Lang, m)+" قابل انجام نیست ("+uiReasonFa(why)+"). چیزی تغییر نکرد و عملیات همان‌طور باز است.")})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	st.chooseMode(m)
	a.audit(ctx, u, t.ID, "mode_changed", map[string]any{"to": m, "by": "user_text", "stage": st.Stage})
	switch st.Stage {
	case StageApproval:
		// still waiting for approval: the same card, now with this mode (never auto-approved)
		a.save(ctx, t, st)
		e.Emit("message", map[string]any{"text": tr(st.Lang, "OK — once you approve, I'll do it "+modeLabel(st.Lang, m)+".",
			"باشه؛ پس از تأیید شما، "+modeLabel(st.Lang, m)+" انجامش می‌دهم.")})
		a.reissueIn(ctx, u, t, st, e)
	case StageUIFailed:
		// already approved; the screen attempt registered nothing (checked below)
		if m == execmode.Direct {
			if st.Plan != nil {
				a.planUIResult(ctx, u, t, st, UIResult{FallbackAPI: true}, e)
				return
			}
			a.uiResultIn(ctx, u, t.ID, UIResult{FallbackAPI: true}, e)
			return
		}
		if st.Plan != nil {
			a.dispatchPlan(ctx, u, t, st, e)
			return
		}
		a.save(ctx, t, st)
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
	default:
		// a question is still open: the mode is remembered for when it runs
		a.save(ctx, t, st)
		e.Emit("message", map[string]any{"text": tr(st.Lang, "OK — I'll do it "+modeLabel(st.Lang, m)+" when it runs.", "باشه؛ هنگام اجرا "+modeLabel(st.Lang, m)+" انجامش می‌دهم.")})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
	}
}

// reportLast answers a bare mode instruction when no operation is open: the state of
// the conversation's last operation, read from its persisted task — never a new plan.
func (a *Agent) reportLast(ctx context.Context, u *User, c *conversation, e Emitter) {
	l := lang(u)
	if c == nil || c.state.ActiveTask == "" {
		e.Emit("message", map[string]any{"text": tr(l, "There is no operation waiting to be carried out. Tell me what you want done.", "کاری در انتظار اجرا نیست. بگویید چه کاری انجام شود.")})
		e.Emit("done", map[string]any{"stage": "none"})
		return
	}
	t, st, err := a.load(ctx, u, c.state.ActiveTask)
	if err != nil {
		e.Emit("message", map[string]any{"text": tr(l, "There is no operation waiting to be carried out.", "کاری در انتظار اجرا نیست.")})
		e.Emit("done", map[string]any{"stage": "none"})
		return
	}
	prev := truncate(st.UserText, 80)
	msg := tr(l, "There is no operation waiting to be carried out.", "کاری در انتظار اجرا نیست.")
	switch {
	case st.Stage == StageDone && st.Result != nil && st.Result.Verified:
		msg = tr(l, "The last request («"+prev+"») is already done and verified; there is nothing left to run.",
			"درخواست قبلی («"+prev+"») انجام و در نرم‌افزار تأیید شده است؛ چیزی برای اجرا باقی نمانده.")
	case st.Stage == StageDone && st.Result == nil:
		msg = tr(l, "The last request («"+prev+"») was answered; nothing is waiting to be carried out.",
			"به درخواست قبلی («"+prev+"») پاسخ داده شد و کاری در انتظار اجرا نیست.")
	case st.Stage == StageDone:
		msg = tr(l, "The last request («"+prev+"») already ran; I could not confirm every value — check it in the application.",
			"درخواست قبلی («"+prev+"») اجرا شده است اما همه مقادیر تأیید نشد؛ لطفاً در نرم‌افزار بررسی کنید.")
	case st.Exec != nil && st.Exec.FailureCode == FailNoExecutor:
		msg = tr(l, "The last request («"+prev+"») can't be carried out by the assistant in any mode; repeating it won't change that. Nothing was changed.",
			"درخواست قبلی («"+prev+"») به هیچ روشی توسط دستیار قابل انجام نیست و تکرارش هم نتیجه را تغییر نمی‌دهد. چیزی تغییر نکرد.")
	case st.Stage == StageFailed || st.Stage == StageCancelled:
		msg = tr(l, "The last request («"+prev+"») is closed (nothing was registered). Tell me again what you want done.",
			"درخواست قبلی («"+prev+"») بسته شده و چیزی ثبت نشد. دوباره بگویید چه کاری انجام شود.")
	}
	a.audit(ctx, u, t.ID, "mode_instruction_without_operation", map[string]any{"stage": st.Stage})
	e.Emit("message", map[string]any{"text": msg})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// reissueIn issues a fresh token for the unchanged payload and shows the card again.
func (a *Agent) reissueIn(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	hash := payloadHash(st.Proposal.Action, st.ArgsJSON, st.Uploads)
	if st.Plan != nil {
		hash = planHash(st.Plan)
	}
	if hash != st.PayloadHash {
		e.Emit("error", map[string]any{"message": "the details changed; review again"})
		return
	}
	tok, exp, err := a.Approvals.Issue(ctx, u.Scope, t.ID, hash)
	if err != nil {
		e.Emit("error", map[string]any{"message": a.plain(st.Lang, err)})
		return
	}
	reasons, warnings := []map[string]string{}, []string{}
	if st.Decision != nil {
		reasons = reasonTexts(st.Lang, st.Decision.Reasons)
		warnings = st.Decision.Warnings
	}
	direct, preview := supports(st.Proposal)
	ui := !st.NoUI
	if st.Plan != nil {
		direct, _ = modeSupported(st, execmode.Direct)
		uiOK, _ := modeSupported(st, execmode.HumanUI)
		ui = ui && uiOK
	}
	e.Emit("approval", map[string]any{"task_id": t.ID, "token": tok, "expires_at": exp, "payload_hash": hash, "reasons": reasons, "exec_mode": st.ExecMode,
		"warnings": reasonTexts(st.Lang, warnings), "can_preview": preview, "can_direct": direct, "can_ui": ui})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}
