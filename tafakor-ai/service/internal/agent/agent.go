package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/approval"
	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/duplicates"
	"github.com/tafakorpardazan/tafakor-ai/internal/extract"
	"github.com/tafakorpardazan/tafakor-ai/internal/files"
	"github.com/tafakorpardazan/tafakor-ai/internal/learning"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/policy"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// User is the authenticated caller (validated against the accounting API).
type User struct {
	Me    *accounting.Me
	Cred  accounting.Credentials
	Scope store.Scope
}

type Agent struct {
	Store       store.Repository
	Acc         *accounting.Client
	Router      *provider.Router
	VisionModel string
	Tools       *tools.Registry
	Files       *files.Store
	Approvals   *approval.Service
	Learn       *learning.Service
	Knowledge   string
	EnvLockAuto bool
	Now         func() time.Time
}

var ErrNotFound = errors.New("task not found")
var ErrState = errors.New("the task is not in a state that allows this")

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Agent) env(u *User) tools.Env {
	return tools.Env{Acc: a.Acc, Cred: u.Cred, Me: u.Me, Files: a.Files}
}

func lang(u *User) string {
	if u.Me != nil && u.Me.Language == "en" {
		return "en"
	}
	return "fa"
}

func newTaskID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- persistence & audit ----------

func (a *Agent) load(ctx context.Context, u *User, id string) (*store.Task, *State, error) {
	t, err := a.Store.GetTask(ctx, u.Scope, id)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	var st State
	if err := json.Unmarshal(t.State, &st); err != nil {
		return nil, nil, err
	}
	if st.Answers == nil {
		st.Answers = map[string]string{}
	}
	return &t, &st, nil
}

func (a *Agent) save(ctx context.Context, t *store.Task, st *State) {
	t.Status = st.Stage
	b, _ := json.Marshal(st)
	t.State = b
	if err := a.Store.SaveTask(ctx, t); err != nil {
		slog.Error("save task", "err", err)
	}
}

func (a *Agent) audit(ctx context.Context, u *User, taskID, kind string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte(`{}`)
	}
	b = []byte(normalize.MaskSensitive(string(b)))
	if err := a.Store.AppendAudit(ctx, store.AuditEntry{CompanyID: u.Scope.CompanyID, UserID: u.Scope.UserID, TaskID: taskID, Kind: kind, Data: b}); err != nil {
		slog.Error("audit", "err", err)
	}
}

func (a *Agent) cost(ctx context.Context, u *User, taskID string, res provider.Result) {
	_ = a.Store.LogCost(ctx, store.CostEntry{TaskID: taskID, CompanyID: u.Scope.CompanyID, UserID: u.Scope.UserID, Model: res.Model,
		PromptTokens: res.Usage.PromptTokens, CompletionTokens: res.Usage.CompletionTokens, CostUSD: res.Usage.Cost})
}

