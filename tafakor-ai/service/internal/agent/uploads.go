package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/extract"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// Attached files are the operation's reference documents (D44). A file the user sends
// with a request is never only read: once the record is registered, the ORIGINAL file is
// attached to that same record in the application (the record's own "Attachments"
// panel), verified by listing the record's attachments, and reported with a download
// link. Extraction only pre-fills the operation; it never replaces the file.

// AttachedFile is one original file, stored in the application on a record.
type AttachedFile struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"` // the application's own download link (the record's Attachments panel uses the same)
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
}

func attachmentURL(id int64) string { return fmt.Sprintf("/api/v1/attachments/%d/download", id) }

// masterEntities describe someone rather than an event: a file goes to them only when
// the operation registered no document (a scanned ID card for a new counterparty, yes;
// the invoice image of "create the supplier and register the purchase", no).
var masterEntities = map[string]bool{"party": true, "employee": true}

// referenceRecord picks the record a plan's attached files belong to: the first
// document the plan created or changed, else the first master record. nil when the plan
// changed no record the application can carry attachments on.
func referenceRecord(steps []PlanStep) *Artifact {
	var master *Artifact
	for _, s := range steps {
		if s.Status != "done" || s.Artifact == nil {
			continue
		}
		et := attachEntity(*s.Artifact)
		switch {
		case et == "":
		case !masterEntities[et]:
			a := *s.Artifact
			return &a
		case master == nil:
			a := *s.Artifact
			master = &a
		}
	}
	return master
}

// planWrote reports whether a plan changed anything (a pure lookup needs no attachment).
func planWrote(steps []PlanStep) bool {
	for _, s := range steps {
		if s.Status == "done" {
			if op, ok := catalog.Load().Get(s.Tool); ok && op.Kind != "read" {
				return true
			}
		}
	}
	return false
}

// attachedByPlan: uploads a plan step already sent to attachments.create itself (the
// user asked for that file on a specific record) — they are not attached a second time.
func attachedByPlan(steps []PlanStep) map[string]bool {
	out := map[string]bool{}
	for _, s := range steps {
		if id := attachStepUpload(s); id != "" && s.Status == "done" {
			out[id] = true
		}
	}
	return out
}

// attachStepUpload is the upload an attachments.create step sends, or "".
func attachStepUpload(s PlanStep) string {
	if s.Tool != "attachments.create" {
		return ""
	}
	var body map[string]any
	if json.Unmarshal(s.Args.Body, &body) == nil {
		id, _ := body["upload_id"].(string)
		return id
	}
	return ""
}

// isSourceAttach: the step attaches one of the user's own files of this task. It is
// not a form for the user to fill (a browser cannot pre-fill a file input): the system
// attaches the source document itself, on screen or not.
func isSourceAttach(st *State, s PlanStep) bool {
	id := attachStepUpload(s)
	if id == "" || s.Status == "done" {
		return false
	}
	for _, up := range st.Uploads {
		if up.ID == id {
			return true
		}
	}
	return false
}

// runSourceAttach carries out one such step through the same verified, idempotent
// attaching as the automatic link, and records the verified attachment on the step.
func (a *Agent) runSourceAttach(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, s *PlanStep, mode string) error {
	args, err := resolveRefs(st.Plan, s.Args)
	if err != nil {
		return err
	}
	var body map[string]any
	_ = json.Unmarshal(args.Body, &body)
	et := fmt.Sprint(body["entity_type"])
	eid := int64Of(body["entity_id"])
	if !tools.AttachEntities[et] || eid <= 0 {
		return fmt.Errorf("%s: %s %v", s.ID, et, body["entity_id"])
	}
	up := attachStepUpload(*s)
	skip := map[string]bool{}
	for _, x := range st.Uploads {
		if x.ID != up {
			skip[x.ID] = true
		}
	}
	files := a.linkUploads(ctx, u, t, st, e, et, eid, mode, skip)
	if len(files) == 0 {
		return fmt.Errorf("the file was not found on the record after attaching it")
	}
	s.Files, s.Status, s.Verified, s.Args = files, "done", true, args
	s.Result, _ = json.Marshal(map[string]any{"id": files[0].ID, "file_name": files[0].Name})
	e.Emit("tool", map[string]any{"tool": s.Tool, "step": s.ID, "ok": true})
	return nil
}

