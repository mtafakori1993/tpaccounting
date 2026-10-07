package budgets

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

// Policies, from mild to strict.
const (
	PolicyNone    = "none"
	PolicyWarning = "warning"
	PolicySoft    = "soft"
	PolicyHard    = "hard"
)

// Request-scoped budget control state: the user's confirmation / override and
// the warnings to report back.
type requestState struct {
	confirmed bool
	override  string
	mu        sync.Mutex
	warnings  []Warning
	// blocked hard-limit attempts; the posting transaction rolls back, so they
	// are written after the response by the middleware.
	blocked []blockedAttempt
}

type blockedAttempt struct {
	companyID, userID int64
	roleCode          string
	ev                Evaluation
	limit, exceeded   decimal.Decimal
	src               entryInfo
	reason            string
}

type stateKey struct{}

// Warning describes a budget line pushed over its limit by a posting.
type Warning struct {
	Policy      string          `json:"policy"`
	Outcome     string          `json:"outcome"`
	BudgetName  string          `json:"budget"`
	AccountCode string          `json:"account_code"`
	AccountName string          `json:"account_name"`
	CostCenter  string          `json:"cost_center,omitempty"`
	Budget      decimal.Decimal `json:"budget_amount"`
	Actual      decimal.Decimal `json:"actual_amount"`
	Excess      decimal.Decimal `json:"excess"`
	Utilization decimal.Decimal `json:"utilization"`
}

// WarningsHeader carries warnings of a successful request (URL-encoded JSON).
const WarningsHeader = "X-Budget-Warnings"

// Middleware reads X-Budget-Confirm / X-Budget-Override and reports warnings
// raised by budget control in the X-Budget-Warnings response header. It does
// not log blocked attempts; use LoggingMiddleware for that.
func Middleware(next http.Handler) http.Handler { return LoggingMiddleware(nil)(next) }

// LoggingMiddleware is Middleware that also records hard-limit postings that
// were blocked. Those postings are rolled back with their transaction, so the
// "blocked" events are written afterwards on their own connection.
func LoggingMiddleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			st := &requestState{confirmed: r.Header.Get("X-Budget-Confirm") == "true"}
			if o := r.Header.Get("X-Budget-Override"); o != "" {
				if dec, err := url.QueryUnescape(o); err == nil {
					st.override = strings.TrimSpace(dec)
				}
			}
			ctx := context.WithValue(r.Context(), stateKey{}, st)
			next.ServeHTTP(&warningWriter{ResponseWriter: w, st: st}, r.WithContext(ctx))
			if pool == nil {
				return
			}
			st.mu.Lock()
			blocked := st.blocked
			st.mu.Unlock()
			for _, b := range blocked {
				if err := insertEvent(context.WithoutCancel(r.Context()), pool, b.companyID, nil, b.ev, "blocked", b.reason, b.userID, b.roleCode, b.limit, b.exceeded, b.src); err != nil {
					slog.Error("budget: log blocked posting", "err", err)
				}
			}
		})
	}
}

type warningWriter struct {
	http.ResponseWriter
	st      *requestState
	written bool
}

