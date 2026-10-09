package agent

// Deterministic reads of configuration the user asks to SEE (D61). «قواعد حقوق رو بگو»
// was answered "they are under Settings → …" by the chat path: the user asked for the
// rules, not their location. These read the application's own state and report exactly
// what it returned — including that nothing is configured.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

const (
	skillPayrollRules   = "read_payroll_rules"
	skillPayrollPosting = "read_payroll_posting"
)

type payrollRules struct {
	TaxTables []struct {
		ID            int64   `json:"id"`
		Name          string  `json:"name"`
		EffectiveFrom string  `json:"effective_from"`
		EffectiveTo   *string `json:"effective_to"`
		Mode          string  `json:"mode"`
		Exemption     string  `json:"exemption"`
		Brackets      []struct {
			Upto *string `json:"upto"`
			Rate string  `json:"rate"`
		} `json:"brackets"`
		IsActive bool `json:"is_active"`
	} `json:"tax_tables"`
	InsuranceRules []struct {
		ID               int64   `json:"id"`
		Name             string  `json:"name"`
		EffectiveFrom    string  `json:"effective_from"`
		EffectiveTo      *string `json:"effective_to"`
		EmployeeRate     string  `json:"employee_rate"`
		EmployerRate     string  `json:"employer_rate"`
		UnemploymentRate string  `json:"unemployment_rate"`
		Ceiling          string  `json:"ceiling"`
		IsActive         bool    `json:"is_active"`
	} `json:"insurance_rules"`
	Settings map[string]string `json:"settings"`
}

func (st *State) num(s string) string {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return s
	}
	out := groupThousands(d.String())
	if st.Lang == "fa" {
		return persianDigits(out)
	}
	return out
}

func groupThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.Index(s, "."); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteRune(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String() + frac
	}
	return b.String() + frac
}

func (st *State) period(from string, to *string) string {
	j := func(d string) string {
		if st.Lang == "fa" {
			return persianDigits(jalaliOf(d))
		}
		return jalaliOf(d)
	}
	if to == nil || *to == "" {
		return tr(st.Lang, "from "+j(from), "از "+j(from))
	}
	return tr(st.Lang, "from "+j(from)+" to "+j(*to), "از "+j(from)+" تا "+j(*to))
}

func (st *State) active(b bool) string {
	if b {
		return tr(st.Lang, "active", "فعال")
	}
	return tr(st.Lang, "inactive", "غیرفعال")
}

// readPayrollRules answers «قواعد حقوق رو بگو» from GET /payroll/rules.
func (a *Agent) readPayrollRules(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	out, err := a.Tools.Call(ctx, a.env(u), "payroll.rules.list", json.RawMessage(`{}`), nil)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	e.Emit("tool", map[string]any{"tool": "payroll.rules.list", "ok": true, "kind": "read"})
	raw, _ := json.Marshal(out)
	st.Plan.Reads = append(st.Plan.Reads, ReadRecord{Tool: "payroll.rules.list", OK: true, Raw: raw, Result: truncate(string(raw), 3500)})
	var r payrollRules
	_ = json.Unmarshal(raw, &r)
	if len(r.TaxTables) == 0 && len(r.InsuranceRules) == 0 {
		a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, "No payroll rules are configured at the moment (no tax table and no insurance rule).",
			"در حال حاضر قاعده‌ای برای حقوق ثبت نشده است (نه جدول مالیات و نه قاعده بیمه)."))
		return
	}
	pct := func(s string) string { return st.num(s) + "٪" }
	var b strings.Builder
	b.WriteString(tr(st.Lang, "Payroll rules configured in the application:\n", "قواعد حقوق ثبت‌شده در نرم‌افزار:\n"))
	if len(r.TaxTables) > 0 {
		b.WriteString(tr(st.Lang, "\nTax tables:\n", "\nجدول‌های مالیات حقوق:\n"))
		for _, x := range r.TaxTables {
			mode := map[string]string{"monthly": tr(st.Lang, "monthly", "ماهانه"), "annual": tr(st.Lang, "annual", "سالانه")}[x.Mode]
			if mode == "" {
				mode = x.Mode
			}
			fmt.Fprintf(&b, "• %s — %s، %s، %s: %s (%s)\n", x.Name, st.period(x.EffectiveFrom, x.EffectiveTo), mode,
				tr(st.Lang, "exemption", "معافیت"), st.num(x.Exemption), st.active(x.IsActive))
			for _, br := range x.Brackets {
				if br.Upto == nil {
					fmt.Fprintf(&b, "   – %s: %s\n", tr(st.Lang, "above that", "بیشتر از آن"), pct(br.Rate))
				} else {
					fmt.Fprintf(&b, "   – %s %s: %s\n", tr(st.Lang, "up to", "تا"), st.num(*br.Upto), pct(br.Rate))
				}
			}
		}
	}
	if len(r.InsuranceRules) > 0 {
		b.WriteString(tr(st.Lang, "\nInsurance rules:\n", "\nقواعد بیمه:\n"))
		for _, x := range r.InsuranceRules {
			ceiling := st.num(x.Ceiling)
			if d, err := decimal.NewFromString(x.Ceiling); err == nil && d.IsZero() {
				ceiling = tr(st.Lang, "none", "ندارد")
			}
			fmt.Fprintf(&b, "• %s — %s: %s %s، %s %s، %s %s، %s: %s (%s)\n", x.Name, st.period(x.EffectiveFrom, x.EffectiveTo),
				tr(st.Lang, "employee share", "سهم کارمند"), pct(x.EmployeeRate), tr(st.Lang, "employer share", "سهم کارفرما"), pct(x.EmployerRate),
				tr(st.Lang, "unemployment", "بیمه بیکاری"), pct(x.UnemploymentRate), tr(st.Lang, "ceiling", "سقف"), ceiling, st.active(x.IsActive))
		}
	}
	if len(r.Settings) > 0 {
		labels := map[string][2]string{"period_days": {"days per period", "روزهای هر دوره"}, "divisor": {"daily-wage divisor", "مقسوم‌علیه دستمزد روزانه"},
			"monthly_hours": {"monthly hours", "ساعات کار ماهانه"}}
		var parts []string
		for _, k := range []string{"period_days", "divisor", "monthly_hours"} {
			if v, ok := r.Settings[k]; ok {
				parts = append(parts, tr(st.Lang, labels[k][0], labels[k][1])+": "+st.num(v))
			}
		}
		if len(parts) > 0 {
			b.WriteString(tr(st.Lang, "\nSettings: ", "\nتنظیمات: ") + strings.Join(parts, tr(st.Lang, ", ", "، ")) + "\n")
		}
	}
	a.skillAnswer(ctx, u, t, st, e, strings.TrimSpace(b.String()))
}