func (a *Agent) fail(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, msg string, err error) {
	st.Stage = StageFailed
	st.Message = msg
	a.save(ctx, t, st)
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	a.audit(ctx, u, t.ID, "error", map[string]any{"message": msg, "detail": detail})
	e.Emit("error", map[string]any{"message": msg})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

func (a *Agent) stop(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, msg string) {
	st.Stage = StageStopped
	st.Message = msg
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "stopped", map[string]any{"message": msg})
	status(e, st.Lang, "stopped")
	e.Emit("message", map[string]any{"text": msg})
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// ---------- start ----------

type StartRequest struct {
	Message     string   `json:"message"`
	Uploads     []string `json:"uploads"`
	AskThisTime bool     `json:"ask_this_time"`
	ExecMode    string   `json:"exec_mode"`  // ui (default when the widget can drive the page) | api
	Transcript  bool     `json:"transcript"` // the text came from a (user-confirmed) voice transcript
	// ConversationID continues an existing conversation (empty = new conversation).
	ConversationID string `json:"conversation_id"`
	// pre is an interpretation already made for this message (explicit change of subject).
	pre *classify.Interpretation
}

// Start handles one user message. Inside an open operation it is a follow-up that is merged
// into the existing state; otherwise a new operation starts — with the conversation history.
func (a *Agent) Start(ctx context.Context, u *User, req StartRequest, e Emitter) string {
	var taskID string
	a.withConversation(ctx, u, req.ConversationID, e, func(c *conversation, e Emitter) {
		text := strings.TrimSpace(req.Message)
		c.add(ConvMessage{Role: "user", Kind: "text", Text: text})
		if t, st := a.activeOperation(ctx, u, c); t != nil && text != "" && len(req.Uploads) == 0 {
			taskID = t.ID
			if req.AskThisTime {
				st.AskThisTime = true
			}
			a.followUp(ctx, u, t, st, text, c, e)
			return
		}
		taskID = a.startTask(ctx, u, req, c, e)
	})
	return taskID
}

func (a *Agent) startTask(ctx context.Context, u *User, req StartRequest, c *conversation, e Emitter) string {
	st := &State{Stage: StageNew, Lang: lang(u), UserText: strings.TrimSpace(req.Message), Answers: map[string]string{}, AskThisTime: req.AskThisTime,
		ExecMode: "api"}
	if req.ExecMode == "ui" {
		st.ExecMode = "ui"
	}
	if len([]rune(st.UserText)) > 4000 {
		st.UserText = string([]rune(st.UserText)[:4000])
	}
	if c != nil {
		st.ConversationID = c.row.ID
	}
	t := &store.Task{ID: newTaskID(), CompanyID: u.Scope.CompanyID, UserID: u.Scope.UserID, Request: st.UserText}
	e.Emit("task", map[string]any{"task_id": t.ID})
	a.audit(ctx, u, t.ID, "request", map[string]any{"text": st.UserText, "uploads": req.Uploads, "voice": req.Transcript,
		"user": u.Me.Email, "ask_this_time": req.AskThisTime})

	// uploads → extraction
	var docs []extract.Document
	for _, id := range req.Uploads {
		if len(docs) >= 3 {
			break
		}
		m, err := a.Files.Meta(u.Scope.CompanyID, u.Scope.UserID, id)
		if err != nil || !files.DocumentTypes[m.ContentType] {
			a.fail(ctx, u, t, st, e, tr(st.Lang, "An attachment could not be found or is not a supported document.", "پیوست پیدا نشد یا نوع آن پشتیبانی نمی‌شود."), err)
			return t.ID
		}
		name, content, err := a.Files.Read(ctx, u.Scope.CompanyID, u.Scope.UserID, id)
		if err != nil {
			a.fail(ctx, u, t, st, e, tr(st.Lang, "An attachment could not be read.", "پیوست خوانده نشد."), err)
			return t.ID
		}
		sum := sha256.Sum256(content)
		st.Uploads = append(st.Uploads, Upload{ID: id, Name: name, ContentType: m.ContentType, SHA256: hex.EncodeToString(sum[:])})
		docs = append(docs, extract.Document{UploadID: id, Name: name, ContentType: m.ContentType, Content: content})
	}
	a.save(ctx, t, st)
	for _, d := range docs {
		status(e, st.Lang, "reading")
		ai, model := a.Router.For(provider.PurposeExtract, a.VisionModel)
		x, res, err := extract.Run(ctx, ai, model, d)
		a.cost(ctx, u, t.ID, res)
		if err != nil {
			a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
			return t.ID
		}
		st.Extractions = append(st.Extractions, x)
		st.Model = res.Model
		a.audit(ctx, u, t.ID, "extraction", map[string]any{"model": res.Model, "file": d.Name, "extraction": x})
		e.Emit("extraction", x)
	}
	if st.UserText == "" && len(st.Extractions) == 0 {
		a.fail(ctx, u, t, st, e, tr(st.Lang, "Please type a request or attach a document.", "لطفاً درخواستی بنویسید یا سندی پیوست کنید."), nil)
		return t.ID
	}

	// load company configuration (as the user)
	status(e, st.Lang, "understanding")
	env := a.env(u)
	cfg, err := tools.LoadCompanyConfig(ctx, env)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return t.ID
	}
	hints := a.hints(ctx, u)
	ai, model := a.Router.For(provider.PurposeClassify, "")
	var hist []classify.Turn
	if c != nil {
		hist = c.history(12)
		if n := len(hist); n > 0 && hist[n-1].Role == "user" && hist[n-1].Text == st.UserText {
			hist = hist[:n-1]
		}
	}
	it, res, err := req.pre, provider.Result{Model: st.Model}, error(nil)
	if it == nil {
		it, res, err = classify.Interpret(ctx, ai, model, classify.InterpretInput{UserText: st.UserText, History: hist, Extractions: st.Extractions, Config: cfg,
			Hints: hints, Knowledge: a.Knowledge, Today: a.today(), Lang: st.Lang})
		a.cost(ctx, u, t.ID, res)
	}
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return t.ID
	}
	st.Interp = it
	st.Model = res.Model
	if it.Intent == "register" {
		// the operation's structured state; later messages are merged into it
		st.Frame = classify.NewFrame(it, st.UserText)
		st.Interp.Event = st.Frame.Event
	}
	a.audit(ctx, u, t.ID, "interpretation", map[string]any{"model": res.Model, "interpretation": it})
	a.save(ctx, t, st)

	switch it.Intent {
	case "register":
		a.register(ctx, u, t, st, cfg, e)
	case "operate":
		a.startPlan(ctx, u, t, st, c, e)
	case "query", "navigate":
		if it.Intent == "query" && !fixedQueries[it.Query.Kind] {
			a.startPlan(ctx, u, t, st, c, e) // any other question: read the software and answer
			return t.ID
		}
		a.query(ctx, u, t, st, e)
	case "undo":
		a.pickTarget(ctx, u, t, st, e, "undo")
	case "attach":
		a.pickTarget(ctx, u, t, st, e, "attach")
	case "pdf":
		a.pickTarget(ctx, u, t, st, e, "pdf")
	default:
		a.chat(ctx, u, t, st, e)
	}
	return t.ID
}