func (w *warningWriter) WriteHeader(status int) {
	if !w.written {
		w.written = true
		w.st.mu.Lock()
		if status < 400 && len(w.st.warnings) > 0 {
			b, _ := json.Marshal(w.st.warnings)
			w.Header().Set(WarningsHeader, url.QueryEscape(string(b)))
		}
		w.st.mu.Unlock()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *warningWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func stateFrom(ctx context.Context) *requestState {
	st, _ := ctx.Value(stateKey{}).(*requestState)
	return st
}

// WithConfirmation marks ctx as confirmed (soft limits) and/or overriding (hard limits); used by tests and internal callers.
func WithConfirmation(ctx context.Context, confirmed bool, overrideReason string) context.Context {
	return context.WithValue(ctx, stateKey{}, &requestState{confirmed: confirmed, override: overrideReason})
}

// ---- Rules ----

type Rule struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	BudgetID         *int64          `json:"budget_id"`
	BudgetName       *string         `json:"budget_name"`
	AccountID        *int64          `json:"account_id"`
	AccountCode      *string         `json:"account_code"`
	AccountName      *string         `json:"account_name"`
	CostCenterID     *int64          `json:"cost_center_id"`
	CostCenterName   *string         `json:"cost_center_name"`
	RoleID           *int64          `json:"role_id"`
	RoleName         *string         `json:"role_name"`
	Policy           string          `json:"policy"`
	ThresholdPercent decimal.Decimal `json:"threshold_percent"`
	Basis            string          `json:"basis"`
	IsActive         bool            `json:"is_active"`
	accountPath      string
	roleCode         string
}

const ruleCols = `r.id, r.name, r.budget_id, b.name, r.account_id, a.code, a.name, r.cost_center_id, cc.name, r.role_id, ro.name, r.policy,
	r.threshold_percent, r.basis, r.is_active, COALESCE(a.path, ''), COALESCE(ro.code, '')`

const ruleJoins = ` FROM budget_control_rules r LEFT JOIN budgets b ON b.id = r.budget_id LEFT JOIN accounts a ON a.id = r.account_id
	LEFT JOIN cost_centers cc ON cc.id = r.cost_center_id LEFT JOIN roles ro ON ro.id = r.role_id`

func ListRules(ctx context.Context, q db.DBTX, companyID int64, activeOnly bool) ([]Rule, error) {
	sql := `SELECT ` + ruleCols + ruleJoins + ` WHERE r.company_id = $1`
	if activeOnly {
		sql += ` AND r.is_active`
	}
	rows, err := q.Query(ctx, sql+` ORDER BY r.id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var x Rule
		if err := rows.Scan(&x.ID, &x.Name, &x.BudgetID, &x.BudgetName, &x.AccountID, &x.AccountCode, &x.AccountName, &x.CostCenterID,
			&x.CostCenterName, &x.RoleID, &x.RoleName, &x.Policy, &x.ThresholdPercent, &x.Basis, &x.IsActive, &x.accountPath, &x.roleCode); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func SaveRule(ctx context.Context, q db.DBTX, companyID, userID, id int64, in Rule) (int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Basis == "" {
		in.Basis = "ytd"
	}
	if in.ThresholdPercent.IsZero() {
		in.ThresholdPercent = hundred
	}
	v := apperr.NewValidator()
	v.Check(in.Policy == PolicyNone || in.Policy == PolicyWarning || in.Policy == PolicySoft || in.Policy == PolicyHard, "policy", "invalid")
	v.Check(in.Basis == "period" || in.Basis == "ytd" || in.Basis == "annual", "basis", "invalid")
	v.Check(in.ThresholdPercent.IsPositive() && in.ThresholdPercent.LessThanOrEqual(decimal.NewFromInt(1000)), "threshold_percent", "invalid")
	v.MaxLen("name", in.Name, 200)
	if err := v.Err(); err != nil {
		return 0, err
	}
	refs := []struct {
		id    *int64
		table string
		field string
	}{{in.BudgetID, "budgets", "budget_id"}, {in.AccountID, "accounts", "account_id"}, {in.CostCenterID, "cost_centers", "cost_center_id"}}
	for _, ref := range refs {
		if ref.id == nil {
			continue
		}
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+ref.table+` WHERE company_id = $1 AND id = $2)`, companyID, *ref.id).Scan(&ok); err != nil {
			return 0, err
		}
		v.Check(ok, ref.field, "invalid")
	}
	if in.RoleID != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM roles WHERE id = $1)`, *in.RoleID).Scan(&ok); err != nil {
			return 0, err
		}
		v.Check(ok, "role_id", "invalid")
	}
	if err := v.Err(); err != nil {
		return 0, err
	}
	if id == 0 {
		err := q.QueryRow(ctx, `INSERT INTO budget_control_rules (company_id, name, budget_id, account_id, cost_center_id, role_id, policy, threshold_percent,
			basis, is_active, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE, $10) RETURNING id`, companyID, in.Name, in.BudgetID,
			in.AccountID, in.CostCenterID, in.RoleID, in.Policy, in.ThresholdPercent, in.Basis, userID).Scan(&id)
		return id, err
	}
	tag, err := q.Exec(ctx, `UPDATE budget_control_rules SET name = $3, budget_id = $4, account_id = $5, cost_center_id = $6, role_id = $7, policy = $8,
		threshold_percent = $9, basis = $10, is_active = $11, updated_at = now() WHERE company_id = $1 AND id = $2`, companyID, id, in.Name, in.BudgetID,
		in.AccountID, in.CostCenterID, in.RoleID, in.Policy, in.ThresholdPercent, in.Basis, in.IsActive)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, apperr.NotFound("budget_rule")
	}
	return id, nil
}

