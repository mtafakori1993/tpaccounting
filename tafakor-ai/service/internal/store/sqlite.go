package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite implements Repository on a single WAL-mode database file.
type SQLite struct{ db *sql.DB }

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS user_settings (
		company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, data TEXT NOT NULL, updated_at TEXT NOT NULL,
		PRIMARY KEY (company_id, user_id))`,
	`CREATE TABLE IF NOT EXISTS company_settings (
		company_id INTEGER PRIMARY KEY, data TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS learned_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
		scope TEXT NOT NULL CHECK (scope IN ('personal','company')), kind TEXT NOT NULL, pattern TEXT NOT NULL,
		result TEXT NOT NULL, label TEXT NOT NULL DEFAULT '', times_confirmed INTEGER NOT NULL DEFAULT 1,
		source TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS learned_rules_lookup ON learned_rules (company_id, kind, pattern)`,
	`CREATE TABLE IF NOT EXISTS aliases (
		id INTEGER PRIMARY KEY AUTOINCREMENT, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, kind TEXT NOT NULL,
		alias TEXT NOT NULL, target_id INTEGER NOT NULL, target_name TEXT NOT NULL, created_at TEXT NOT NULL,
		UNIQUE (company_id, user_id, kind, alias))`,
	`CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, task_id TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL, data TEXT NOT NULL, created_at TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS audit_task ON audit_log (company_id, user_id, task_id)`,
	`CREATE INDEX IF NOT EXISTS audit_created ON audit_log (created_at)`,
	`CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, status TEXT NOT NULL, request TEXT NOT NULL,
		state TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS tasks_user ON tasks (company_id, user_id, updated_at)`,
	`CREATE TABLE IF NOT EXISTS approval_tokens (
		token_hash TEXT PRIMARY KEY, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, task_id TEXT NOT NULL,
		payload_hash TEXT NOT NULL, expires_at TEXT NOT NULL, used_at TEXT)`,
	`CREATE TABLE IF NOT EXISTS idempotency (
		company_id INTEGER NOT NULL, user_id INTEGER NOT NULL, key TEXT NOT NULL, result TEXT NOT NULL, created_at TEXT NOT NULL,
		PRIMARY KEY (company_id, user_id, key))`,
	`CREATE TABLE IF NOT EXISTS cost_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, company_id INTEGER NOT NULL, user_id INTEGER NOT NULL,
		model TEXT NOT NULL, prompt_tokens INTEGER NOT NULL, completion_tokens INTEGER NOT NULL, cost_usd REAL NOT NULL,
		created_at TEXT NOT NULL)`,
}

const ts = time.RFC3339Nano

// OpenSQLite opens (and migrates) the database at path.
func OpenSQLite(path string) (*SQLite, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error                   { return s.db.Close() }
func (s *SQLite) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func now() string { return time.Now().UTC().Format(ts) }

func parseTime(v string) time.Time {
	t, _ := time.Parse(ts, v)
	return t
}

func (s *SQLite) GetUserSettings(ctx context.Context, sc Scope) (UserSettings, error) {
	var data, upd string
	err := s.db.QueryRowContext(ctx, `SELECT data, updated_at FROM user_settings WHERE company_id = ? AND user_id = ?`, sc.CompanyID, sc.UserID).Scan(&data, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultUserSettings(sc), nil
	}
	if err != nil {
		return UserSettings{}, err
	}
	u := DefaultUserSettings(sc)
	if err := json.Unmarshal([]byte(data), &u); err != nil {
		return UserSettings{}, err
	}
	u.CompanyID, u.UserID, u.UpdatedAt = sc.CompanyID, sc.UserID, parseTime(upd)
	return u, nil
}

func (s *SQLite) SaveUserSettings(ctx context.Context, u UserSettings) error {
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_settings (company_id, user_id, data, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (company_id, user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		u.CompanyID, u.UserID, string(data), now())
	return err
}

func (s *SQLite) GetCompanySettings(ctx context.Context, companyID int64) (CompanySettings, error) {
	var data, upd string
	c := CompanySettings{CompanyID: companyID}
	err := s.db.QueryRowContext(ctx, `SELECT data, updated_at FROM company_settings WHERE company_id = ?`, companyID).Scan(&data, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal([]byte(data), &c)
	c.CompanyID, c.UpdatedAt = companyID, parseTime(upd)
	return c, err
}

func (s *SQLite) SaveCompanySettings(ctx context.Context, c CompanySettings) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO company_settings (company_id, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (company_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, c.CompanyID, string(data), now())
	return err
}

const ruleCols = `id, company_id, user_id, scope, kind, pattern, result, label, times_confirmed, source, status, created_at, updated_at`

func scanRule(row interface{ Scan(...any) error }) (Rule, error) {
	var r Rule
	var res, c, u string
	err := row.Scan(&r.ID, &r.CompanyID, &r.UserID, &r.Scope, &r.Kind, &r.Pattern, &res, &r.Label, &r.TimesConfirmed, &r.Source, &r.Status, &c, &u)
	r.Result, r.CreatedAt, r.UpdatedAt = json.RawMessage(res), parseTime(c), parseTime(u)
	return r, err
}

func (s *SQLite) ListRules(ctx context.Context, sc Scope) ([]Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ruleCols+` FROM learned_rules
		WHERE company_id = ? AND (scope = 'company' OR user_id = ?) ORDER BY updated_at DESC, id DESC`, sc.CompanyID, sc.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) GetRule(ctx context.Context, sc Scope, id int64) (Rule, error) {
	r, err := scanRule(s.db.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM learned_rules
		WHERE id = ? AND company_id = ? AND (scope = 'company' OR user_id = ?)`, id, sc.CompanyID, sc.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *SQLite) SaveRule(ctx context.Context, r *Rule) error {
	t := now()
	if r.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO learned_rules (company_id, user_id, scope, kind, pattern, result, label, times_confirmed, source, status, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.CompanyID, r.UserID, r.Scope, r.Kind, r.Pattern, string(r.Result), r.Label,
			r.TimesConfirmed, r.Source, r.Status, t, t)
		if err != nil {
			return err
		}
		r.ID, _ = res.LastInsertId()
		r.CreatedAt, r.UpdatedAt = parseTime(t), parseTime(t)
		return nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE learned_rules SET scope = ?, pattern = ?, result = ?, label = ?, times_confirmed = ?, source = ?, status = ?, updated_at = ?
		WHERE id = ? AND company_id = ?`, r.Scope, r.Pattern, string(r.Result), r.Label, r.TimesConfirmed, r.Source, r.Status, t, r.ID, r.CompanyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	r.UpdatedAt = parseTime(t)
	return nil
}

func (s *SQLite) DeleteRule(ctx context.Context, sc Scope, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM learned_rules WHERE id = ? AND company_id = ? AND (scope = 'company' OR user_id = ?)`, id, sc.CompanyID, sc.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) ListAliases(ctx context.Context, sc Scope) ([]Alias, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, company_id, user_id, kind, alias, target_id, target_name, created_at FROM aliases
		WHERE company_id = ? AND user_id = ? ORDER BY id DESC`, sc.CompanyID, sc.UserID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alias{}
	for rows.Next() {
		var a Alias
		var c string
		if err := rows.Scan(&a.ID, &a.CompanyID, &a.UserID, &a.Kind, &a.Alias, &a.TargetID, &a.TargetName, &c); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(c)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *SQLite) SaveAlias(ctx context.Context, a *Alias) error {
	t := now()
	err := s.db.QueryRowContext(ctx, `INSERT INTO aliases (company_id, user_id, kind, alias, target_id, target_name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (company_id, user_id, kind, alias) DO UPDATE SET target_id = excluded.target_id, target_name = excluded.target_name
		RETURNING id`, a.CompanyID, a.UserID, a.Kind, a.Alias, a.TargetID, a.TargetName, t).Scan(&a.ID)
	a.CreatedAt = parseTime(t)
	return err
}

func (s *SQLite) DeleteAlias(ctx context.Context, sc Scope, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM aliases WHERE id = ? AND company_id = ? AND user_id = ?`, id, sc.CompanyID, sc.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) AppendAudit(ctx context.Context, e AuditEntry) error {
	if len(e.Data) == 0 {
		e.Data = json.RawMessage("{}")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (company_id, user_id, task_id, kind, data, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		e.CompanyID, e.UserID, e.TaskID, e.Kind, string(e.Data), now())
	return err
}

func (s *SQLite) ListAudit(ctx context.Context, sc Scope, taskID string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id, company_id, user_id, task_id, kind, data, created_at FROM audit_log WHERE company_id = ? AND user_id = ?`
	args := []any{sc.CompanyID, sc.UserID}
	if taskID != "" {
		q += ` AND task_id = ?`
		args = append(args, taskID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var d, c string
		if err := rows.Scan(&e.ID, &e.CompanyID, &e.UserID, &e.TaskID, &e.Kind, &d, &c); err != nil {
			return nil, err
		}
		e.Data, e.CreatedAt = json.RawMessage(d), parseTime(c)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *SQLite) PurgeAudit(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audit_log WHERE created_at < ?`, before.UTC().Format(ts))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	_, _ = s.db.ExecContext(ctx, `DELETE FROM approval_tokens WHERE expires_at < ?`, before.UTC().Format(ts))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM idempotency WHERE created_at < ?`, before.UTC().Format(ts))
	return n, nil
}

func (s *SQLite) SaveTask(ctx context.Context, t *Task) error {
	n := now()
	if len(t.State) == 0 {
		t.State = json.RawMessage("{}")
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = parseTime(n)
	}
	t.UpdatedAt = parseTime(n)
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks (id, company_id, user_id, status, request, state, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET status = excluded.status, state = excluded.state, updated_at = excluded.updated_at
		WHERE tasks.company_id = excluded.company_id AND tasks.user_id = excluded.user_id`,
		t.ID, t.CompanyID, t.UserID, t.Status, t.Request, string(t.State), t.CreatedAt.Format(ts), n)
	return err
}

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var st, c, u string
	err := row.Scan(&t.ID, &t.CompanyID, &t.UserID, &t.Status, &t.Request, &st, &c, &u)
	t.State, t.CreatedAt, t.UpdatedAt = json.RawMessage(st), parseTime(c), parseTime(u)
	return t, err
}