func (a *Agent) today() string {
	return a.now().In(tehran()).Format("2006-01-02")
}

func tehran() *time.Location {
	if l, err := time.LoadLocation("Asia/Tehran"); err == nil {
		return l
	}
	return time.Local
}

// hints are company playbook rules + the user's learned rules, shown as suggestions.
func (a *Agent) hints(ctx context.Context, u *User) []classify.Hint {
	rules, err := a.Store.ListRules(ctx, u.Scope)
	if err != nil {
		return nil
	}
	var out []classify.Hint
	for _, r := range rules {
		if r.Status != "active" || len(out) >= 40 {
			continue
		}
		var res learning.Result
		_ = json.Unmarshal(r.Result, &res)
		out = append(out, classify.Hint{Kind: r.Kind + "/" + r.Scope, Pattern: strings.SplitN(r.Pattern, ":", 2)[len(strings.SplitN(r.Pattern, ":", 2))-1], Result: res.Name})
	}
	return out
}

// ---------- registration ----------

func (a *Agent) resolveContext(ctx context.Context, u *User, st *State, cfg *tools.CompanyConfig) (*classify.Context, error) {
	env := a.env(u)
	var parties []tools.Party
	var accounts []tools.Account
	var err error
	if u.Me.Can("parties.view") {
		if parties, err = tools.ListAllParties(ctx, env); err != nil {
			return nil, err
		}
	}
	if u.Me.Can("accounts.view") {
		var raw []byte
		if err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: "GET", Path: "/accounts", Query: url.Values{"postable": {"true"}, "active": {"true"}}}, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &accounts); err != nil {
			return nil, err
		}
	}
	aliases, _ := a.Store.ListAliases(ctx, u.Scope)
	userText := st.UserText
	if st.Frame != nil {
		userText = st.Frame.UserText()
	}
	rc := &classify.Context{Lang: st.Lang, Today: a.today(), UserText: userText, Interp: st.Interp, Extractions: st.Extractions,
		Config: cfg, Parties: parties, Accounts: accounts, Aliases: aliases, Learn: a.Learn, Scope: u.Scope, Answers: st.Answers, Frame: st.Frame}
	if st.Interp != nil && st.Interp.Event.Type == classify.EvInventoryIn {
		if u.Me.Can("products.view") {
			name := st.Interp.Event.ProductName
			if rc.Products, err = tools.ListProducts(ctx, env, name); err != nil {
				return nil, err
			}
		}
		if u.Me.Can("inventory.view") {
			if rc.Warehouses, err = tools.ListWarehouses(ctx, env); err != nil {
				return nil, err
			}
		}
	}
	return rc, nil
}