// runSourceAttaches runs every pending source-document step whose record exists now.
func (a *Agent) runSourceAttaches(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, mode string) {
	if st.Plan == nil {
		return
	}
	for i := range st.Plan.Steps {
		s := &st.Plan.Steps[i]
		if !isSourceAttach(st, *s) {
			continue
		}
		if _, err := resolveRefs(st.Plan, s.Args); err != nil {
			continue // its record is not created yet
		}
		if err := a.runSourceAttach(ctx, u, t, st, e, s, mode); err != nil {
			a.audit(ctx, u, t.ID, "attach_step_failed", map[string]any{"step": s.ID, "error": err.Error()})
		}
	}
}

// linkUploads attaches every original upload of the task to the record, once
// (idempotent per task, file and record), then lists the record's attachments and
// reports only what the application actually holds.
func (a *Agent) linkUploads(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, entityType string, entityID int64, mode string, skip map[string]bool) []AttachedFile {
	if len(st.Uploads) == 0 || entityID <= 0 || !tools.AttachEntities[entityType] {
		return nil
	}
	status(e, st.Lang, "attaching")
	env := a.env(u)
	want := map[int64]string{} // attachment id → upload name
	for _, up := range st.Uploads {
		if skip[up.ID] {
			continue
		}
		key := fmt.Sprintf("%s:attach:%s:%s:%d", t.ID, up.ID, entityType, entityID)
		if prev, ok, _ := a.Store.Recall(ctx, u.Scope, key); ok {
			var saved struct {
				ID int64 `json:"id"`
			}
			if json.Unmarshal(prev, &saved) == nil && saved.ID > 0 {
				want[saved.ID] = up.Name
				continue
			}
		}
		args, _ := json.Marshal(tools.AttachArgs{EntityType: entityType, EntityID: entityID, UploadID: up.ID})
		auth := &tools.Authorization{PayloadHash: tools.PayloadHash("attach_document", args), Mode: mode, ApprovedBy: st.ApprovedBy, TaskID: t.ID}
		out, err := a.Tools.Call(ctx, env, "attach_document", args, auth)
		var got struct {
			ID int64 `json:"id"`
		}
		if err == nil {
			b, _ := json.Marshal(out)
			_ = json.Unmarshal(b, &got)
			if got.ID > 0 {
				_ = a.Store.Remember(ctx, u.Scope, key, b)
				want[got.ID] = up.Name
			}
		}
		a.audit(ctx, u, t.ID, "tool_call", map[string]any{"tool": "attach_document", "file": up.Name, "upload_id": up.ID, "sha256": up.SHA256,
			"entity_type": entityType, "entity_id": entityID, "attachment_id": got.ID, "ok": err == nil, "error": errString(err)})
		if err != nil {
			e.Emit("message", map[string]any{"text": tr(st.Lang, "The record was registered but attaching the original file failed: ",
				"رکورد ثبت شد اما پیوست فایل اصلی ناموفق بود: ") + up.Name + " — " + a.plain(st.Lang, err)})
		}
	}
	// verify: the record's own attachment list must show each file
	var list []struct {
		ID       int64  `json:"id"`
		FileName string `json:"file_name"`
	}
	q := map[string][]string{"entity_type": {entityType}, "entity_id": {fmt.Sprint(entityID)}}
	if err := a.Acc.Do(ctx, u.Cred, accounting.Call{Method: http.MethodGet, Path: "/attachments", Query: q}, &list); err != nil {
		a.audit(ctx, u, t.ID, "attachments_unverified", map[string]any{"entity_type": entityType, "entity_id": entityID, "error": err.Error()})
		return nil
	}
	var files []AttachedFile
	for _, l := range list {
		if _, ok := want[l.ID]; ok {
			files = append(files, AttachedFile{ID: l.ID, Name: l.FileName, URL: attachmentURL(l.ID), EntityType: entityType, EntityID: entityID})
			delete(want, l.ID)
		}
	}
	for _, name := range want {
		e.Emit("message", map[string]any{"text": tr(st.Lang, "I could not find this file among the record's attachments after attaching it: ",
			"پس از پیوست، این فایل را در پیوست‌های رکورد پیدا نکردم: ") + name})
	}
	if len(files) > 0 {
		st.UploadsHandled = true
	}
	return files
}

