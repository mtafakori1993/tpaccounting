package agent

// Invented-value guard (D64). Every value of a model-written plan that must come from
// the application or from the user is checked deterministically before the plan is
// shown. Nothing unresolved is ever replaced by a guessed id or literal text: the plan is
// sent back ("read X first") or the user is asked.
//
//	references   *_id fields in the body and in line items: a $ref to an earlier step, or
//	             an id the matching resource's list/read returned in this work
//	uploads      upload_id: one of the user's own files of this request
//	identifiers  check number, Sayadi id, national id, IBAN, card/account number: their
//	             digits appear in the user's words, the document or the application's data
//	check type   received/issued follows from the user's words
//	dates        a check's due date is a date the user stated; issue/received dates are a
//	             stated date or today
//	numbers      rates, percents, quantities, discounts, days, hours, brackets: stated by
//	             the user or returned by the application (money fields: checkAmounts)

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// refTools: the resource whose records a reference field names (by field name, as it
// appears in bodies and line items). accounts.create's parent_id is an account.
var refTools = map[string][]string{
	"party_id":                {"parties.list", "parties.get"},
	"treasury_account_id":     {"treasury.accounts.list", "treasury.accounts.get"},
	"to_treasury_account_id":  {"treasury.accounts.list", "treasury.accounts.get"},
	"account_id":              {"accounts.list", "accounts.get"},
	"counter_account_id":      {"accounts.list", "accounts.get"},
	"receivable_account_id":   {"accounts.list", "accounts.get"},
	"payable_account_id":      {"accounts.list", "accounts.get"},
	"cost_center_id":          {"cost_centers.list", "cost_centers.get"},
	"project_id":              {"cost_centers.list", "cost_centers.get"},
	"category_id":             {"expenses.categories.list"},
	"warehouse_id":            {"inventory.warehouses.list"},
	"to_warehouse_id":         {"inventory.warehouses.list"},
	"product_id":              {"products.list", "products.get"},
	"employee_id":             {"employees.list", "employees.get"},
	"component_id":            {"payroll.components.list"},
	"fiscal_year_id":          {"company.fiscal_years.list", "company.fiscal_years.get"},
	"role_id":                 {"roles.list", "roles.get"},
	"budget_id":               {"budgets.list", "budgets.get"},
	"bank_id":                 {"checks.banks.list"},
	"treasury_transaction_id": {"treasury.transactions.list", "treasury.transactions.get"},
	"replacement_check_id":    {"checks.list", "checks.get"},
	"invoice_id":              {"sales.kind.list", "purchases.kind.list", "sales.kind.get", "purchases.kind.get"},
	"related_invoice_id":      {"sales.kind.list", "purchases.kind.list", "sales.kind.get", "purchases.kind.get"},
}

// createdBy: the create operation whose result is a record of the field's resource.
func createdBy(tool string) string {
	for f, tools := range refTools {
		for _, t := range tools {
			if strings.TrimSuffix(t, ".list") == strings.TrimSuffix(tool, ".create") {
				return f
			}
		}
	}
	return ""
}

// userIdentifiers: strings whose digits only the user (or the document/app) can know.
var userIdentifiers = map[string]bool{"check_number": true, "serial_number": true, "sayadi_id": true, "national_id": true, "iban": true,
	"card_number": true, "account_number": true, "insurance_number": true}

// skipNumbers: numeric fields that are layout or bookkeeping, not business values.
func skipNumber(op *catalog.Operation, key string) bool {
	if strings.HasSuffix(key, "_mm") || strings.HasSuffix(key, "_pt") || key == "seq" || key == "amount_decimals" || key == "number" ||
		key == "id" || strings.HasSuffix(key, "_id") || key == "year" || key == "month" {
		return true
	}
	return moneyFields[key] // checkAmounts (D58), unit-aware
}


// statedDates: the dates the user (or the document) gave, as Gregorian YYYY-MM-DD.
func statedDates(st *State, p *PlanState, today string) map[string]bool {
	out := map[string]bool{}
	t0, _ := time.Parse("2006-01-02", today)
	texts := append([]string{p.Goal}, p.UserTexts...)
	for _, x := range st.Extractions {
		texts = append(texts, x.Date.Value)
	}
	for _, txt := range texts {
		n := normalize.Text(normalize.Digits(txt))
		for _, d := range normalize.DatesIn(txt) {
			if d.Valid {
				out[d.Gregorian] = true
			}
		}
		for word, off := range map[string]int{"امروز": 0, "today": 0, "دیروز": -1, "yesterday": -1, "فردا": 1, "tomorrow": 1, "پریروز": -2} {
			if strings.Contains(" "+n+" ", " "+word) {
				out[t0.AddDate(0, 0, off).Format("2006-01-02")] = true
			}
		}
	}
	return out
}

