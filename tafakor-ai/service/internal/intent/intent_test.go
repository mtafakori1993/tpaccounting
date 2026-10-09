package intent

import "testing"

// The permanent regression of the audit (2026-10-08): a salary-structure request
// must be payroll configuration — never an expense with a "who paid?" question.
func TestSalaryComponentIsPayrollConfiguration(t *testing.T) {
	for _, in := range []string{
		"به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان",
		"به اجزای حقوق کمک هزینه سفر اضافه کن ۳ میلیون تومان",
		"کمک هزینه سفر را به اجزای حقوق اضافه کن",
		"کمک‌هزینه سفر رو به اجزای حقوق اضافه کن 3,000,000 تومان",
		"add a travel allowance of 3000000 to the salary components",
	} {
		d := Detect(in)
		if d.Domain != Payroll || d.Operation != "create_or_update_component" || d.Intent != OpSalaryComponent {
			t.Fatalf("%q → %+v", in, d)
		}
		if d.IsAccountingEvent() {
			t.Fatalf("%q must not enter the accounting-event workflow", in)
		}
	}
}

func TestContextNotKeywordDecides(t *testing.T) {
	cases := map[string]string{
		"هزینه سفر را ثبت کن":                                     General, // accounting transaction (model decides the event)
		"کمک هزینه سفر را به اجزای حقوق اضافه کن":                 Payroll,
		"برای کارمند زهرا معتضد کمک هزینه سفر ۳ میلیون ثبت کن":    Payroll,
		"۳ میلیون بابت سفر به شرکت پرداخت کردم":                   General,
		"به کارمند محمد رضایی ۳ میلیون کمک هزینه سفر پرداخت کردم": General, // money moved: a payment
		"این را برای دفعات بعد به خاطر بسپار":                     Memory,
		"حقوق کارمندان این ماه را پرداخت کن":                      General,
	}
	for in, want := range cases {
		if got := Detect(in).Domain; got != want {
			t.Fatalf("%q: domain %q, want %q", in, got, want)
		}
	}
	if d := Detect("برای کارمند زهرا معتضد کمک هزینه سفر ۳ میلیون ثبت کن"); d.Operation != "set_employee_component" {
		t.Fatalf("employee compensation: %+v", d)
	}
}

func TestMemoryIsSeparateFromTheTask(t *testing.T) {
	d := Detect("کمک هزینه سفر را ۳ میلیون تعیین کن و به اجزای حقوق اضافه کن و برای دفعات بعد هم به خاطر بسپار")
	if d.Domain != Payroll || d.Memory == nil || d.Memory.Only {
		t.Fatalf("task + memory: %+v %+v", d, d.Memory)
	}
	if d.Memory.Scope != ScopeCompanyConfig {
		t.Fatalf("business configuration must not become personal memory: %q", d.Memory.Scope)
	}
	m := Detect("اینو برای دفعه بعد یادت باشه")
	if m.Domain != Memory || m.Memory == nil || !m.Memory.Only || m.Memory.Scope != ScopeUserPreference {
		t.Fatalf("memory only: %+v %+v", m, m.Memory)
	}
	if Detect("هزینه سفر را ثبت کن").Memory != nil {
		t.Fatal("no memory instruction")
	}
}

func TestComponentNameAndAmount(t *testing.T) {
	cases := []struct{ in, name, amount, cur string }{
		{"به اجزای حقوق کمک هزینه سفر اضافه کن ۳۰۰۰۰۰۰ تومان", "کمک هزینه سفر", "3000000", "IRT"},
		{"کمک هزینه سفر را به اجزای حقوق اضافه کن ۳ میلیون", "کمک هزینه سفر", "3000000", ""},
		{"به اجزای حقوق حق جذب با مبلغ ۲۱,۰۰۰,۰۰۰ ریال اضافه کن", "حق جذب", "21000000", "IRR"},
		{"کمک‌هزینه سفر رو به اجزای حقوق اضافه کن 2232 تومان و به خاطر بسپار", "کمک هزینه سفر", "2232", "IRT"},
		{"به اجزای حقوق بن کارگری اضافه کن", "بن کارگری", "", ""},
	}
	for _, c := range cases {
		if got := ComponentName(c.in); got != c.name {
			t.Fatalf("%q: name %q, want %q", c.in, got, c.name)
		}
		a, cur := Amount(c.in)
		if a != c.amount || cur != c.cur {
			t.Fatalf("%q: amount %q %q, want %q %q", c.in, a, cur, c.amount, c.cur)
		}
	}
}

