package agent

// Deterministic skills for requests the intent layer has classified exactly
// (package intent). A skill reads the application's current state, builds the
// exact typed payload itself — the model invents nothing — and hands the result
// to the normal plan machinery: one approval card, execution with idempotency,
// read-back verification of every value. Only information that is genuinely
// missing is asked for.
//
// The reported failure these replace: «به اجزای حقوق کمک هزینه سفر اضافه کن
// ۳۰۰۰۰۰۰ تومان» was treated as an expense («این هزینه را چه کسی پرداخت کرد؟»)
// or, through the generic planner, rejected round after round until the task
// stopped with "I couldn't work out a safe way".

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/intent"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

const (
	skillSalaryComponent = "salary_component"
	skillEmployeeComp    = "employee_compensation"
	skillPurchaseDoc     = "purchase_document"
)

// runSkill starts the skill the decision names. It reports false when no skill
// applies (the request continues through interpretation and the planner).
func (a *Agent) runSkill(ctx context.Context, u *User, t *store.Task, st *State, c *conversation, e Emitter) bool {
	d := st.Intent
	if d == nil {
		return false
	}
	switch {
	case d.Intent == intent.ActRead && (d.Operation == intent.OpReadPayrollRules || d.Operation == intent.OpReadPayrollPosting):
		skill := skillPayrollRules
		if d.Operation == intent.OpReadPayrollPosting {
			skill = skillPayrollPosting
		}
		st.Plan = &PlanState{Goal: st.UserText, Status: "planning", UserTexts: []string{st.UserText}, Entities: map[string]any{}, Domain: d.Domain, Skill: skill}
		a.audit(ctx, u, t.ID, "plan_started", map[string]any{"goal": st.UserText, "skill": skill})
		if skill == skillPayrollPosting {
			a.readPayrollPosting(ctx, u, t, st, e)
		} else {
			a.readPayrollRules(ctx, u, t, st, e)
		}
		return true
	case d.Domain == intent.Memory:
		a.memoryOnly(ctx, u, t, st, c, e)
		return true
	case d.Domain == intent.Payroll && d.Intent == intent.OpSalaryComponent:
		st.Plan = &PlanState{Goal: st.UserText, Status: "planning", UserTexts: []string{st.UserText}, Entities: map[string]any{}, Domain: d.Domain, Skill: skillSalaryComponent}
		a.audit(ctx, u, t.ID, "plan_started", map[string]any{"goal": st.UserText, "skill": skillSalaryComponent})
		a.salaryComponent(ctx, u, t, st, e)
		return true
	case d.Domain == intent.Payroll && d.Intent == intent.OpEmployeeCompensation:
		st.Plan = &PlanState{Goal: st.UserText, Status: "planning", UserTexts: []string{st.UserText}, Entities: map[string]any{}, Domain: d.Domain, Skill: skillEmployeeComp}
		a.audit(ctx, u, t.ID, "plan_started", map[string]any{"goal": st.UserText, "skill": skillEmployeeComp})
		a.employeeCompensation(ctx, u, t, st, e)
		return true
	}
	return false
}

// continueSkill re-runs the skill with everything the user said so far (the
// original request plus the answers), so an answer completes the same operation.
func (a *Agent) continueSkill(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if st.Plan.Entities == nil { // nil after a reload when it was empty
		st.Plan.Entities = map[string]any{}
	}
	switch st.Plan.Skill {
	case skillSalaryComponent:
		a.salaryComponent(ctx, u, t, st, e)
	case skillEmployeeComp:
		a.employeeCompensation(ctx, u, t, st, e)
	case skillPurchaseDoc:
		a.purchaseFromDocument(ctx, u, t, st, e)
	case skillTransfer:
		cfg, err := tools.LoadCompanyConfig(ctx, a.env(u))
		if err != nil {
			a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
			return
		}
		a.transferSkill(ctx, u, t, st, cfg, e)
	}
}

func (st *State) skillText() string {
	if st.Plan == nil {
		return st.UserText
	}
	return strings.Join(st.Plan.UserTexts, " ")
}

