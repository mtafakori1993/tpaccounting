package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

// partyMatch is an existing party that a pending "create party" step would duplicate.
type partyMatch struct {
	Step  string      `json:"step"`
	By    string      `json:"by"` // name | phone
	Value string      `json:"value"`
	Party tools.Party `json:"party"`
}

func (m partyMatch) text(lang string) string {
	by := map[string][2]string{"name": {"the same name", "همین نام"}, "phone": {"the same phone number", "همین شماره تماس"}}[m.By]
	return tr(lang, fmt.Sprintf("A party with %s already exists: %s (code %s). I have not created another one.", by[0], m.Party.Name, m.Party.Code),
		fmt.Sprintf("طرف حسابی با %s از قبل وجود دارد: %s (کد %s). طرف حساب تکراری نساختم.", by[1], m.Party.Name, m.Party.Code))
}

// partyDuplicates searches the application (its own /parties search, which matches
// name, mobile and phone) for each party a plan is about to create. A party with the
// same normalized name or phone number is a duplicate: it is reported and the plan
// waits for the user instead of silently creating a second record.
func (a *Agent) partyDuplicates(ctx context.Context, u *User, steps []PlanStep) []partyMatch {
	var out []partyMatch
	for _, s := range steps {
		if s.Tool != "parties.create" {
			continue
		}
		var body struct {
			Name   string `json:"name"`
			Phone  string `json:"phone"`
			Mobile string `json:"mobile"`
		}
		_ = json.Unmarshal(s.Args.Body, &body)
		name := normalize.Text(body.Name)
		phones := map[string]bool{}
		for _, p := range []string{body.Phone, body.Mobile} {
			if k := phoneKey(p); k != "" {
				phones[k] = true
			}
		}
		queries := []string{strings.TrimSpace(body.Name)}
		if c := coreName(body.Name); c != "" && c != normalize.Text(body.Name) {
			queries = append(queries, c)
		}
		for k := range phones {
			queries = append(queries, k)
		}
		seen := map[int64]bool{}
		for _, q := range queries {
			if q == "" {
				continue
			}
			var list struct {
				Items []tools.Party `json:"items"`
			}
			if err := a.Acc.Do(ctx, u.Cred, accountingCall{Method: "GET", Path: "/parties", Query: map[string][]string{"q": {q}, "page_size": {"20"}}}, &list); err != nil {
				continue
			}
			for _, p := range list.Items {
				if seen[p.ID] {
					continue
				}
				switch {
				case name != "" && normalize.Text(p.Name) == name:
					out = append(out, partyMatch{Step: s.ID, By: "name", Value: body.Name, Party: p})
				case name != "" && sameCore(p.Name, body.Name):
					// «پارس نوآور» vs «شرکت پارس نوآور» (audit 2026-10-08: the model
					// dropped «شرکت» and a duplicate supplier was created automatically)
					out = append(out, partyMatch{Step: s.ID, By: "name", Value: body.Name, Party: p})
				case phones[phoneKey(p.Mobile)] || phones[phoneKey(p.Phone)]:
					out = append(out, partyMatch{Step: s.ID, By: "phone", Value: q, Party: p})
				default:
					continue
				}
				seen[p.ID] = true
			}
		}
	}
	return out
}

// phoneKey normalizes a phone number for matching: Persian/Arabic digits, spaces,
// dashes, parentheses and the +98 / 0098 / leading-0 prefixes are removed, so
// "۰۹۳۵۷۵۷۸۸۰۸", "0935 757 8808" and "+989357578808" all give "9357578808".
func phoneKey(s string) string {
	var b strings.Builder
	for _, r := range normalize.Digits(s) {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	switch {
	case strings.HasPrefix(d, "0098"):
		d = d[4:]
	case strings.HasPrefix(d, "98") && len(d) == 12:
		d = d[2:]
	}
	d = strings.TrimLeft(d, "0")
	if len(d) < 7 {
		return ""
	}
	return d
}

// legalForms are words that are not part of a counterparty's identity.
var legalForms = map[string]bool{}

func init() {
	for _, w := range []string{"شرکت", "شركت", "موسسه", "مؤسسه", "گروه", "سهامی", "خاص", "عام", "مسئولیت", "محدود", "تعاونی", "فروشگاه",
		"co", "company", "ltd", "inc", "llc", "group"} {
		legalForms[normalize.Text(w)] = true
	}
}

// coreName is a party name without legal-form words.
func coreName(name string) string {
	var out []string
	for _, w := range strings.Fields(normalize.Text(strings.NewReplacer("(", " ", ")", " ", ".", " ").Replace(name))) {
		if !legalForms[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

func sameCore(a, b string) bool {
	ca, cb := coreName(a), coreName(b)
	return ca != "" && ca == cb
}