func (a *Agent) register(ctx context.Context, u *User, t *store.Task, st *State, cfg *tools.CompanyConfig, e Emitter) {
	status(e, st.Lang, "looking_up")
	if cfg == nil {
		var err error
		if cfg, err = tools.LoadCompanyConfig(ctx, a.env(u)); err != nil {
			a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
			return
		}
	}
	rc, err := a.resolveContext(ctx, u, st, cfg)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	p, err := classify.Resolve(ctx, rc)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	st.Proposal = p
	if tool, ok := a.Tools.Get(p.Action); ok && p.Action != "" {
		perms := tool.Permissions
		if p.Inventory != nil && p.Inventory.NewProduct != nil {
			perms = append(append([]string{}, perms...), "products.create")
		}
		for _, perm := range perms {
			if !u.Me.Can(perm) {
				a.audit(ctx, u, t.ID, "policy", map[string]any{"decision": "blocked", "missing_permission": perm})
				a.stop(ctx, u, t, st, e, tr(st.Lang, "You don't have permission to do this in the accounting app ("+perm+"), so I can't do it either.",
					"شما در نرم‌افزار حسابداری مجوز این کار را ندارید ("+perm+")، پس من هم نمی‌توانم انجام دهم."))
				return
			}
		}
	}
	unclassified := p.Question != nil && p.Question.Field == "event_type"
	if (p.StopCode != "" || p.Action == "create_invoice" || p.Action == "create_purchase" || unclassified) && st.Plan == nil {
		// a record the operation needs does not exist: plan it (create it first, then continue)
		a.audit(ctx, u, t.ID, "handover_to_planner", map[string]any{"reason": p.StopCode})
		a.startPlan(ctx, u, t, st, nil, e)
		return
	}
	if p.Stop != "" {
		a.stop(ctx, u, t, st, e, p.Stop)
		return
	}
	if p.Question != nil {
		st.Stage = StageQuestion
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "question", p.Question)
		status(e, st.Lang, "question")
		e.Emit("question", p.Question)
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
		return
	}
	// apply description/reference edits from review
	if v, ok := st.Overrides["description"]; ok {
		setText(p, "description", v)
	}
	if v, ok := st.Overrides["reference"]; ok {
		setText(p, "reference", v)
	}
	a.audit(ctx, u, t.ID, "proposal", map[string]any{"action": p.Action, "args": p.Args(), "display": p.Display, "flags": p.Flags, "used_rules": p.UsedRules})
	a.decide(ctx, u, t, st, cfg, e)
}

func setText(p *classify.Proposal, field, v string) {
	switch {
	case p.Tx != nil && field == "description":
		p.Tx.Description = v
	case p.Tx != nil && field == "reference":
		p.Tx.Reference = v
	case p.Expense != nil && field == "description":
		p.Expense.Description = v
	case p.Expense != nil && field == "reference":
		p.Expense.Reference = v
	}
	if field == "description" {
		p.Display.Description = v
	} else {
		p.Display.Reference = v
	}
}

