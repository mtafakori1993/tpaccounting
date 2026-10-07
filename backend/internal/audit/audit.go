// Package audit records who changed what, and exposes the audit trail.
package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

// Record writes an audit entry using the same connection/transaction as the change itself,
// so the log can never disagree with the data.
func Record(ctx context.Context, q db.DBTX, action, entity string, entityID int64, oldValue, newValue any) error {
	p := identity.From(ctx)
	m := identity.MetaFrom(ctx)
	var userID, companyID *int64
	if p != nil {
		userID, companyID = &p.UserID, &p.CompanyID
	}
	var eid *int64
	if entityID > 0 {
		eid = &entityID
	}
	_, err := q.Exec(ctx, `
		INSERT INTO audit_logs (company_id, user_id, action, entity, entity_id, ip, user_agent, old_value, new_value)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		companyID, userID, action, entity, eid, m.IP, truncate(m.UserAgent, 300), toJSON(oldValue), toJSON(newValue))
	return err
}

func toJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return nil
	}
	return b
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type Entry struct {
	ID        int64           `json:"id"`
	UserID    *int64          `json:"user_id"`
	UserName  *string         `json:"user_name"`
	Action    string          `json:"action"`
	Entity    string          `json:"entity"`
	EntityID  *int64          `json:"entity_id"`
	IP        string          `json:"ip"`
	OldValue  json.RawMessage `json:"old_value"`
	NewValue  json.RawMessage `json:"new_value"`
	CreatedAt time.Time       `json:"created_at"`
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	lp := httpx.ParseList(r, "created_at", true)
	f := db.NewFilter("a.company_id = $1", p.CompanyID)
	add := f.Add
	qv := r.URL.Query()
	if v := qv.Get("entity"); v != "" {
		add("a.entity = $N", v)
	}
	if v := httpx.QueryInt64(r, "entity_id"); v > 0 {
		add("a.entity_id = $N", v)
	}
	if v := httpx.QueryInt64(r, "user_id"); v > 0 {
		add("a.user_id = $N", v)
	}
	if v := qv.Get("action"); v != "" {
		add("a.action = $N", v)
	}
	if from, err := httpx.QueryDate(r, "from"); err != nil {
		return err
	} else if !from.IsZero() {
		add("a.created_at >= $N::date", from)
	}
	if to, err := httpx.QueryDate(r, "to"); err != nil {
		return err
	} else if !to.IsZero() {
		add("a.created_at < ($N::date + 1)", to)
	}
	var total int
	if err := h.Pool.QueryRow(r.Context(), `SELECT count(*) FROM audit_logs a WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return err
	}
	order := lp.OrderBy(map[string]string{"created_at": "a.created_at", "entity": "a.entity", "action": "a.action"}, "a.created_at")
	rows, err := h.Pool.Query(r.Context(), `
		SELECT a.id, a.user_id, u.full_name, a.action, a.entity, a.entity_id, a.ip,
		       COALESCE(a.old_value, 'null'::jsonb), COALESCE(a.new_value, 'null'::jsonb), a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id
		WHERE `+f.Where()+` ORDER BY `+order+`, a.id DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var items []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserName, &e.Action, &e.Entity, &e.EntityID, &e.IP, &e.OldValue, &e.NewValue, &e.CreatedAt); err != nil {
			return err
		}
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, httpx.NewPage(items, total, lp))
	return nil
}
