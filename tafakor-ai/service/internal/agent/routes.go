package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/files"
	"github.com/tafakorpardazan/tafakor-ai/internal/httpapi"
	"github.com/tafakorpardazan/tafakor-ai/internal/learning"
	"github.com/tafakorpardazan/tafakor-ai/internal/policy"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
	"github.com/tafakorpardazan/tafakor-ai/internal/speech"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// HTTP exposes the agent under /ai-api.
type HTTP struct {
	A          *Agent
	Speech     speech.SpeechProvider
	TTS        bool
	SpeechOn   bool
	MaxBody    int64
	TaskBudget time.Duration
}

func user(r *http.Request) *User {
	hu := httpapi.UserFrom(r.Context())
	return &User{Me: hu.Me, Cred: hu.Cred, Scope: hu.Scope}
}

func (h *HTTP) Register(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	handle := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, auth(fn)) }
	handle("GET /ai-api/me", h.me)
	handle("PUT /ai-api/settings", h.putSettings)
	handle("PUT /ai-api/company-settings", h.putCompanySettings)
	handle("POST /ai-api/uploads", h.upload)
	handle("GET /ai-api/files/{id}", h.file)
	handle("POST /ai-api/speech", h.speech)
	handle("POST /ai-api/tasks", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		var req StartRequest
		if err := httpapi.DecodeJSON(r, &req, 64<<10); err != nil {
			e.Emit("error", map[string]any{"message": "invalid request"})
			return
		}
		h.A.Start(ctx, u, req, e)
	}))
	handle("POST /ai-api/tasks/{id}/answer", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		var req AnswerRequest
		if err := httpapi.DecodeJSON(r, &req, 16<<10); err != nil {
			e.Emit("error", map[string]any{"message": "invalid request"})
			return
		}
		h.A.Answer(ctx, u, r.PathValue("id"), req, e)
	}))
	handle("POST /ai-api/tasks/{id}/edit", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		var req EditRequest
		if err := httpapi.DecodeJSON(r, &req, 16<<10); err != nil {
			e.Emit("error", map[string]any{"message": "invalid request"})
			return
		}
		h.A.Edit(ctx, u, r.PathValue("id"), req, e)
	}))
	handle("POST /ai-api/tasks/{id}/approve", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		var req ApproveRequest
		if err := httpapi.DecodeJSON(r, &req, 16<<10); err != nil {
			e.Emit("error", map[string]any{"message": "invalid request"})
			return
		}
		h.A.Approve(ctx, u, r.PathValue("id"), req, e)
	}))
	handle("POST /ai-api/tasks/{id}/undo", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		h.A.Undo(ctx, u, r.PathValue("id"), e)
	}))
	handle("POST /ai-api/tasks/{id}/pdf", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		h.A.PDF(ctx, u, r.PathValue("id"), e)
	}))
	handle("POST /ai-api/tasks/{id}/ui-result", h.sse(func(ctx context.Context, u *User, r *http.Request, e Emitter) {
		var req UIResult
		if err := httpapi.DecodeJSON(r, &req, 32<<10); err != nil {
			e.Emit("error", map[string]any{"message": "invalid request"})
			return
		}
		h.A.UIResult(ctx, u, r.PathValue("id"), req, e)
	}))
	handle("POST /ai-api/tasks/{id}/cancel", h.cancel)
	handle("POST /ai-api/tasks/{id}/remember", h.remember)
	handle("GET /ai-api/tasks/{id}", h.getTask)
	handle("GET /ai-api/tasks", h.listTasks)
	handle("GET /ai-api/activity", h.activity)
	handle("GET /ai-api/rules", h.listRules)
	handle("PUT /ai-api/rules/{id}", h.updateRule)
	handle("DELETE /ai-api/rules/{id}", h.deleteRule)
	handle("DELETE /ai-api/aliases/{id}", h.deleteAlias)
	handle("GET /ai-api/audit", h.audit)
	handle("POST /ai-api/ui/locate", h.locate)
}

// sse runs fn with a server-sent-event emitter and a bounded task budget.
func (h *HTTP) sse(fn func(ctx context.Context, u *User, r *http.Request, e Emitter)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := user(r)
		budget := h.TaskBudget
		if budget == 0 {
			budget = 5 * time.Minute
		}
		// Read the (small) JSON body BEFORE writing the stream headers: on HTTP/1.1 the
		// server may close the request body once the response has started.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			httpapi.WriteErr(w, 413, "too_large", "request too large")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		ctx, cancel := context.WithTimeout(r.Context(), budget)
		defer cancel()
		e := NewSSE(w)
		fn(ctx, u, r, e)
	}
}

