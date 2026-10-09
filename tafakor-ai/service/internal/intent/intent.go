// Package intent is the assistant's domain layer: BEFORE any tool is chosen it
// decides which business domain a request belongs to, what operation on which
// entity it asks for, and whether it carries a separate "remember this"
// instruction. Keywords never decide alone — a word like «هزینه», «پرداخت» or
// «حقوق» appears in requests of several domains; the surrounding phrase does
// («کمک هزینه سفر را به اجزای حقوق اضافه کن» is payroll configuration, «هزینه
// سفر را ثبت کن» is an accounting transaction).
//
// The rules here are deterministic and run on the user's own words only (never
// on document or page text). The language model's interpretation is checked
// against them: a request the rules place in a non-accounting domain can never
// enter the accounting-event workflow (which asks who paid, from which account),
// and a plan for it can never contain money movements.
package intent

import (
	"regexp"
	"sort"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// Domains (the top level of the hierarchy).
const (
	Accounting    = "accounting"    // transactions, invoices, payments, receipts, checks, reports
	Payroll       = "payroll"       // employees, salary components/structure, payroll runs, compensation
	CRM           = "crm"           // customers, suppliers, contacts
	Configuration = "configuration" // accounts, cash boxes, payroll/system settings
	Documents     = "documents"     // OCR of invoices/receipts, document processing
	Memory        = "memory"        // "remember this" with no other task
	General       = ""              // not decided by the rules: the model's interpretation stands
)

// Operations the rules recognise (domain-qualified).
const (
	OpSalaryComponent      = "modify_salary_components" // create or update a salary component (company payroll structure)
	OpEmployeeCompensation = "employee_compensation"    // a component amount for one employee
	OpRemember             = "remember"                 // memory instruction only
	OpAccountingEvent      = "record_accounting_event"  // money moved / a cost was incurred
	OpConfigure            = "configure"                // other configuration
	OpCheck                = "register_check"           // a check (cheque) document: checks.create, never a receipt/payment
)

// Memory scopes. Business configuration is never stored as personal memory: when
// the task itself changes the company's configuration, that change IS the memory.
const (
	ScopeUserPreference = "user_preference"
	ScopeCompanyConfig  = "company_configuration"
	ScopeEmployee       = "employee_preference"
	ScopeConversation   = "conversation"
)

// MemoryInstruction is the "remember this for next time" part of a request,
// separated from the task it accompanies.
type MemoryInstruction struct {
	Text  string `json:"text"`
	Scope string `json:"scope"`
	// Only: the message is nothing but the memory instruction.
	Only bool `json:"only"`
}

// Decision is the structured pre-tool decision. It is persisted on the task and
// audited, so the reason a workflow was chosen is always visible.
type Decision struct {
	Domain    string             `json:"domain"`
	Intent    string             `json:"intent"`
	Operation string             `json:"operation"`
	Target    string             `json:"target,omitempty"`
	Entities  map[string]string  `json:"entities,omitempty"`
	Required  []string           `json:"required_information,omitempty"`
	Missing   []string           `json:"missing_information,omitempty"`
	Tool      string             `json:"tool,omitempty"`
	Reason    string             `json:"reason"`
	Source    string             `json:"source"` // rules | model
	Memory    *MemoryInstruction `json:"memory,omitempty"`
}

// IsAccountingEvent reports whether the decision allows the accounting-event
// workflow (expense/receipt/payment/... with "who paid" questions).
func (d Decision) IsAccountingEvent() bool {
	return d.Domain == General || d.Domain == Accounting || d.Domain == Documents
}

func has(t string, phrases []string) string {
	for _, p := range phrases {
		if strings.Contains(t, " "+normalize.Text(p)) {
			return p
		}
	}
	return ""
}

// salaryStructure: phrases that name the company's salary structure itself.
var salaryStructure = []string{
	"اجزای حقوق", "اجزاء حقوق", "اجزای حقوقی", "جزء حقوق", "جز حقوق", "جزء حقوقی", "جزو حقوق", "اجزای فیش", "اقلام حقوق",
	"آیتم حقوق", "آیتم های حقوق", "آیتم‌های حقوق", "آیتم حقوقی", "عوامل حقوق", "عوامل حقوقی", "ساختار حقوق", "ساختار حقوقی",
	"مزایای حقوق", "اجزای حقوق و دستمزد", "اجزای لیست حقوق",
	"salary component", "salary components", "payroll component", "payroll components", "pay component", "salary structure",
}

// compensationTerms: salary-component vocabulary (allowances, bonuses, deductions).
var compensationTerms = []string{
	"کمک هزینه", "کمک‌هزینه", "حق مسکن", "حق اولاد", "حق ماموریت", "حق مأموریت", "فوق العاده", "فوق‌العاده", "بن کارگری", "بن خواربار",
	"اضافه کار", "اضافه‌کار", "پاداش", "مزایا", "حق جذب", "حق سرپرستی", "حق ایاب و ذهاب", "ایاب و ذهاب",
	"allowance", "bonus", "stipend",
}

// moneyMoved: the money actually moved (or a cost was incurred) — accounting.
var moneyMoved = []string{
	"پرداخت کردم", "پرداخت کردیم", "پرداخت شد", "پرداختم", "پرداختیم", "پرداخت کرد", "واریز کردم", "واریز کردیم", "واریز شد",
	"دادم", "دادیم", "گرفتم", "گرفتیم", "دریافت کردم", "دریافت کردیم", "خرج کردم", "خرج کردیم", "هزینه کردم", "هزینه کردیم",
	"از صندوق", "از حساب", "کارت به کارت",
	"paid", "we paid", "i paid", "transferred", "spent", "received",
}

// checkDocument: the request is about a check (cheque) DOCUMENT. «چک» alone also means
// "verify" («چک کن»), so only document phrasings count.
var checkDocument = []string{"چک دریافتی", "چک پرداختی", "چک صادره", "چک وارده", "چک شماره", "چک به مبلغ", "چک به شماره", "یک چک", "یه چک",
	"چک بانکی", "چک صیادی", "چکی به مبلغ", "received check", "issued check", "received cheque", "issued cheque", "a cheque", "cheque number", "check number"}

// employeeAnchor: the request is about one employee.
var employeeAnchor = []string{"برای کارمند", "به کارمند", "کارمند", "پرسنل", "for employee", "employee"}

// memoryPhrases: "remember this", "from now on".
var memoryPhrases = []string{
	"به خاطر بسپار", "بخاطر بسپار", "به خاطر داشته باش", "بخاطر داشته باش", "یادت باشه", "یادت باشد", "یادت بمونه", "یادت بماند",
	"یادت نره", "یادت نرود", "به یاد داشته باش", "برای دفعه بعد", "برای دفعات بعد", "دفعه بعد", "دفعات بعد", "از این به بعد",
	"remember this", "remember that", "remember it", "remember for next time", "for next time", "next time", "from now on",
}

// configChange: verbs of configuring (adding/setting a structure element).
var configVerbs = []string{"اضافه کن", "اضافه شود", "اضافه بشه", "تعریف کن", "بساز", "ایجاد کن", "تعیین کن", "بذار", "بگذار",
	"قرار بده", "تنظیم کن", "عوض کن", "تغییر بده", "کن ", "add", "create", "define", "set"}

// Detect classifies the user's own words into the domain hierarchy. A zero
// Domain means the rules do not decide and the model's interpretation stands.
func Detect(text string) Decision {
	t := " " + clean(text) + " "
	d := Decision{Source: "rules", Entities: map[string]string{}}
	if m := detectMemory(t, text); m != nil {
		d.Memory = m
	}
	if r, ok := detectRead(t, text); ok {
		r.Memory = d.Memory
		return r
	}
	// «اجزای حقوق رو بگو» asks to SEE the structure: never the skills that change it (D61)
	if RequestAction(text) == ActRead && (has(t, salaryStructure) != "" || has(t, compensationTerms) != "") {
		d.Domain, d.Intent, d.Operation = Payroll, ActRead, "read_payroll_structure"
		d.Tool, d.Reason = "payroll.components.list", "the user asks to see payroll configuration: a read, not a change"
		return d
	}
	if p := has(t, checkDocument); p != "" {
		d.Domain, d.Intent, d.Operation, d.Target, d.Tool = Accounting, "register", OpCheck, "check", "checks.create"
		d.Reason = "the request names a check document («" + p + "»): a check, not a receipt or payment"
		return d
	}
	if p := has(t, salaryStructure); p != "" {
		d.Domain, d.Intent, d.Operation, d.Target = Payroll, OpSalaryComponent, "create_or_update_component", "salary_component"
		d.Reason = "the request names the salary structure («" + p + "»): payroll configuration, not a money movement"
		d.Tool = "payroll.components.create|payroll.components.update"
		d.Required = []string{"component_name", "amount"}
	} else if c := has(t, compensationTerms); c != "" && has(t, employeeAnchor) != "" && has(t, moneyMoved) == "" {
		d.Domain, d.Intent, d.Operation, d.Target = Payroll, OpEmployeeCompensation, "set_employee_component", "employee"
		d.Reason = "a salary component («" + c + "») for an employee, and no money is said to have moved: employee compensation"
		d.Tool = "employees.update"
		d.Required = []string{"employee", "component_name", "amount"}
	}
	if d.Memory != nil && d.Domain == General && d.Memory.Only {
		d.Domain, d.Intent, d.Operation = Memory, OpRemember, OpRemember
		d.Reason = "the message only asks to remember something"
	}
	if d.Memory != nil && d.Domain == Payroll {
		// the payroll structure is the company's configuration: changing it is what
		// makes it apply next time; it is not a personal note
		d.Memory.Scope = ScopeCompanyConfig
		if d.Operation == OpEmployeeCompensation {
			d.Memory.Scope = ScopeEmployee
		}
	}
	return d
}

// punct separates words: «(کارمند)», «سفر،» and «"اجزای حقوق"» are words too.
var punct = strings.NewReplacer("(", " ", ")", " ", "«", " ", "»", " ", "\"", " ", "'", " ", ":", " ", "؛", " ", ";", " ",
	"،", " ", "!", " ", "؟", " ", "?", " ", "[", " ", "]", " ", "/", " ")

func clean(text string) string { return normalize.Text(punct.Replace(normalize.Digits(text))) }

func detectMemory(t, raw string) *MemoryInstruction {
	p := has(t, memoryPhrases)
	if p == "" {
		return nil
	}
	m := &MemoryInstruction{Text: strings.TrimSpace(raw), Scope: ScopeUserPreference}
	// what is left without the memory phrases and filler words
	rest := t
	phrases := append([]string{}, memoryPhrases...)
	sort.Slice(phrases, func(i, j int) bool { return len(phrases[i]) > len(phrases[j]) })
	for _, ph := range phrases {
		rest = strings.ReplaceAll(rest, " "+normalize.Text(ph)+" ", " ")
	}
	for i := 0; i < 2; i++ {
		for _, f := range []string{"اینو", "این رو", "این را", "اینها را", "این ها را", "این", "لطفا", "هم", "و", "رو", "را", "برای",
			"this", "that", "it", "please", "and", "also", "for"} {
			rest = strings.ReplaceAll(rest, " "+normalize.Text(f)+" ", " ")
		}
	}
	m.Only = len(strings.Fields(rest)) == 0
	return m
}

var amountRe = regexp.MustCompile(`([0-9][0-9,٬]*(?:\.[0-9]+)?)\s*(میلیارد|میلیون|هزار|billion|million|thousand)?`)

// Amount returns the amount the user wrote (number words and Persian digits
// normalised) and its currency word: "IRT" for تومان, "IRR" for ریال, "" when
// none was said.
func Amount(text string) (string, string) {
	t := normalize.ReplaceNumberWords(normalize.Digits(text))
	cur := ""
	switch {
	case strings.Contains(t, "تومان") || strings.Contains(t, "تومن") || strings.Contains(strings.ToLower(t), "toman"):
		cur = "IRT"
	case strings.Contains(t, "ریال") || strings.Contains(strings.ToLower(t), "rial"):
		cur = "IRR"
	}
	best := ""
	for _, m := range amountRe.FindAllStringSubmatch(t, -1) {
		n := strings.NewReplacer(",", "", "٬", "").Replace(m[1])
		switch m[2] {
		case "هزار", "thousand":
			n += "000"
		case "میلیون", "million":
			n += "000000"
		case "میلیارد", "billion":
			n += "000000000"
		}
		if strings.Contains(n, ".") {
			continue
		}
		if len(strings.TrimLeft(n, "0")) > len(strings.TrimLeft(best, "0")) {
			best = n
		}
	}
	return strings.TrimLeft(best, "0"), cur
}

// componentStop: words that are not part of a component's name.
var componentStop = map[string]bool{}

func init() {
	for _, w := range []string{"به", "را", "رو", "با", "مبلغ", "مقدار", "اضافه", "کن", "کنید", "شود", "بشه", "تعریف", "بساز", "ایجاد", "تعیین",
		"بذار", "بگذار", "قرار", "بده", "تنظیم", "عوض", "تغییر", "ثبت", "برای", "در", "از", "یک", "جدید", "ماهانه", "ماهیانه", "هر", "ماه",
		"تومان", "تومن", "ریال", "میلیون", "هزار", "میلیارد", "لطفا", "لطفاً", "هم", "و", "که", "اینو", "این",
		"add", "to", "the", "a", "an", "of", "set", "create", "define", "with", "amount", "monthly", "toman", "rial", "please", "salary", "component", "components", "payroll"} {
		componentStop[normalize.Text(w)] = true
	}
}

// ComponentName extracts the salary component's name from a salary-structure
// request: the words that are not the structure phrase, verbs, amounts, the
// memory phrase or fillers («به اجزای حقوق کمک هزینه سفر اضافه کن ۳ میلیون» →
// «کمک هزینه سفر»). Empty when nothing is left.
func ComponentName(text string) string {
	t := " " + clean(normalize.ReplaceNumberWords(text)) + " "
	cut := func(list []string) {
		sorted := append([]string{}, list...)
		sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
		for _, p := range sorted {
			t = strings.ReplaceAll(t, " "+normalize.Text(p)+" ", " | ")
		}
	}
	cut(salaryStructure)
	cut(memoryPhrases)
	cut(employeeAnchor)
	var best, cur []string
	flush := func() {
		if len(cur) > len(best) {
			best = cur
		}
		cur = nil
	}
	for _, w := range strings.Fields(t) {
		if w == "|" || componentStop[w] || strings.ContainsAny(w, "0123456789") {
			flush()
			continue
		}
		cur = append(cur, w)
	}
	flush()
	return strings.Join(best, " ")
}

// moneyModules are the catalog modules whose writes move money or post entries.
var moneyModules = map[string]bool{"treasury": true, "expenses": true, "sales": true, "purchases": true, "checks": true,
	"journal-entries": true, "inventory": true}

// domainModules: the modules a domain's plan may WRITE to (reads are always allowed).
var domainModules = map[string]map[string]bool{
	Payroll:       {"payroll": true, "employees": true, "cost-centers": true, "accounts": true},
	CRM:           {"parties": true},
	Configuration: {"accounts": true, "treasury": true, "settings": true, "company": true, "payroll": true, "cost-centers": true, "expenses": true, "products": true},
}

// CompatibleWrite is the pre-tool check: may a plan for this domain write through
// a tool of this module? It stops a configuration request from silently turning
// into a transaction (the reported failure: a salary component became an expense
// with a "who paid?" question). Unknown/general domains allow everything.
func CompatibleWrite(domain, module, toolID string) (bool, string) {
	allowed, ok := domainModules[domain]
	if !ok {
		return true, ""
	}
	if allowed[module] {
		// configuration may create a cash box/bank account, never move money
		if domain == Configuration && moneyModules[module] && !strings.HasSuffix(toolID, ".accounts.create") &&
			!strings.HasSuffix(toolID, ".accounts.update") && !strings.Contains(toolID, "categories") {
			return false, "this is a configuration request: do not record money movements or documents"
		}
		return true, ""
	}
	return false, "this request is in the " + domain + " domain (" + reasonOf(domain) + "): " + toolID +
		" belongs to " + module + " — do not record an expense, payment, receipt or any money movement for it"
}

func reasonOf(domain string) string {
	switch domain {
	case Payroll:
		return "salary structure / employee compensation"
	case CRM:
		return "customer/supplier records"
	}
	return "configuration"
}
