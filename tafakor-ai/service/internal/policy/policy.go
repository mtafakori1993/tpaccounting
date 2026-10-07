// Package policy is the deterministic decision engine that chooses whether an
// action may be registered automatically or needs a human. It takes facts that
// the service computed itself (never model output) plus the user's settings.
// The LLM can request an action but can never set the mode, the switch or a limit:
// none of those are inputs it can reach.
package policy

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

type Mode string

const (
	Auto    Mode = "auto"
	Human   Mode = "human"
	Blocked Mode = "blocked"
)

// NeverAuto actions always need a human, whatever the switch says.
var NeverAuto = map[string]bool{
	"cancel_document": true, "create_journal_entry": true, "create_check": true, "payroll": true,
	"create_invoice": true, "create_purchase": true,
}

// Facts are computed by the service (resolution, duplicate check, period check …).
type Facts struct {
	Action           string
	Amount           decimal.Decimal
	Currency         string
	Duplicate        bool
	Conflict         bool // instruction vs document mismatch (amount/party/date)
	Ambiguous        bool // party/project/account unknown or not unique
	LowConfidence    bool
	PeriodBlocked    bool // closed fiscal year / locked period / no fiscal year
	BudgetEvent      bool // active budget control or a budget warning/limit
	ValidationIssue  bool
	NotRequested     bool // the user did not explicitly ask for this registration
	FromDocumentText bool // the request (or a value) originates from text inside a document/page
	ForeignCurrency  bool // FX conversion needed
	LearnedSuggest   bool // a learned rule is still in "suggest" stage
	HasPermission    bool
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type Decision struct {
	Mode       Mode     `json:"mode"`
	Reasons    []string `json:"reasons"` // codes of failed conditions, in order
	Checks     []Check  `json:"checks"`
	AutoSwitch bool     `json:"auto_switch"`
	Simulated  bool     `json:"simulated"`  // simulation on: auto would have run but was not executed
	WouldAuto  bool     `json:"would_auto"` // what auto mode would have done (for simulation)
	Limits     Limits   `json:"limits"`
}

type Limits struct {
	MaxAmount string `json:"max_amount"`
	MaxPerDay int    `json:"max_per_day"`
	UsedToday int    `json:"used_today"`
}

type Input struct {
	Facts       Facts
	Settings    store.UserSettings
	CompanyLock bool
	AutoToday   int
	AskThisTime bool // per-task override "this time ask me"
}

// Decide is a pure function: same input, same decision.
func Decide(in Input) Decision {
	f, s := in.Facts, in.Settings
	d := Decision{AutoSwitch: s.AutoMode, Limits: Limits{MaxAmount: s.MaxAmount[f.Currency], MaxPerDay: s.MaxPerDay, UsedToday: in.AutoToday}}
	add := func(name string, ok bool, detail string) {
		d.Checks = append(d.Checks, Check{Name: name, Passed: ok, Detail: detail})
		if !ok {
			d.Reasons = append(d.Reasons, name)
		}
	}
	if !f.HasPermission {
		add("no_permission", false, "")
		d.Mode = Blocked
		return d
	}
	if f.PeriodBlocked {
		// Not executable at all until the period is opened; a human must decide.
		add("period_locked", false, "")
	}
	add("auto_switch_on", s.AutoMode, "")
	add("company_allows_auto", !in.CompanyLock, "")
	add("not_asked_this_time", !in.AskThisTime, "")
	add("action_never_automatic", !NeverAuto[f.Action], f.Action)
	add("action_allowed_automatic", contains(s.AutoActions, f.Action), f.Action)
	add("not_duplicate", !f.Duplicate, "")
	add("no_conflict", !f.Conflict, "")
	add("not_ambiguous", !f.Ambiguous, "")
	add("confident_fields", !f.LowConfidence, "")
	add("open_period", !f.PeriodBlocked, "")
	add("no_budget_event", !f.BudgetEvent, "")
	add("no_validation_issue", !f.ValidationIssue, "")
	add("explicitly_requested", !f.NotRequested, "")
	add("not_from_document_text", !f.FromDocumentText, "")
	add("base_currency", !f.ForeignCurrency, "")
	add("rule_trusted", !f.LearnedSuggest, "")
	// amount limit (per currency; a currency without a limit never auto-registers)
	maxStr, ok := s.MaxAmount[f.Currency]
	within := false
	if ok {
		if max, err := decimal.NewFromString(maxStr); err == nil {
			within = f.Amount.IsPositive() && f.Amount.LessThanOrEqual(max)
		}
	}
	add("within_amount_limit", within, fmt.Sprintf("%s %s", f.Amount.String(), f.Currency))
	add("within_daily_cap", s.MaxPerDay > 0 && in.AutoToday < s.MaxPerDay, fmt.Sprintf("%d/%d", in.AutoToday, s.MaxPerDay))

	// would auto be allowed ignoring the switch itself? (simulation)
	d.WouldAuto = true
	for _, c := range d.Checks {
		if !c.Passed && c.Name != "auto_switch_on" {
			d.WouldAuto = false
		}
	}
	switch {
	case len(d.Reasons) == 0 && s.Simulation:
		d.Mode, d.Simulated = Human, true
	case len(d.Reasons) == 0:
		d.Mode = Auto
	default:
		d.Mode = Human
	}
	if s.Simulation && d.WouldAuto {
		d.Simulated = true
	}
	return d
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ValidateSettings enforces sane values for user-controlled limits.
func ValidateSettings(s *store.UserSettings) error {
	if s.MaxPerDay < 0 || s.MaxPerDay > 500 {
		return fmt.Errorf("max_per_day must be between 0 and 500")
	}
	for cur, v := range s.MaxAmount {
		if len(cur) != 3 {
			return fmt.Errorf("invalid currency %q", cur)
		}
		d, err := decimal.NewFromString(v)
		if err != nil || d.IsNegative() {
			return fmt.Errorf("invalid limit for %s", cur)
		}
	}
	allowed := map[string]bool{"create_receipt": true, "create_payment": true, "create_expense": true, "attach_document": true, "generate_pdf": true}
	for _, a := range s.AutoActions {
		if !allowed[a] {
			return fmt.Errorf("action %q can never be automatic", a)
		}
	}
	return nil
}

// ApplySwitch changes the approval switch. Turning auto ON needs an explicit risk
// confirmation and is refused under a company lock; turning it OFF is always instant.
func ApplySwitch(s *store.UserSettings, wantAuto, riskConfirmed, companyLock bool, now time.Time) error {
	if !wantAuto {
		s.AutoMode = false
		return nil
	}
	if companyLock {
		return fmt.Errorf("company_lock")
	}
	if !riskConfirmed {
		return fmt.Errorf("risk_confirmation_required")
	}
	s.AutoMode = true
	s.AutoConfirmedAt = &now
	return nil
}

// ReasonText gives a plain-language explanation for a failed check (en, fa).
var ReasonText = map[string][2]string{
	"no_permission":            {"you don't have permission for this action", "شما مجوز این کار را ندارید"},
	"period_locked":            {"the date falls in a closed fiscal year or locked period", "تاریخ در سال مالی بسته یا دوره قفل‌شده است"},
	"auto_switch_on":           {"you asked to approve every registration", "تنظیم شما «قبل از ثبت از من بپرس» است"},
	"company_allows_auto":      {"your company does not allow automatic registration", "ثبت خودکار در شرکت شما مجاز نیست"},
	"not_asked_this_time":      {"you asked me to check with you this time", "خواستید این بار از شما بپرسم"},
	"action_never_automatic":   {"this kind of action always needs your approval", "این نوع عملیات همیشه تایید شما را لازم دارد"},
	"action_allowed_automatic": {"this action type is not enabled for automatic registration", "این نوع عملیات برای ثبت خودکار فعال نیست"},
	"not_duplicate":            {"possible duplicate", "احتمال ثبت تکراری"},
	"no_conflict":              {"your instruction and the document don't match", "دستور شما با سند مطابقت ندارد"},
	"not_ambiguous":            {"a party, project or account is unclear", "طرف حساب، پروژه یا حساب مشخص نیست"},
	"confident_fields":         {"some values were read with low confidence", "برخی مقادیر با اطمینان کم خوانده شدند"},
	"open_period":              {"the period is not open", "دوره مالی باز نیست"},
	"no_budget_event":          {"budget control applies", "کنترل بودجه اعمال می‌شود"},
	"no_validation_issue":      {"there is a validation warning", "هشدار اعتبارسنجی وجود دارد"},
	"explicitly_requested":     {"you didn't explicitly ask for this registration", "این ثبت را صریحاً درخواست نکرده‌اید"},
	"not_from_document_text":   {"the request comes from text inside a document", "درخواست از متن داخل سند آمده است"},
	"base_currency":            {"a foreign-currency amount needs an exchange rate", "مبلغ ارزی نیاز به نرخ تبدیل دارد"},
	"rule_trusted":             {"a learned rule still needs your confirmation", "قاعده آموخته‌شده هنوز نیاز به تایید دارد"},
	"within_amount_limit":      {"the amount is above your automatic limit", "مبلغ بیشتر از سقف ثبت خودکار شماست"},
	"within_daily_cap":         {"today's automatic registrations limit is reached", "سقف ثبت خودکار امروز پر شده است"},
}