// skillAsk asks for genuinely missing information (one question).
func (a *Agent) skillAsk(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, en, fa string, options []classify.Option) {
	p := st.Plan
	p.Rounds++
	p.Status = "waiting_for_input"
	st.Stage = StageQuestion
	st.Proposal = &classify.Proposal{Question: &classify.Question{ID: fmt.Sprintf("plan:%d", p.Rounds), Field: "details", FreeText: true,
		Text: tr(st.Lang, en, fa), Options: options}, Suggested: map[string]classify.Choice{}}
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "skill_question", map[string]any{"skill": p.Skill, "text": st.Proposal.Question.Text})
	status(e, st.Lang, "question")
	e.Emit("question", st.Proposal.Question)
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// skillAnswer finishes a skill without a change (e.g. the value is already set).
func (a *Agent) skillAnswer(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, text string) {
	st.Plan.Status = "done"
	st.Stage, st.Message = StageDone, text
	a.save(ctx, t, st)
	a.audit(ctx, u, t.ID, "skill_answer", map[string]any{"skill": st.Plan.Skill, "text": text})
	e.Emit("message", map[string]any{"text": text})
	status(e, st.Lang, "completed")
	e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
}

// baseAmount converts the user's amount to the company's base currency.
func baseAmount(amount, cur, base string) (decimal.Decimal, bool) {
	d, err := decimal.NewFromString(amount)
	if err != nil || !d.IsPositive() {
		return decimal.Zero, false
	}
	base = strings.ToUpper(base)
	switch {
	case cur == "IRT" && base == "IRR":
		d = d.Mul(decimal.NewFromInt(10))
	case cur == "IRR" && base == "IRT":
		d = d.Div(decimal.NewFromInt(10))
	}
	return d, true
}

func (a *Agent) moneyText(st *State, d decimal.Decimal, base string) string {
	s := groupDigits(d) + " " + currencyName(st.Lang, base)
	if strings.EqualFold(base, "IRR") && st.Lang == "fa" && d.Mod(decimal.NewFromInt(10)).IsZero() {
		s += " (" + groupDigits(d.Div(decimal.NewFromInt(10))) + " تومان)"
	}
	return s
}

func currencyName(lang, c string) string {
	switch strings.ToUpper(c) {
	case "IRR":
		return tr(lang, "rials", "ریال")
	case "IRT":
		return tr(lang, "tomans", "تومان")
	}
	return c
}

// readList calls a read tool and returns its records.
func (a *Agent) readList(ctx context.Context, u *User, tool string, query map[string]string) ([]map[string]any, error) {
	args := map[string]any{"path": map[string]string{}, "query": query}
	raw, _ := json.Marshal(args)
	out, err := a.Tools.Call(ctx, a.env(u), tool, raw, nil)
	if err != nil {
		return nil, err
	}
	return recordItems(out), nil
}

// matchByName finds the record whose name (or name_en/full_name) is exactly the
// given name (normalised). Records whose name only contains it, or is contained in
// it, are returned as partial candidates: «حق سرپرستی ارشد» is NOT «حق سرپرستی»
// (the audit found a new component silently treated as an existing one).
func matchByName(recs []map[string]any, name string, keys ...string) (match map[string]any, partial []map[string]any) {
	n := normalize.Text(name)
	if n == "" {
		return nil, nil
	}
	for _, r := range recs {
		for _, k := range keys {
			v, _ := r[k].(string)
			v = normalize.Text(v)
			if v == "" {
				continue
			}
			if v == n {
				return r, nil
			}
			if strings.Contains(v, n) || strings.Contains(n, v) {
				partial = append(partial, r)
				break
			}
		}
	}
	return nil, partial
}

// skillName is the component name of the request, extracted once from the first
// message that names one (answers to later questions do not rename it).
func skillName(p *PlanState, extract func(string) string) string {
	if v, _ := p.Entities["component_name"].(string); v != "" {
		return v
	}
	for _, t := range p.UserTexts {
		if n := extract(t); n != "" {
			p.Entities["component_name"] = n
			return n
		}
	}
	return ""
}

const newChoice = "یک جزء جدید"