// decide runs duplicate/period checks, the policy engine, and then either executes
// (auto), issues an approval (human) or stops (blocked).
func (a *Agent) decide(ctx context.Context, u *User, t *store.Task, st *State, cfg *tools.CompanyConfig, e Emitter) {
	p := st.Proposal
	status(e, st.Lang, "checking")
	args, _ := json.Marshal(p.Args())
	tool, ok := a.Tools.Get(p.Action)
	if !ok {
		a.fail(ctx, u, t, st, e, tr(st.Lang, "This action is not available.", "این عملیات در دسترس نیست."), nil)
		return
	}
	// validate arguments exactly as the tool will
	if err := validateArgs(p, u); err != nil {
		a.fail(ctx, u, t, st, e, tr(st.Lang, "The proposed values are not valid: ", "مقادیر پیشنهادی معتبر نیستند: ")+err.Error(), err)
		return
	}
	hasPerm := true
	for _, perm := range tool.Permissions {
		if !u.Me.Can(perm) {
			hasPerm = false
		}
	}
	dups := a.findDuplicates(ctx, u, p)
	st.Duplicates, _ = json.Marshal(dups)
	periodOK, periodReason := true, ""
	validation := false
	if len(cfg.FiscalYears) > 0 {
		periodOK, periodReason = tools.PeriodState(cfg.FiscalYears, dateOf(p))
	} else {
		validation = true // cannot verify fiscal periods (no permission to view them)
	}
	amount, _ := decimal.NewFromString(amountOf(p))
	settings, err := a.Store.GetUserSettings(ctx, u.Scope)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	cs, _ := a.Store.GetCompanySettings(ctx, u.Scope.CompanyID)
	autoToday, _ := a.Store.CountAutoToday(ctx, u.Scope, startOfDay(a.now()))
	// How the operation can be carried out. Derived from the operation's own contract
	// (catalog Spec) — not invented here — so "directly" and "show me on screen" are
	// only offered when the operation and the app's forms actually support them.
	classification, supportsDirect, supportsPreview := "", !policy.NeverAuto[p.Action], false
	if op, ok := catalog.Load().Get(p.Action); ok {
		spec := op.Spec()
		classification, supportsDirect, supportsPreview = op.Classification, spec.SupportsDirect, spec.SupportsUIPreview
	} else if p.Tx != nil || p.Expense != nil {
		// the built-in receipt/payment/expense operations have real forms
		supportsPreview = true
	}
	facts := policy.Facts{
		Action: p.Action, Amount: amount, Currency: strings.ToUpper(cfg.BaseCurrency),
		Duplicate: len(dups) > 0, Conflict: p.Flags.Conflict, Ambiguous: p.Flags.Ambiguous, LowConfidence: p.Flags.LowConfidence || anyLow(st.Extractions),
		PeriodBlocked: !periodOK, BudgetEvent: cfg.BudgetsActive && (p.Action == "create_expense" || p.Action == "create_payment") || budgetConfirm(p),
		ValidationIssue: validation || p.Flags.AssumedDate, NotRequested: p.Flags.NotRequested, FromDocumentText: p.Flags.FromDocumentText,
		ForeignCurrency: p.Flags.ForeignCurrency, LearnedSuggest: p.Flags.LearnedSuggest, HasPermission: hasPerm,
		Classification: classification, SupportsDirect: supportsDirect, SupportsPreview: supportsPreview,
		ExplicitDirect: st.ExecDirect || st.PendingDirect, ExplicitPreview: st.ExecPreview,
	}
	d := policy.Decide(policy.Input{Facts: facts, Settings: settings, CompanyLock: cs.LockAuto || a.EnvLockAuto, AutoToday: autoToday, AskThisTime: st.AskThisTime})
	st.Decision = &d
	st.ArgsJSON = args
	st.PayloadHash = payloadHash(p.Action, args, st.Uploads)
	a.audit(ctx, u, t.ID, "policy", map[string]any{"facts": facts, "decision": d, "period": periodReason, "switch": settings.AutoMode,
		"limits": map[string]any{"max_amount": settings.MaxAmount, "max_per_day": settings.MaxPerDay, "auto_actions": settings.AutoActions}})

	preview := map[string]any{"action": p.Action, "display": p.Display, "preview": p.Preview, "notes": p.Notes, "flags": p.Flags,
		"duplicates": dups, "decision": d, "reasons": reasonTexts(st.Lang, d.Reasons), "warnings": reasonTexts(st.Lang, d.Warnings), "period": periodReason}
	e.Emit("preview", preview)

	// "Show me on screen": open the real form, pre-filled, without registering anything.
	if d.Action == policy.ActionUIPreview {
		if plan, err := BuildPreviewPlan(st); err == nil {
			plan.TaskID = t.ID
			st.Stage = StageUIPreview
			st.PendingDirect = false
			a.save(ctx, t, st)
			a.audit(ctx, u, t.ID, "ui_preview", plan)
			e.Emit("ui_plan", plan)
			e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
			return
		}
		// The form cannot express this exact payload: fall back to a confirmation card.
		d.Action = policy.ActionConfirm
		st.Decision = &d
	}

	switch d.Action {
	case policy.ActionBlocked:
		a.stop(ctx, u, t, st, e, tr(st.Lang, "You don't have permission to do this in the accounting app, so I can't do it either.",
			"شما در نرم‌افزار حسابداری مجوز این کار را ندارید، پس من هم نمی‌توانم انجام دهم."))
	case policy.ActionDirect:
		st.Approved = true
		st.PendingDirect = false
		a.save(ctx, t, st)
		a.execute(ctx, u, t, st, &tools.Authorization{PayloadHash: tools.PayloadHash(p.Action, args), Mode: "auto", TaskID: t.ID}, e)
	default:
		// Remember the explicit "directly" instruction for the following turns of the
		// same operation, so filling in a missing field does not silently drop it.
		if st.ExecDirect {
			st.PendingDirect = true
		}
		tok, exp, err := a.Approvals.Issue(ctx, u.Scope, t.ID, st.PayloadHash)
		if err != nil {
			a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
			return
		}
		st.Stage = StageApproval
		a.save(ctx, t, st)
		status(e, st.Lang, "waiting")
		e.Emit("approval", map[string]any{"task_id": t.ID, "token": tok, "expires_at": exp, "payload_hash": st.PayloadHash,
			"reasons": reasonTexts(st.Lang, d.Reasons), "warnings": reasonTexts(st.Lang, d.Warnings),
			"action": d.Action, "duplicate": len(dups) > 0, "simulated": d.Simulated, "would_auto": d.WouldAuto,
			"can_preview": supportsPreview, "can_direct": supportsDirect})
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
	}
}