func (h *HTTP) me(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	s, err := h.A.Store.GetUserSettings(r.Context(), u.Scope)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "settings unavailable")
		return
	}
	cs, _ := h.A.Store.GetCompanySettings(r.Context(), u.Scope.CompanyID)
	httpapi.WriteJSON(w, 200, map[string]any{
		"user":     map[string]any{"id": u.Me.ID, "name": u.Me.FullName, "language": u.Me.Language, "theme": u.Me.Theme},
		"company":  map[string]any{"id": u.Me.Company.ID, "name": u.Me.Company.Name, "currency": u.Me.Company.BaseCurrency, "lock_auto": cs.LockAuto || h.A.EnvLockAuto},
		"settings": s, "can_promote_rules": canPromote(u), "can_lock_company": canPromote(u),
		"features": map[string]any{"speech": h.SpeechOn, "tts": h.TTS},
	})
}

func canPromote(u *User) bool { return u.Me.Can("settings.edit") }

type settingsRequest struct {
	AutoMode      *bool             `json:"auto_mode"`
	RiskConfirmed bool              `json:"risk_confirmed"`
	MaxAmount     map[string]string `json:"max_amount"`
	MaxPerDay     *int              `json:"max_per_day"`
	AutoActions   []string          `json:"auto_actions"`
	Simulation    *bool             `json:"simulation"`
	VoiceReply    *bool             `json:"voice_reply"`
}

// putSettings is the ONLY way to change the approval switch and limits: an explicit
// user action from the widget. Nothing the model says can reach this handler.
func (h *HTTP) putSettings(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	var req settingsRequest
	if err := httpapi.DecodeJSON(r, &req, 16<<10); err != nil {
		httpapi.WriteErr(w, 400, "invalid", "invalid settings")
		return
	}
	s, err := h.A.Store.GetUserSettings(r.Context(), u.Scope)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "settings unavailable")
		return
	}
	before := s
	cs, _ := h.A.Store.GetCompanySettings(r.Context(), u.Scope.CompanyID)
	if req.AutoMode != nil {
		if err := policy.ApplySwitch(&s, *req.AutoMode, req.RiskConfirmed, cs.LockAuto || h.A.EnvLockAuto, time.Now().UTC()); err != nil {
			httpapi.WriteErr(w, 409, err.Error(), map[string]string{"company_lock": "Your company does not allow automatic registration.",
				"risk_confirmation_required": "Please confirm that you understand the risk of automatic registration."}[err.Error()])
			return
		}
	}
	if req.MaxAmount != nil {
		s.MaxAmount = req.MaxAmount
	}
	if req.MaxPerDay != nil {
		s.MaxPerDay = *req.MaxPerDay
	}
	if req.AutoActions != nil {
		s.AutoActions = req.AutoActions
	}
	if req.Simulation != nil {
		s.Simulation = *req.Simulation
	}
	if req.VoiceReply != nil {
		s.VoiceReply = *req.VoiceReply
	}
	if err := policy.ValidateSettings(&s); err != nil {
		httpapi.WriteErr(w, 422, "invalid", err.Error())
		return
	}
	if err := h.A.Store.SaveUserSettings(r.Context(), s); err != nil {
		httpapi.WriteErr(w, 500, "storage", "could not save")
		return
	}
	h.A.audit(r.Context(), u, "", "settings_changed", map[string]any{"before": before, "after": s})
	httpapi.WriteJSON(w, 200, s)
}

func (h *HTTP) putCompanySettings(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	if !canPromote(u) {
		httpapi.WriteErr(w, 403, "forbidden", "You need the settings.edit permission.")
		return
	}
	var req struct {
		LockAuto bool `json:"lock_auto"`
	}
	if err := httpapi.DecodeJSON(r, &req, 4<<10); err != nil {
		httpapi.WriteErr(w, 400, "invalid", "invalid")
		return
	}
	cs := store.CompanySettings{CompanyID: u.Scope.CompanyID, LockAuto: req.LockAuto}
	if err := h.A.Store.SaveCompanySettings(r.Context(), cs); err != nil {
		httpapi.WriteErr(w, 500, "storage", "could not save")
		return
	}
	h.A.audit(r.Context(), u, "", "company_settings_changed", cs)
	httpapi.WriteJSON(w, 200, cs)
}