func TestPayrollPlanCannotMoveMoney(t *testing.T) {
	if ok, _ := CompatibleWrite(Payroll, "expenses", "expenses.create"); ok {
		t.Fatal("a payroll request must not create an expense")
	}
	if ok, _ := CompatibleWrite(Payroll, "treasury", "treasury.transactions.create"); ok {
		t.Fatal("a payroll request must not create a cash/bank transaction")
	}
	if ok, _ := CompatibleWrite(Payroll, "payroll", "payroll.components.create"); !ok {
		t.Fatal("salary components are payroll")
	}
	if ok, _ := CompatibleWrite(General, "expenses", "expenses.create"); !ok {
		t.Fatal("undecided domain: the model decides")
	}
	if ok, _ := CompatibleWrite(Configuration, "treasury", "treasury.accounts.create"); !ok {
		t.Fatal("configuration may create a cash box")
	}
	if ok, _ := CompatibleWrite(Configuration, "treasury", "treasury.transactions.create"); ok {
		t.Fatal("configuration must not move money")
	}
}

// Stress finding: «(کارمند)» in parentheses was not seen and the request fell
// through to the model.
func TestPunctuationDoesNotHideContext(t *testing.T) {
	for _, in := range []string{"برای محمد رضایی (کارمند) کمک هزینه سفر به مبلغ 1,250,000 ثبت کن", "«کمک هزینه سفر» را به «اجزای حقوق» اضافه کن ۳ میلیون",
		"به اجزای حقوق، کمک هزینه سفر اضافه کن؛ ۳ میلیون تومان"} {
		if Detect(in).Domain != Payroll {
			t.Fatalf("%q not payroll", in)
		}
	}
	if n := ComponentName("«کمک هزینه سفر» را به «اجزای حقوق» اضافه کن ۳ میلیون"); n != "کمک هزینه سفر" {
		t.Fatalf("name %q", n)
	}
}

func TestCheckDocumentIsACheck(t *testing.T) {
	for _, s := range []string{"چک دریافتی شماره ۱۲۳ به مبلغ ۵ میلیون تومان از محمد تفکری روی بانک ملت ثبت کن", "یه چک ۱۰ میلیونی برای علی صادر کن", "record a received cheque from Ali"} {
		if d := Detect(s); d.Operation != OpCheck || d.Tool != "checks.create" {
			t.Errorf("%q: %+v", s, d)
		}
	}
	for _, s := range []string{"موجودی صندوق رو چک کن", "یه رسید دریافت ۵ میلیون از علی بزن"} {
		if d := Detect(s); d.Operation == OpCheck {
			t.Errorf("%q is not a check document: %+v", s, d)
		}
	}
}

func TestReadVersusNavigation(t *testing.T) {
	cases := map[string]struct{ act, op string }{
		"قواعد حقوق رو بگو":                 {ActRead, OpReadPayrollRules},
		"قوانین حقوق و دستمزد رو نمایش بده": {ActRead, OpReadPayrollRules},
		"قواعد ثبت حسابداری حقوق رو بگو":    {ActRead, OpReadPayrollPosting},
		"قواعد حقوق کجاست؟":                 {ActNavigate, ""},
		"برای حقوق قانون جدید بساز":         {"", ""},
		"قاعده حقوق مالیات رو تغییر بده":    {"", ""},
		"مراکز هزینه رو لیست کن":            {ActRead, ""},
	}
	for s, want := range cases {
		if got := RequestAction(s); got != want.act {
			t.Errorf("%q: action %q, want %q", s, got, want.act)
		}
		if got := Detect(s).Operation; want.op != "" && got != want.op {
			t.Errorf("%q: operation %q, want %q", s, got, want.op)
		}
		if want.op == "" && (Detect(s).Operation == OpReadPayrollRules || Detect(s).Operation == OpReadPayrollPosting) {
			t.Errorf("%q must not be a read of payroll rules", s)
		}
	}
}