func budgetConfirm(p *classify.Proposal) bool {
	return (p.Tx != nil && p.Tx.BudgetConfirm) || (p.Expense != nil && p.Expense.BudgetConfirm)
}

func anyLow(xs []*extract.Extraction) bool {
	for _, x := range xs {
		for _, f := range x.LowFields {
			if f == "amount" || f == "date" || f == "reference" {
				return true
			}
		}
	}
	return false
}

func startOfDay(t time.Time) time.Time {
	l := tehran()
	t = t.In(l)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, l)
}

// payloadHash binds the approval to the tool, its canonical arguments and the attached files' hashes.
func payloadHash(action string, args json.RawMessage, ups []Upload) string {
	h := sha256.New()
	h.Write([]byte(tools.PayloadHash(action, args)))
	for _, u := range ups {
		h.Write([]byte{0})
		h.Write([]byte(u.SHA256))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dateOf(p *classify.Proposal) string {
	switch {
	case p.Tx != nil:
		return p.Tx.Date
	case p.Expense != nil:
		return p.Expense.Date
	case p.Invoice != nil:
		return p.Invoice.Date
	case p.Inventory != nil:
		return p.Inventory.Date
	}
	return ""
}

func amountOf(p *classify.Proposal) string {
	switch {
	case p.Tx != nil:
		return p.Tx.Amount
	case p.Expense != nil:
		return p.Expense.Amount
	}
	return p.Display.Amount
}

func validateArgs(p *classify.Proposal, u *User) error {
	dec := u.Me.Company.AmountDecimals
	switch {
	case p.Tx != nil:
		return p.Tx.Validate(dec)
	case p.Expense != nil:
		return p.Expense.Validate(dec)
	case p.Invoice != nil:
		if p.Invoice.PartyID <= 0 || len(p.Invoice.Items) == 0 {
			return fmt.Errorf("invoice incomplete")
		}
	case p.Inventory != nil:
		return p.Inventory.Validate(dec)
	}
	return nil
}

func reasonTexts(lang string, codes []string) []map[string]string {
	out := []map[string]string{}
	for _, c := range codes {
		t := policy.ReasonText[c]
		txt := t[0]
		if lang == "fa" {
			txt = t[1]
		}
		out = append(out, map[string]string{"code": c, "text": txt})
	}
	return out
}

func (a *Agent) findDuplicates(ctx context.Context, u *User, p *classify.Proposal) []duplicates.Match {
	env := a.env(u)
	date := dateOf(p)
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil
	}
	from, to := d.AddDate(0, 0, -7).Format("2006-01-02"), d.AddDate(0, 0, 7).Format("2006-01-02")
	amount, _ := decimal.NewFromString(amountOf(p))
	var cands []duplicates.Candidate
	var prop duplicates.Proposed
	switch {
	case p.Tx != nil:
		kind := strings.TrimPrefix(p.Action, "create_")
		args, _ := json.Marshal(map[string]any{"kind": kind, "from": from, "to": to, "limit": 50})
		res, err := a.Tools.Call(ctx, env, "search_transactions", args, nil)
		if err != nil {
			return nil
		}
		for _, x := range res.([]tools.Transaction) {
			c := duplicates.Candidate{Kind: x.Kind, ID: x.ID, Number: x.Number, Date: x.Date, Amount: x.Amount, Reference: x.Reference, AccountID: x.TreasuryAccountID, Status: x.Status}
			if x.PartyID != nil {
				c.PartyID = *x.PartyID
			}
			cands = append(cands, c)
		}
		prop = duplicates.Proposed{Kind: kind, Date: date, Amount: amount, Reference: p.Tx.Reference, AccountID: p.Tx.TreasuryAccountID}
		if p.Tx.PartyID != nil {
			prop.PartyID = *p.Tx.PartyID
		}
	case p.Expense != nil:
		args, _ := json.Marshal(map[string]any{"from": from, "to": to, "limit": 50})
		res, err := a.Tools.Call(ctx, env, "search_expenses", args, nil)
		if err != nil {
			return nil
		}
		for _, x := range res.([]tools.Expense) {
			c := duplicates.Candidate{Kind: "expense", ID: x.ID, Number: x.Number, Date: x.Date, Amount: x.Amount, Reference: x.Reference, AccountID: x.CategoryID, Status: x.Status}
			if x.PartyID != nil {
				c.PartyID = *x.PartyID
			}
			cands = append(cands, c)
		}
		prop = duplicates.Proposed{Kind: "expense", Date: date, Amount: amount, Reference: p.Expense.Reference, AccountID: p.Expense.CategoryID}
	default:
		return nil
	}
	return duplicates.Find(prop, cands)
}

// ---------- plain-language errors ----------

func (a *Agent) plain(lang string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, provider.ErrNotConfigured) || errors.Is(err, provider.ErrProxy) || errors.Is(err, provider.ErrAuth) || errors.Is(err, provider.ErrRejected) || errors.Is(err, provider.ErrCreditsInFlight) ||
		errors.Is(err, provider.ErrRateLimited) || errors.Is(err, provider.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		msg := provider.PlainMessage(err)
		if lang == "fa" && (errors.Is(err, provider.ErrCreditsInFlight) || strings.Contains(err.Error(), "HTTP 402")) {
			return "اعتبار حساب سرویس هوش مصنوعی کافی نیست؛ مدیر سیستم باید اعتبار را افزایش دهد. چیزی ثبت نشد."
		}
		if lang == "fa" {
			msg = map[bool]string{true: "ارتباط با سرویس هوش مصنوعی برقرار نشد (پروکسی شبکه پاسخ نمی‌دهد). چیزی ثبت نشد.", false: "سرویس هوش مصنوعی در دسترس نیست. چیزی ثبت نشد."}[errors.Is(err, provider.ErrProxy)]
		}
		return msg
	}
	if e, ok := accounting.AsAPIError(err); ok {
		return apiErrorText(lang, e)
	}
	if errors.Is(err, tools.ErrNoPermission) {
		return tr(lang, "You don't have permission for this action.", "شما مجوز این کار را ندارید.")
	}
	if errors.Is(err, tools.ErrInvalidInput) {
		return tr(lang, "Some values are not valid: ", "برخی مقادیر معتبر نیستند: ") + strings.TrimPrefix(err.Error(), tools.ErrInvalidInput.Error()+": ")
	}
	if strings.Contains(err.Error(), "accounting unreachable") {
		return tr(lang, "The accounting application is not reachable right now. Nothing was registered.", "نرم‌افزار حسابداری در دسترس نیست. چیزی ثبت نشد.")
	}
	return tr(lang, "Something went wrong. Nothing was registered.", "مشکلی پیش آمد. چیزی ثبت نشد.")
}