func (h *HTTP) upload(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	r.Body = http.MaxBytesReader(w, r.Body, h.A.Files.MaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		httpapi.WriteErr(w, 413, "file_too_large", files.ErrTooLarge.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpapi.WriteErr(w, 400, "file_required", "A file is required.")
		return
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, h.A.Files.MaxBytes+1))
	if err != nil {
		httpapi.WriteErr(w, 400, "read", "Could not read the file.")
		return
	}
	ct, err := h.A.Files.Validate(content, files.DocumentTypes)
	if err != nil {
		code := "invalid_file_type"
		if errors.Is(err, files.ErrTooLarge) {
			code = "file_too_large"
		}
		httpapi.WriteErr(w, 422, code, err.Error())
		return
	}
	id, err := h.A.Files.Save(r.Context(), u.Scope.CompanyID, u.Scope.UserID, hdr.Filename, ct, content)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "Could not store the file.")
		return
	}
	h.A.audit(r.Context(), u, "", "upload", map[string]any{"id": id, "name": hdr.Filename, "type": ct, "size": len(content)})
	httpapi.WriteJSON(w, 201, map[string]any{"id": id, "name": hdr.Filename, "content_type": ct, "size": len(content)})
}

func (h *HTTP) file(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	m, err := h.A.Files.Meta(u.Scope.CompanyID, u.Scope.UserID, r.PathValue("id"))
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "File not found.")
		return
	}
	name, content, err := h.A.Files.Read(r.Context(), u.Scope.CompanyID, u.Scope.UserID, m.ID)
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "File not found.")
		return
	}
	w.Header().Set("Content-Type", m.ContentType)
	w.Header().Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	_, _ = w.Write(content)
}

func (h *HTTP) speech(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	if h.Speech == nil || !h.Speech.Enabled() {
		httpapi.WriteErr(w, 503, "speech_disabled", "Voice input is not configured on this server.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	audio, err := io.ReadAll(r.Body)
	if err != nil || len(audio) == 0 {
		httpapi.WriteErr(w, 413, "audio", "The recording is empty or too long.")
		return
	}
	ct := files.Sniff(audio)
	if !files.AudioTypes[ct] {
		httpapi.WriteErr(w, 422, "audio_type", "Unsupported audio format.")
		return
	}
	lang := "fa"
	if u.Me.Language == "en" {
		lang = "en"
	}
	if q := r.URL.Query().Get("lang"); q == "fa" || q == "en" {
		lang = q
	}
	text, err := h.Speech.Transcribe(r.Context(), audio, ct, lang)
	if err != nil {
		httpapi.WriteErr(w, 502, "speech_failed", provider.PlainMessage(err))
		return
	}
	aliases, _ := h.A.Store.ListAliases(r.Context(), u.Scope)
	var corr []speech.Correction
	for _, a := range aliases {
		if a.Kind == "asr" || a.Kind == "party" {
			corr = append(corr, speech.Correction{From: a.Alias, To: a.TargetName})
		}
	}
	norm := speech.Normalize(text, corr)
	h.A.audit(r.Context(), u, "", "transcript", map[string]any{"chars": len(text)})
	httpapi.WriteJSON(w, 200, speech.Transcript{Text: text, Normalized: norm})
}

func (h *HTTP) cancel(w http.ResponseWriter, r *http.Request) {
	if err := h.A.Cancel(r.Context(), user(r), r.PathValue("id")); err != nil {
		httpapi.WriteErr(w, 409, "state", err.Error())
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (h *HTTP) remember(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OfferID string `json:"offer_id"`
	}
	if err := httpapi.DecodeJSON(r, &req, 4<<10); err != nil {
		httpapi.WriteErr(w, 400, "invalid", "invalid")
		return
	}
	if err := h.A.Remember(r.Context(), user(r), r.PathValue("id"), req.OfferID); err != nil {
		httpapi.WriteErr(w, 404, "not_found", "offer not found")
		return
	}
	httpapi.WriteJSON(w, 200, map[string]any{"ok": true})
}

// taskView strips internal fields (approval tokens are never stored; extraction keeps masked data only).
func taskView(t store.Task) map[string]any {
	var st State
	_ = json.Unmarshal(t.State, &st)
	return map[string]any{"id": t.ID, "status": t.Status, "request": t.Request, "created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
		"stage": st.Stage, "proposal": st.Proposal, "decision": st.Decision, "result": st.Result, "message": st.Message, "offers": st.Offers,
		"payload_hash": st.PayloadHash, "ui_plan": uiPlanIfRunning(&st)}
}

func (h *HTTP) getTask(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	t, err := h.A.Store.GetTask(r.Context(), u.Scope, r.PathValue("id"))
	if err != nil {
		httpapi.WriteErr(w, 404, "not_found", "Task not found.")
		return
	}
	httpapi.WriteJSON(w, 200, taskView(t))
}

func (h *HTTP) listTasks(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	ts, err := h.A.Store.ListTasks(r.Context(), u.Scope, 30)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "unavailable")
		return
	}
	out := []map[string]any{}
	for _, t := range ts {
		out = append(out, taskView(t))
	}
	httpapi.WriteJSON(w, 200, out)
}

// activity lists registrations made through the assistant (with Undo availability).
func (h *HTTP) activity(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	ts, err := h.A.Store.ListTasks(r.Context(), u.Scope, 100)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "unavailable")
		return
	}
	out := []map[string]any{}
	for _, t := range ts {
		var st State
		_ = json.Unmarshal(t.State, &st)
		if st.Result == nil {
			continue
		}
		reason := ""
		if st.Decision != nil && st.Result.Mode == "human" && len(st.Decision.Reasons) > 0 {
			reason = reasonTexts(st.Lang, st.Decision.Reasons[:1])[0]["text"]
		}
		out = append(out, map[string]any{"task_id": t.ID, "when": t.UpdatedAt, "request": t.Request, "result": st.Result, "summary": summary(&st),
			"undo_available": st.Result.Verified && !st.Result.Undone && st.UndoOf == "" && (st.Result.EntityType == "treasury_transaction" || st.Result.EntityType == "expense"),
			"mode":           st.Result.Mode, "reason": reason, "is_undo": st.UndoOf != ""})
	}
	since := time.Now().AddDate(0, 0, -30)
	cost, _ := h.A.Store.CostSummary(r.Context(), u.Scope, since)
	httpapi.WriteJSON(w, 200, map[string]any{"items": out, "cost_30d": cost})
}

