// Package users manages user accounts, roles and role permissions.
package users

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/auth"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

type User struct {
	ID          int64      `json:"id"`
	Email       string     `json:"email"`
	FullName    string     `json:"full_name"`
	RoleID      int64      `json:"role_id"`
	RoleName    string     `json:"role_name"`
	RoleNameEn  string     `json:"role_name_en"`
	IsActive    bool       `json:"is_active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type Input struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	Password string `json:"password"`
	RoleID   int64  `json:"role_id"`
	IsActive *bool  `json:"is_active"`
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func get(ctx context.Context, q db.DBTX, companyID, id int64) (*User, error) {
	var u User
	err := q.QueryRow(ctx, `SELECT u.id, u.email, u.full_name, u.role_id, r.name, r.name_en, u.is_active, u.last_login_at, u.created_at
		FROM users u JOIN roles r ON r.id = u.role_id WHERE u.company_id = $1 AND u.id = $2`, companyID, id).
		Scan(&u.ID, &u.Email, &u.FullName, &u.RoleID, &u.RoleName, &u.RoleNameEn, &u.IsActive, &u.LastLoginAt, &u.CreatedAt)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("user")
	}
	return &u, err
}

// Create inserts a user with a hashed password (also used by setup).
func Create(ctx context.Context, q db.DBTX, companyID int64, in Input) (int64, error) {
	in.Email, in.FullName = strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.FullName)
	v := apperr.NewValidator()
	v.Required("email", in.Email)
	v.Check(in.Email == "" || emailRe.MatchString(in.Email), "email", "invalid_email")
	v.Required("full_name", in.FullName)
	v.MaxLen("full_name", in.FullName, 150)
	auth.ValidatePassword(v, "password", in.Password)
	v.RequiredID("role_id", in.RoleID)
	if err := v.Err(); err != nil {
		return 0, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return 0, err
	}
	var id int64
	err = q.QueryRow(ctx, `INSERT INTO users (company_id, email, full_name, password_hash, role_id) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		companyID, in.Email, in.FullName, hash, in.RoleID).Scan(&id)
	return id, err
}

type Handler struct{ Pool *pgxpool.Pool }

func (h *Handler) UserRoutes(r chi.Router) {
	r.With(identity.Require("users.view")).Get("/", httpx.Handler(h.list))
	r.With(identity.Require("users.create")).Post("/", httpx.Handler(h.create))
	r.With(identity.Require("users.edit")).Put("/{id}", httpx.Handler(h.update))
}

func (h *Handler) RoleRoutes(r chi.Router) {
	r.With(identity.Require("roles.view")).Get("/", httpx.Handler(h.listRoles))
	r.With(identity.Require("roles.view")).Get("/permissions", httpx.Handler(h.listPermissions))
	r.With(identity.Require("roles.create")).Post("/", httpx.Handler(h.saveRole))
	r.With(identity.Require("roles.edit")).Put("/{id}", httpx.Handler(h.saveRole))
	r.With(identity.Require("roles.delete")).Delete("/{id}", httpx.Handler(h.deleteRole))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `SELECT u.id, u.email, u.full_name, u.role_id, r.name, r.name_en, u.is_active, u.last_login_at, u.created_at
		FROM users u JOIN roles r ON r.id = u.role_id WHERE u.company_id = $1 ORDER BY u.full_name`, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.FullName, &u.RoleID, &u.RoleName, &u.RoleNameEn, &u.IsActive, &u.LastLoginAt, &u.CreatedAt); err != nil {
			return err
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = Create(r.Context(), tx, p.CompanyID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "user", id, nil, map[string]any{"email": in.Email, "full_name": in.FullName, "role_id": in.RoleID})
	})
	if err != nil {
		return err
	}
	u, err := get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, u)
	return nil
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in Input
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Email, in.FullName = strings.ToLower(strings.TrimSpace(in.Email)), strings.TrimSpace(in.FullName)
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := get(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		active := before.IsActive
		if in.IsActive != nil {
			active = *in.IsActive
		}
		v := apperr.NewValidator()
		v.Required("email", in.Email)
		v.Check(in.Email == "" || emailRe.MatchString(in.Email), "email", "invalid_email")
		v.Required("full_name", in.FullName)
		v.RequiredID("role_id", in.RoleID)
		if in.Password != "" {
			auth.ValidatePassword(v, "password", in.Password)
		}
		v.Check(id != p.UserID || active, "is_active", "cannot_disable_self")
		if err := v.Err(); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE users SET email = $3, full_name = $4, role_id = $5, is_active = $6, updated_at = now()
			WHERE company_id = $1 AND id = $2`, p.CompanyID, id, in.Email, in.FullName, in.RoleID, active); err != nil {
			return err
		}
		if in.Password != "" {
			hash, err := auth.HashPassword(in.Password)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), `UPDATE users SET password_hash = $2 WHERE id = $1`, id, hash); err != nil {
				return err
			}
		}
		if !active || in.Password != "" {
			if _, err := tx.Exec(r.Context(), `UPDATE sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL AND id <> $2`, id, p.SessionID); err != nil {
				return err
			}
		}
		// Never leave the company without an active administrator.
		var admins int
		if err := tx.QueryRow(r.Context(), `SELECT count(*) FROM users u JOIN roles ro ON ro.id = u.role_id
			WHERE u.company_id = $1 AND u.is_active AND ro.code = 'admin'`, p.CompanyID).Scan(&admins); err != nil {
			return err
		}
		if admins == 0 {
			return apperr.Rule("last_admin", "at least one active administrator is required")
		}
		return audit.Record(r.Context(), tx, "update", "user", id, before, map[string]any{"email": in.Email, "full_name": in.FullName,
			"role_id": in.RoleID, "is_active": active, "password_changed": in.Password != ""})
	})
	if err != nil {
		return err
	}
	u, err := get(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, u)
	return nil
}

type Role struct {
	ID          int64    `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	NameEn      string   `json:"name_en"`
	Description string   `json:"description"`
	IsSystem    bool     `json:"is_system"`
	UserCount   int      `json:"user_count"`
	Permissions []string `json:"permissions"`
}