// chooseComponent resolves the component the user means: the exact one; a new one
// when nothing is similar; otherwise the user's answer to the question that lists
// the similar ones and "a new component". ask is set when that question is needed.
func chooseComponent(p *PlanState, comps []map[string]any, name string) (match map[string]any, isNew bool, ask []classify.Option) {
	exact, partial := matchByName(comps, name, "name", "name_en")
	if exact != nil {
		return exact, false, nil
	}
	if len(partial) == 0 {
		return nil, true, nil
	}
	if n := len(p.UserTexts); n > 1 {
		last := normalize.Text(p.UserTexts[n-1])
		for _, c := range partial {
			if nm, _ := c["name"].(string); normalize.Text(nm) == last {
				return c, false, nil
			}
		}
		if strings.Contains(last, normalize.Text("جدید")) || strings.Contains(last, "new") {
			return nil, true, nil
		}
	}
	for _, c := range partial {
		nm, _ := c["name"].(string)
		ask = append(ask, classify.Option{Value: nm, Label: "همان «" + nm + "» موجود"})
	}
	ask = append(ask, classify.Option{Value: newChoice + " «" + name + "»", Label: newChoice + ": «" + name + "»"})
	return nil, false, ask
}

// ---------- payroll: salary component (company salary structure) ----------

func (a *Agent) salaryComponent(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	text := st.skillText()
	base := u.Me.Company.BaseCurrency
	p := st.Plan
	name := skillName(p, intent.ComponentName)
	amount, cur := intent.Amount(text)
	p.Entities["domain"], p.Entities["intent"], p.Entities["component"] = intent.Payroll, intent.OpSalaryComponent, name
	if name == "" {
		a.skillAsk(ctx, u, t, st, e, "What is the salary component called?", "نام جزء حقوق چیست؟", nil)
		return
	}
	status(e, st.Lang, "searching")
	comps, err := a.readList(ctx, u, "payroll.components.list", nil)
	e.Emit("tool", map[string]any{"kind": "read", "ok": err == nil, "tool": "payroll.components.list"})
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	match, _, opts := chooseComponent(p, comps, name)
	if len(opts) > 0 {
		a.skillAsk(ctx, u, t, st, e, "«"+name+"» is similar to existing salary components. Do you mean one of them, or a new component?",
			"«"+name+"» به اجزای موجود حقوق شبیه است. منظورتان یکی از این‌هاست یا یک جزء جدید؟", opts)
		return
	}
	var rate decimal.Decimal
	if amount != "" {
		var ok bool
		if rate, ok = baseAmount(amount, cur, base); !ok {
			amount = ""
		}
	}
	if match != nil {
		mname, _ := match["name"].(string)
		method, _ := match["method"].(string)
		current, _ := decimal.NewFromString(fmt.Sprint(match["rate"]))
		if amount == "" {
			a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, fmt.Sprintf("«%s» is already a salary component (method %s, amount %s). Nothing was changed.", mname, method, a.moneyText(st, current, base)),
				fmt.Sprintf("«%s» از قبل در اجزای حقوق وجود دارد (روش %s، مبلغ %s). چیزی تغییر نکرد.", mname, methodName(method), a.moneyText(st, current, base))))
			return
		}
		if method != "fixed" {
			a.skillAsk(ctx, u, t, st, e,
				fmt.Sprintf("«%s» is calculated with method %s, not a fixed amount. Should it become a fixed monthly amount of %s? (yes / no)", mname, method, a.moneyText(st, rate, base)),
				fmt.Sprintf("«%s» با روش «%s» محاسبه می‌شود، نه مبلغ ثابت. به مبلغ ثابت ماهانه %s تبدیل شود؟ (بله / خیر)", mname, methodName(method), a.moneyText(st, rate, base)),
				[]classify.Option{{Value: "بله، مبلغ ثابت شود", Label: tr(st.Lang, "Yes, make it fixed", "بله، مبلغ ثابت شود")}})
			if !strings.Contains(normalize.Text(text), normalize.Text("مبلغ ثابت شود")) {
				return
			}
		}
		if current.Equal(rate) {
			if active, _ := match["is_active"].(bool); active {
				a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, fmt.Sprintf("«%s» is already %s. Nothing was changed.", mname, a.moneyText(st, rate, base)),
					fmt.Sprintf("«%s» از قبل %s است. چیزی تغییر نکرد.", mname, a.moneyText(st, rate, base))))
				return
			}
		}
		body := map[string]any{}
		for k, v := range match {
			body[k] = v
		}
		delete(body, "id")
		body["rate"], body["is_active"] = rate.String(), true
		if method != "fixed" {
			body["method"], body["quantity_field"], body["formula"], body["insurance_share"] = "fixed", "", "", ""
		}
		id := strconv.FormatInt(int64Of(match["id"]), 10)
		a.proposeSkillStep(ctx, u, t, st, e, "payroll.components.update", map[string]string{"id": id}, body,
			tr(st.Lang, fmt.Sprintf("Set salary component «%s» to a fixed monthly %s", mname, a.moneyText(st, rate, base)),
				fmt.Sprintf("تعیین جزء حقوق «%s» به مبلغ ثابت ماهانه %s", mname, a.moneyText(st, rate, base))))
		return
	}
	if amount == "" {
		a.skillAsk(ctx, u, t, st, e, "What monthly amount should «"+name+"» have?", "مبلغ ماهانه «"+name+"» چقدر باشد؟", nil)
		return
	}
	body := newComponent(comps, name, rate, deductionWords(text))
	kindText := tr(st.Lang, "earning (allowance), taxable and insurable", "مزایا (کمک‌هزینه)، مشمول مالیات و بیمه")
	if body["kind"] == "deduction" {
		kindText = tr(st.Lang, "deduction", "کسور")
	}
	a.proposeSkillStep(ctx, u, t, st, e, "payroll.components.create", map[string]string{}, body,
		tr(st.Lang, fmt.Sprintf("Add salary component «%s»: fixed monthly %s, %s", name, a.moneyText(st, rate, base), kindText),
			fmt.Sprintf("افزودن جزء حقوق «%s»: مبلغ ثابت ماهانه %s، %s", name, a.moneyText(st, rate, base), kindText)))
}

