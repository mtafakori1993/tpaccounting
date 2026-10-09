package agent

// Amount guard: a plan may only carry money amounts that come from the user's own
// words, the uploaded document, data read from the application, or results of
// earlier steps (directly, or divided/multiplied by a stated quantity). This stops
// a model slip like 150,000,000,000 for "15 million toman" before anything is
// shown for approval — the human still checks the card, but never as the only guard.

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// money fields in request bodies of the application's operations
var moneyFields = map[string]bool{"amount": true, "unit_price": true, "unit_cost": true, "price": true, "total": true, "debit": true,
	"credit": true, "opening_balance": true, "purchase_price": true, "sales_price": true, "base_salary": true, "limit": true}

var numRe = regexp.MustCompile(`\d[\d,٬]*(?:\.\d+)?`)

// allowedAmounts collects the amounts a plan may use, in the base currency.
func allowedAmounts(st *State, p *PlanState, baseIRR bool) (amounts []decimal.Decimal, quantities []decimal.Decimal) {
	add := func(d decimal.Decimal) {
		if d.IsPositive() {
			amounts = append(amounts, d)
		}
	}
	for _, t := range p.UserTexts {
		text := normalize.ReplaceNumberWords(normalize.Digits(t))
		for _, loc := range numRe.FindAllStringIndex(text, -1) {
			d, err := normalize.Amount(text[loc[0]:loc[1]])
			if err != nil {
				continue
			}
			// the unit written right after THIS number decides it (audit 2026-10-08:
			// «۵ میلیون تومان» passed as 5,000,000 rial because the raw number was
			// allowed next to its ×10)
			if numberUnit(text[loc[1]:]) == "IRT" && baseIRR {
				add(d.Mul(decimal.NewFromInt(10)))
			} else {
				add(d)
			}
			if d.LessThan(decimal.NewFromInt(100000)) {
				quantities = append(quantities, d)
			}
		}
	}
	for _, x := range st.Extractions {
		if d, err := decimal.NewFromString(x.Amount.Value); err == nil {
			add(d)
			if x.Currency.Value == "IRT" && baseIRR {
				add(d.Mul(decimal.NewFromInt(10)))
			}
		}
	}
	// numbers the application itself returned (reads and earlier steps)
	collect := func(raw []byte) {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return
		}
		var walk func(any)
		walk = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for _, y := range x {
					walk(y)
				}
			case []any:
				for _, y := range x {
					walk(y)
				}
			case string:
				if d, err := decimal.NewFromString(x); err == nil {
					add(d)
				}
			case float64:
				add(decimal.NewFromFloat(x))
			}
		}
		walk(v)
	}
	for _, r := range p.Reads {
		collect(r.Raw)
		collect([]byte(r.Result))
	}
	for _, s := range p.Steps {
		collect(s.Result)
	}
	// p.Entities are the model's own notes and are deliberately NOT a source of amounts.
	return amounts, quantities
}

func amountOK(v decimal.Decimal, amounts, qty []decimal.Decimal) bool {
	if !v.IsPositive() {
		return true
	}
	for _, a := range amounts {
		if v.Equal(a) {
			return true
		}
		for _, q := range qty {
			if q.IsPositive() && (v.Equal(a.Div(q).Round(4)) || v.Equal(a.Mul(q))) {
				return true
			}
		}
	}
	return false
}

// checkAmounts returns a feedback note if any money field of the new steps is not grounded.
func checkAmounts(st *State, p *PlanState, steps []PlanStep, baseIRR bool) string {
	amounts, qty := allowedAmounts(st, p, baseIRR)
	var bad []string
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, y := range x {
				if moneyFields[k] {
					var d decimal.Decimal
					var err error
					switch z := y.(type) {
					case string:
						if refRe.MatchString(z) {
							continue // value from an earlier step's result
						}
						d, err = decimal.NewFromString(normalize.Digits(strings.ReplaceAll(z, ",", "")))
					case float64:
						d = decimal.NewFromFloat(z)
					default:
						continue
					}
					if err == nil && !amountOK(d, amounts, qty) {
						bad = append(bad, path+"."+k+"="+d.String())
					}
					continue
				}
				walk(path+"."+k, y)
			}
		case []any:
			for _, y := range x {
				walk(path, y)
			}
		}
	}
	for _, s := range steps {
		if len(s.Args.Body) == 0 {
			continue
		}
		var body any
		if json.Unmarshal(s.Args.Body, &body) == nil {
			walk(s.ID, body)
		}
	}
	if len(bad) == 0 {
		return ""
	}
	var stated []string
	seen := map[string]bool{}
	for _, a := range amounts {
		if s := a.String(); !seen[s] && len(stated) < 12 {
			seen[s] = true
			stated = append(stated, s)
		}
	}
	sort.Strings(stated)
	return "AMOUNT CHECK FAILED: " + strings.Join(bad, ", ") + " is not an amount the user stated or the application returned. " +
		"The user's own amounts, already in the base currency: " + strings.Join(userAmounts(p, baseIRR), ", ") +
		" — use these exactly (do not ask the user to choose: the unit they wrote decides). Other amounts the application returned: " +
		strings.Join(stated, ", ") + "."
}

// stepAmounts lists the money fields of a step ("amount: 150,000,000") so the approval
// card shows them prominently instead of only inside the raw arguments.
func stepAmounts(s PlanStep) []string {
	var out []string
	var body any
	if json.Unmarshal(s.Args.Body, &body) != nil {
		return nil
	}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if moneyFields[k] {
					var d decimal.Decimal
					var err error
					switch z := x[k].(type) {
					case string:
						if refRe.MatchString(z) {
							out = append(out, k+": "+z)
							continue
						}
						d, err = decimal.NewFromString(strings.ReplaceAll(normalize.Digits(z), ",", ""))
					case float64:
						d = decimal.NewFromFloat(z)
					default:
						continue
					}
					if err == nil {
						out = append(out, k+": "+groupDigits(d))
					}
					continue
				}
				walk(x[k])
			}
		case []any:
			for _, y := range x {
				walk(y)
			}
		}
	}
	walk(body)
	return out
}

func groupDigits(d decimal.Decimal) string {
	s := d.String()
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	var b strings.Builder
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String() + frac
	}
	return b.String() + frac
}

// numberUnit is the currency word that directly follows a number ("IRT" for
// تومان, "IRR" for ریال), or "" when none does.
func numberUnit(after string) string {
	w := strings.Fields(after)
	if len(w) == 0 {
		return ""
	}
	first := strings.ToLower(strings.Trim(w[0], ".,،؛:()«»"))
	switch first {
	case "تومان", "تومن", "تومانی", "تومنی", "toman", "tomans", "هزارتومان":
		return "IRT"
	case "ریال", "ریالی", "rial", "rials", "irr":
		return "IRR"
	}
	return ""
}

// userAmounts lists the amounts in the user's words converted to the base currency,
// labelled with what was written («150000000 (= 15000000 تومان)»).
func userAmounts(p *PlanState, baseIRR bool) []string {
	var out []string
	for _, t := range p.UserTexts {
		text := normalize.ReplaceNumberWords(normalize.Digits(t))
		for _, loc := range numRe.FindAllStringIndex(text, -1) {
			d, err := normalize.Amount(text[loc[0]:loc[1]])
			if err != nil || d.LessThan(decimal.NewFromInt(1000)) {
				continue
			}
			if numberUnit(text[loc[1]:]) == "IRT" && baseIRR {
				out = append(out, d.Mul(decimal.NewFromInt(10)).String()+" (= "+d.String()+" تومان)")
			} else {
				out = append(out, d.String())
			}
		}
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}