func (h *Handler) listRoles(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `
		SELECT ro.id, ro.code, ro.name, ro.name_en, ro.description, ro.is_system,
		       (SELECT count(*) FROM users u WHERE u.role_id = ro.id AND u.company_id = $1),
		       COALESCE((SELECT array_agg(permission_code ORDER BY permission_code) FROM role_permissions rp WHERE rp.role_id = ro.id), '{}')
		FROM roles ro ORDER BY ro.is_system DESC, ro.id`, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Role{}
	for rows.Next() {
		var ro Role
		if err := rows.Scan(&ro.ID, &ro.Code, &ro.Name, &ro.NameEn, &ro.Description, &ro.IsSystem, &ro.UserCount, &ro.Permissions); err != nil {
			return err
		}
		items = append(items, ro)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

type Permission struct {
	Code   string `json:"code"`
	Module string `json:"module"`
	Action string `json:"action"`
}

func (h *Handler) listPermissions(w http.ResponseWriter, r *http.Request) error {
	rows, err := h.Pool.Query(r.Context(), `SELECT code, module, action FROM permissions ORDER BY module, action`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []Permission{}
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Code, &p.Module, &p.Action); err != nil {
			return err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,40}$`)

func (h *Handler) saveRole(w http.ResponseWriter, r *http.Request) error {
	var in Role
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Code, in.Name, in.NameEn, in.Description = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.NameEn), strings.TrimSpace(in.Description)
	isCreate := r.Method == http.MethodPost
	v := apperr.NewValidator()
	v.Required("name", in.Name)
	if isCreate {
		v.Check(codeRe.MatchString(in.Code), "code", "invalid")
	}
	if err := v.Err(); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if isCreate {
			if err := tx.QueryRow(r.Context(), `INSERT INTO roles (code, name, name_en, description) VALUES ($1, $2, $3, $4) RETURNING id`,
				in.Code, in.Name, in.NameEn, in.Description).Scan(&in.ID); err != nil {
				return err
			}
		} else {
			id, err := httpx.IDParam(r, "id")
			if err != nil {
				return err
			}
			in.ID = id
			var code string
			if err := tx.QueryRow(r.Context(), `SELECT code FROM roles WHERE id = $1 FOR UPDATE`, id).Scan(&code); err != nil {
				if db.IsNoRows(err) {
					return apperr.NotFound("role")
				}
				return err
			}
			if code == "admin" {
				return apperr.Rule("role_locked", "the administrator role cannot be changed")
			}
			if _, err := tx.Exec(r.Context(), `UPDATE roles SET name = $2, name_en = $3, description = $4 WHERE id = $1`, id, in.Name, in.NameEn, in.Description); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM role_permissions WHERE role_id = $1`, in.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO role_permissions (role_id, permission_code)
			SELECT $1, code FROM permissions WHERE code = ANY($2)`, in.ID, in.Permissions); err != nil {
			return err
		}
		action := "update"
		if isCreate {
			action = "create"
		}
		return audit.Record(r.Context(), tx, action, "role", in.ID, nil, in)
	})
	if err != nil {
		return err
	}
	if isCreate {
		httpx.Created(w, in)
	} else {
		httpx.OK(w, in)
	}
	return nil
}

func (h *Handler) deleteRole(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var system bool
		if err := tx.QueryRow(r.Context(), `SELECT is_system FROM roles WHERE id = $1`, id).Scan(&system); err != nil {
			if db.IsNoRows(err) {
				return apperr.NotFound("role")
			}
			return err
		}
		if system {
			return apperr.Rule("role_locked", "system roles cannot be deleted")
		}
		var used bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM users WHERE role_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return apperr.Rule("in_use", "the role is assigned to users")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM roles WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "role", id, nil, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}