func methodName(m string) string {
	names := map[string]string{"fixed": "مبلغ ثابت", "base_percent": "درصد حقوق پایه", "daily_base": "روزانه", "hourly_base": "ساعتی",
		"per_unit": "به ازای واحد", "per_child": "به ازای فرزند", "manual": "ورود دستی", "formula": "فرمول", "percent_of_gross": "درصد ناخالص",
		"percent_of_insurable": "درصد مشمول بیمه", "tax_table": "جدول مالیات", "tax_brackets": "پلکانی", "insurance_rule": "قانون بیمه"}
	if n, ok := names[m]; ok {
		return n
	}
	return m
}

func deductionWords(text string) bool {
	t := " " + normalize.Text(text) + " "
	for _, w := range []string{" کسر ", " کسور", " کسورات", " deduction"} {
		if strings.Contains(t, normalize.Text(w)) {
			return true
		}
	}
	return false
}

// newComponent is the payload of a new fixed-amount component, shaped like the
// company's existing ones: a free code, the next sequence among its kind.
func newComponent(existing []map[string]any, name string, rate decimal.Decimal, deduction bool) map[string]any {
	kind, category, taxable := "earning", "allowance", true
	if deduction {
		kind, category, taxable = "deduction", "other", false
	}
	codes := map[string]bool{}
	maxSeq := 0
	for _, c := range existing {
		code, _ := c["code"].(string)
		codes[code] = true
		if k, _ := c["kind"].(string); k == kind {
			if s := int(int64Of(c["seq"])); s > maxSeq && (kind != "earning" || s < 200) {
				maxSeq = s
			}
		}
	}
	prefix := "allowance_"
	if deduction {
		prefix = "deduction_"
	}
	code := ""
	for i := 1; ; i++ {
		code = prefix + strconv.Itoa(i)
		if !codes[code] {
			break
		}
	}
	return map[string]any{"code": code, "name": name, "name_en": "", "kind": kind, "category": category, "method": "fixed",
		"rate": rate.String(), "quantity_field": "", "brackets": []any{}, "exemption": "0", "ceiling": "0",
		"taxable": taxable, "insurable": taxable, "seq": maxSeq + 5, "is_active": true, "variability": "fixed", "formula": "", "insurance_share": ""}
}

// proposeSkillStep validates the skill's own step like any plan step and offers it
// for approval.
func (a *Agent) proposeSkillStep(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, tool string, path map[string]string, body map[string]any, summary string) {
	a.proposeSkillSteps(ctx, u, t, st, e, []plannerCall{{ID: "s1", Tool: tool, Path: path, Body: mustJSON(body), Purpose: summary}}, summary)
}

