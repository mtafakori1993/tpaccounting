package budgets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

const (
	StatusDraft     = "draft"
	StatusSubmitted = "submitted"
	StatusApproved  = "approved"
	StatusActive    = "active"
	StatusClosed    = "closed"
	StatusArchived  = "archived"
)

type Budget struct {
	ID             int64           `json:"id"`
	FiscalYearID   int64           `json:"fiscal_year_id"`
	FiscalYearName string          `json:"fiscal_year_name"`
	RootID         int64           `json:"root_id"`
	PreviousID     *int64          `json:"previous_id"`
	Version        int             `json:"version"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	NameEn         string          `json:"name_en"`
	Description    string          `json:"description"`
	CostCenterID   *int64          `json:"cost_center_id"`
	CostCenterName *string         `json:"cost_center_name"`
	RevisionReason string          `json:"revision_reason"`
	Status         string          `json:"status"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	ApprovedAt     *time.Time      `json:"approved_at"`
	ApprovedByName *string         `json:"approved_by_name"`
	ActivatedAt    *time.Time      `json:"activated_at"`
	ClosedAt       *time.Time      `json:"closed_at"`
	CreatedByName  *string         `json:"created_by_name"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ExpenseTotal   decimal.Decimal `json:"expense_total"`
	IncomeTotal    decimal.Decimal `json:"income_total"`
	OtherTotal     decimal.Decimal `json:"other_total"`
	LineCount      int             `json:"line_count"`
	LatestVersion  int             `json:"latest_version"`
	AllowedActions []string        `json:"allowed_actions"`
	Periods        []PeriodInfo    `json:"periods,omitempty"`
	Lines          []Line          `json:"lines,omitempty"`
	Versions       []VersionInfo   `json:"versions,omitempty"`
}

type PeriodInfo struct {
	Seq       int        `json:"seq"`
	Name      string     `json:"name"`
	StartDate civil.Date `json:"start_date"`
	EndDate   civil.Date `json:"end_date"`
}

type VersionInfo struct {
	ID        int64     `json:"id"`
	Version   int       `json:"version"`
	Status    string    `json:"status"`
	Reason    string    `json:"revision_reason"`
	CreatedAt time.Time `json:"created_at"`
}

type Line struct {
	ID               int64             `json:"id"`
	LineNo           int               `json:"line_no"`
	AccountID        int64             `json:"account_id"`
	AccountCode      string            `json:"account_code"`
	AccountName      string            `json:"account_name"`
	AccountNameEn    string            `json:"account_name_en"`
	AccountType      string            `json:"account_type"`
	CostCenterID     *int64            `json:"cost_center_id"`
	CostCenterName   *string           `json:"cost_center_name"`
	CostCenterKind   *string           `json:"cost_center_kind"`
	AllocationMethod string            `json:"allocation_method"`
	AnnualAmount     decimal.Decimal   `json:"annual_amount"`
	Notes            string            `json:"notes"`
	Amounts          []decimal.Decimal `json:"amounts"`
}

// Kind classifies a line by its account type.
func Kind(accountType string) string {
	switch accountType {
	case "revenue":
		return "income"
	case "expense", "cost_of_sales":
		return "expense"
	}
	return "other"
}

const budgetCols = `b.id, b.fiscal_year_id, fy.name, COALESCE(b.root_id, b.id), b.previous_id, b.version, b.code, b.name, b.name_en, b.description,
	b.cost_center_id, cc.name, b.revision_reason, b.status, b.submitted_at, b.approved_at, au.full_name, b.activated_at, b.closed_at, cu.full_name,
	b.created_at, b.updated_at,
	COALESCE((SELECT SUM(l.annual_amount) FROM budget_lines l JOIN accounts a ON a.id = l.account_id WHERE l.budget_id = b.id AND a.type IN ('expense', 'cost_of_sales')), 0),
	COALESCE((SELECT SUM(l.annual_amount) FROM budget_lines l JOIN accounts a ON a.id = l.account_id WHERE l.budget_id = b.id AND a.type = 'revenue'), 0),
	COALESCE((SELECT SUM(l.annual_amount) FROM budget_lines l JOIN accounts a ON a.id = l.account_id WHERE l.budget_id = b.id AND a.type NOT IN ('expense', 'cost_of_sales', 'revenue')), 0),
	(SELECT count(*) FROM budget_lines l WHERE l.budget_id = b.id),
	(SELECT max(v.version) FROM budgets v WHERE COALESCE(v.root_id, v.id) = COALESCE(b.root_id, b.id))`

const budgetJoins = ` FROM budgets b JOIN fiscal_years fy ON fy.id = b.fiscal_year_id LEFT JOIN cost_centers cc ON cc.id = b.cost_center_id
	LEFT JOIN users au ON au.id = b.approved_by LEFT JOIN users cu ON cu.id = b.created_by`

type rowScanner interface{ Scan(...any) error }

func scanBudget(s rowScanner, x *Budget) error {
	err := s.Scan(&x.ID, &x.FiscalYearID, &x.FiscalYearName, &x.RootID, &x.PreviousID, &x.Version, &x.Code, &x.Name, &x.NameEn, &x.Description,
		&x.CostCenterID, &x.CostCenterName, &x.RevisionReason, &x.Status, &x.SubmittedAt, &x.ApprovedAt, &x.ApprovedByName, &x.ActivatedAt,
		&x.ClosedAt, &x.CreatedByName, &x.CreatedAt, &x.UpdatedAt, &x.ExpenseTotal, &x.IncomeTotal, &x.OtherTotal, &x.LineCount, &x.LatestVersion)
	x.AllowedActions = Actions(x.Status, x.Version == x.LatestVersion)
	return err
}

// workflow: action → allowed from-statuses, resulting status and permission.
var workflow = map[string]struct {
	from []string
	to   string
	perm string
}{
	"submit":   {[]string{StatusDraft}, StatusSubmitted, "budgets.submit"},
	"approve":  {[]string{StatusSubmitted}, StatusApproved, "budgets.approve"},
	"reject":   {[]string{StatusSubmitted}, StatusDraft, "budgets.approve"},
	"activate": {[]string{StatusApproved}, StatusActive, "budgets.activate"},
	"close":    {[]string{StatusActive}, StatusClosed, "budgets.close"},
	"archive":  {[]string{StatusApproved, StatusClosed}, StatusArchived, "budgets.close"},
}

var actionOrder = []string{"submit", "approve", "reject", "activate", "revise", "close", "archive"}

// Actions lists the workflow actions available for a budget version.
func Actions(status string, latest bool) []string {
	out := []string{}
	for _, a := range actionOrder {
		if a == "revise" {
			if latest && (status == StatusApproved || status == StatusActive || status == StatusClosed) {
				out = append(out, a)
			}
			continue
		}
		for _, f := range workflow[a].from {
			if f == status {
				out = append(out, a)
			}
		}
	}
	return out
}

// Permission returns the permission needed for a workflow action.
func Permission(action string) string {
	if action == "revise" {
		return "budgets.create"
	}
	if w, ok := workflow[action]; ok {
		return w.perm
	}
	return "budgets.edit"
}

func Get(ctx context.Context, q db.DBTX, companyID, id int64, withLines bool) (*Budget, error) {
	var x Budget
	err := scanBudget(q.QueryRow(ctx, `SELECT `+budgetCols+budgetJoins+` WHERE b.company_id = $1 AND b.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("budget")
	}
	if err != nil {
		return nil, err
	}
	if !withLines {
		return &x, nil
	}
	if x.Periods, err = Periods(ctx, q, x.FiscalYearID); err != nil {
		return nil, err
	}
	if x.Lines, err = loadLines(ctx, q, id, len(x.Periods)); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT id, version, status, revision_reason, created_at FROM budgets WHERE COALESCE(root_id, id) = $1 ORDER BY version`, x.RootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v VersionInfo
		if err := rows.Scan(&v.ID, &v.Version, &v.Status, &v.Reason, &v.CreatedAt); err != nil {
			return nil, err
		}
		x.Versions = append(x.Versions, v)
	}
	return &x, rows.Err()
}

// Periods returns the accounting periods of a fiscal year in order.
func Periods(ctx context.Context, q db.DBTX, fiscalYearID int64) ([]PeriodInfo, error) {
	rows, err := q.Query(ctx, `SELECT seq, name, start_date, end_date FROM accounting_periods WHERE fiscal_year_id = $1 ORDER BY seq`, fiscalYearID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PeriodInfo{}
	for rows.Next() {
		var p PeriodInfo
		if err := rows.Scan(&p.Seq, &p.Name, &p.StartDate, &p.EndDate); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

const lineCols = `l.id, l.line_no, l.account_id, a.code, a.name, a.name_en, a.type, l.cost_center_id, cc.name, cc.kind, l.allocation_method,
	l.annual_amount, l.notes`

func loadLines(ctx context.Context, q db.DBTX, budgetID int64, n int) ([]Line, error) {
	rows, err := q.Query(ctx, `SELECT `+lineCols+` FROM budget_lines l JOIN accounts a ON a.id = l.account_id
		LEFT JOIN cost_centers cc ON cc.id = l.cost_center_id WHERE l.budget_id = $1 ORDER BY l.line_no, l.id`, budgetID)
	if err != nil {
		return nil, err
	}
	var out []Line
	idx := map[int64]int{}
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.LineNo, &l.AccountID, &l.AccountCode, &l.AccountName, &l.AccountNameEn, &l.AccountType, &l.CostCenterID,
			&l.CostCenterName, &l.CostCenterKind, &l.AllocationMethod, &l.AnnualAmount, &l.Notes); err != nil {
			rows.Close()
			return nil, err
		}
		l.Amounts = make([]decimal.Decimal, n)
		idx[l.ID] = len(out)
		out = append(out, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	arows, err := q.Query(ctx, `SELECT m.line_id, m.seq, m.amount FROM budget_line_amounts m JOIN budget_lines l ON l.id = m.line_id WHERE l.budget_id = $1`, budgetID)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var lid int64
		var seq int
		var amt decimal.Decimal
		if err := arows.Scan(&lid, &seq, &amt); err != nil {
			return nil, err
		}
		if i, ok := idx[lid]; ok && seq >= 1 && seq <= n {
			out[i].Amounts[seq-1] = amt
		}
	}
	if out == nil {
		out = []Line{}
	}
	return out, arows.Err()
}

type ListFilter struct {
	FiscalYearID int64
	Status       string
	LatestOnly   bool
}

func List(ctx context.Context, q db.DBTX, companyID int64, lf ListFilter) ([]Budget, error) {
	f := db.NewFilter("b.company_id = $1", companyID)
	if lf.FiscalYearID > 0 {
		f.Add("b.fiscal_year_id = $N", lf.FiscalYearID)
	}
	if lf.Status != "" {
		f.Add("b.status = $N", lf.Status)
	}
	if lf.LatestOnly {
		f.AddRaw("b.version = (SELECT max(v.version) FROM budgets v WHERE COALESCE(v.root_id, v.id) = COALESCE(b.root_id, b.id))")
	}
	rows, err := q.Query(ctx, `SELECT `+budgetCols+budgetJoins+` WHERE `+f.Where()+` ORDER BY fy.start_date DESC, b.code, b.version DESC`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Budget{}
	for rows.Next() {
		var x Budget
		if err := scanBudget(rows, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- Input ----

type LineInput struct {
	AccountID        int64           `json:"account_id"`
	CostCenterID     *int64          `json:"cost_center_id"`
	AllocationMethod string          `json:"allocation_method"`
	AnnualAmount     decimal.Decimal `json:"annual_amount"`
	// Percentage method: one share per period (sums to 100).
	Percents []decimal.Decimal `json:"percents"`
	// Manual method: one amount per period.
	Amounts []decimal.Decimal `json:"amounts"`
	Notes   string            `json:"notes"`
}

type Input struct {
	FiscalYearID int64       `json:"fiscal_year_id"`
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	NameEn       string      `json:"name_en"`
	Description  string      `json:"description"`
	CostCenterID *int64      `json:"cost_center_id"`
	Lines        []LineInput `json:"lines"`
}

// historyWeights returns last year's actual per period for an account/cost center, used as allocation weights.
func historyWeights(ctx context.Context, q db.DBTX, companyID, fiscalYearID, accountID int64, ccID *int64, n int) ([]decimal.Decimal, error) {
	var prevID int64
	err := q.QueryRow(ctx, `SELECT p.id FROM fiscal_years p JOIN fiscal_years c ON c.id = $2
		WHERE p.company_id = $1 AND p.end_date < c.start_date ORDER BY p.end_date DESC LIMIT 1`, companyID, fiscalYearID).Scan(&prevID)
	if db.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT ap.seq, SUM(CASE WHEN ba.type IN ('revenue', 'liability', 'equity') THEN l.credit - l.debit ELSE l.debit - l.credit END)
		FROM accounting_periods ap
		JOIN accounts ba ON ba.id = $3
		JOIN journal_entries e ON e.company_id = $1 AND e.posted_at IS NOT NULL AND e.source_type <> 'closing' AND e.date BETWEEN ap.start_date AND ap.end_date
		JOIN journal_entry_lines l ON l.entry_id = e.id
		JOIN accounts la ON la.id = l.account_id AND la.path LIKE ba.path || '%'
		WHERE ap.fiscal_year_id = $2 AND ($4::bigint IS NULL OR l.cost_center_id = $4)
		GROUP BY ap.seq`, companyID, prevID, accountID, ccID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	w := make([]decimal.Decimal, n)
	for rows.Next() {
		var seq int
		var amt decimal.Decimal
		if err := rows.Scan(&seq, &amt); err != nil {
			return nil, err
		}
		if seq >= 1 && seq <= n && amt.IsPositive() {
			w[seq-1] = amt
		}
	}
	return w, rows.Err()
}