// payrollMappingLabels: the application's own names for its payroll posting keys.
var payrollMappingLabels = map[string][2]string{
	"payroll_salary_expense":    {"Salary expense", "هزینه حقوق و دستمزد"},
	"payroll_employer_expense":  {"Employer insurance expense", "هزینه بیمه سهم کارفرما"},
	"payroll_salaries_payable":  {"Salaries payable", "حقوق پرداختنی"},
	"payroll_insurance_payable": {"Insurance payable", "بیمه پرداختنی"},
	"payroll_tax_payable":       {"Payroll tax payable", "مالیات حقوق پرداختنی"},
	"payroll_other_deductions":  {"Employee loans and advances", "وام و مساعده کارکنان"},
}

// readPayrollPosting answers «قواعد ثبت حسابداری حقوق رو بگو» from the payroll keys of
// GET /company/account-mappings.
func (a *Agent) readPayrollPosting(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	out, err := a.Tools.Call(ctx, a.env(u), "company.account_mappings.list", json.RawMessage(`{}`), nil)
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	e.Emit("tool", map[string]any{"tool": "company.account_mappings.list", "ok": true, "kind": "read"})
	raw, _ := json.Marshal(out)
	st.Plan.Reads = append(st.Plan.Reads, ReadRecord{Tool: "company.account_mappings.list", OK: true, Raw: raw, Result: truncate(string(raw), 3500)})
	var rows []map[string]any
	_ = json.Unmarshal(raw, &rows)
	var lines []string
	for _, m := range rows {
		key, _ := m["key"].(string)
		if !strings.HasPrefix(key, "payroll_") {
			continue
		}
		label := key
		if l, ok := payrollMappingLabels[key]; ok {
			label = tr(st.Lang, l[0], l[1])
		}
		acc := tr(st.Lang, "not set", "تعیین نشده")
		if code, _ := m["account_code"].(string); code != "" {
			acc = code + " " + fmt.Sprint(m["account_name"])
		}
		lines = append(lines, "• "+label+": "+acc)
	}
	if len(lines) == 0 {
		a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, "No payroll posting rules are configured at the moment.", "در حال حاضر قاعده‌ای برای ثبت حسابداری حقوق تعیین نشده است."))
		return
	}
	a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, "Payroll posting rules (which account each payroll entry uses):\n", "قواعد ثبت حسابداری حقوق (هر ثبت حقوق از کدام حساب استفاده می‌کند):\n")+strings.Join(lines, "\n"))
}

func persianDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '۰' + (r - '0')
		}
		if r == ',' {
			return '٬'
		}
		return r
	}, s)
}

// jalaliOf renders a YYYY-MM-DD application date in the Jalali calendar.
func jalaliOf(date string) string {
	var y, m, d int
	if _, err := fmt.Sscanf(date, "%d-%d-%d", &y, &m, &d); err != nil || y == 0 {
		return date
	}
	return normalize.FormatJalali(y, m, d)
}
