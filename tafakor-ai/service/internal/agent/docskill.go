package agent

// Purchase invoice from the user's document (D56). The printed lines, discount and
// VAT become the invoice's items exactly; the supplier is resolved against the
// company's parties (exact name; several or similar ones are asked; a missing one is
// created in the same plan). The document's own arithmetic was checked at reading
// time — inconsistent numbers are shown, never trusted silently — and the invoice
// the application stores is compared with the document's total after the write.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/classify"
	"github.com/tafakorpardazan/tafakor-ai/internal/extract"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// purchaseDoc returns the single uploaded invoice with printed lines, if any.
func purchaseDoc(st *State) *extract.Extraction {
	if len(st.Extractions) != 1 {
		return nil
	}
	x := st.Extractions[0]
	if x == nil || x.Invoice == nil || len(x.Invoice.Lines) == 0 {
		return nil
	}
	return x
}

func dz(s string) decimal.Decimal {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil {
		return decimal.Zero
	}
	return d
}

// invoiceItems turns printed lines into application items: line discounts kept,
// the document discount spread over the lines in proportion (the last line takes
// the rounding remainder), and the document's VAT rate on every line.
func invoiceItems(inv *extract.Invoice) ([]map[string]any, decimal.Decimal) {
	var nets []decimal.Decimal
	gross := decimal.Zero
	for _, l := range inv.Lines {
		n := dz(l.Quantity).Mul(dz(l.UnitPrice)).Sub(dz(l.Discount))
		nets = append(nets, n)
		gross = gross.Add(n)
	}
	docDisc := dz(inv.Discount)
	rate := dz(inv.TaxRate)
	if rate.IsZero() && dz(inv.Tax).IsPositive() && gross.Sub(docDisc).IsPositive() {
		rate = dz(inv.Tax).Div(gross.Sub(docDisc)).Mul(decimal.NewFromInt(100)).Round(2)
	}
	var items []map[string]any
	left := docDisc
	for i, l := range inv.Lines {
		share := decimal.Zero
		if docDisc.IsPositive() && gross.IsPositive() {
			share = docDisc.Mul(nets[i]).Div(gross).Round(0)
			if i == len(inv.Lines)-1 {
				share = left
			}
			left = left.Sub(share)
		}
		desc := strings.TrimSpace(l.Description)
		if desc == "" {
			desc = fmt.Sprintf("ردیف %d", i+1)
		}
		items = append(items, map[string]any{"description": desc, "quantity": dz(l.Quantity).String(), "unit_price": dz(l.UnitPrice).String(),
			"discount": dz(l.Discount).Add(share).String(), "tax_rate": rate.String()})
	}
	return items, rate
}

// expectedTotal is what the application will compute for these items (per-line VAT).
func expectedTotal(items []map[string]any) decimal.Decimal {
	t := decimal.Zero
	for _, it := range items {
		net := dz(fmt.Sprint(it["quantity"])).Mul(dz(fmt.Sprint(it["unit_price"]))).Sub(dz(fmt.Sprint(it["discount"])))
		tax := net.Mul(dz(fmt.Sprint(it["tax_rate"]))).Div(decimal.NewFromInt(100)).Round(0)
		t = t.Add(net).Add(tax)
	}
	return t
}