// resolveLine validates a line and computes its per-period amounts.
func resolveLine(ctx context.Context, q db.DBTX, companyID, fiscalYearID int64, i int, l *LineInput, n int, places int32, v *apperr.Validator) []decimal.Decimal {
	f := fmt.Sprintf("lines.%d.", i)
	if l.AllocationMethod == "" {
		l.AllocationMethod = MethodEqual
	}
	l.Notes = strings.TrimSpace(l.Notes)
	v.RequiredID(f+"account_id", l.AccountID)
	v.Check(map[string]bool{MethodManual: true, MethodEqual: true, MethodPercentage: true, MethodHistorical: true}[l.AllocationMethod], f+"allocation_method", "invalid")
	v.MaxLen(f+"notes", l.Notes, 500)
	switch l.AllocationMethod {
	case MethodManual:
		v.Check(len(l.Amounts) == n, f+"amounts", "invalid")
		for j, a := range l.Amounts {
			v.Check(!a.IsNegative() && a.Equal(a.Round(places)), fmt.Sprintf("%samounts.%d", f, j), "invalid")
		}
		if len(l.Amounts) == n {
			l.AnnualAmount = Sum(l.Amounts)
			return append([]decimal.Decimal(nil), l.Amounts...)
		}
		return nil
	case MethodPercentage:
		v.Check(len(l.Percents) == n, f+"percents", "invalid")
		sum := Sum(l.Percents)
		for _, p := range l.Percents {
			v.Check(!p.IsNegative(), f+"percents", "invalid")
		}
		v.Check(len(l.Percents) != n || sum.Equal(hundred), f+"percents", "allocation_total")
	}
	v.Check(!l.AnnualAmount.IsNegative() && l.AnnualAmount.Equal(l.AnnualAmount.Round(places)), f+"annual_amount", "invalid")
	if !v.Valid() {
		return nil
	}
	switch l.AllocationMethod {
	case MethodPercentage:
		return Distribute(l.AnnualAmount, l.Percents, n, places)
	case MethodHistorical:
		w, err := historyWeights(ctx, q, companyID, fiscalYearID, l.AccountID, l.CostCenterID, n)
		if err != nil {
			v.Check(false, f+"allocation_method", "invalid")
			return nil
		}
		return Distribute(l.AnnualAmount, w, n, places)
	}
	return Equal(l.AnnualAmount, n, places)
}

