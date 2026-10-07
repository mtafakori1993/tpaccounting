// Package attachments stores files attached to documents and parties through
// the storage abstraction.
package attachments

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/storage"
)

type Attachment struct {
	ID          int64     `json:"id"`
	EntityType  string    `json:"entity_type"`
	EntityID    int64     `json:"entity_id"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	UploadedBy  *string   `json:"uploaded_by"`
	CreatedAt   time.Time `json:"created_at"`
}

// entityTables maps attachable entity types to their table.
var entityTables = map[string]string{
	"journal_entry": "journal_entries", "invoice": "invoices", "expense": "expenses", "party": "parties",
	"treasury_transaction": "treasury_transactions", "stock_document": "stock_documents", "check": "checks",
	"employee": "employees", "payroll_period": "payroll_periods", "budget": "budgets",
}

var allowedExt = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true, ".txt": true, ".csv": true,
	".xlsx": true, ".xls": true, ".docx": true, ".doc": true, ".zip": true,
}

type Handler struct {
	Pool     *pgxpool.Pool
	Storage  storage.Storage
	MaxBytes int64
}

func (h *Handler) Routes(r chi.Router) {
	r.Get("/", httpx.Handler(h.list))
	r.Post("/", httpx.Handler(h.upload))
	r.Get("/{id}/download", httpx.Handler(h.download))
	r.Delete("/{id}", httpx.Handler(h.delete))
}

// module returns the permission module that governs an entity.
func module(ctx context.Context, q db.DBTX, entityType string, entityID int64) (string, error) {
	switch entityType {
	case "journal_entry":
		return "journal", nil
	case "expense":
		return "expenses", nil
	case "party":
		return "parties", nil
	case "treasury_transaction":
		return "treasury", nil
	case "stock_document":
		return "inventory", nil
	case "check":
		return "checks", nil
	case "employee", "payroll_period":
		return "payroll", nil
	case "budget":
		return "budgets", nil
	case "invoice":
		var side string
		if err := q.QueryRow(ctx, `SELECT side FROM invoices WHERE id = $1`, entityID).Scan(&side); err != nil {
			return "", apperr.NotFound("invoice")
		}
		if side == "sales" {
			return "sales", nil
		}
		return "purchases", nil
	}
	return "", apperr.BadRequest("invalid_entity", "unsupported entity type")
}

// authorize checks the entity exists in the company and the user may perform action on it.
func (h *Handler) authorize(ctx context.Context, entityType string, entityID int64, action string) error {
	table, ok := entityTables[entityType]
	if !ok {
		return apperr.BadRequest("invalid_entity", "unsupported entity type")
	}
	var exists bool
	if err := h.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1 AND company_id = $2)`, entityID, identity.From(ctx).CompanyID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return apperr.NotFound(entityType)
	}
	mod, err := module(ctx, h.Pool, entityType, entityID)
	if err != nil {
		return err
	}
	return identity.Check(ctx, mod+"."+action)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	et := r.URL.Query().Get("entity_type")
	eid := httpx.QueryInt64(r, "entity_id")
	if err := h.authorize(r.Context(), et, eid, "view"); err != nil {
		return err
	}
	rows, err := h.Pool.Query(r.Context(), `SELECT a.id, a.entity_type, a.entity_id, a.file_name, a.content_type, a.size_bytes, u.full_name, a.created_at
		FROM attachments a LEFT JOIN users u ON u.id = a.uploaded_by
		WHERE a.company_id = $1 AND a.entity_type = $2 AND a.entity_id = $3 ORDER BY a.id`, identity.From(r.Context()).CompanyID, et, eid)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.EntityType, &a.EntityID, &a.FileName, &a.ContentType, &a.SizeBytes, &a.UploadedBy, &a.CreatedAt); err != nil {
			return err
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, h.MaxBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return apperr.Rule("file_too_large", "the file is too large").WithParams("max_mb", h.MaxBytes>>20)
	}
	et := r.FormValue("entity_type")
	eid, _ := strconv.ParseInt(r.FormValue("entity_id"), 10, 64)
	if err := h.authorize(r.Context(), et, eid, "edit"); err != nil {
		return err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return apperr.BadRequest("file_required", "a file is required")
	}
	defer file.Close()
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedExt[ext] {
		return apperr.Rule("invalid_file_type", "this file type is not allowed")
	}
	if len(name) > 200 {
		name = name[len(name)-200:]
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	ctype := http.DetectContentType(head[:n])
	if strings.HasPrefix(ctype, "text/html") || strings.Contains(ctype, "javascript") {
		return apperr.Rule("invalid_file_type", "this file type is not allowed")
	}
	if byExt := mime.TypeByExtension(ext); byExt != "" && (ctype == "application/octet-stream" || ctype == "application/zip" || strings.HasPrefix(ctype, "text/plain")) {
		ctype = byExt
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	key := storage.NewKey("attachments")
	size, err := h.Storage.Put(r.Context(), key, file, h.MaxBytes)
	if err == storage.ErrTooLarge {
		return apperr.Rule("file_too_large", "the file is too large").WithParams("max_mb", h.MaxBytes>>20)
	}
	if err != nil {
		return err
	}
	var a Attachment
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO attachments (company_id, entity_type, entity_id, file_name, content_type, size_bytes, storage_key, uploaded_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id, created_at`, p.CompanyID, et, eid, name, ctype, size, key, p.UserID).Scan(&a.ID, &a.CreatedAt); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "attach", et, eid, nil, map[string]any{"attachment_id": a.ID, "file_name": name})
	})
	if err != nil {
		_ = h.Storage.Delete(r.Context(), key)
		return err
	}
	a.EntityType, a.EntityID, a.FileName, a.ContentType, a.SizeBytes = et, eid, name, ctype, size
	a.UploadedBy = &p.FullName
	httpx.Created(w, a)
	return nil
}

func (h *Handler) load(ctx context.Context, id int64) (Attachment, string, error) {
	var a Attachment
	var key string
	err := h.Pool.QueryRow(ctx, `SELECT id, entity_type, entity_id, file_name, content_type, size_bytes, storage_key FROM attachments WHERE id = $1 AND company_id = $2`,
		id, identity.From(ctx).CompanyID).Scan(&a.ID, &a.EntityType, &a.EntityID, &a.FileName, &a.ContentType, &a.SizeBytes, &key)
	if db.IsNoRows(err) {
		return a, "", apperr.NotFound("attachment")
	}
	return a, key, err
}

func (h *Handler) download(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	a, key, err := h.load(r.Context(), id)
	if err != nil {
		return err
	}
	if err := h.authorize(r.Context(), a.EntityType, a.EntityID, "view"); err != nil {
		return err
	}
	f, err := h.Storage.Open(r.Context(), key)
	if err != nil {
		return apperr.NotFound("attachment")
	}
	defer f.Close()
	disposition := "attachment"
	if strings.HasPrefix(a.ContentType, "image/") || a.ContentType == "application/pdf" {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", a.ContentType)
	w.Header().Set("Content-Length", fmt.Sprint(a.SizeBytes))
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.FileName}))
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	_, err = io.Copy(w, f)
	return err
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	a, key, err := h.load(r.Context(), id)
	if err != nil {
		return err
	}
	if err := h.authorize(r.Context(), a.EntityType, a.EntityID, "edit"); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `DELETE FROM attachments WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "detach", a.EntityType, a.EntityID, map[string]any{"attachment_id": id, "file_name": a.FileName}, nil)
	})
	if err != nil {
		return err
	}
	_ = h.Storage.Delete(r.Context(), key)
	httpx.NoContent(w)
	return nil
}