func (a *Agent) purchaseFromDocument(ctx context.Context, u *User, t *store.Task, st *State, e Emitter) {
	x := purchaseDoc(st)
	p := st.Plan
	if p.Entities == nil {
		p.Entities = map[string]any{}
	}
	inv := x.Invoice
	seller := strings.TrimSpace(x.Sender.Value)
	if seller == "" {
		seller = strings.TrimSpace(x.Merchant.Value)
	}
	if seller == "" {
		if n := len(p.UserTexts); n > 1 {
			seller = strings.TrimSpace(p.UserTexts[n-1])
		} else {
			a.skillAsk(ctx, u, t, st, e, "Who is the seller on this invoice?", "فروشنده این فاکتور کیست؟", nil)
			return
		}
	}
	status(e, st.Lang, "searching")
	parties, err := a.readList(ctx, u, "parties.list", map[string]string{"page_size": "200"})
	e.Emit("tool", map[string]any{"kind": "read", "ok": err == nil, "tool": "parties.list"})
	if err != nil {
		a.fail(ctx, u, t, st, e, a.plain(st.Lang, err), err)
		return
	}
	var calls []plannerCall
	partyRef := any(nil)
	party, newParty, ask := chooseParty(p, parties, seller)
	switch {
	case len(ask) > 0:
		a.skillAsk(ctx, u, t, st, e, "Which counterparty is the seller «"+seller+"»?",
			"فروشنده «"+seller+"» کدام طرف حساب است؟ (چند طرف حساب مشابه وجود دارد)", ask)
		return
	case newParty:
		calls = append(calls, plannerCall{ID: "s1", Tool: "parties.create", Body: mustJSON(map[string]any{"name": seller, "kind": "company", "is_supplier": true}),
			Purpose: tr(st.Lang, "Create supplier «"+seller+"»", "ایجاد تأمین‌کننده «"+seller+"»")})
		partyRef = "$s1.id"
	default:
		partyRef = int64Of(party["id"])
		if sup, _ := party["is_supplier"].(bool); !sup {
			full := map[string]any{}
			for k, v := range party {
				full[k] = v
			}
			for _, k := range []string{"id", "balance", "created_at", "updated_at", "code"} {
				delete(full, k)
			}
			full["is_supplier"] = true
			calls = append(calls, plannerCall{ID: "s1", Tool: "parties.update", Path: map[string]string{"id": strconv.FormatInt(int64Of(party["id"]), 10)},
				Body: mustJSON(full), Purpose: tr(st.Lang, "Mark «"+seller+"» as a supplier too", "«"+seller+"» به‌عنوان تأمین‌کننده هم علامت بخورد")})
		}
	}
	date := x.Date.Value
	if !x.Date.Valid || date == "" {
		if n := len(p.UserTexts); n > 1 {
			if d, _, err := normalize.Date(normalize.Digits(strings.TrimSpace(p.UserTexts[n-1]))); err == nil {
				date = d.Format("2006-01-02")
			}
		}
		if date == "" || !x.Date.Valid && date == x.Date.Value {
			a.skillAsk(ctx, u, t, st, e, "The invoice date is not readable. What is it?", "تاریخ فاکتور خوانا نیست. تاریخ آن چیست؟ (مثلاً ۱۴۰۵/۰۷/۱۰)", nil)
			return
		}
	}
	items, rate := invoiceItems(inv)
	body := map[string]any{"date": date, "party_id": partyRef, "reference": inv.Number, "items": items, "post": true,
		"notes": tr(st.Lang, "From the attached document "+x.FileName, "از سند پیوست "+x.FileName)}
	if inv.Number == "" {
		body["reference"] = x.Reference.Value
	}
	total := expectedTotal(items)
	docTotal := dz(inv.Total)
	if docTotal.IsZero() {
		docTotal = dz(x.Amount.Value)
	}
	id := fmt.Sprintf("s%d", len(calls)+1)
	summary := tr(st.Lang, fmt.Sprintf("Purchase invoice %s from «%s»: %d lines, VAT %s%%, total %s", inv.Number, seller, len(items), rate.String(), groupDigits(total)),
		fmt.Sprintf("فاکتور خرید %s از «%s»: %d ردیف، مالیات بر ارزش افزوده %s٪، جمع کل %s ریال", inv.Number, seller, len(items), rate.String(), groupDigits(total)))
	calls = append(calls, plannerCall{ID: id, Tool: "purchases.kind.create", Path: map[string]string{"kind": "invoices"}, Body: mustJSON(body), Purpose: summary})
	if !inv.Consistent {
		// the numbers did not add up when the document was read: a person checks them
		st.AskThisTime = true
		e.Emit("message", map[string]any{"text": tr(st.Lang, "Check the amounts: the document's own sums do not agree ("+strings.Join(inv.Problems, "; ")+").",
			"مبالغ را بررسی کنید: جمع‌های خود سند با هم نمی‌خوانند ("+strings.Join(inv.Problems, "؛ ")+").")})
	}
	if !docTotal.IsZero() && !total.Equal(docTotal) {
		e.Emit("message", map[string]any{"text": tr(st.Lang, fmt.Sprintf("Note: the invoice will total %s; the document says %s.", groupDigits(total), groupDigits(docTotal)),
			fmt.Sprintf("توجه: جمع فاکتور در نرم‌افزار %s می‌شود؛ سند %s نوشته است.", groupDigits(total), groupDigits(docTotal)))})
	}
	p.Entities["expect_total"] = total.String()
	a.proposeSkillSteps(ctx, u, t, st, e, calls, summary)
	// the stored invoice must show the total the document's lines produce
	for i := range st.Plan.Steps {
		if st.Plan.Steps[i].Tool == "purchases.kind.create" && st.Plan.Steps[i].Status != "done" {
			st.Plan.Steps[i].Expect = map[string]string{"total": total.String()}
		}
	}
	a.save(ctx, t, st)
}

// chooseParty resolves the seller: one exact counterparty; a new one when nothing
// is similar; otherwise the user's choice among the exact duplicates / similar
// names (answered with "party:<id>" or "new").
func chooseParty(p *PlanState, parties []map[string]any, name string) (map[string]any, bool, []classify.Option) {
	if n := len(p.UserTexts); n > 1 {
		last := strings.TrimSpace(p.UserTexts[n-1])
		if strings.HasPrefix(last, "party:") {
			id, _ := strconv.ParseInt(strings.TrimPrefix(last, "party:"), 10, 64)
			for _, r := range parties {
				if int64Of(r["id"]) == id {
					return r, false, nil
				}
			}
		}
		if strings.Contains(normalize.Text(last), normalize.Text("جدید")) || strings.Contains(strings.ToLower(last), "new") {
			return nil, true, nil
		}
	}
	n := normalize.Text(name)
	var exact, similar []map[string]any
	for _, r := range parties {
		v := normalize.Text(fmt.Sprint(r["name"]))
		switch {
		case v == n:
			exact = append(exact, r)
		case v != "" && (strings.Contains(v, n) || strings.Contains(n, v)):
			similar = append(similar, r)
		}
	}
	if len(exact) == 1 {
		return exact[0], false, nil
	}
	cands := exact
	if len(cands) == 0 {
		cands = similar
	}
	if len(cands) == 0 {
		return nil, true, nil
	}
	var opts []classify.Option
	for _, r := range cands {
		role := "—"
		if b, _ := r["is_supplier"].(bool); b {
			role = "تأمین‌کننده"
		} else if b, _ := r["is_customer"].(bool); b {
			role = "مشتری"
		}
		opts = append(opts, classify.Option{Value: fmt.Sprintf("party:%d", int64Of(r["id"])), Label: fmt.Sprintf("%v (%v، کد %v)", r["name"], role, r["code"])})
	}
	if len(exact) == 0 {
		opts = append(opts, classify.Option{Value: "new", Label: "طرف حساب جدید: «" + name + "»"})
	}
	return nil, false, opts
}
