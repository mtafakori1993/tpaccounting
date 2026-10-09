package intent

// Read vs navigation (D61). «قواعد حقوق رو بگو» asks for the configured rules
// themselves; «قواعد حقوق کجاست؟» asks where they are. The ACTION the user requests
// decides — not the subject's keywords. A tell/show/list request is a READ of the
// application's data and is never answered with a location in the UI.

const (
	ActRead     = "read"
	ActNavigate = "navigate"
)

var navigatePhrases = []string{"کجاست", "کجا هست", "کجا است", "کجا میتونم", "کجا می‌تونم", "کجا می توانم", "کجا می‌توانم", "از کجا", "کجا پیدا", "کجا ببینم",
	"چطور پیدا", "چطوری پیدا", "چطور ببینم", "چطوری ببینم", "کدوم صفحه", "کدام صفحه", "کدوم منو", "کدام منو",
	"where is", "where are", "where can i", "where do i", "how do i find", "how can i find", "how to find", "which page", "which menu"}

var readPhrases = []string{"بگو", "بگید", "بگوید", "نشون بده", "نشونم بده", "نشان بده", "نشانم بده", "نمایش بده", "نمایش بدهید", "لیست کن", "لیستشو", "فهرست کن",
	"فهرست", "لیست", "چیه", "چیست", "چی هست", "چیا هست", "کدومن", "کدامند", "کدام‌اند", "را ببینم", "رو ببینم", "توضیح بده",
	"show", "list", "tell me", "what are", "what is", "display", "give me"}

// mutationVerbs: the user wants something changed — not a read, whatever else it says.
var mutationVerbs = []string{"بساز", "ایجاد کن", "اضافه کن", "تغییر بده", "عوض کن", "حذف کن", "پاک کن", "ویرایش کن", "ثبت کن", "بزن", "تعریف کن",
	"تنظیم کن", "به روز کن", "بروز کن", "آپدیت کن", "create", "add", "delete", "remove", "change", "update", "set", "edit", "register"}

// RequestAction classifies the action the user asks for: ActNavigate (where/how to
// find), ActRead (tell/show/list) or "" (anything else, including any change).
func RequestAction(text string) string {
	t := " " + clean(text) + " "
	if has(t, mutationVerbs) != "" {
		return ""
	}
	if has(t, navigatePhrases) != "" {
		return ActNavigate
	}
	if has(t, readPhrases) != "" {
		return ActRead
	}
	return ""
}

// Read subjects with a canonical read in the application.
const (
	OpReadPayrollRules   = "read_payroll_rules"   // GET /payroll/rules: tax tables, insurance rules, payroll settings
	OpReadPayrollPosting = "read_payroll_posting" // the payroll keys of GET /company/account-mappings
)

var payrollRulesSubject = []string{"قواعد حقوق", "قوانین حقوق", "قانون حقوق", "قاعده حقوق", "قاعده‌های حقوق", "قواعد حقوقی", "قوانین حقوقی",
	"قوانین مالیات و بیمه حقوق", "قواعد مالیات و بیمه", "جدول مالیات حقوق", "جدول مالیات", "نرخ بیمه", "نرخ‌های بیمه", "نرخ های بیمه",
	"payroll rules", "payroll rule", "salary rules", "tax table", "insurance rules", "insurance rates"}

var postingWords = []string{"ثبت حسابداری", "ثبت خودکار", "قواعد ثبت", "قوانین ثبت", "حساب‌های حقوق", "حساب های حقوق", "posting", "account mapping", "account mappings"}

// detectRead decides a READ of a known subject (payroll rules, payroll posting rules).
func detectRead(t, text string) (Decision, bool) {
	if RequestAction(text) != ActRead {
		return Decision{}, false
	}
	posting := has(t, postingWords) != ""
	payroll := has(t, payrollRulesSubject) != "" || (posting && has(t, []string{"حقوق", "payroll", "salary"}) != "")
	if !payroll {
		return Decision{}, false
	}
	d := Decision{Source: "rules", Domain: Payroll, Intent: ActRead, Entities: map[string]string{}}
	if posting {
		d.Operation, d.Tool, d.Reason = OpReadPayrollPosting, "company.account_mappings.list", "the user asks to see the payroll posting rules: read them from the application"
	} else {
		d.Operation, d.Tool, d.Reason = OpReadPayrollRules, "payroll.rules.list", "the user asks to see the payroll rules (tax tables, insurance rules, settings): read them from the application"
	}
	return d, true
}
