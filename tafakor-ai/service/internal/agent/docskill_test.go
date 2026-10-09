package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

const ariaInvoice = `{"document_kind":"invoice","amount":{"value":"126,500,000","confidence":0.9},"currency":{"value":"IRR","confidence":0.9},
 "date":{"value":"1405/07/10","confidence":0.9},"sender":{"value":"فروشگاه لوازم اداری آریا","confidence":0.9},
 "invoice":{"number":"ف-8820","lines":[{"description":"کاغذ A4","quantity":"20","unit_price":"1,850,000","amount":"37,000,000"},
   {"description":"تونر HP 85A","quantity":"3","unit_price":"14,200,000","amount":"42,600,000"},
   {"description":"سرویس نگهداری","quantity":"1","unit_price":"40,400,000","amount":"40,400,000"}],
   "subtotal":"120,000,000","discount":"5,000,000","tax":"11,500,000","tax_rate":"10","total":"126,500,000"}}`

const purchaseInterp = `{"intent":"register","user_requested_registration":true,"confidence":0.9,
 "event":{"type":"purchase","counterparty":"فروشگاه لوازم اداری آریا","amount":"126500000","currency":"IRR","date":"2026-10-02"}}`

func addPurchases(t *testing.T, h *harness) *[]map[string]any {
	var invs []map[string]any
	h.acc.extra.HandleFunc("POST /api/v1/purchases/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		total := 0.0
		for _, it := range in["items"].([]any) {
			m := it.(map[string]any)
			var q, p, d, tr float64
			fmt.Sscan(fmt.Sprint(m["quantity"]), &q)
			fmt.Sscan(fmt.Sprint(m["unit_price"]), &p)
			fmt.Sscan(fmt.Sprint(m["discount"]), &d)
			fmt.Sscan(fmt.Sprint(m["tax_rate"]), &tr)
			net := q*p - d
			total += net + float64(int64(net*tr/100+0.5))
		}
		h.acc.mu.Lock()
		in["id"] = 700 + len(invs)
		in["total"] = fmt.Sprintf("%.0f", total)
		in["status"] = "posted"
		invs = append(invs, in)
		h.acc.mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": in["id"]})
	})
	h.acc.extra.HandleFunc("GET /api/v1/purchases/{kind}/{id}", func(w http.ResponseWriter, r *http.Request) {
		h.acc.mu.Lock()
		defer h.acc.mu.Unlock()
		for _, x := range invs {
			if fmt.Sprint(x["id"]) == r.PathValue("id") {
				_ = json.NewEncoder(w).Encode(x)
				return
			}
		}
		w.WriteHeader(404)
	})
	return &invs
}

// Live finding (D3/D4): purchase invoices from documents went to the model planner,
// which lumped everything into one 0%-VAT line and, with the small model, returned
// the same malformed JSON until "I couldn't work out a safe way". Now the printed
// lines, discount and VAT become the invoice deterministically.
func TestPurchaseInvoiceFromDocumentIsBuiltFromItsLines(t *testing.T) {
	h := newHarness(t, "admin")
	h.user.Me.Language = "fa"
	h.user.Me.Permissions = append(h.user.Me.Permissions, "purchases.create", "purchases.post", "purchases.view")
	invs := addPurchases(t, h)
	h.ai.extraction = ariaInvoice
	h.ai.queue = []string{purchaseInterp}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "فاکتور خرید ملزومات اداری را ثبت کن", Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	pv, _ := rec.Last("preview").(map[string]any)
	if pv == nil || len(h.ai.planPrompts) != 0 {
		t.Fatalf("expected a deterministic plan (no model planner): %+v", rec.Events)
	}
	steps := pv["steps"].([]map[string]any)
	if len(steps) != 2 || steps[0]["tool"] != "parties.create" || steps[1]["tool"] != "purchases.kind.create" {
		t.Fatalf("supplier is new → create it, then the invoice: %+v", steps)
	}
	var args tools.OpArgs
	_ = json.Unmarshal(steps[1]["args"].(json.RawMessage), &args)
	body := string(args.Body)
	for _, want := range []string{`"date":"2026-10-02"`, `"reference":"ف-8820"`, `"tax_rate":"10"`, `"unit_price":"1850000"`, `"party_id":"$s1.id"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %s: %s", want, body)
		}
	}
	res := approve(t, h, rec)
	out, ok := res.Last("result").(map[string]any)
	if !ok || !out["result"].(*Result).Verified || len(*invs) != 1 || (*invs)[0]["total"] != "126500000" {
		t.Fatalf("invoice total must equal the document's 126,500,000: %+v / %v", res.Events, *invs)
	}
}

func TestDuplicateSupplierIsAskedForDocumentInvoices(t *testing.T) {
	h := newHarness(t, "admin")
	h.user.Me.Language = "fa"
	h.user.Me.Permissions = append(h.user.Me.Permissions, "purchases.create", "purchases.post", "purchases.view")
	addPurchases(t, h)
	h.acc.parties = append(h.acc.parties,
		map[string]any{"id": 300, "code": "0300", "name": "فروشگاه لوازم اداری آریا", "kind": "company", "is_supplier": true, "is_active": true},
		map[string]any{"id": 301, "code": "0301", "name": "فروشگاه لوازم اداری آریا", "kind": "company", "is_supplier": true, "is_active": true})
	h.ai.extraction = ariaInvoice
	h.ai.queue = []string{purchaseInterp}
	rec := &Recorder{}
	h.a.Start(context.Background(), h.user, StartRequest{Message: "این فاکتور خرید را ثبت کن", Uploads: []string{h.upload(t)}, ExecMode: "api"}, rec)
	q := questions(rec)
	if len(q) != 1 || !strings.Contains(q[0], "party:300") || !strings.Contains(q[0], "party:301") {
		t.Fatalf("two identical suppliers must be asked: %v", q)
	}
	r2, _ := talk(t, h, fmt.Sprint(rec.Last("conversation").(map[string]any)["conversation_id"]), "party:301")
	steps := r2.Last("preview").(map[string]any)["steps"].([]map[string]any)
	var args tools.OpArgs
	_ = json.Unmarshal(steps[0]["args"].(json.RawMessage), &args)
	if len(steps) != 1 || !strings.Contains(string(args.Body), `"party_id":301`) {
		t.Fatalf("chosen supplier not used: %+v", steps)
	}
}
