package agent

// Duplicate protection for master data and checks beyond parties (audit
// 2026-10-08: the same cash box was created twice with no warning; a check with an
// existing number could be registered again). A pending create step whose key
// values (same name / code / number) already exist in the application is reported
// before the approval, exactly like duplicate parties.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

type dupRule struct {
	list  string   // read tool listing the existing records
	keys  []string // body fields that identify a record (any one equal = duplicate)
	query string   // body field used as ?q= (empty: list all)
	what  [2]string
}

var dupRules = map[string]dupRule{
	"treasury.accounts.create":  {list: "treasury.accounts.list", keys: []string{"name", "account_number", "iban"}, what: [2]string{"cash/bank account", "صندوق/حساب بانکی"}},
	"employees.create":          {list: "employees.list", keys: []string{"code", "national_id"}, query: "code", what: [2]string{"employee", "کارمند"}},
	"products.create":           {list: "products.list", keys: []string{"name", "sku"}, query: "name", what: [2]string{"product", "کالا"}},
	"checks.create":             {list: "checks.list", keys: []string{"sayadi_id", "serial_number", "check_number"}, what: [2]string{"check", "چک"}},
	"payroll.components.create": {list: "payroll.components.list", keys: []string{"name", "code"}, what: [2]string{"salary component", "جزء حقوق"}},
}

func (a *Agent) recordDuplicates(ctx context.Context, u *User, steps []PlanStep) []partyMatch {
	var out []partyMatch
	for _, s := range steps {
		rule, ok := dupRules[s.Tool]
		if !ok {
			continue
		}
		var body map[string]any
		_ = json.Unmarshal(s.Args.Body, &body)
		q := map[string]string{"page_size": "200"}
		if rule.query != "" {
			if v, _ := body[rule.query].(string); v != "" {
				q["q"] = v
			}
		}
		if rule.list == "treasury.accounts.list" || rule.list == "payroll.components.list" {
			delete(q, "page_size")
		}
		recs, err := a.readList(ctx, u, rule.list, q)
		if err != nil {
			continue
		}
		for _, r := range recs {
			for _, k := range rule.keys {
				want := normalize.Text(fmt.Sprint(body[k]))
				if body[k] == nil || want == "" || want == "0" {
					continue
				}
				if normalize.Text(fmt.Sprint(r[k])) == want {
					name, _ := r["name"].(string)
					if name == "" {
						name, _ = r["full_name"].(string)
					}
					if name == "" {
						name = fmt.Sprint(r[k])
					}
					out = append(out, partyMatch{Step: s.ID, By: k, Value: fmt.Sprint(body[k]),
						Party: tools.Party{ID: int64Of(r["id"]), Name: rule.what[1] + ": " + name, Code: fmt.Sprint(r["code"])}})
					goto next
				}
			}
		}
	next:
	}
	return out
}

// dupText describes a duplicate found by recordDuplicates.
func dupText(lang string, m partyMatch) string {
	return tr(lang, fmt.Sprintf("A record with the same %s (%s) already exists: %s. Registering again would duplicate it.", m.By, m.Value, m.Party.Name),
		fmt.Sprintf("رکوردی با همین %s (%s) از قبل وجود دارد: %s. ثبت دوباره، رکورد تکراری می‌سازد.", strings.ReplaceAll(m.By, "_", " "), m.Value, m.Party.Name))
}

// ambiguousParty finds a plan step whose party_id is one of several active parties
// with the same name (audit A5: two «ایرانیکارت» suppliers, the plan silently used
// the first). The user picks; the answer names the party's code, which resolves it.
func (a *Agent) ambiguousParty(ctx context.Context, u *User, p *PlanState, steps []PlanStep) (string, []classify.Option) {
	said := normalize.Digits(strings.Join(p.UserTexts, " "))
	for _, s := range steps {
		var body map[string]any
		_ = json.Unmarshal(s.Args.Body, &body)
		id := int64Of(body["party_id"])
		if id == 0 {
			continue
		}
		raw, _ := json.Marshal(map[string]any{"path": map[string]string{"id": fmt.Sprint(id)}})
		out, err := a.Tools.Call(ctx, a.env(u), "parties.get", raw, nil)
		if err != nil {
			continue
		}
		b, _ := json.Marshal(out)
		var party map[string]any
		_ = json.Unmarshal(b, &party)
		name, _ := party["name"].(string)
		if name == "" {
			continue
		}
		same, err := a.readList(ctx, u, "parties.list", map[string]string{"q": name, "page_size": "50"})
		if err != nil {
			continue
		}
		var twins []map[string]any
		for _, r := range same {
			if normalize.Text(fmt.Sprint(r["name"])) == normalize.Text(name) && r["is_active"] != false {
				twins = append(twins, r)
			}
		}
		if len(twins) < 2 {
			continue
		}
		resolved := false
		var opts []classify.Option
		for _, r := range twins {
			code := fmt.Sprint(r["code"])
			if code != "" && strings.Contains(said, code) {
				resolved = true
			}
			role := "—"
			if v, _ := r["is_supplier"].(bool); v {
				role = "تأمین‌کننده"
			} else if v, _ := r["is_customer"].(bool); v {
				role = "مشتری"
			}
			opts = append(opts, classify.Option{Value: fmt.Sprintf("%s — کد %s", r["name"], code), Label: fmt.Sprintf("%s (%s، کد %s، مانده %v)", r["name"], role, code, r["balance"])})
		}
		if !resolved {
			return name, opts
		}
	}
	return "", nil
}