var apiCodes = map[string][2]string{
	"unauthorized":                 {"Your session has expired. Please sign in again; nothing was registered.", "نشست شما منقضی شده است. دوباره وارد شوید؛ چیزی ثبت نشد."},
	"forbidden":                    {"You don't have permission for this in the accounting app.", "در نرم‌افزار حسابداری مجوز این کار را ندارید."},
	"csrf":                         {"Security token expired. Refresh the page and try again.", "توکن امنیتی منقضی شده است. صفحه را تازه کنید."},
	"period_locked":                {"The accounting period of this date is locked.", "دوره مالی این تاریخ قفل است."},
	"fiscal_year_closed":           {"The fiscal year of this date is closed.", "سال مالی این تاریخ بسته است."},
	"no_fiscal_year":               {"No open fiscal year covers this date. Add one in Settings → Fiscal years.", "هیچ سال مالی بازی این تاریخ را پوشش نمی‌دهد."},
	"insufficient_funds":           {"The cash balance is not sufficient.", "موجودی صندوق کافی نیست."},
	"budget_confirmation_required": {"This posting exceeds a budget and needs your confirmation.", "این ثبت از بودجه فراتر می‌رود و نیاز به تایید شما دارد."},
	"budget_exceeded":              {"This posting exceeds a hard budget limit. An authorised user must override it in the app.", "این ثبت از سقف قطعی بودجه فراتر است؛ کاربر مجاز باید در برنامه آن را تایید کند."},
	"mapping_missing":              {"A posting rule has no account. Fix it in Settings → Posting rules.", "یکی از قواعد ثبت حساب ندارد. در تنظیمات → قواعد ثبت اصلاح کنید."},
	"validation":                   {"The accounting app rejected some values", "نرم‌افزار حسابداری برخی مقادیر را نپذیرفت"},
	"already_cancelled":            {"It is already cancelled.", "قبلاً لغو شده است."},
	"treasury_inactive":            {"The cash/bank account is disabled.", "حساب نقد/بانک غیرفعال است."},
}

func apiErrorText(lang string, e *accounting.APIError) string {
	t, ok := apiCodes[e.Code]
	if !ok {
		if e.Status == 401 {
			t = apiCodes["unauthorized"]
		} else if e.Status == 403 {
			t = apiCodes["forbidden"]
		} else {
			return tr(lang, "The accounting app refused this ("+e.Code+"). Nothing was registered.", "نرم‌افزار حسابداری این را نپذیرفت ("+e.Code+"). چیزی ثبت نشد.")
		}
	}
	msg := t[0]
	if lang == "fa" {
		msg = t[1]
	}
	if len(e.Fields) > 0 {
		var parts []string
		for f, c := range e.Fields {
			parts = append(parts, f+": "+c)
		}
		msg += " (" + strings.Join(parts, ", ") + ")"
	}
	return msg
}

// fixedQueries are answered by the built-in query handlers; everything else goes to the planner.
var fixedQueries = map[string]bool{"expenses": true, "transactions": true, "open_screen": true}
