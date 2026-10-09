package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

const jonkPlan = `{"action":"plan","summary":"Create customer jonk, then a sales quotation of 19,000,000 toman.",
 "steps":[
  {"id":"s1","tool":"parties.create","body":{"name":"jonk","kind":"company","is_customer":true},"purpose":"create customer jonk"},
  {"id":"s2","tool":"sales.kind.create","path":{"kind":"quotations"},"body":{"date":"2025-10-08","party_id":"$s1.id","warehouse_id":1,
    "items":[{"description":"سیستم","quantity":"1","unit_price":"190000000"}]},"purpose":"create the sales quotation"}]}`

// Acceptance (D39): «پیش فاکتور فروش سیستم به شرکت jonk ایجاد کن ۱۹ میلیون تومان» then «رسید pdf این رو بده»
// renders THAT quotation — no payment/expense search, no "which document?" question.
func TestPDFOfThisResolvesToTheQuotationJustCreated(t *testing.T) {
	h := newHarness(t, "admin")
	ctx := context.Background()
	h.ai.queue = []string{`{"intent":"operate","user_requested_registration":true}`}
	h.ai.planQueue = []string{jonkPlan}
	r1, conv := talk(t, h, "", "پیش فاکتور فروش سیستم به شرکت jonk ایجاد کن ۱۹ میلیون تومان")
	ap := approvalOf(t, r1)
	rec := &Recorder{}
	h.a.Approve(ctx, h.user, ap["task_id"].(string), ApproveRequest{Token: ap["token"].(string)}, rec)
	if rec.Last("result") == nil || len(h.acc.invoices) != 1 {
		t.Fatalf("quotation not created: %+v", rec.Events)
	}
	quote := h.acc.invoices[0]

	// the structured result — not the summary text — made it the active document
	view, _ := h.a.ConversationView(ctx, h.user, conv)
	act, _ := view["active"].(*Artifact)
	if act == nil || act.Type != "sales_quotation" || fmt.Sprint(act.ID) != fmt.Sprint(quote["id"]) || act.Label != "jonk" {
		t.Fatalf("active document %+v", view["active"])
	}

	h.acc.mu.Lock()
	before := len(h.acc.hits)
	h.acc.mu.Unlock()
	h.ai.queue = []string{`{"intent":"pdf"}`}
	r2, _ := talk(t, h, conv, "رسید pdf این رو بده")
	if q := r2.Last("question"); q != nil {
		t.Fatalf("asked which document instead of using the active one: %+v", q)
	}
	file, ok := r2.Last("file").(map[string]any)
	if !ok || !strings.HasPrefix(fmt.Sprint(file["url"]), "/ai-api/files/") || !strings.Contains(fmt.Sprint(file["name"]), "sales-quotation") {
		t.Fatalf("no PDF returned: %+v", r2.Events)
	}
	if len(h.acc.pdfs) != 1 {
		t.Fatalf("pdf service calls: %d", len(h.acc.pdfs))
	}
	doc := h.acc.pdfs[0]
	title := fmt.Sprint(doc["title"])
	if (title != "پیش‌فاکتور فروش" && title != "Sales quotation") || fmt.Sprint(doc["number"]) != fmt.Sprint(quote["number"]) {
		t.Fatalf("wrong document rendered: %+v", doc)
	}
	h.acc.mu.Lock()
	for _, hit := range h.acc.hits[before:] {
		if hit == "GET /api/v1/treasury/transactions" || hit == "GET /api/v1/expenses" {
			t.Fatalf("searched unrelated documents: %v", h.acc.hits[before:])
		}
	}
	h.acc.mu.Unlock()
	// the interpreter was told what "this" is
	if p := h.ai.prompts[len(h.ai.prompts)-1]; !strings.Contains(p, "ACTIVE") || !strings.Contains(p, "type=sales_quotation") {
		t.Fatalf("interpreter not given the active document:\n%s", p)
	}

	// «حالا تبدیلش کن به فاکتور»: the planner gets the active quotation's id, so it needs no search
	h.ai.queue = []string{`{"intent":"operate","user_requested_registration":true}`}
	talk(t, h, conv, "حالا تبدیلش کن به فاکتور")
	p := h.ai.planPrompts[len(h.ai.planPrompts)-1]
	if !strings.Contains(p, "<conversation_documents>") || !strings.Contains(p, fmt.Sprintf("type=sales_quotation id=%v", quote["id"])) {
		t.Fatalf("planner not given the active document:\n%s", p)
	}
}

func TestResolveReferencePriority(t *testing.T) {
	c := &conversation{}
	c.remember(Artifact{Type: "expense", ID: 3, Number: 3})
	c.remember(Artifact{Type: "sales_quotation", ID: 7, Number: 1, Side: "sales", Kind: "quotation", Label: "jonk"})
	c.remember(Artifact{Type: "party", ID: 9, Label: "jonk"})
	cases := []struct {
		text string
		want string // "type:id", or "" = not in context (search)
	}{
		{"PDF این رو بده", "sales_quotation:7"},           // party is newest but not printable
		{"رسید pdf همین پیش‌فاکتور", "sales_quotation:7"}, // «رسید» does not mean a payment receipt
		{"pdf هزینه رو بده", "expense:3"},                 // a named type picks the newest of that type
		{"pdf فاکتور خرید", ""},                           // nothing of that type in this conversation
		{"pdf سند شماره ۴۲", ""},                          // explicit number the conversation does not know
		{"pdf هزینه ۳", "expense:3"},                      // explicit number it does know
	}
	for _, tc := range cases {
		got := ""
		if a := c.resolveReference(tc.text, printable); a != nil {
			got = fmt.Sprintf("%s:%d", a.Type, a.ID)
		}
		if got != tc.want {
			t.Errorf("%q → %q, want %q", tc.text, got, tc.want)
		}
	}
	// referencing a document again moves it to the top instead of duplicating it
	c.remember(Artifact{Type: "expense", ID: 3, Number: 3})
	if n := len(c.state.Artifacts); n != 3 || c.state.Artifacts[n-1].Type != "expense" || c.state.Active.Type != "expense" {
		t.Fatalf("stack %+v", c.state.Artifacts)
	}
}