// DefaultRule applies when no configured rule matches an active budget line.
var DefaultRule = Rule{Policy: PolicyWarning, ThresholdPercent: hundred, Basis: "ytd"}

// matchRule returns the most specific active rule for a posting; a rule
// matches when each of its non-empty dimensions matches.
func matchRule(rules []Rule, budgetID int64, accountPath string, ccID *int64, roleCode string) Rule {
	best, bestScore := DefaultRule, -1
	for _, r := range rules {
		if !r.IsActive {
			continue
		}
		score := 0
		if r.BudgetID != nil {
			if *r.BudgetID != budgetID {
				continue
			}
			score += 1
		}
		if r.AccountID != nil {
			if !strings.HasPrefix(accountPath, r.accountPath) {
				continue
			}
			score += 2 + strings.Count(r.accountPath, "/")
		}
		if r.CostCenterID != nil {
			if ccID == nil || *ccID != *r.CostCenterID {
				continue
			}
			score += 20
		}
		if r.RoleID != nil {
			if r.roleCode != roleCode {
				continue
			}
			score += 40
		}
		if score > bestScore {
			best, bestScore = r, score
		}
	}
	return best
}

// ---- Evaluation ----

type posting struct {
	accountID   int64
	accountPath string
	accountCode string
	accountName string
	ccID        *int64
	ccName      *string
	amount      decimal.Decimal
}

type activeLine struct {
	lineID      int64
	budgetID    int64
	budgetName  string
	accountPath string
	ccID        *int64
	amounts     []decimal.Decimal
}

// Evaluation is the budget position of one budget line after a posting.
type Evaluation struct {
	BudgetID    int64           `json:"budget_id"`
	BudgetName  string          `json:"budget_name"`
	LineID      int64           `json:"line_id"`
	AccountCode string          `json:"account_code"`
	AccountName string          `json:"account_name"`
	CostCenter  string          `json:"cost_center"`
	Policy      string          `json:"policy"`
	Basis       string          `json:"basis"`
	Threshold   decimal.Decimal `json:"threshold_percent"`
	RuleID      *int64          `json:"rule_id"`
	Amount      decimal.Decimal `json:"amount"`
	Budget      decimal.Decimal `json:"budget"`
	Actual      decimal.Decimal `json:"actual"`
	Remaining   decimal.Decimal `json:"remaining"`
	Utilization decimal.Decimal `json:"utilization"`
	Exceeded    bool            `json:"exceeded"`
}

