package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// execute performs the approved write through the allowlisted tool (API path),
// then verifies the result by reading it back, attaches the original document and
// reports only verified state.
func (a *Agent) execute(ctx context.Context, u *User, t *store.Task, st *State, auth *tools.Authorization, e Emitter) {
	p := st.Proposal
	if st.Decision != nil && st.Decision.Simulated {
		// never reached for real executions; simulation stops at the approval card
		return
	}
	if st.ExecMode == "ui" && (p.Action == "create_receipt" || p.Action == "create_payment" || p.Action == "create_expense") {
		a.startUI(ctx, u, t, st, e)
		return
	}
	status(e, st.Lang, "registering")
	env := a.env(u)
	// Idempotency: the same approved payload of the same task is executed at most once.
	idemKey := t.ID + ":" + st.PayloadHash
	if prev, ok, _ := a.Store.Recall(ctx, u.Scope, idemKey); ok {
		var r Result
		_ = json.Unmarshal(prev, &r)
		st.Result, st.Stage = &r, StageDone
		a.save(ctx, t, st)
		e.Emit("result", a.resultPayload(st))
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	out, err := a.Tools.Call(ctx, env, p.Action, st.ArgsJSON, auth)
	a.audit(ctx, u, t.ID, "tool_call", map[string]any{"tool": p.Action, "args": json.RawMessage(st.ArgsJSON), "mode": auth.Mode, "ok": err == nil,
		"error": errString(err)})
	if err != nil {
		if ae, ok := accounting.AsAPIError(err); ok && ae.Code == "budget_confirmation_required" && !budgetConfirm(p) {
			// soft budget limit: ask the human, with the confirmation header bound into the new payload
			if p.Tx != nil {
				p.Tx.BudgetConfirm = true
			} else if p.Expense != nil {
				p.Expense.BudgetConfirm = true
			}
			st.AskThisTime = true
			e.Emit("message", map[string]any{"text": apiErrorText(st.Lang, ae)})
			cfg, cerr := tools.LoadCompanyConfig(ctx, env)
			if cerr != nil {
				a.fail(ctx, u, t, st, e, a.plain(st.Lang, cerr), cerr)
				return
			}
			a.decide(ctx, u, t, st, cfg, e)
			return
		}
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	res := a.verify(ctx, u, st, out)
	res.Mode = auth.Mode
	st.Result = res
	b, _ := json.Marshal(res)
	_ = a.Store.Remember(ctx, u.Scope, idemKey, b)
	a.audit(ctx, u, t.ID, "accounting_result", map[string]any{"entity_type": res.EntityType, "entity_id": res.EntityID, "number": res.Number,
		"journal_number": res.JournalNumber, "verified": res.Verified, "mismatches": res.Mismatches})
	if res.EntityID > 0 {
		a.attachUploads(ctx, u, t, st, e)
	}
	a.finish(ctx, u, t, st, e)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// finish records the outcome, grows trust in used rules and emits the verified summary.
func (a *Agent) finish(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	res := st.Result
	st.Stage = StageDone
	if res.Mode == "auto" {
		a.audit(ctx, u, t.ID, "auto_registered", map[string]any{"entity_type": res.EntityType, "entity_id": res.EntityID})
	}
	if res.Mode == "human" && st.Proposal != nil {
		for _, id := range st.Proposal.UsedRules {
			_ = a.Learn.Confirm(ctx, u.Scope, id)
		}
	}
	a.save(ctx, t, st)
	status(e, st.Lang, "verifying")
	e.Emit("result", a.resultPayload(st))
	if res.Mode == "auto" {
		e.Emit("notify", map[string]any{"text": tr(st.Lang, "Registered automatically, within your limits.", "به‌صورت خودکار و در محدوده سقف‌های شما ثبت شد."), "task_id": t.ID})
	}
	if len(st.Offers) > 0 {
		e.Emit("offers", st.Offers)
	}
	status(e, st.Lang, "completed")
	a.audit(ctx, u, t.ID, "result", a.resultPayload(st))
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

func (a *Agent) resultPayload(st *State) map[string]any {
	r := st.Result
	return map[string]any{"result": r, "summary": summary(st), "undo_available": r != nil && r.Verified && !r.Undone &&
		(r.EntityType == "treasury_transaction" || r.EntityType == "expense")}
}

// summary is built ONLY from verified fields read back from the API.
func summary(st *State) string {
	r := st.Result
	if r == nil {
		return ""
	}
	if !r.Verified {
		return tr(st.Lang, "The document was sent but I could not verify it: ", "سند ارسال شد اما نتوانستم آن را تایید کنم: ") + strings.Join(r.Mismatches, ", ")
	}
	kind := map[string][2]string{"treasury_transaction": {"Treasury document", "سند خزانه‌داری"}, "expense": {"Expense", "هزینه"}, "invoice": {"Draft invoice", "پیش‌نویس فاکتور"}}[r.EntityType]
	k := kind[0]
	if st.Lang == "fa" {
		k = kind[1]
	}
	s := fmt.Sprintf("%s #%d", k, r.Number)
	if r.JournalNumber != nil {
		s += tr(st.Lang, fmt.Sprintf(" (journal #%d)", *r.JournalNumber), fmt.Sprintf(" (سند حسابداری %d)", *r.JournalNumber))
	}
	s += tr(st.Lang, " registered: ", " ثبت شد: ") + r.Amount
	if r.Party != "" {
		s += " — " + r.Party
	}
	s += " — " + r.Date
	if len(r.Attachments) > 0 {
		s += tr(st.Lang, " — attached: ", " — پیوست: ") + strings.Join(r.Attachments, ", ")
	}
	if r.BudgetWarning {
		s += tr(st.Lang, " (budget warning raised)", " (هشدار بودجه)")
	}
	return s
}

// verify reads the created document back and compares it with the approved payload.
func (a *Agent) verify(ctx context.Context, u *User, st *State, out any) *Result {
	p := st.Proposal
	r := &Result{}
	switch x := out.(type) {
	case *tools.Transaction:
		var got tools.Transaction
		err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: http.MethodGet, Path: fmt.Sprintf("/treasury/transactions/%d", x.ID)}, &got)
		r.EntityType, r.EntityID, r.Route = "treasury_transaction", x.ID, "/treasury/transactions"
		if err != nil {
			r.Mismatches = append(r.Mismatches, "read-back failed")
			return r
		}
		r.Number, r.JournalNumber, r.Amount, r.Date, r.Status = got.Number, got.JournalNumber, got.Amount.String(), got.Date[:min(10, len(got.Date))], got.Status
		if got.PartyName != nil {
			r.Party = *got.PartyName
		}
		r.Mismatches = compareTx(p.Tx, &got)
	case *tools.Expense:
		var got tools.Expense
		err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: http.MethodGet, Path: fmt.Sprintf("/expenses/%d", x.ID)}, &got)
		r.EntityType, r.EntityID, r.Route = "expense", x.ID, "/expenses"
		if err != nil {
			r.Mismatches = append(r.Mismatches, "read-back failed")
			return r
		}
		r.Number, r.JournalNumber, r.Amount, r.Date, r.Status = got.Number, got.JournalNumber, got.Amount.String(), got.Date[:min(10, len(got.Date))], got.Status
		if got.PartyName != nil {
			r.Party = *got.PartyName
		}
		r.Mismatches = compareExpense(p.Expense, &got)
	case *tools.Invoice:
		var got tools.Invoice
		side := "sales"
		if p.Action == "create_purchase" {
			side = "purchases"
		}
		err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: http.MethodGet, Path: fmt.Sprintf("/%s/invoices/%d", side, x.ID)}, &got)
		r.EntityType, r.EntityID, r.Route = "invoice", x.ID, fmt.Sprintf("/%s/invoices/%d", side, x.ID)
		if err != nil {
			r.Mismatches = append(r.Mismatches, "read-back failed")
			return r
		}
		r.Number, r.Amount, r.Date, r.Status, r.Party = got.Number, got.Total.String(), got.Date[:min(10, len(got.Date))], got.Status, got.PartyName
		if got.PartyID != p.Invoice.PartyID {
			r.Mismatches = append(r.Mismatches, "party")
		}
	default:
		r.Mismatches = append(r.Mismatches, "unexpected response")
		return r
	}
	r.Verified = len(r.Mismatches) == 0
	return r
}

