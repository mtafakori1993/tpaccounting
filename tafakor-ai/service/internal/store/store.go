// Package store is the AI service's own persistence. It never touches the
// accounting database. Repository is the interface the rest of the service uses,
// so the SQLite implementation can later be replaced (e.g. by Postgres).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

// Scope identifies the tenant and user every record belongs to.
type Scope struct {
	CompanyID int64
	UserID    int64
}

type UserSettings struct {
	CompanyID int64 `json:"-"`
	UserID    int64 `json:"-"`
	// AutoMode false = "Ask me before registering" (default).
	AutoMode        bool              `json:"auto_mode"`
	AutoConfirmedAt *time.Time        `json:"auto_confirmed_at,omitempty"`
	MaxAmount       map[string]string `json:"max_amount"` // currency → max amount per document
	MaxPerDay       int               `json:"max_per_day"`
	AutoActions     []string          `json:"auto_actions"` // action types allowed automatically
	Simulation      bool              `json:"simulation"`
	VoiceReply      bool              `json:"voice_reply"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// DefaultUserSettings are the safe defaults: human approval, small limits.
func DefaultUserSettings(s Scope) UserSettings {
	return UserSettings{
		CompanyID: s.CompanyID, UserID: s.UserID,
		MaxAmount:   map[string]string{"IRR": "100000000", "IRT": "10000000", "USD": "100", "EUR": "100"},
		MaxPerDay:   10,
		AutoActions: []string{"create_receipt", "create_expense"},
	}
}

type CompanySettings struct {
	CompanyID int64     `json:"-"`
	LockAuto  bool      `json:"lock_auto"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Rule struct {
	ID             int64           `json:"id"`
	CompanyID      int64           `json:"-"`
	UserID         int64           `json:"user_id"` // owner (creator)
	Scope          string          `json:"scope"`   // personal | company
	Kind           string          `json:"kind"`    // expense_category | event_account | payment_default | party_default | party_alias
	Pattern        string          `json:"pattern"` // normalised match key
	Result         json.RawMessage `json:"result"`
	Label          string          `json:"label"` // human description
	TimesConfirmed int             `json:"times_confirmed"`
	Source         string          `json:"source"` // correction | manual | promoted
	Status         string          `json:"status"` // active | disabled
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Alias struct {
	ID         int64     `json:"id"`
	CompanyID  int64     `json:"-"`
	UserID     int64     `json:"user_id"`
	Kind       string    `json:"kind"` // party | asr
	Alias      string    `json:"alias"`
	TargetID   int64     `json:"target_id"`
	TargetName string    `json:"target_name"`
	CreatedAt  time.Time `json:"created_at"`
}

type AuditEntry struct {
	ID        int64           `json:"id"`
	CompanyID int64           `json:"-"`
	UserID    int64           `json:"user_id"`
	TaskID    string          `json:"task_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type Task struct {
	ID        string          `json:"id"`
	CompanyID int64           `json:"-"`
	UserID    int64           `json:"-"`
	Status    string          `json:"status"`
	Request   string          `json:"request"`
	State     json.RawMessage `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type ApprovalToken struct {
	TokenHash   string
	CompanyID   int64
	UserID      int64
	TaskID      string
	PayloadHash string
	ExpiresAt   time.Time
	UsedAt      *time.Time
}

type CostEntry struct {
	TaskID           string    `json:"task_id"`
	CompanyID        int64     `json:"-"`
	UserID           int64     `json:"-"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	CreatedAt        time.Time `json:"created_at"`
}

type Repository interface {
	GetUserSettings(ctx context.Context, s Scope) (UserSettings, error)
	SaveUserSettings(ctx context.Context, u UserSettings) error
	GetCompanySettings(ctx context.Context, companyID int64) (CompanySettings, error)
	SaveCompanySettings(ctx context.Context, c CompanySettings) error

	// Rules visible to the user: personal rules of the user + company rules.
	ListRules(ctx context.Context, s Scope) ([]Rule, error)
	GetRule(ctx context.Context, s Scope, id int64) (Rule, error)
	SaveRule(ctx context.Context, r *Rule) error
	DeleteRule(ctx context.Context, s Scope, id int64) error

	ListAliases(ctx context.Context, s Scope) ([]Alias, error)
	SaveAlias(ctx context.Context, a *Alias) error
	DeleteAlias(ctx context.Context, s Scope, id int64) error

	AppendAudit(ctx context.Context, e AuditEntry) error
	ListAudit(ctx context.Context, s Scope, taskID string, limit int) ([]AuditEntry, error)
	PurgeAudit(ctx context.Context, before time.Time) (int64, error)

	SaveTask(ctx context.Context, t *Task) error
	GetTask(ctx context.Context, s Scope, id string) (Task, error)
	ListTasks(ctx context.Context, s Scope, limit int) ([]Task, error)

	SaveApproval(ctx context.Context, a ApprovalToken) error
	// ConsumeApproval atomically marks a token used; it fails if missing, used, expired
	// or bound to a different user/task/payload.
	ConsumeApproval(ctx context.Context, a ApprovalToken, now time.Time) error

	// Idempotency: Remember stores the result of a mutation by key; Recall returns it.
	Recall(ctx context.Context, s Scope, key string) (json.RawMessage, bool, error)
	Remember(ctx context.Context, s Scope, key string, result json.RawMessage) error

	LogCost(ctx context.Context, c CostEntry) error
	CostSummary(ctx context.Context, s Scope, since time.Time) (CostEntry, error)
	CountAutoToday(ctx context.Context, s Scope, dayStart time.Time) (int, error)

	Ping(ctx context.Context) error
	Close() error
}