// evaluate computes, for spending postings on date, every active budget line
// they fall under. extra is added to the ledger actual (for simulations of
// postings not yet in the ledger).
func evaluate(ctx context.Context, q db.DBTX, companyID int64, date civil.Date, posts []posting, extra bool, roleCode string) ([]Evaluation, error) {
	var fyID int64
	var seq int
	err := q.QueryRow(ctx, `SELECT fy.id, COALESCE(p.seq, 0) FROM fiscal_years fy
		LEFT JOIN accounting_periods p ON p.fiscal_year_id = fy.id AND $2::date BETWEEN p.start_date AND p.end_date
		WHERE fy.company_id = $1 AND $2::date BETWEEN fy.start_date AND fy.end_date`, companyID, date).Scan(&fyID, &seq)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT l.id, b.id, b.name, a.path, l.cost_center_id,
		COALESCE((SELECT array_agg(m.amount ORDER BY m.seq) FROM budget_line_amounts m WHERE m.line_id = l.id), '{}')
		FROM budget_lines l JOIN budgets b ON b.id = l.budget_id JOIN accounts a ON a.id = l.account_id
		WHERE b.company_id = $1 AND b.fiscal_year_id = $2 AND b.status = 'active' AND a.type IN ('expense', 'cost_of_sales')`, companyID, fyID)
	if err != nil {
		return nil, err
	}
	var lines []activeLine
	for rows.Next() {
		var l activeLine
		if err := rows.Scan(&l.lineID, &l.budgetID, &l.budgetName, &l.accountPath, &l.ccID, &l.amounts); err != nil {
			rows.Close()
			return nil, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, nil
	}
	rules, err := ListRules(ctx, q, companyID, true)
	if err != nil {
		return nil, err
	}
	var out []Evaluation
	for _, l := range lines {
		// Postings that fall under this line.
		amount := decimal.Zero
		var first *posting
		for i := range posts {
			p := &posts[i]
			if !strings.HasPrefix(p.accountPath, l.accountPath) {
				continue
			}
			if l.ccID != nil && (p.ccID == nil || *p.ccID != *l.ccID) {
				continue
			}
			amount = amount.Add(p.amount)
			if first == nil {
				first = p
			}
		}
		if first == nil || !amount.IsPositive() {
			continue
		}
		rule := matchRule(rules, l.budgetID, first.accountPath, first.ccID, roleCode)
		if rule.Policy == PolicyNone {
			continue
		}
		budget := decimal.Zero
		fromSeq, toSeq := 1, len(l.amounts)
		switch rule.Basis {
		case "period":
			fromSeq, toSeq = seq, seq
		case "ytd":
			toSeq = seq
		}
		for i, a := range l.amounts {
			if i+1 >= fromSeq && i+1 <= toSeq {
				budget = budget.Add(a)
			}
		}
		var actual decimal.Decimal
		if err := q.QueryRow(ctx, `
			SELECT COALESCE(SUM(jl.debit - jl.credit), 0)
			FROM accounting_periods ap
			JOIN journal_entries e ON e.company_id = $1 AND e.posted_at IS NOT NULL AND e.source_type <> 'closing' AND e.date BETWEEN ap.start_date AND ap.end_date
			JOIN journal_entry_lines jl ON jl.entry_id = e.id
			JOIN accounts la ON la.id = jl.account_id AND la.path LIKE $3 || '%'
			WHERE ap.fiscal_year_id = $2 AND ap.seq BETWEEN $5 AND $6 AND ($4::bigint IS NULL OR jl.cost_center_id = $4)`,
			companyID, fyID, l.accountPath, l.ccID, fromSeq, toSeq).Scan(&actual); err != nil {
			return nil, err
		}
		if extra {
			actual = actual.Add(amount)
		}
		limit := budget.Mul(rule.ThresholdPercent).Div(hundred)
		ev := Evaluation{BudgetID: l.budgetID, BudgetName: l.budgetName, LineID: l.lineID, AccountCode: first.accountCode, AccountName: first.accountName,
			Policy: rule.Policy, Basis: rule.Basis, Threshold: rule.ThresholdPercent, Amount: amount, Budget: budget, Actual: actual,
			Remaining: budget.Sub(actual), Exceeded: actual.GreaterThan(limit)}
		if rule.ID > 0 {
			id := rule.ID
			ev.RuleID = &id
		}
		if first.ccName != nil {
			ev.CostCenter = *first.ccName
		}
		if u := Utilization(budget, actual); u != nil {
			ev.Utilization = *u
		} else if actual.IsPositive() {
			ev.Utilization = decimal.NewFromInt(999)
		}
		out = append(out, ev)
	}
	sort.SliceStable(out, func(i, j int) bool { return policyRank(out[i].Policy) > policyRank(out[j].Policy) })
	return out, nil
}

func policyRank(p string) int {
	return map[string]int{PolicyWarning: 1, PolicySoft: 2, PolicyHard: 3}[p]
}

// Control is the ledger post hook. It applies budget control to the spending
// lines of a newly posted entry: warnings are recorded, soft limits need the
// user's confirmation, hard limits block posting unless the user may override
// and gives a reason. Everything happens in the posting transaction.
func Control(ctx context.Context, q db.DBTX, companyID, entryID int64, src ledger.Source) error {
	switch src.Type {
	case ledger.SourceReversal, ledger.SourceClosing, ledger.SourceOpening:
		return nil
	}
	p := identity.From(ctx)
	if p == nil {
		return nil
	}
	var date civil.Date
	if err := q.QueryRow(ctx, `SELECT date FROM journal_entries WHERE id = $1`, entryID).Scan(&date); err != nil {
		return err
	}
	rows, err := q.Query(ctx, `SELECT l.account_id, a.path, a.code, a.name, l.cost_center_id, cc.name, SUM(l.debit - l.credit)
		FROM journal_entry_lines l JOIN accounts a ON a.id = l.account_id LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id
		WHERE l.entry_id = $1 AND a.type IN ('expense', 'cost_of_sales')
		GROUP BY l.account_id, a.path, a.code, a.name, l.cost_center_id, cc.name HAVING SUM(l.debit - l.credit) > 0`, entryID)
	if err != nil {
		return err
	}
	var posts []posting
	for rows.Next() {
		var x posting
		if err := rows.Scan(&x.accountID, &x.accountPath, &x.accountCode, &x.accountName, &x.ccID, &x.ccName, &x.amount); err != nil {
			rows.Close()
			return err
		}
		posts = append(posts, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(posts) == 0 {
		return nil
	}
	evals, err := evaluate(ctx, q, companyID, date, posts, false, p.RoleCode)
	if err != nil {
		return err
	}
	st := stateFrom(ctx)
	if st == nil {
		st = &requestState{}
	}
	info := entryInfo{SourceType: src.Type}
	if err := q.QueryRow(ctx, `SELECT source_type, source_id, number, reference, description FROM journal_entries WHERE id = $1`, entryID).
		Scan(&info.SourceType, &info.SourceID, &info.Number, &info.Reference, &info.Description); err != nil {
		return err
	}
	for _, ev := range evals {
		if !ev.Exceeded {
			continue
		}
		limit := ev.Budget.Mul(ev.Threshold).Div(hundred).Round(2)
		exceeded := decimal.Max(decimal.Zero, ev.Actual.Sub(limit))
		w := Warning{Policy: ev.Policy, BudgetName: ev.BudgetName, AccountCode: ev.AccountCode, AccountName: ev.AccountName, CostCenter: ev.CostCenter,
			Budget: ev.Budget, Actual: ev.Actual, Excess: ev.Actual.Sub(ev.Budget), Utilization: ev.Utilization}
		params := []any{"budget_name", ev.BudgetName, "account", ev.AccountCode + " " + ev.AccountName, "budget", ev.Budget.String(),
			"actual", ev.Actual.String(), "excess", ev.Actual.Sub(ev.Budget).String(), "utilization", ev.Utilization.String(),
			"limit", limit.String(), "requested", ev.Amount.String(), "exceeded", exceeded.String()}
		outcome := "warned"
		reason := ""
		switch ev.Policy {
		case PolicySoft:
			if !st.confirmed && st.override == "" {
				return apperr.Conflict("budget_confirmation_required", "the posting exceeds the budget; confirm to continue").WithParams(params...)
			}
			outcome = "confirmed"
		case PolicyHard:
			canOverride := p.Can("budgets.override")
			if st.override == "" || !canOverride {
				// Only an actual override attempt (a reason given) or a user who
				// cannot override is a decision worth logging; a first try without
				// a reason just asks the user for one.
				if st.override != "" || !canOverride {
					st.mu.Lock()
					st.blocked = append(st.blocked, blockedAttempt{companyID: companyID, userID: p.UserID, roleCode: p.RoleCode, ev: ev, limit: limit,
						exceeded: exceeded, src: info, reason: st.override})
					st.mu.Unlock()
				}
				return apperr.Rule("budget_exceeded", "the posting exceeds the budget").WithParams(append(params, "can_override", canOverride)...)
			}
			outcome, reason = "overridden", st.override
		}
		w.Outcome = outcome
		if err := insertEvent(ctx, q, companyID, &entryID, ev, outcome, reason, p.UserID, p.RoleCode, limit, exceeded, info); err != nil {
			return err
		}
		if outcome == "overridden" {
			if err := audit.Record(ctx, q, "budget_override", "budget", ev.BudgetID, nil, map[string]any{
				"journal_entry_id": entryID, "journal_number": info.Number, "source_type": info.SourceType, "source_id": info.SourceID,
				"budget_line_id": ev.LineID, "account": ev.AccountCode + " " + ev.AccountName, "cost_center": ev.CostCenter,
				"policy": ev.Policy, "basis": ev.Basis, "threshold_percent": ev.Threshold, "budget": ev.Budget, "limit": limit,
				"actual": ev.Actual, "requested": ev.Amount, "exceeded": exceeded, "reason": reason, "decision": "overridden"}); err != nil {
				return err
			}
		}
		st.mu.Lock()
		st.warnings = append(st.warnings, w)
		st.mu.Unlock()
	}
	return nil
}

// entryInfo identifies the transaction behind a budget control event.
type entryInfo struct {
	SourceType  string
	SourceID    *int64
	Number      int64
	Reference   string
	Description string
}

func insertEvent(ctx context.Context, q db.DBTX, companyID int64, entryID *int64, ev Evaluation, outcome, reason string, userID int64, roleCode string,
	limit, exceeded decimal.Decimal, src entryInfo) error {
	_, err := q.Exec(ctx, `INSERT INTO budget_control_events (company_id, journal_entry_id, rule_id, budget_id, budget_line_id, account_id,
		cost_center_id, policy, outcome, amount, budget_amount, actual_amount, reason, user_id, threshold_percent, basis, limit_amount, exceeded_amount,
		source_type, source_id, entry_reference, entry_description, role_code)
		SELECT $1, $2, $3, $4, $5, l.account_id, l.cost_center_id, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21
		FROM budget_lines l WHERE l.id = $5`,
		companyID, entryID, ev.RuleID, ev.BudgetID, ev.LineID, ev.Policy, outcome, ev.Amount, ev.Budget, ev.Actual, reason, userID,
		ev.Threshold, ev.Basis, limit, exceeded, src.SourceType, src.SourceID, src.Reference, src.Description, roleCode)
	return err
}

// CheckInput simulates a spending posting.
type CheckInput struct {
	AccountID    int64           `json:"account_id"`
	CostCenterID *int64          `json:"cost_center_id"`
	Date         civil.Date      `json:"date"`
	Amount       decimal.Decimal `json:"amount"`
}

// Check reports how a prospective posting would be treated by budget control.
func Check(ctx context.Context, q db.DBTX, companyID int64, in CheckInput, roleCode string) ([]Evaluation, error) {
	if in.Date.IsZero() {
		in.Date = civil.Today()
	}
	v := apperr.NewValidator()
	v.RequiredID("account_id", in.AccountID)
	v.Check(in.Amount.IsPositive(), "amount", "positive")
	if err := v.Err(); err != nil {
		return nil, err
	}
	var p posting
	err := q.QueryRow(ctx, `SELECT a.id, a.path, a.code, a.name, a.type IN ('expense', 'cost_of_sales') FROM accounts a WHERE a.company_id = $1 AND a.id = $2`,
		companyID, in.AccountID).Scan(&p.accountID, &p.accountPath, &p.accountCode, &p.accountName, new(bool))
	if db.IsNoRows(err) {
		v.Check(false, "account_id", "invalid")
		return nil, v.Err()
	}
	if err != nil {
		return nil, err
	}
	p.ccID, p.amount = in.CostCenterID, in.Amount
	if p.ccID != nil {
		var name string
		if err := q.QueryRow(ctx, `SELECT name FROM cost_centers WHERE company_id = $1 AND id = $2`, companyID, *p.ccID).Scan(&name); err == nil {
			p.ccName = &name
		}
	}
	out, err := evaluate(ctx, q, companyID, in.Date, []posting{p}, true, roleCode)
	if out == nil {
		out = []Evaluation{}
	}
	return out, err
}

type Event struct {
	ID               int64            `json:"id"`
	JournalEntryID   *int64           `json:"journal_entry_id"`
	JournalNumber    *int64           `json:"journal_number"`
	SourceType       string           `json:"source_type"`
	SourceID         *int64           `json:"source_id"`
	EntryReference   string           `json:"entry_reference"`
	EntryDescription string           `json:"entry_description"`
	BudgetID         *int64           `json:"budget_id"`
	BudgetName       *string          `json:"budget_name"`
	BudgetLineID     *int64           `json:"budget_line_id"`
	AccountCode      *string          `json:"account_code"`
	AccountName      *string          `json:"account_name"`
	CostCenterName   *string          `json:"cost_center_name"`
	Policy           string           `json:"policy"`
	Basis            string           `json:"basis"`
	ThresholdPercent *decimal.Decimal `json:"threshold_percent"`
	Outcome          string           `json:"outcome"`
	Amount           decimal.Decimal  `json:"amount"`
	BudgetAmount     decimal.Decimal  `json:"budget_amount"`
	LimitAmount      *decimal.Decimal `json:"limit_amount"`
	ActualAmount     decimal.Decimal  `json:"actual_amount"`
	ExceededAmount   *decimal.Decimal `json:"exceeded_amount"`
	Reason           string           `json:"reason"`
	UserName         *string          `json:"user_name"`
	RoleCode         string           `json:"role_code"`
	CreatedAt        time.Time        `json:"created_at"`
}

const eventCols = `ev.id, ev.journal_entry_id, je.number, ev.source_type, ev.source_id, ev.entry_reference, ev.entry_description, ev.budget_id, b.name,
	ev.budget_line_id, a.code, a.name, cc.name, ev.policy, ev.basis, ev.threshold_percent, ev.outcome, ev.amount, ev.budget_amount, ev.limit_amount,
	ev.actual_amount, ev.exceeded_amount, ev.reason, u.full_name, ev.role_code, ev.created_at`

const eventJoins = ` FROM budget_control_events ev LEFT JOIN journal_entries je ON je.id = ev.journal_entry_id LEFT JOIN budgets b ON b.id = ev.budget_id
	LEFT JOIN accounts a ON a.id = ev.account_id LEFT JOIN cost_centers cc ON cc.id = ev.cost_center_id LEFT JOIN users u ON u.id = ev.user_id`

func scanEvent(s rowScanner, e *Event) error {
	return s.Scan(&e.ID, &e.JournalEntryID, &e.JournalNumber, &e.SourceType, &e.SourceID, &e.EntryReference, &e.EntryDescription, &e.BudgetID,
		&e.BudgetName, &e.BudgetLineID, &e.AccountCode, &e.AccountName, &e.CostCenterName, &e.Policy, &e.Basis, &e.ThresholdPercent, &e.Outcome,
		&e.Amount, &e.BudgetAmount, &e.LimitAmount, &e.ActualAmount, &e.ExceededAmount, &e.Reason, &e.UserName, &e.RoleCode, &e.CreatedAt)
}

func ListEvents(ctx context.Context, q db.DBTX, companyID int64, limit int) ([]Event, error) {
	rows, err := q.Query(ctx, `SELECT `+eventCols+eventJoins+` WHERE ev.company_id = $1 ORDER BY ev.created_at DESC, ev.id DESC LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := scanEvent(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func GetEvent(ctx context.Context, q db.DBTX, companyID, id int64) (*Event, error) {
	var e Event
	err := scanEvent(q.QueryRow(ctx, `SELECT `+eventCols+eventJoins+` WHERE ev.company_id = $1 AND ev.id = $2`, companyID, id), &e)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("budget_event")
	}
	return &e, err
}