func (a *Agent) proposeSkillSteps(ctx context.Context, u *User, t *store.Task, st *State, e Emitter, calls []plannerCall, summary string) {
	p := st.Plan
	var kept []PlanStep
	for _, s := range p.Steps {
		if s.Status == "done" {
			kept = append(kept, s)
		}
	}
	p.Steps = kept
	steps, problem := a.validatePlan(u, p, calls)
	if problem != "" {
		a.audit(ctx, u, t.ID, "plan_rejected", map[string]any{"skill": p.Skill, "problem": problem})
		if strings.HasPrefix(problem, "permission:") {
			a.stop(ctx, u, t, st, e, tr(st.Lang, "You don't have permission for this in the accounting app ("+strings.TrimPrefix(problem, "permission:")+"), so I can't do it either.",
				"شما در نرم‌افزار حسابداری مجوز این کار را ندارید ("+strings.TrimPrefix(problem, "permission:")+")، پس من هم نمی‌توانم انجام دهم."))
			return
		}
		a.fail(ctx, u, t, st, e, tr(st.Lang, "I could not build a valid request ("+problem+"). Nothing was changed.",
			"نتوانستم درخواست معتبری بسازم ("+problem+"). چیزی تغییر نکرد."), nil)
		return
	}
	p.Steps = append(p.Steps, steps...)
	p.Summary = summary
	a.audit(ctx, u, t.ID, "skill_plan", map[string]any{"skill": p.Skill, "steps": steps, "decision": st.Intent})
	a.proposePlan(ctx, u, t, st, e)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// ---------- payroll: one employee's compensation ----------

func (a *Agent) employeeCompensation(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	text := st.skillText()
	base := u.Me.Company.BaseCurrency
	p := st.Plan
	status(e, st.Lang, "searching")
	emps, err := a.readList(ctx, u, "employees.list", map[string]string{"page_size": "200"})
	e.Emit("tool", map[string]any{"kind": "read", "ok": err == nil, "tool": "employees.list"})
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	emp, cands := employeeIn(emps, text)
	if emp == nil {
		var opts []classify.Option
		for _, c := range cands {
			nm, _ := c["full_name"].(string)
			opts = append(opts, classify.Option{Value: nm, Label: nm + " (" + fmt.Sprint(c["code"]) + ")"})
		}
		if len(cands) > 1 {
			a.skillAsk(ctx, u, t, st, e, "Several employees match. Which one?", "چند کارمند با این نام هست. کدام؟", opts)
		} else {
			a.skillAsk(ctx, u, t, st, e, "Which employee is it for? (I did not find the name among the employees.)", "برای کدام کارمند؟ (نام را در فهرست کارکنان پیدا نکردم.)", nil)
		}
		return
	}
	empName, _ := emp["full_name"].(string)
	// the component name is what is left once the employee's name is removed
	name := skillName(p, func(t string) string {
		for _, k := range []string{"full_name", "first_name", "last_name"} {
			if v, _ := emp[k].(string); v != "" {
				t = strings.ReplaceAll(normalize.Text(t), normalize.Text(v), " ")
			}
		}
		return intent.ComponentName(t)
	})
	amount, cur := intent.Amount(text)
	p.Entities["domain"], p.Entities["intent"], p.Entities["employee"], p.Entities["component"] = intent.Payroll, intent.OpEmployeeCompensation, empName, name
	if name == "" {
		a.skillAsk(ctx, u, t, st, e, "Which salary component (e.g. travel allowance)?", "کدام جزء حقوق (مثلاً کمک هزینه سفر)؟", nil)
		return
	}
	if amount == "" {
		a.skillAsk(ctx, u, t, st, e, "What monthly amount for «"+name+"»?", "مبلغ ماهانه «"+name+"» برای "+empName+" چقدر باشد؟", nil)
		return
	}
	rate, ok := baseAmount(amount, cur, base)
	if !ok {
		a.skillAsk(ctx, u, t, st, e, "What monthly amount for «"+name+"»?", "مبلغ ماهانه «"+name+"» برای "+empName+" چقدر باشد؟", nil)
		return
	}
	comps, err := a.readList(ctx, u, "payroll.components.list", nil)
	e.Emit("tool", map[string]any{"kind": "read", "ok": err == nil, "tool": "payroll.components.list"})
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	comp, _, opts := chooseComponent(p, comps, name)
	if len(opts) > 0 {
		a.skillAsk(ctx, u, t, st, e, "«"+name+"» is similar to existing salary components. Do you mean one of them, or a new component?",
			"«"+name+"» به اجزای موجود حقوق شبیه است. منظورتان یکی از این‌هاست یا یک جزء جدید؟", opts)
		return
	}
	// the employee's full record: the update replaces it (and its overrides) entirely
	empID := strconv.FormatInt(int64Of(emp["id"]), 10)
	raw, _ := json.Marshal(map[string]any{"path": map[string]string{"id": empID}})
	rec, err := a.Tools.Call(ctx, a.env(u), "employees.get", raw, nil)
	e.Emit("tool", map[string]any{"kind": "read", "ok": err == nil, "tool": "employees.get"})
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	var body map[string]any
	b, _ := json.Marshal(rec)
	_ = json.Unmarshal(b, &body)
	for _, k := range []string{"id", "created_at", "updated_at", "department_name", "cost_center_name"} {
		delete(body, k)
	}
	overrides, _ := body["components"].([]any)
	var calls []plannerCall
	compID := any(nil)
	summary := ""
	if comp != nil {
		compID = int64Of(comp["id"])
		cname, _ := comp["name"].(string)
		name = cname
		for _, o := range overrides {
			if om, ok := o.(map[string]any); ok && int64Of(om["component_id"]) == int64Of(comp["id"]) {
				if cur, err := decimal.NewFromString(fmt.Sprint(om["rate"])); err == nil && cur.Equal(rate) {
					a.skillAnswer(ctx, u, t, st, e, tr(st.Lang, fmt.Sprintf("%s already has «%s» = %s. Nothing was changed.", empName, name, a.moneyText(st, rate, base)),
						fmt.Sprintf("«%s» برای %s از قبل %s است. چیزی تغییر نکرد.", name, empName, a.moneyText(st, rate, base))))
					return
				}
			}
		}
	} else {
		// a new component, 0 for everyone; this employee's amount is the override
		nc := newComponent(comps, name, decimal.Zero, deductionWords(text))
		calls = append(calls, plannerCall{ID: "s1", Tool: "payroll.components.create", Path: map[string]string{}, Body: mustJSON(nc),
			Purpose: tr(st.Lang, fmt.Sprintf("Add salary component «%s» (0 by default; set per employee)", name), fmt.Sprintf("افزودن جزء حقوق «%s» (پیش‌فرض صفر؛ مبلغ برای هر کارمند)", name))})
		compID = "$s1.id"
	}
	var kept []any
	for _, o := range overrides {
		if om, ok := o.(map[string]any); ok && comp != nil && int64Of(om["component_id"]) == int64Of(comp["id"]) {
			continue
		}
		kept = append(kept, o)
	}
	body["components"] = append(kept, map[string]any{"component_id": compID, "rate": rate.String()})
	id := "s1"
	if len(calls) > 0 {
		id = "s2"
	}
	summary = tr(st.Lang, fmt.Sprintf("%s: «%s» = %s per month (employee's own amount)", empName, name, a.moneyText(st, rate, base)),
		fmt.Sprintf("%s: «%s» به مبلغ ماهانه %s (مبلغ اختصاصی این کارمند)", empName, name, a.moneyText(st, rate, base)))
	calls = append(calls, plannerCall{ID: id, Tool: "employees.update", Path: map[string]string{"id": empID}, Body: mustJSON(body), Purpose: summary})
	// an employee's pay is a payroll (financial) change: a person always approves it,
	// even though the generic policy lets routine employee edits run on request
	st.AskThisTime = true
	a.proposeSkillSteps(ctx, u, t, st, e, calls, summary)
}

// employeeIn finds the employee the text names (full name, or first+last name
// both present). cands are the near matches when none or several fit.
func employeeIn(emps []map[string]any, text string) (map[string]any, []map[string]any) {
	t := " " + normalize.Text(text) + " "
	var hits, partial []map[string]any
	for _, e := range emps {
		full, _ := e["full_name"].(string)
		first, _ := e["first_name"].(string)
		last, _ := e["last_name"].(string)
		switch {
		case full != "" && strings.Contains(t, " "+normalize.Text(full)+" "):
			hits = append(hits, e)
		case last != "" && strings.Contains(t, " "+normalize.Text(last)+" "):
			partial = append(partial, e)
		case first != "" && strings.Contains(t, " "+normalize.Text(first)+" "):
			partial = append(partial, e)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		return nil, hits
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	sort.Slice(partial, func(i, j int) bool { return fmt.Sprint(partial[i]["full_name"]) < fmt.Sprint(partial[j]["full_name"]) })
	return nil, partial
}

// ---------- memory ----------

// memoryOnly handles a message that only asks to remember something. Business
// configuration is never stored as a personal note: if the last operation of the
// conversation changed the company's configuration, that change is already what
// applies next time. Corrections the user made (learning offers) are remembered
// as their personal preferences — the user's own explicit instruction.
func (a *Agent) memoryOnly(ctx context.Context, u *User, t *store.Task, st *State, c *conversation, e Emitter) {
	finish := func(text string, data map[string]any) {
		st.Stage, st.Message = StageDone, text
		a.save(ctx, t, st)
		a.audit(ctx, u, t.ID, "memory", data)
		e.Emit("message", map[string]any{"text": text})
		status(e, st.Lang, "completed")
		e.Emit("done", map[string]any{"task_id": t.ID, "stage": st.Stage})
	}
	var prev *State
	var prevID string
	if c != nil {
		for i := len(c.state.Messages) - 1; i >= 0 && prev == nil; i-- {
			id := c.state.Messages[i].TaskID
			if id == "" || id == t.ID {
				continue
			}
			if _, ps, err := a.load(ctx, u, id); err == nil {
				prev, prevID = ps, id
			}
		}
	}
	if prev != nil && len(prev.Offers) > 0 {
		var labels []string
		for _, o := range prev.Offers {
			a.acceptOffer(ctx, u, prevID, o)
			labels = append(labels, o.Label)
		}
		finish(tr(st.Lang, "Remembered for next time (your personal preference): "+strings.Join(labels, "; "),
			"برای دفعات بعد به خاطر سپردم (ترجیح شخصی شما): "+strings.Join(labels, "؛ ")),
			map[string]any{"scope": intent.ScopeUserPreference, "offers": prev.Offers, "source_task": prevID})
		return
	}
	if prev != nil && prev.Plan != nil && prev.Plan.Domain != "" && prev.Plan.Domain != intent.Accounting && prev.Stage == StageDone {
		finish(tr(st.Lang, "That change is saved in the company's settings in the accounting app, so it already applies next time. I did not store a separate personal note.",
			"این تغییر در تنظیمات شرکت در نرم‌افزار حسابداری ذخیره شده و دفعات بعد خودبه‌خود اعمال می‌شود؛ یادداشت شخصی جداگانه‌ای ذخیره نکردم."),
			map[string]any{"scope": intent.ScopeCompanyConfig, "source_task": prevID})
		return
	}
	finish(tr(st.Lang, "I didn't find anything in this conversation to remember. Tell me exactly what to remember (e.g. «pay taxi expenses from the main cash box»).",
		"در این گفتگو چیزی برای به خاطر سپردن پیدا نکردم. دقیقاً بگویید چه چیزی را به خاطر بسپارم (مثلاً «هزینه تاکسی را از صندوق اصلی پرداخت کن»)."),
		map[string]any{"scope": intent.ScopeConversation, "found": false})
}

// memoryAfterTask reports how a memory instruction that came with a task was
// handled (called when the task finished).
func (a *Agent) memoryAfterTask(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	if st.Intent == nil || st.Intent.Memory == nil || st.Intent.Memory.Only {
		return
	}
	switch st.Intent.Memory.Scope {
	case intent.ScopeCompanyConfig, intent.ScopeEmployee:
		e.Emit("message", map[string]any{"text": tr(st.Lang, "This is now part of the company's payroll settings in the accounting app, so it applies next time too (not stored as a personal note).",
			"این مقدار اکنون در تنظیمات حقوق شرکت در نرم‌افزار حسابداری ثبت است و دفعات بعد هم اعمال می‌شود (به‌عنوان یادداشت شخصی ذخیره نشد).")})
		a.audit(ctx, u, t.ID, "memory", map[string]any{"scope": st.Intent.Memory.Scope, "stored_as": "company_configuration"})
	case intent.ScopeUserPreference:
		if len(st.Offers) == 0 {
			return
		}
		var labels []string
		for _, o := range st.Offers {
			a.acceptOffer(ctx, u, t.ID, o)
			labels = append(labels, o.Label)
		}
		e.Emit("message", map[string]any{"text": tr(st.Lang, "Remembered for next time: "+strings.Join(labels, "; "), "برای دفعات بعد به خاطر سپردم: "+strings.Join(labels, "؛ "))})
		a.audit(ctx, u, t.ID, "memory", map[string]any{"scope": intent.ScopeUserPreference, "offers": st.Offers})
	}
}
