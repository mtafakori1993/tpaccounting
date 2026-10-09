// Package textlang keeps the free text the assistant writes into records (descriptions,
// notes, item lines, plan summaries) in the user's interface language (D45). With a
// Persian interface no English words may be generated; with an English interface no
// Persian ones. Words the user or the source document actually wrote (a Latin brand
// name, a Persian counterparty name), numbers and identifiers are kept as they are.
package textlang

import (
	"regexp"
	"strings"
	"unicode"
)

var latinWord = regexp.MustCompile(`[A-Za-z][A-Za-z'\-]*`)
var persianWord = regexp.MustCompile(`[\x{0600}-\x{06FF}\x{200C}]+`)

// FreeTextKeys are body fields whose value is prose the assistant composes. Names,
// codes, kinds and other identifiers are not checked.
var FreeTextKeys = map[string]bool{"description": true, "notes": true, "note": true, "memo": true, "narration": true, "purpose": true,
	"subject": true, "title": true, "comment": true, "comments": true, "remarks": true, "explanation": true}

// Foreign returns the words of text written in the script that does not belong to lang
// and that none of sources contains (case-insensitive): the words the model invented
// in the wrong language.
func Foreign(text, lang string, sources ...string) []string {
	re := latinWord
	if lang == "en" {
		re = persianWord
	}
	src := map[string]bool{}
	for _, w := range re.FindAllString(strings.ToLower(strings.Join(sources, "\n")), -1) {
		src[strings.Trim(w, "'-\u200c")] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, w := range re.FindAllString(text, -1) {
		lw := strings.ToLower(strings.Trim(w, "'-‌"))
		if lw == "" || seen[lw] || (lang != "en" && len(lw) < 2) || allowed[lw] || src[lw] {
			continue
		}
		seen[lw] = true
		out = append(out, w)
	}
	return out
}

// allowed: Latin tokens that are normal inside Persian accounting text.
var allowed = map[string]bool{"vat": true, "pdf": true, "id": true, "usd": true, "eur": true, "irr": true, "irt": true,
	"aed": true, "sku": true, "iban": true, "sheba": true, "pos": true, "atm": true, "kg": true, "cm": true, "mm": true, "ml": true}

// glossary: common words a model writes in English into Persian records.
var glossary = map[string]string{
	"payment": "پرداخت", "payments": "پرداخت‌ها", "pay": "پرداخت", "paid": "پرداخت‌شده", "receipt": "دریافت", "received": "دریافت‌شده",
	"receive": "دریافت", "purchase": "خرید", "purchases": "خرید", "purchased": "خرید", "buy": "خرید", "bought": "خرید", "sale": "فروش",
	"sales": "فروش", "sold": "فروش", "medicine": "دارو", "medicines": "دارو", "medication": "دارو", "drug": "دارو", "drugs": "دارو",
	"pharmacy": "داروخانه", "invoice": "فاکتور", "invoices": "فاکتورها", "bill": "صورتحساب", "expense": "هزینه", "expenses": "هزینه‌ها",
	"cost": "هزینه", "costs": "هزینه‌ها", "transfer": "انتقال", "deposit": "واریز", "withdrawal": "برداشت", "refund": "استرداد",
	"return": "برگشت", "returns": "برگشت", "salary": "حقوق", "salaries": "حقوق", "wage": "دستمزد", "wages": "دستمزد", "rent": "اجاره",
	"loan": "وام", "borrowed": "قرض", "repayment": "بازپرداخت", "advance": "پیش‌پرداخت", "prepayment": "پیش‌پرداخت", "fee": "کارمزد",
	"fees": "کارمزد", "commission": "کمیسیون", "tax": "مالیات", "insurance": "بیمه", "utilities": "قبوض", "electricity": "برق",
	"water": "آب", "gas": "گاز", "phone": "تلفن", "internet": "اینترنت", "fuel": "سوخت", "transport": "حمل‌ونقل", "transportation": "حمل‌ونقل",
	"shipping": "حمل", "food": "غذا", "lunch": "ناهار", "dinner": "شام", "meal": "غذا", "office": "اداری", "supplies": "ملزومات",
	"stationery": "لوازم‌التحریر", "equipment": "تجهیزات", "repair": "تعمیر", "repairs": "تعمیرات", "maintenance": "نگهداری",
	"service": "خدمات", "services": "خدمات", "goods": "کالا", "product": "کالا", "products": "کالاها", "item": "قلم", "items": "اقلام",
	"stock": "موجودی", "inventory": "موجودی", "customer": "مشتری", "supplier": "تأمین‌کننده", "vendor": "فروشنده", "counterparty": "طرف حساب",
	"bank": "بانک", "cash": "نقد", "card": "کارت", "cheque": "چک", "check": "چک", "opening": "افتتاحیه", "balance": "مانده",
	"capital": "سرمایه", "contribution": "آورده", "settlement": "تسویه", "settle": "تسویه", "partial": "جزئی", "full": "کامل",
	"monthly": "ماهانه", "daily": "روزانه", "annual": "سالانه", "month": "ماه", "year": "سال", "consulting": "مشاوره", "software": "نرم‌افزار",
	"hardware": "سخت‌افزار", "advertising": "تبلیغات", "marketing": "بازاریابی", "training": "آموزش", "travel": "سفر", "hotel": "هتل",
	"taxi": "تاکسی", "parking": "پارکینگ", "cleaning": "نظافت", "printing": "چاپ", "postage": "پست", "subscription": "اشتراک",
	"to": "به", "from": "از", "for": "بابت", "of": "", "the": "", "a": "", "an": "", "and": "و", "with": "با", "by": "توسط", "on": "در",
	"in": "در", "at": "در", "per": "به ازای", "via": "از طریق", "new": "جدید", "company": "شرکت", "record": "ثبت", "create": "ایجاد",
	"created": "ایجادشده", "register": "ثبت", "post": "قطعی", "posted": "قطعی", "order": "سفارش", "quotation": "پیش‌فاکتور",
	"journal": "سند", "entry": "حسابداری", "document": "سند", "documents": "اسناد", "account": "حساب", "accounts": "حساب‌ها",
	"warehouse": "انبار", "employee": "کارمند", "employees": "کارکنان", "payroll": "حقوق و دستمزد", "budget": "بودجه",
}

// Localize rewrites the invented foreign words of text into lang where a translation is
// known (Persian only). It returns the new text and the foreign words still left.
func Localize(text, lang string, sources ...string) (string, []string) {
	if lang == "en" {
		return text, Foreign(text, lang, sources...)
	}
	bad := Foreign(text, lang, sources...)
	if len(bad) == 0 {
		return text, nil
	}
	foreign := map[string]bool{}
	for _, w := range bad {
		foreign[strings.ToLower(w)] = true
	}
	var left []string
	out := latinWord.ReplaceAllStringFunc(text, func(w string) string {
		lw := strings.ToLower(w)
		if !foreign[lw] {
			return w
		}
		if fa, ok := glossary[lw]; ok {
			return fa
		}
		left = append(left, w)
		return w
	})
	return tidy(out), left
}

var spaces = regexp.MustCompile(`\s{2,}`)

func tidy(s string) string {
	s = spaces.ReplaceAllString(s, " ")
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == '-' || r == ':' })
}