func (h *HTTP) listRules(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	rules, err := h.A.Store.ListRules(r.Context(), u.Scope)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "unavailable")
		return
	}
	aliases, _ := h.A.Store.ListAliases(r.Context(), u.Scope)
	views := []map[string]any{}
	for _, rl := range rules {
		var res learning.Result
		_ = json.Unmarshal(rl.Result, &res)
		stage := "suggest"
		if rl.TimesConfirmed >= learning.TrustApply {
			stage = "apply"
		}
		views = append(views, map[string]any{"id": rl.ID, "kind": rl.Kind, "pattern": strings.SplitN(rl.Pattern, ":", 2)[1], "result": res, "label": rl.Label,
			"scope": rl.Scope, "times_confirmed": rl.TimesConfirmed, "stage": stage, "source": rl.Source, "status": rl.Status,
			"created_at": rl.CreatedAt, "updated_at": rl.UpdatedAt, "mine": rl.UserID == u.Scope.UserID})
	}
	httpapi.WriteJSON(w, 200, map[string]any{"rules": views, "aliases": aliases, "can_promote": canPromote(u)})
}

func (h *HTTP) updateRule(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Scope  string `json:"scope"`
		Status string `json:"status"`
		Label  string `json:"label"`
	}
	if err := httpapi.DecodeJSON(r, &req, 4<<10); err != nil {
		httpapi.WriteErr(w, 400, "invalid", "invalid")
		return
	}
	rl, err := h.A.Learn.Update(r.Context(), u.Scope, id, req.Scope, req.Status, req.Label, canPromote(u))
	if err != nil {
		httpapi.WriteErr(w, 403, "rule", err.Error())
		return
	}
	h.A.audit(r.Context(), u, "", "rule_updated", map[string]any{"rule_id": id, "scope": rl.Scope, "status": rl.Status})
	httpapi.WriteJSON(w, 200, rl)
}

func (h *HTTP) deleteRule(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.A.Learn.Delete(r.Context(), u.Scope, id, canPromote(u)); err != nil {
		httpapi.WriteErr(w, 403, "rule", err.Error())
		return
	}
	h.A.audit(r.Context(), u, "", "rule_deleted", map[string]any{"rule_id": id})
	httpapi.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (h *HTTP) deleteAlias(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := h.A.Store.DeleteAlias(r.Context(), u.Scope, id); err != nil {
		httpapi.WriteErr(w, 404, "not_found", "not found")
		return
	}
	h.A.audit(r.Context(), u, "", "alias_deleted", map[string]any{"alias_id": id})
	httpapi.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (h *HTTP) audit(w http.ResponseWriter, r *http.Request) {
	u := user(r)
	items, err := h.A.Store.ListAudit(r.Context(), u.Scope, r.URL.Query().Get("task_id"), 300)
	if err != nil {
		httpapi.WriteErr(w, 500, "storage", "unavailable")
		return
	}
	httpapi.WriteJSON(w, 200, items)
}