func eqID(a *int64, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func compareTx(want *tools.TxArgs, got *tools.Transaction) []string {
	var m []string
	wa, _ := decimal.NewFromString(want.Amount)
	if !wa.Equal(got.Amount) {
		m = append(m, "amount")
	}
	if want.Date != got.Date[:min(10, len(got.Date))] {
		m = append(m, "date")
	}
	if want.TreasuryAccountID != got.TreasuryAccountID {
		m = append(m, "treasury_account")
	}
	if !eqID(want.PartyID, got.PartyID) {
		m = append(m, "party")
	}
	if !eqID(want.CostCenterID, got.CostCenterID) {
		m = append(m, "project")
	}
	if want.CounterAccountID != nil && !eqID(want.CounterAccountID, got.CounterAccountID) {
		m = append(m, "counter_account")
	}
	if got.Status != "posted" {
		m = append(m, "status="+got.Status)
	}
	return m
}

func compareExpense(want *tools.ExpenseArgs, got *tools.Expense) []string {
	var m []string
	wa, _ := decimal.NewFromString(want.Amount)
	if !wa.Equal(got.Amount) {
		m = append(m, "amount")
	}
	if want.Date != got.Date[:min(10, len(got.Date))] {
		m = append(m, "date")
	}
	if want.CategoryID != got.CategoryID {
		m = append(m, "category")
	}
	if !eqID(want.TreasuryAccountID, got.TreasuryAccountID) {
		m = append(m, "treasury_account")
	}
	if !eqID(want.CostCenterID, got.CostCenterID) {
		m = append(m, "project")
	}
	if got.PaymentMethod != want.PaymentMethod {
		m = append(m, "payment_method")
	}
	if got.Status != "posted" {
		m = append(m, "status="+got.Status)
	}
	return m
}

// attachUploads attaches the original documents (shown in the approved preview) and verifies the listing.
func (a *Agent) attachUploads(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if len(st.Uploads) == 0 || st.Result == nil {
		return
	}
	status(e, st.Lang, "attaching")
	env := a.env(u)
	for _, up := range st.Uploads {
		args, _ := json.Marshal(tools.AttachArgs{EntityType: st.Result.EntityType, EntityID: st.Result.EntityID, UploadID: up.ID})
		auth := &tools.Authorization{PayloadHash: tools.PayloadHash("attach_document", args), Mode: st.Result.Mode, TaskID: t.ID}
		_, err := a.Tools.Call(ctx, env, "attach_document", args, auth)
		a.audit(ctx, u, t.ID, "tool_call", map[string]any{"tool": "attach_document", "file": up.Name, "ok": err == nil, "error": errString(err)})
		if err != nil {
			e.Emit("message", map[string]any{"text": tr(st.Lang, "The document was registered but attaching the file failed: ", "سند ثبت شد اما پیوست فایل ناموفق بود: ") + a.plain(st.Lang, err)})
		}
	}
	// verify attachments by listing them
	var list []struct {
		FileName string `json:"file_name"`
	}
	q := map[string][]string{"entity_type": {st.Result.EntityType}, "entity_id": {fmt.Sprint(st.Result.EntityID)}}
	if err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: http.MethodGet, Path: "/attachments", Query: q}, &list); err == nil {
		for _, l := range list {
			st.Result.Attachments = append(st.Result.Attachments, l.FileName)
		}
	}
}

