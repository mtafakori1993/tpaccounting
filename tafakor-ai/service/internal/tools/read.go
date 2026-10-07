package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

func get(ctx context.Context, env Env, path string, q url.Values) ([]byte, error) {
	var raw []byte
	err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodGet, Path: path, Query: q}, &raw)
	return raw, err
}

type searchArgs struct {
	Q        string `json:"q"`
	Role     string `json:"role,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Status   string `json:"status,omitempty"`
	PartyID  int64  `json:"party_id,omitempty"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Side     string `json:"side,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Payment  string `json:"payment_status,omitempty"`
	Amount   string `json:"amount,omitempty"`
	Category int64  `json:"category_id,omitempty"`
}

func (a *searchArgs) validate() error {
	a.Q = strings.TrimSpace(normalize.Digits(a.Q))
	if len([]rune(a.Q)) > 100 {
		return fmt.Errorf("%w: query too long", ErrInvalidInput)
	}
	if a.Limit <= 0 || a.Limit > 50 {
		a.Limit = 20
	}
	for _, d := range []string{a.From, a.To} {
		if d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return fmt.Errorf("%w: dates must be YYYY-MM-DD", ErrInvalidInput)
			}
		}
	}
	return nil
}

func readTools() []Tool {
	return []Tool{
		{Name: "search_customer", Kind: Read, Permissions: []string{"parties.view"}, Run: searchParties("customer")},
		{Name: "search_supplier", Kind: Read, Permissions: []string{"parties.view"}, Run: searchParties("supplier")},
		{Name: "search_party", Kind: Read, Permissions: []string{"parties.view"}, Run: searchParties("")},
		{Name: "search_project", Kind: Read, Permissions: []string{"cost_centers.view"}, Run: searchProjects},
		{Name: "search_account", Kind: Read, Run: searchAccounts},
		{Name: "search_invoice", Kind: Read, Run: searchInvoices},
		{Name: "search_transactions", Kind: Read, Permissions: []string{"treasury.view"}, Run: searchTransactions},
		{Name: "search_expenses", Kind: Read, Permissions: []string{"expenses.view"}, Run: searchExpenses},
		{Name: "search_reports", Kind: Read, Run: searchReports},
		{Name: "get_expense_categories", Kind: Read, Permissions: []string{"expenses.view"}, Run: getExpenseCategories},
		{Name: "get_company_config", Kind: Read, Run: getCompanyConfig},
	}
}

func searchParties(role string) func(context.Context, Env, json.RawMessage) (any, error) {
	return func(ctx context.Context, env Env, args json.RawMessage) (any, error) {
		var a searchArgs
		if err := decodeStrict(args, &a); err != nil {
			return nil, err
		}
		if err := a.validate(); err != nil {
			return nil, err
		}
		q := url.Values{"page_size": {strconv.Itoa(a.Limit)}, "active": {"true"}}
		if a.Q != "" {
			q.Set("q", a.Q)
		}
		r := role
		if r == "" {
			r = a.Role
		}
		if r == "customer" || r == "supplier" || r == "other" {
			q.Set("role", r)
		}
		raw, err := get(ctx, env, "/parties", q)
		if err != nil {
			return nil, err
		}
		return decodeList[Party](raw)
	}
}

// ListAllParties loads active parties (bounded) for fuzzy matching.
func ListAllParties(ctx context.Context, env Env) ([]Party, error) {
	raw, err := get(ctx, env, "/parties", url.Values{"page_size": {"500"}, "active": {"true"}})
	if err != nil {
		return nil, err
	}
	return decodeList[Party](raw)
}

func searchProjects(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	all, err := ListCostCenters(ctx, env)
	if err != nil {
		return nil, err
	}
	var out []CostCenter
	nq := normalize.Text(a.Q)
	for _, c := range all {
		if a.Kind != "" && c.Kind != a.Kind {
			continue
		}
		if a.Kind == "" && c.Kind != "project" {
			continue
		}
		if nq == "" || strings.Contains(normalize.Text(c.Name+" "+c.NameEn+" "+c.Code), nq) {
			out = append(out, c)
		}
	}
	return out, nil
}

func ListCostCenters(ctx context.Context, env Env) ([]CostCenter, error) {
	raw, err := get(ctx, env, "/cost-centers", url.Values{"page_size": {"500"}})
	if err != nil {
		return nil, err
	}
	return decodeList[CostCenter](raw)
}

func ListTreasury(ctx context.Context, env Env) ([]TreasuryAccount, error) {
	raw, err := get(ctx, env, "/treasury/accounts", nil)
	if err != nil {
		return nil, err
	}
	out, err := decodeList[TreasuryAccount](raw)
	for i := range out {
		out[i].CardNumber = normalize.Card(out[i].CardNumber)
		out[i].AccountNumber = normalize.Card(out[i].AccountNumber)
	}
	return out, err
}

func searchAccounts(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	switch a.Kind {
	case "cash", "bank":
		if env.Me != nil && !env.Me.Can("treasury.view") {
			return nil, fmt.Errorf("%w (treasury.view)", ErrNoPermission)
		}
		all, err := ListTreasury(ctx, env)
		if err != nil {
			return nil, err
		}
		var out []TreasuryAccount
		nq := normalize.Text(a.Q)
		for _, t := range all {
			if t.Kind == a.Kind && t.IsActive && (nq == "" || strings.Contains(normalize.Text(t.Name+" "+t.BankName), nq)) {
				out = append(out, t)
			}
		}
		return out, nil
	case "", "ledger":
		if env.Me != nil && !env.Me.Can("accounts.view") {
			return nil, fmt.Errorf("%w (accounts.view)", ErrNoPermission)
		}
		q := url.Values{"postable": {"true"}, "active": {"true"}}
		if a.Q != "" {
			q.Set("q", a.Q)
		}
		raw, err := get(ctx, env, "/accounts", q)
		if err != nil {
			return nil, err
		}
		list, err := decodeList[Account](raw)
		if len(list) > a.Limit {
			list = list[:a.Limit]
		}
		return list, err
	}
	return nil, fmt.Errorf("%w: kind must be cash, bank or ledger", ErrInvalidInput)
}

func searchInvoices(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	side := "sales"
	if a.Side == "purchase" || a.Side == "purchases" {
		side = "purchases"
	}
	if env.Me != nil && !env.Me.Can(side+".view") {
		return nil, fmt.Errorf("%w (%s.view)", ErrNoPermission, side)
	}
	q := url.Values{"page_size": {strconv.Itoa(a.Limit)}}
	for k, v := range map[string]string{"q": a.Q, "status": a.Status, "payment_status": a.Payment, "from": a.From, "to": a.To} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if a.PartyID > 0 {
		q.Set("party_id", strconv.FormatInt(a.PartyID, 10))
	}
	raw, err := get(ctx, env, "/"+side+"/invoices", q)
	if err != nil {
		return nil, err
	}
	return decodeList[Invoice](raw)
}

func searchTransactions(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	q := url.Values{"page_size": {strconv.Itoa(a.Limit)}}
	for k, v := range map[string]string{"q": a.Q, "kind": a.Kind, "status": a.Status, "from": a.From, "to": a.To} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if a.PartyID > 0 {
		q.Set("party_id", strconv.FormatInt(a.PartyID, 10))
	}
	raw, err := get(ctx, env, "/treasury/transactions", q)
	if err != nil {
		return nil, err
	}
	return decodeList[Transaction](raw)
}

func searchExpenses(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	q := url.Values{"page_size": {strconv.Itoa(a.Limit)}}
	for k, v := range map[string]string{"q": a.Q, "status": a.Status, "from": a.From, "to": a.To} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if a.Category > 0 {
		q.Set("category_id", strconv.FormatInt(a.Category, 10))
	}
	raw, err := get(ctx, env, "/expenses", q)
	if err != nil {
		return nil, err
	}
	return decodeList[Expense](raw)
}

// Reports the assistant may open or summarise (allowlist; maps to /reports/{name}).
var reportNames = map[string]string{
	"receivables": "reports.view", "payables": "reports.view", "expenses": "reports.view", "profit-loss": "reports.view",
	"balance-sheet": "reports.view", "trial-balance": "reports.view", "cash-position": "reports.view",
	"sales": "reports.view", "purchases": "reports.view", "cost-centers": "reports.view", "journal": "reports.view",
}

func searchReports(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a searchArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(a.Kind)
	if _, ok := reportNames[name]; !ok {
		names := make([]string, 0, len(reportNames))
		for n := range reportNames {
			names = append(names, n)
		}
		return map[string]any{"available": names}, nil
	}
	q := url.Values{}
	for k, v := range map[string]string{"from": a.From, "to": a.To} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if name == "receivables" || name == "payables" || name == "balance-sheet" || name == "cash-position" {
		if a.To != "" {
			q.Set("as_of", a.To)
		}
	}
	raw, err := get(ctx, env, "/reports/"+name, q)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return map[string]any{"report": name, "route": "/reports/" + name, "data": v}, nil
}

func getExpenseCategories(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	return ListExpenseCategories(ctx, env)
}

func ListExpenseCategories(ctx context.Context, env Env) ([]ExpenseCategory, error) {
	raw, err := get(ctx, env, "/expenses/categories", nil)
	if err != nil {
		return nil, err
	}
	return decodeList[ExpenseCategory](raw)
}

func ListFiscalYears(ctx context.Context, env Env) ([]FiscalYear, error) {
	raw, err := get(ctx, env, "/company/fiscal-years", nil)
	if err != nil {
		return nil, err
	}
	years, err := decodeList[FiscalYear](raw)
	if err != nil {
		return nil, err
	}
	for i := range years {
		if years[i].Status != "open" || len(years[i].Periods) > 0 {
			continue
		}
		var fy FiscalYear
		if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodGet, Path: fmt.Sprintf("/company/fiscal-years/%d", years[i].ID)}, &fy); err == nil {
			years[i].Periods = fy.Periods
		}
	}
	return years, nil
}

// LoadCompanyConfig gathers everything classification may choose from. Sections the
// user cannot view are left empty (permissions are inherited, never bypassed).
func LoadCompanyConfig(ctx context.Context, env Env) (*CompanyConfig, error) {
	c := &CompanyConfig{}
	if env.Me != nil {
		c.CompanyName, c.BaseCurrency, c.AmountDecimals, c.Calendar = env.Me.Company.Name, env.Me.Company.BaseCurrency, env.Me.Company.AmountDecimals, env.Me.Company.Calendar
	}
	can := func(p string) bool { return env.Me == nil || env.Me.Can(p) }
	var err error
	if can("treasury.view") {
		if c.TreasuryAccounts, err = ListTreasury(ctx, env); err != nil {
			return nil, err
		}
	}
	if can("expenses.view") {
		if c.ExpenseCategories, err = ListExpenseCategories(ctx, env); err != nil {
			return nil, err
		}
	}
	if can("cost_centers.view") {
		all, err := ListCostCenters(ctx, env)
		if err != nil {
			return nil, err
		}
		for _, cc := range all {
			if !cc.IsActive {
				continue
			}
			if cc.Kind == "project" {
				c.Projects = append(c.Projects, cc)
			} else {
				c.CostCenters = append(c.CostCenters, cc)
			}
		}
	}
	if can("company.view") {
		if c.FiscalYears, err = ListFiscalYears(ctx, env); err != nil {
			return nil, err
		}
	}
	if can("budgets.view") {
		if raw, err := get(ctx, env, "/budgets", url.Values{"status": {"active"}}); err == nil {
			var any []json.RawMessage
			if l, err := decodeList[json.RawMessage](raw); err == nil {
				any = l
			}
			c.BudgetsActive = len(any) > 0
		}
	}
	return c, nil
}

func getCompanyConfig(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	return LoadCompanyConfig(ctx, env)
}

// PeriodState reports whether a date can be posted to: in an open fiscal year and an unlocked period.
func PeriodState(years []FiscalYear, date string) (ok bool, reason string) {
	for _, y := range years {
		if date < y.StartDate[:min(10, len(y.StartDate))] || date > y.EndDate[:min(10, len(y.EndDate))] {
			continue
		}
		if y.Status != "open" {
			return false, "fiscal_year_closed"
		}
		for _, p := range y.Periods {
			if date >= p.StartDate[:min(10, len(p.StartDate))] && date <= p.EndDate[:min(10, len(p.EndDate))] && p.IsLocked {
				return false, "period_locked"
			}
		}
		return true, ""
	}
	return false, "no_fiscal_year"
}