func (s *SQLite) GetTask(ctx context.Context, sc Scope, id string) (Task, error) {
	t, err := scanTask(s.db.QueryRowContext(ctx, `SELECT id, company_id, user_id, status, request, state, created_at, updated_at FROM tasks
		WHERE id = ? AND company_id = ? AND user_id = ?`, id, sc.CompanyID, sc.UserID))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *SQLite) ListTasks(ctx context.Context, sc Scope, limit int) ([]Task, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, company_id, user_id, status, request, state, created_at, updated_at FROM tasks
		WHERE company_id = ? AND user_id = ? ORDER BY updated_at DESC LIMIT ?`, sc.CompanyID, sc.UserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *SQLite) SaveApproval(ctx context.Context, a ApprovalToken) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO approval_tokens (token_hash, company_id, user_id, task_id, payload_hash, expires_at) VALUES (?, ?, ?, ?, ?, ?)`,
		a.TokenHash, a.CompanyID, a.UserID, a.TaskID, a.PayloadHash, a.ExpiresAt.UTC().Format(ts))
	return err
}

var ErrApprovalInvalid = errors.New("approval token invalid, used, expired or bound to another payload")

func (s *SQLite) ConsumeApproval(ctx context.Context, a ApprovalToken, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE approval_tokens SET used_at = ?
		WHERE token_hash = ? AND company_id = ? AND user_id = ? AND task_id = ? AND payload_hash = ? AND used_at IS NULL AND expires_at > ?`,
		at.UTC().Format(ts), a.TokenHash, a.CompanyID, a.UserID, a.TaskID, a.PayloadHash, at.UTC().Format(ts))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrApprovalInvalid
	}
	return nil
}

func (s *SQLite) Recall(ctx context.Context, sc Scope, key string) (json.RawMessage, bool, error) {
	var r string
	err := s.db.QueryRowContext(ctx, `SELECT result FROM idempotency WHERE company_id = ? AND user_id = ? AND key = ?`, sc.CompanyID, sc.UserID, key).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return json.RawMessage(r), err == nil, err
}

func (s *SQLite) Remember(ctx context.Context, sc Scope, key string, result json.RawMessage) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO idempotency (company_id, user_id, key, result, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, sc.CompanyID, sc.UserID, key, string(result), now())
	return err
}

func (s *SQLite) LogCost(ctx context.Context, c CostEntry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cost_log (task_id, company_id, user_id, model, prompt_tokens, completion_tokens, cost_usd, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, c.TaskID, c.CompanyID, c.UserID, c.Model, c.PromptTokens, c.CompletionTokens, c.CostUSD, now())
	return err
}

func (s *SQLite) CostSummary(ctx context.Context, sc Scope, since time.Time) (CostEntry, error) {
	var c CostEntry
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(prompt_tokens),0), COALESCE(SUM(completion_tokens),0), COALESCE(SUM(cost_usd),0)
		FROM cost_log WHERE company_id = ? AND user_id = ? AND created_at >= ?`, sc.CompanyID, sc.UserID, since.UTC().Format(ts)).
		Scan(&c.PromptTokens, &c.CompletionTokens, &c.CostUSD)
	return c, err
}

func (s *SQLite) CountAutoToday(ctx context.Context, sc Scope, dayStart time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE company_id = ? AND user_id = ? AND kind = 'auto_registered' AND created_at >= ?`,
		sc.CompanyID, sc.UserID, dayStart.UTC().Format(ts)).Scan(&n)
	return n, err
}