// checkTypeSaid: the check type the user's words state ("" when they do not).
func checkTypeSaid(p *PlanState) string {
	t := " " + normalize.Text(strings.Join(append([]string{p.Goal}, p.UserTexts...), " ")) + " "
	for _, w := range []string{"دریافتی", "وارده", "دریافت کردیم", "دریافت کردم", "received"} {
		if strings.Contains(t, " "+normalize.Text(w)) {
			return "received"
		}
	}
	for _, w := range []string{"صادره", "پرداختی", "صادر کن", "صادر کردیم", "در وجه", "issued", "issue"} {
		if strings.Contains(t, " "+normalize.Text(w)) {
			return "issued"
		}
	}
	return ""
}

// refIssue is a reference the application has not returned in this work (yet).
type refIssue struct {
	Step, Key, Val string
	Tools          []string
}

func (r refIssue) String() string {
	return fmt.Sprintf("step %s: %s=%s was not returned by %s in this work — read it first and use an id from the result, never a guessed one", r.Step, r.Key, r.Val, r.Tools[0])
}

// guardValues checks the new steps of a model-written plan: references not (yet)
// grounded, other problems, and the fields only the user can supply.
func guardValues(st *State, p *PlanState, steps []PlanStep, today string, cfg *tools.CompanyConfig) ([]refIssue, string, []string) {
	// what the application returned in this work, per tool
	seenBy := map[string][]string{}
	if cfg != nil {
		// the company configuration the assistant loaded from the application
		add := func(tool string, v any) {
			b, _ := json.Marshal(v)
			seenBy[tool] = append(seenBy[tool], string(b))
		}
		add("treasury.accounts.list", cfg.TreasuryAccounts)
		add("expenses.categories.list", cfg.ExpenseCategories)
		add("cost_centers.list", cfg.Projects)
		add("cost_centers.list", cfg.CostCenters)
		add("company.fiscal_years.list", cfg.FiscalYears)
	}
	var appData []string
	for _, r := range p.Reads {
		txt := r.Result
		if len(r.Raw) > 0 {
			txt = string(r.Raw)
		}
		seenBy[r.Tool] = append(seenBy[r.Tool], txt)
		appData = append(appData, txt)
	}
	for _, s := range p.Steps {
		if s.Status == "done" {
			appData = append(appData, string(s.Result))
			if f := createdBy(s.Tool); f != "" {
				for _, tool := range refTools[f] {
					seenBy[tool] = append(seenBy[tool], string(s.Result))
				}
			}
		}
	}
	userDigits := map[string]bool{}
	for _, txt := range append(append(append([]string{p.Goal}, p.UserTexts...), appData...), extractionTexts(st)...) {
		for _, m := range digitsRe.FindAllString(strings.ReplaceAll(normalize.Digits(normalize.ReplaceNumberWords(txt)), ",", ""), -1) {
			userDigits[canonNum(m)] = true
			userDigits[strings.TrimLeft(m, "0")] = true
		}
	}
	uploads := map[string]bool{}
	for _, u := range st.Uploads {
		uploads[u.ID] = true
	}
	dates := statedDates(st, p, today)
	var problems, askUser []string
	var issues []refIssue
	for _, s := range steps {
		op, ok := catalog.Load().Get(s.Tool)
		if !ok || op.IsRead() || len(s.Args.Body) == 0 {
			continue
		}
		var body any
		if json.Unmarshal(s.Args.Body, &body) != nil {
			continue
		}
		var walk func(key string, v any)
		walk = func(key string, v any) {
			switch x := v.(type) {
			case map[string]any:
				for k, y := range x {
					walk(k, y)
				}
				return
			case []any:
				for _, y := range x {
					walk(key, y)
				}
				return
			case nil, bool:
				return
			}
			val := strings.TrimSpace(fmt.Sprint(v))
			if f, ok := v.(float64); ok {
				val = strconv.FormatFloat(f, 'f', -1, 64)
			}
			if val == "" || refRe.MatchString(val) {
				return // empty, or produced by an earlier step of this plan
			}
			switch {
			case nameKeys[key] && (op.Kind == "create" || op.Kind == "update"):
				// a name the model misspelled from the user's words (D64: «زهرا معتمد» for
				// the user's «زهرا معتضد» was about to become a NEW person)
				if wrong, right := misspelledName(val, p); wrong != "" {
					problems = append(problems, fmt.Sprintf("step %s: %s «%s» — the user wrote «%s»; use the user's spelling exactly", s.ID, key, wrong, right))
				}
			case key == "upload_id":
				if !uploads[val] {
					problems = append(problems, fmt.Sprintf("step %s: upload_id %s is not one of the user's files", s.ID, val))
				}
			case refTools[key] != nil:
				if val == "0" {
					return
				}
				grounded := false
				for _, tool := range refTools[key] {
					for _, txt := range seenBy[tool] {
						grounded = grounded || idSeen(txt, val)
					}
				}
				if !grounded {
					issues = append(issues, refIssue{Step: s.ID, Key: key, Val: val, Tools: refTools[key]})
				}
			case userIdentifiers[key]:
				d := strings.TrimLeft(normalize.Digits(strings.ReplaceAll(val, " ", "")), "0")
				d = strings.TrimPrefix(strings.ToUpper(d), "IR")
				if d != "" && !userDigits[d] {
					askUser = append(askUser, key)
				}
			case key == "check_type" && op.ID == "checks.create":
				if said := checkTypeSaid(p); said == "" {
					askUser = append(askUser, "check_type")
				} else if said != val {
					problems = append(problems, fmt.Sprintf("step %s: check_type=%s but the user said %s", s.ID, val, said))
				}
			case op.ID == "checks.create" && key == "due_date":
				if !dates[val] {
					askUser = append(askUser, "due_date")
				}
			case op.ID == "checks.create" && (key == "issue_date" || key == "received_date"):
				if !dates[val] && val != today {
					askUser = append(askUser, key)
				}
			default:
				if _, isNum := v.(float64); !isNum {
					if _, err := strconv.ParseFloat(normalize.Digits(val), 64); err != nil || !isNumericString(v) {
						return
					}
				}
				if skipNumber(op, key) {
					return
				}
				n := canonNum(val)
				if n == "0" || n == "1" || userDigits[n] {
					return // 0, a single unit, or a number the user/app gave
				}
				askUser = append(askUser, key)
			}
		}
		walk("", body)
	}
	sort.Strings(problems)
	return issues, strings.Join(dedupe(problems), "; "), dedupe(askUser)
}