// ---------- approval ----------

type ApproveRequest struct {
	Token       string `json:"token"`
	PayloadHash string `json:"payload_hash"`
	ExecMode    string `json:"exec_mode"`
}

// Approve consumes the single-use token for the payload stored on the task. The
// payload hash is recomputed server-side; the client's copy is only a consistency check.
func (a *Agent) Approve(ctx context.Context, u *User, id string, req ApproveRequest, e Emitter) {
	t, st, err := a.load(ctx, u, id)
	if err != nil {
		e.Emit("error", map[string]any{"message": "task not found"})
		return
	}
	if st.Stage != StageApproval || st.Proposal == nil {
		e.Emit("error", map[string]any{"message": tr(st.Lang, "This task is not waiting for approval.", "این کار منتظر تایید نیست.")})
		return
	}
	recomputed := payloadHash(st.Proposal.Action, st.ArgsJSON, st.Uploads)
	if req.PayloadHash != "" && req.PayloadHash != recomputed {
		e.Emit("error", map[string]any{"message": tr(st.Lang, "The details changed after you reviewed them. Please review again.", "جزئیات پس از بررسی شما تغییر کرد. دوباره بررسی کنید.")})
		return
	}
	if err := a.Approvals.Consume(ctx, u.Scope, t.ID, req.Token, recomputed); err != nil {
		a.audit(ctx, u, t.ID, "approval_rejected", map[string]any{"reason": err.Error()})
		e.Emit("error", map[string]any{"message": tr(st.Lang, "This approval is no longer valid (used, expired or the details changed). Please review again.", "این تایید دیگر معتبر نیست (استفاده‌شده، منقضی یا تغییرکرده). دوباره بررسی کنید.")})
		return
	}
	if req.ExecMode == "ui" || req.ExecMode == "api" {
		st.ExecMode = req.ExecMode
	}
	st.Approved, st.ApprovedBy = true, u.Scope.UserID
	a.audit(ctx, u, t.ID, "approved", map[string]any{"by": u.Me.Email, "payload_hash": recomputed, "exec_mode": st.ExecMode})
	a.save(ctx, t, st)
	if st.UndoOf != "" {
		a.executeUndo(ctx, u, t, st, e)
		return
	}
	a.execute(ctx, u, t, st, &tools.Authorization{PayloadHash: tools.PayloadHash(st.Proposal.Action, st.ArgsJSON), Mode: "human", ApprovedBy: u.Scope.UserID, TaskID: t.ID}, e)
}

// compile-time check that classify is used (proposal types live there)
var _ = classify.Proposal{}