// linkPlanUploads attaches a finished plan's uploads to its reference record. When the
// plan changed nothing that can carry attachments, the user is told plainly where the
// original file is kept instead of it silently disappearing.
func (a *Agent) linkPlanUploads(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, mode string) []AttachedFile {
	p := st.Plan
	if p == nil || len(st.Uploads) == 0 || !planWrote(p.Steps) {
		return nil
	}
	a.runSourceAttaches(ctx, u, t, st, e, mode) // any left (their record exists now)
	skip := attachedByPlan(p.Steps)
	if len(skip) > 0 {
		st.UploadsHandled = true
	}
	var stepFiles []AttachedFile
	for _, s := range p.Steps {
		stepFiles = append(stepFiles, s.Files...)
	}
	pending := 0
	for _, up := range st.Uploads {
		if !skip[up.ID] {
			pending++
		}
	}
	if pending == 0 {
		return stepFiles
	}
	ref := referenceRecord(p.Steps)
	if ref == nil {
		var names []string
		for _, up := range st.Uploads {
			if !skip[up.ID] {
				names = append(names, up.Name+" (/ai-api/files/"+up.ID+")")
			}
		}
		st.UploadsHandled = true // reported, kept in the assistant; not carried to a later record
		a.audit(ctx, u, t.ID, "attachment_unsupported", map[string]any{"files": names})
		e.Emit("message", map[string]any{"text": tr(st.Lang,
			"The application does not take attachments on this kind of record, so the original file stays in the assistant: ",
			"نرم‌افزار برای این نوع رکورد پیوست نمی‌پذیرد؛ فایل اصلی در دستیار نگه داشته شد: ") + strings.Join(names, "، ")})
		return stepFiles
	}
	return append(stepFiles, a.linkUploads(ctx, u, t, st, e, attachEntity(*ref), ref.ID, mode, skip)...)
}

// uploadNames lists the files that will be attached, for the preview the user approves.
func uploadNames(st *State) string {
	var names []string
	for _, up := range st.Uploads {
		names = append(names, up.Name)
	}
	return strings.Join(names, "، ")
}

// ---------- files sent before the operation ----------

// PendingUploads are files the user sent in a turn that registered nothing ("سند را
// خواندم، با آن چه کنم؟"). The next request of the conversation inherits them, so
// "ثبتش کن" registers the document AND attaches it. They are dropped after a
// registration links them, on cancel, when new files are sent, or after a few turns.
type PendingUploads struct {
	Uploads     []Upload              `json:"uploads"`
	Extractions []*extract.Extraction `json:"extractions,omitempty"`
	Turns       int                   `json:"turns,omitempty"`
}

const pendingUploadTurns = 2

// takePending hands the conversation's waiting files to a new task (once).
func (c *conversation) takePending() *PendingUploads {
	if c == nil || c.state.Pending == nil {
		return nil
	}
	p := c.state.Pending
	c.state.Pending = nil
	return p
}

// keepPending holds a finished task's files for the next request when nothing used them.
func (c *conversation) keepPending(st *State, turns int) {
	if c == nil || len(st.Uploads) == 0 || st.UploadsHandled || turns > pendingUploadTurns {
		return
	}
	switch st.Stage {
	case StageDone, StageFailed:
	default:
		return // still open (the task keeps them) or cancelled by the user
	}
	if st.Result != nil && st.Result.EntityID > 0 {
		return
	}
	c.state.Pending = &PendingUploads{Uploads: st.Uploads, Extractions: st.Extractions, Turns: turns}
}