// isNumericString: a JSON string that holds only a number ("12.5"), not a name with digits.
func isNumericString(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	_, err := strconv.ParseFloat(strings.ReplaceAll(normalize.Digits(s), ",", ""), 64)
	return err == nil
}

func extractionTexts(st *State) []string {
	var out []string
	for _, x := range st.Extractions {
		b, _ := json.Marshal(x)
		out = append(out, string(b))
	}
	return out
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// canonNum renders a number the same way wherever it was written ("012.50" → "12.5").
func canonNum(s string) string {
	f, err := strconv.ParseFloat(strings.ReplaceAll(normalize.Digits(s), ",", ""), 64)
	if err != nil {
		return s
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// nameKeys: body fields that carry a name the user wrote.
var nameKeys = map[string]bool{"name": true, "full_name": true, "first_name": true, "last_name": true, "title": true, "account_holder": true}

// misspelledName returns a word of v that the user did not write but that is within edit
// distance 2 of a word they did (and that word), or "".
func misspelledName(v string, p *PlanState) (string, string) {
	var said []string
	for _, t := range append([]string{p.Goal}, p.UserTexts...) {
		said = append(said, strings.Fields(normalize.Text(t))...)
	}
	has := map[string]bool{}
	for _, w := range said {
		has[w] = true
	}
	for _, w := range strings.Fields(normalize.Text(v)) {
		if has[w] || len([]rune(w)) < 4 {
			continue
		}
		for _, s := range said {
			if len([]rune(s)) >= 4 && editDistance(w, s) <= 1 {
				return w, s
			}
		}
	}
	return "", ""
}

// impossibleDate: a date the user wrote that no calendar has («۱۴۰۵/۱۳/۴۰», «۳۱ اسفند ۱۴۰۴»).
func impossibleDate(text string) string {
	for _, d := range normalize.DatesIn(text) {
		if !d.Valid {
			return d.Raw
		}
	}
	return ""
}
