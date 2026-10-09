package agent

// Transfer between the company's own cash/bank accounts (audit 2026-10-08). The
// resolver had no transfer path and handed every transfer to the model planner,
// which with the small model kept trying to create accounts that already exist.
// Both accounts are resolved from the user's own words («از X به Y»); a missing or
// unclear one is asked with the company's accounts as options.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/intent"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

const skillTransfer = "transfer"

type acctHit struct {
	acct tools.TreasuryAccount
	pos  int
	role string // from | to | ""
}

// transferAccounts finds the accounts named in the text and their roles from the
// word before them (از → source; به/داخل/توی → destination).
func transferAccounts(text string, accts []tools.TreasuryAccount) (from, to *tools.TreasuryAccount, named []acctHit) {
	t := " " + normalize.Text(text) + " "
	for _, a := range accts {
		if !a.IsActive {
			continue
		}
		n := normalize.Text(a.Name)
		if n == "" {
			continue
		}
		i := strings.Index(t, " "+n+" ")
		if i < 0 {
			continue
		}
		before := strings.Fields(t[:i])
		role := ""
		if len(before) > 0 {
			switch before[len(before)-1] {
			case "از", "from":
				role = "from"
			case "به", "داخل", "توی", "into", "to":
				role = "to"
			}
		}
		named = append(named, acctHit{a, i, role})
	}
	// a longer name inside which a shorter one was found wins («بانک ملت جاری» over «بانک ملت»)
	sort.Slice(named, func(i, j int) bool { return len(named[i].acct.Name) > len(named[j].acct.Name) })
	var kept []acctHit
	for _, h := range named {
		inside := false
		for _, k := range kept {
			if h.pos >= k.pos && h.pos < k.pos+len(normalize.Text(k.acct.Name))+1 {
				inside = true
			}
		}
		if !inside {
			kept = append(kept, h)
		}
	}
	// a generic «صندوق» / «بانک» after از/به is that kind's only active account
	words := strings.Fields(t)
	for i := 0; i+1 < len(words); i++ {
		kind := map[string]string{"صندوق": "cash", "بانک": "bank", "cash": "cash", "bank": "bank"}[words[i+1]]
		role := map[string]string{"از": "from", "from": "from", "به": "to", "to": "to", "into": "to"}[words[i]]
		if kind == "" || role == "" {
			continue
		}
		var only *tools.TreasuryAccount
		n := 0
		for j := range accts {
			if accts[j].IsActive && accts[j].Kind == kind {
				only, n = &accts[j], n+1
			}
		}
		if n == 1 {
			kept = append(kept, acctHit{*only, -1, role})
		}
	}
	for i := range kept {
		h := kept[i]
		switch h.role {
		case "from":
			if from == nil {
				from = &kept[i].acct
			}
		case "to":
			if to == nil {
				to = &kept[i].acct
			}
		}
	}
	return from, to, kept
}

func (a *Agent) transferSkill(ctx context.Context, u *User, t *store.Task, st *State, cfg *tools.CompanyConfig, e Emitter) {
	p := st.Plan
	text := st.skillText()
	from, to, _ := transferAccounts(text, cfg.TreasuryAccounts)
	opts := func(exclude *tools.TreasuryAccount) []classify.Option {
		var o []classify.Option
		for _, x := range cfg.TreasuryAccounts {
			if x.IsActive && (exclude == nil || x.ID != exclude.ID) {
				o = append(o, classify.Option{Value: x.Name, Label: x.Name})
			}
		}
		return o
	}
	// an answer to "from which / to which account" is just the account's name
	if n := len(p.UserTexts); n > 1 {
		last := normalize.Text(p.UserTexts[n-1])
		for i, x := range cfg.TreasuryAccounts {
			if normalize.Text(x.Name) == last {
				if p.Entities["asked"] == "from" {
					from = &cfg.TreasuryAccounts[i]
				} else if p.Entities["asked"] == "to" {
					to = &cfg.TreasuryAccounts[i]
				}
			}
		}
	}
	if from != nil {
		p.Entities["from"] = from.ID
	} else if id, ok := p.Entities["from"].(float64); ok {
		from = acctByID(cfg, int64(id))
	}
	if to != nil {
		p.Entities["to"] = to.ID
	} else if id, ok := p.Entities["to"].(float64); ok {
		to = acctByID(cfg, int64(id))
	}
	if from == nil {
		p.Entities["asked"] = "from"
		a.skillAsk(ctx, u, t, st, e, "From which cash/bank account?", "از کدام صندوق یا حساب بانکی منتقل شد؟", opts(to))
		return
	}
	if to == nil {
		p.Entities["asked"] = "to"
		a.skillAsk(ctx, u, t, st, e, "To which cash/bank account?", "به کدام صندوق یا حساب بانکی منتقل شد؟", opts(from))
		return
	}
	if from.ID == to.ID {
		p.Entities["asked"] = "to"
		a.skillAsk(ctx, u, t, st, e, "Source and destination are the same account. To which account?", "مبدأ و مقصد یکی است. به کدام حساب منتقل شد؟", opts(from))
		return
	}
	amount, cur := intent.Amount(text)
	rate, ok := baseAmount(amount, cur, u.Me.Company.BaseCurrency)
	if !ok {
		p.Entities["asked"] = "amount"
		a.skillAsk(ctx, u, t, st, e, "How much was transferred?", "مبلغ انتقال چقدر بود؟", nil)
		return
	}
	date := a.today()
	if st.Interp != nil {
		if d, _, err := normalize.Date(normalize.Digits(st.Interp.Event.Date)); err == nil && st.Interp.Event.Date != "" {
			date = d.Format("2006-01-02")
		}
	}
	desc := tr(st.Lang, "Transfer from "+from.Name+" to "+to.Name, "انتقال از "+from.Name+" به "+to.Name)
	body := map[string]any{"kind": "transfer", "date": date, "treasury_account_id": from.ID, "to_treasury_account_id": to.ID,
		"amount": rate.String(), "description": desc}
	summary := tr(st.Lang, fmt.Sprintf("%s: %s", desc, a.moneyText(st, rate, u.Me.Company.BaseCurrency)),
		fmt.Sprintf("%s: %s", desc, a.moneyText(st, rate, u.Me.Company.BaseCurrency)))
	a.proposeSkillStep(ctx, u, t, st, e, "treasury.transactions.create", map[string]string{}, body, summary)
}

func acctByID(cfg *tools.CompanyConfig, id int64) *tools.TreasuryAccount {
	for i := range cfg.TreasuryAccounts {
		if cfg.TreasuryAccounts[i].ID == id {
			return &cfg.TreasuryAccounts[i]
		}
	}
	return nil
}