func validate(ctx context.Context, q db.DBTX, companyID int64, in *Input) ([][]decimal.Decimal, int, error) {
	in.Code, in.Name, in.NameEn, in.Description = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.NameEn), strings.TrimSpace(in.Description)
	v := apperr.NewValidator()
	v.Required("code", in.Code)
	v.MaxLen("code", in.Code, 30)
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 200)
	v.MaxLen("description", in.Description, 2000)
	v.RequiredID("fiscal_year_id", in.FiscalYearID)
	v.Check(len(in.Lines) <= 2000, "lines", "too_many")
	if err := v.Err(); err != nil {
		return nil, 0, err
	}
	var fyOK bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM fiscal_years WHERE company_id = $1 AND id = $2)`, companyID, in.FiscalYearID).Scan(&fyOK); err != nil {
		return nil, 0, err
	}
	if !fyOK {
		v.Check(false, "fiscal_year_id", "invalid")
		return nil, 0, v.Err()
	}
	periods, err := Periods(ctx, q, in.FiscalYearID)
	if err != nil {
		return nil, 0, err
	}
	n := len(periods)
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return nil, 0, err
	}
	amounts := make([][]decimal.Decimal, len(in.Lines))
	seen := map[string]bool{}
	for i := range in.Lines {
		l := &in.Lines[i]
		key := fmt.Sprintf("%d/%d", l.AccountID, val(l.CostCenterID))
		v.Check(!seen[key], fmt.Sprintf("lines.%d.account_id", i), "duplicate_budget_line")
		seen[key] = true
		amounts[i] = resolveLine(ctx, q, companyID, in.FiscalYearID, i, l, n, places, v)
	}
	if err := v.Err(); err != nil {
		return nil, 0, err
	}
	// References: accounts and cost centers of the company.
	for i, l := range in.Lines {
		var active bool
		if err := q.QueryRow(ctx, `SELECT is_active FROM accounts WHERE company_id = $1 AND id = $2`, companyID, l.AccountID).Scan(&active); err != nil || !active {
			v.Check(false, fmt.Sprintf("lines.%d.account_id", i), "invalid")
		}
		if l.CostCenterID != nil {
			var ok bool
			if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cost_centers WHERE company_id = $1 AND id = $2)`, companyID, *l.CostCenterID).Scan(&ok); err != nil || !ok {
				v.Check(false, fmt.Sprintf("lines.%d.cost_center_id", i), "invalid")
			}
		}
	}
	if in.CostCenterID != nil {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM cost_centers WHERE company_id = $1 AND id = $2)`, companyID, *in.CostCenterID).Scan(&ok); err != nil || !ok {
			v.Check(false, "cost_center_id", "invalid")
		}
	}
	return amounts, n, v.Err()
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func writeLines(ctx context.Context, q db.DBTX, budgetID int64, lines []LineInput, amounts [][]decimal.Decimal) error {
	if _, err := q.Exec(ctx, `DELETE FROM budget_lines WHERE budget_id = $1`, budgetID); err != nil {
		return err
	}
	for i, l := range lines {
		var lid int64
		if err := q.QueryRow(ctx, `INSERT INTO budget_lines (budget_id, line_no, account_id, cost_center_id, allocation_method, annual_amount, notes)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, budgetID, i+1, l.AccountID, l.CostCenterID, l.AllocationMethod, l.AnnualAmount, l.Notes).Scan(&lid); err != nil {
			return err
		}
		for seq, a := range amounts[i] {
			if _, err := q.Exec(ctx, `INSERT INTO budget_line_amounts (line_id, seq, amount) VALUES ($1, $2, $3)`, lid, seq+1, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func Create(ctx context.Context, q db.DBTX, companyID, userID int64, in Input) (int64, error) {
	amounts, _, err := validate(ctx, q, companyID, &in)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `INSERT INTO budgets (company_id, fiscal_year_id, code, name, name_en, description, cost_center_id, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8::bigint, 0), NULLIF($8::bigint, 0)) RETURNING id`, companyID, in.FiscalYearID, in.Code, in.Name, in.NameEn, in.Description,
		in.CostCenterID, userID).Scan(&id); err != nil {
		return 0, err
	}
	return id, writeLines(ctx, q, id, in.Lines, amounts)
}

func lock(ctx context.Context, q db.DBTX, companyID, id int64) (*Budget, error) {
	var dummy int64
	if err := q.QueryRow(ctx, `SELECT id FROM budgets WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&dummy); err != nil {
		if db.IsNoRows(err) {
			return nil, apperr.NotFound("budget")
		}
		return nil, err
	}
	return Get(ctx, q, companyID, id, false)
}

// Update replaces a draft budget. Submitted and later versions are immutable.
func Update(ctx context.Context, q db.DBTX, companyID, userID, id int64, in Input) error {
	cur, err := lock(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if cur.Status != StatusDraft {
		return apperr.Rule("budget_locked", "only draft budgets can be changed; create a revision")
	}
	if cur.Version > 1 {
		// A revision keeps the identity of its budget.
		in.FiscalYearID, in.Code = cur.FiscalYearID, cur.Code
	}
	amounts, _, err := validate(ctx, q, companyID, &in)
	if err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE budgets SET fiscal_year_id = $3, code = $4, name = $5, name_en = $6, description = $7, cost_center_id = $8,
		updated_by = $9, updated_at = now() WHERE company_id = $1 AND id = $2`, companyID, id, in.FiscalYearID, in.Code, in.Name, in.NameEn,
		in.Description, in.CostCenterID, userID); err != nil {
		return err
	}
	return writeLines(ctx, q, id, in.Lines, amounts)
}

func Delete(ctx context.Context, q db.DBTX, companyID, id int64) error {
	cur, err := lock(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if cur.Status != StatusDraft {
		return apperr.Rule("not_deletable", "only draft budgets can be deleted")
	}
	if _, err := q.Exec(ctx, `UPDATE budgets SET previous_id = NULL WHERE previous_id = $1`, id); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `DELETE FROM budgets WHERE id = $1`, id)
	return err
}

// Transition performs a workflow action. Activating a revision archives the
// previously active version of the same budget.
func Transition(ctx context.Context, q db.DBTX, companyID, userID, id int64, action string) error {
	cur, err := lock(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	w, ok := workflow[action]
	allowed := false
	if ok {
		for _, f := range w.from {
			allowed = allowed || f == cur.Status
		}
	}
	if !allowed {
		return apperr.Rule("invalid_transition", "this action is not available in the current status").WithParams("action", action, "status", cur.Status)
	}
	switch action {
	case "submit":
		if cur.LineCount == 0 {
			return apperr.Rule("budget_empty", "add at least one budget line")
		}
	case "activate":
		if _, err := q.Exec(ctx, `UPDATE budgets SET status = 'archived', archived_at = now(), updated_at = now()
			WHERE COALESCE(root_id, id) = $1 AND status = 'active' AND id <> $2`, cur.RootID, id); err != nil {
			return err
		}
	}
	stamp := map[string]string{
		"submit": ", submitted_at = now(), submitted_by = $3", "approve": ", approved_at = now(), approved_by = $3",
		"reject": ", submitted_at = NULL, submitted_by = NULL, updated_by = $3", "activate": ", activated_at = now(), activated_by = $3",
		"close": ", closed_at = now(), closed_by = $3", "archive": ", archived_at = now(), updated_by = $3",
	}[action]
	_, err = q.Exec(ctx, `UPDATE budgets SET status = $2, updated_at = now()`+stamp+` WHERE id = $1`, id, w.to, userID)
	return err
}

// Revise creates a new draft version copying the lines of an approved, active
// or closed version. The original stays untouched until the revision is activated.
func Revise(ctx context.Context, q db.DBTX, companyID, userID, id int64, reason string) (int64, error) {
	cur, err := lock(ctx, q, companyID, id)
	if err != nil {
		return 0, err
	}
	reason = strings.TrimSpace(reason)
	v := apperr.NewValidator()
	v.Required("reason", reason)
	v.MaxLen("reason", reason, 500)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if cur.Version != cur.LatestVersion {
		return 0, apperr.Rule("revision_pending", "a newer version of this budget exists")
	}
	if cur.Status != StatusApproved && cur.Status != StatusActive && cur.Status != StatusClosed {
		return 0, apperr.Rule("invalid_transition", "only approved, active or closed budgets can be revised").WithParams("action", "revise", "status", cur.Status)
	}
	var nid int64
	if err := q.QueryRow(ctx, `INSERT INTO budgets (company_id, fiscal_year_id, root_id, previous_id, version, code, name, name_en, description,
		cost_center_id, revision_reason, created_by, updated_by)
		SELECT company_id, fiscal_year_id, COALESCE(root_id, id), id, $3, code, name, name_en, description, cost_center_id, $4, $5, $5
		FROM budgets WHERE company_id = $1 AND id = $2 RETURNING id`, companyID, id, cur.LatestVersion+1, reason, userID).Scan(&nid); err != nil {
		return 0, err
	}
	rows, err := q.Query(ctx, `SELECT id FROM budget_lines WHERE budget_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return 0, err
	}
	var lineIDs []int64
	for rows.Next() {
		var lid int64
		if err := rows.Scan(&lid); err != nil {
			rows.Close()
			return 0, err
		}
		lineIDs = append(lineIDs, lid)
	}
	rows.Close()
	for _, lid := range lineIDs {
		var newLine int64
		if err := q.QueryRow(ctx, `INSERT INTO budget_lines (budget_id, line_no, account_id, cost_center_id, allocation_method, annual_amount, notes)
			SELECT $2, line_no, account_id, cost_center_id, allocation_method, annual_amount, notes FROM budget_lines WHERE id = $1 RETURNING id`, lid, nid).Scan(&newLine); err != nil {
			return 0, err
		}
		if _, err := q.Exec(ctx, `INSERT INTO budget_line_amounts (line_id, seq, amount) SELECT $2, seq, amount FROM budget_line_amounts WHERE line_id = $1`, lid, newLine); err != nil {
			return 0, err
		}
	}
	return nid, nil
}
