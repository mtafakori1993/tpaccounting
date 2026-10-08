package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
)

// TestSalesAndPurchaseWorkflow covers the definition-of-done workflow:
// parties → product → purchase → sale → receipt/payment → balances and reports.
func TestSalesAndPurchaseWorkflow(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)

	cust := c.must("POST", "/parties", map[string]any{"name": "Customer A", "is_customer": true})
	supp := c.must("POST", "/parties", map[string]any{"name": "Supplier B", "is_supplier": true})
	laptop := c.must("POST", "/products", map[string]any{"name": "Laptop", "kind": "goods", "tax_rate": "10"})
	support := c.must("POST", "/products", map[string]any{"name": "Support", "kind": "service", "tax_rate": "10"})
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Bank", "bank_name": "Mellat",
		"opening_balance": "100000000", "opening_date": today})
	if str(bank, "balance") != "100000000" {
		t.Fatalf("opening balance not posted: %v", bank)
	}

	pi := c.must("POST", "/purchases/invoices", map[string]any{"date": today, "party_id": id(supp), "post": true,
		"items": []any{map[string]any{"product_id": id(laptop), "quantity": "10", "unit_price": "1000000", "tax_rate": "10"}}})
	if str(pi, "status") != "posted" || str(pi, "total") != "11000000" {
		t.Fatalf("purchase: %v", pi)
	}

	si := c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true, "items": []any{
		map[string]any{"product_id": id(laptop), "quantity": "4", "unit_price": "1500000", "discount": "100000", "tax_rate": "10"},
		map[string]any{"product_id": id(support), "quantity": "2", "unit_price": "500000", "tax_rate": "10"},
	}})
	for k, want := range map[string]string{"subtotal": "7000000", "discount_total": "100000", "tax_total": "690000", "total": "7590000"} {
		if got := str(si, k); got != want {
			t.Errorf("sales %s = %s, want %s", k, got, want)
		}
	}

	// The generated entry must be balanced and hit the configured accounts.
	je := c.must("GET", "/journal-entries/"+str(si, "journal_entry_id"), nil)
	if str(je, "total_debit") != str(je, "total_credit") || str(je, "source_type") != "sales_invoice" {
		t.Fatalf("sales entry: %v", je)
	}

	rc := c.must("POST", "/treasury/transactions", map[string]any{"kind": "receipt", "date": today, "treasury_account_id": id(bank),
		"party_id": id(cust), "amount": "5000000", "auto_allocate": true})
	if str(rc, "allocated") != "5000000" {
		t.Fatalf("receipt not allocated: %v", rc)
	}
	si = c.must("GET", "/sales/invoices/"+str(si, "id"), nil)
	if str(si, "payment_status") != "partial" || str(si, "outstanding") != "2590000" {
		t.Fatalf("invoice after receipt: %v", si)
	}
	c.must("POST", "/treasury/transactions", map[string]any{"kind": "payment", "date": today, "treasury_account_id": id(bank),
		"party_id": id(supp), "amount": "11000000", "auto_allocate": true})
	if pi = c.must("GET", "/purchases/invoices/"+str(pi, "id"), nil); str(pi, "payment_status") != "paid" {
		t.Fatalf("purchase should be paid: %v", pi)
	}

	// Party balances.
	if b := str(c.must("GET", "/parties/"+str(cust, "id"), nil), "balance"); b != "2590000" {
		t.Errorf("customer balance %s", b)
	}
	if b := str(c.must("GET", "/parties/"+str(supp, "id"), nil), "balance"); b != "0" {
		t.Errorf("supplier balance %s", b)
	}
	stmt := c.must("GET", "/reports/ledger?party_id="+str(cust, "id"), nil)
	if str(stmt, "closing") != "2590000" || len(stmt["rows"].([]any)) != 2 {
		t.Errorf("party statement: %v", stmt)
	}

	// Account balances: bank = 100M + 5M − 11M.
	if b := str(c.must("GET", "/treasury/accounts/"+str(bank, "id"), nil), "balance"); b != "94000000" {
		t.Errorf("bank balance %s", b)
	}

	// Reports.
	tb := sub(c.must("GET", "/reports/trial-balance?level=3", nil), "totals")
	if str(tb, "closing_debit") != str(tb, "closing_credit") || str(tb, "period_debit") != str(tb, "period_credit") {
		t.Errorf("trial balance not balanced: %v", tb)
	}
	pl := c.must("GET", "/reports/profit-loss", nil)
	for k, want := range map[string]string{"total_revenue": "6900000", "total_cost_of_sales": "4000000", "gross_profit": "2900000", "net_profit": "2900000"} {
		if got := str(pl, k); got != want {
			t.Errorf("P&L %s = %s, want %s", k, got, want)
		}
	}
	bs := c.must("GET", "/reports/balance-sheet", nil)
	if bs["balanced"] != true || str(bs, "current_earnings") != "2900000" {
		t.Errorf("balance sheet: %v", bs)
	}
	stock := items(c.must("GET", "/inventory/stock", nil))
	if len(stock) != 1 || str(stock[0], "quantity") != "6" || str(stock[0], "value") != "6000000" {
		t.Errorf("stock: %v", stock)
	}
	recv := sub(c.must("GET", "/reports/receivables", nil), "total")
	if str(recv, "balance") != "2590000" {
		t.Errorf("receivables %v", recv)
	}
}

func TestJournalRules(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cash := accountByCode(c, "5101")
	capital := accountByCode(c, "6201")
	group := accountByCode(c, "1")

	c.expect(http.StatusUnprocessableEntity, "unbalanced", "POST", "/journal-entries", map[string]any{"date": today, "post": true,
		"lines": []any{map[string]any{"account_id": cash, "debit": "100"}, map[string]any{"account_id": capital, "credit": "90"}}})
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", "/journal-entries", map[string]any{"date": today,
		"lines": []any{map[string]any{"account_id": group, "debit": "100"}, map[string]any{"account_id": capital, "credit": "100"}}})
	c.expect(http.StatusUnprocessableEntity, "no_fiscal_year", "POST", "/journal-entries", map[string]any{"date": "2001-01-01",
		"lines": []any{map[string]any{"account_id": cash, "debit": "1"}, map[string]any{"account_id": capital, "credit": "1"}}})

	draft := c.must("POST", "/journal-entries", map[string]any{"date": today, "description": "draft",
		"lines": []any{map[string]any{"account_id": cash, "debit": "100"}, map[string]any{"account_id": capital, "credit": "90"}}})
	c.expect(http.StatusUnprocessableEntity, "unbalanced", "POST", "/journal-entries/"+str(draft, "id")+"/post", nil)
	c.must("PUT", "/journal-entries/"+str(draft, "id"), map[string]any{"date": today, "post": true,
		"lines": []any{map[string]any{"account_id": cash, "debit": "100"}, map[string]any{"account_id": capital, "credit": "100"}}})

	// Posted entries cannot be edited or deleted, only cancelled through a reversal.
	c.expect(http.StatusUnprocessableEntity, "not_draft", "PUT", "/journal-entries/"+str(draft, "id"), map[string]any{"date": today,
		"lines": []any{map[string]any{"account_id": cash, "debit": "5"}, map[string]any{"account_id": capital, "credit": "5"}}})
	c.expect(http.StatusUnprocessableEntity, "not_deletable", "DELETE", "/journal-entries/"+str(draft, "id"), nil)
	cancelled := c.must("POST", "/journal-entries/"+str(draft, "id")+"/cancel", map[string]any{})
	if str(cancelled, "status") != "cancelled" || cancelled["reversed_by_id"] == nil {
		t.Fatalf("cancel: %v", cancelled)
	}
	led := c.must("GET", "/reports/ledger?account_id="+fmt.Sprint(cash), nil)
	if str(led, "closing") != "0" || len(led["rows"].([]any)) != 2 {
		t.Fatalf("reversal should net to zero: %v", led)
	}
	// Duplicate document numbers are reported with a friendly code.
	c.expect(http.StatusConflict, "duplicate_document_number", "POST", "/journal-entries", map[string]any{"number": cancelled["number"], "date": today,
		"lines": []any{map[string]any{"account_id": cash, "debit": "1"}, map[string]any{"account_id": capital, "credit": "1"}}})
}

func TestInventoryMovingAverageAndCancellation(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "C", "is_customer": true})
	supp := c.must("POST", "/parties", map[string]any{"name": "S", "is_supplier": true})
	p := c.must("POST", "/products", map[string]any{"name": "Widget", "kind": "goods"})
	buy := func(price string) {
		c.must("POST", "/purchases/invoices", map[string]any{"date": today, "party_id": id(supp), "post": true,
			"items": []any{map[string]any{"product_id": id(p), "quantity": "10", "unit_price": price}}})
	}
	buy("100")
	buy("200")
	sale := c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true,
		"items": []any{map[string]any{"product_id": id(p), "quantity": "5", "unit_price": "500"}}})
	pl := c.must("GET", "/reports/profit-loss", nil)
	if str(pl, "total_cost_of_sales") != "750" {
		t.Fatalf("moving average COGS should be 750: %v", pl)
	}
	stock := items(c.must("GET", "/inventory/stock", nil))
	if str(stock[0], "quantity") != "15" || str(stock[0], "value") != "2250" {
		t.Fatalf("stock after sale: %v", stock)
	}
	// Overselling is blocked and leaves no partial data behind.
	c.expect(http.StatusUnprocessableEntity, "insufficient_stock", "POST", "/sales/invoices", map[string]any{"date": today,
		"party_id": id(cust), "post": true, "items": []any{map[string]any{"product_id": id(p), "quantity": "100", "unit_price": "1"}}})
	if n := c.must("GET", "/sales/invoices", nil)["total"].(float64); n != 1 {
		t.Fatalf("failed invoice must roll back completely, found %v invoices", n)
	}

	// Cancelling the sale restores stock and reverses the accounting.
	c.must("POST", "/sales/invoices/"+str(sale, "id")+"/cancel", map[string]any{})
	stock = items(c.must("GET", "/inventory/stock", nil))
	if str(stock[0], "quantity") != "20" || str(stock[0], "value") != "3000" {
		t.Fatalf("stock after cancel: %v", stock)
	}
	pl = c.must("GET", "/reports/profit-loss", nil)
	if str(pl, "net_profit") != "0" {
		t.Fatalf("cancelled sale must not affect profit: %v", pl)
	}
	ledger := c.must("GET", "/inventory/ledger?product_id="+str(p, "id"), nil)
	if str(ledger, "closing_qty") != "20" || len(ledger["movements"].([]any)) != 4 {
		t.Fatalf("stock ledger: %v", ledger)
	}

	// Stock adjustment (shrinkage) books to the adjustment account.
	wh := items(c.must("GET", "/inventory/warehouses", nil))[0]
	c.must("POST", "/inventory/documents", map[string]any{"kind": "adjustment", "date": today, "warehouse_id": id(wh), "post": true,
		"lines": []any{map[string]any{"product_id": id(p), "quantity": "-2"}}})
	stock = items(c.must("GET", "/inventory/stock", nil))
	if str(stock[0], "quantity") != "18" || str(stock[0], "value") != "2700" {
		t.Fatalf("stock after adjustment: %v", stock)
	}
	if bs := c.must("GET", "/reports/balance-sheet", nil); bs["balanced"] != true {
		t.Fatalf("balance sheet must stay balanced: %v", bs)
	}
}

func TestPaymentCancellationReleasesAllocation(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "C", "is_customer": true})
	cash := c.must("POST", "/treasury/accounts", map[string]any{"kind": "cash", "name": "Cash"})
	inv := func(amount string) map[string]any {
		return c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true,
			"items": []any{map[string]any{"description": "Consulting", "quantity": "1", "unit_price": amount}}})
	}
	a, b := inv("300"), inv("500")
	rc := c.must("POST", "/treasury/transactions", map[string]any{"kind": "receipt", "date": today, "treasury_account_id": id(cash),
		"party_id": id(cust), "amount": "400", "allocations": []any{map[string]any{"invoice_id": id(b), "amount": "400"}}})
	if got := str(c.must("GET", "/sales/invoices/"+str(b, "id"), nil), "paid_amount"); got != "400" {
		t.Fatalf("explicit allocation: %s", got)
	}
	if got := str(c.must("GET", "/sales/invoices/"+str(a, "id"), nil), "paid_amount"); got != "0" {
		t.Fatalf("other invoice untouched: %s", got)
	}
	// Over-allocation is rejected.
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", "/treasury/transactions", map[string]any{"kind": "receipt", "date": today,
		"treasury_account_id": id(cash), "party_id": id(cust), "amount": "1000",
		"allocations": []any{map[string]any{"invoice_id": id(a), "amount": "301"}}})
	// Paid invoices cannot be cancelled until the payment is cancelled.
	c.expect(http.StatusUnprocessableEntity, "has_payments", "POST", "/sales/invoices/"+str(b, "id")+"/cancel", map[string]any{})
	c.must("POST", "/treasury/transactions/"+str(rc, "id")+"/cancel", map[string]any{})
	if got := str(c.must("GET", "/sales/invoices/"+str(b, "id"), nil), "paid_amount"); got != "0" {
		t.Fatalf("allocation not released: %s", got)
	}
	c.must("POST", "/sales/invoices/"+str(b, "id")+"/cancel", map[string]any{})
	if bal := str(c.must("GET", "/parties/"+str(cust, "id"), nil), "balance"); bal != "300" {
		t.Fatalf("customer balance after cancellations: %s", bal)
	}
	// Cash cannot go negative.
	c.expect(http.StatusUnprocessableEntity, "insufficient_funds", "POST", "/treasury/transactions", map[string]any{"kind": "payment",
		"date": today, "treasury_account_id": id(cash), "party_id": id(cust), "amount": "1"})
}

func TestFiscalYearClosing(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "C", "is_customer": true})
	c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true,
		"items": []any{map[string]any{"description": "Service", "quantity": "1", "unit_price": "1000"}}})
	years := items(c.must("GET", "/company/fiscal-years", nil))
	fy := years[0]
	closed := c.must("POST", "/company/fiscal-years/"+str(fy, "id")+"/close", map[string]any{})
	if str(closed, "status") != "closed" || closed["closing_entry_id"] == nil {
		t.Fatalf("close: %v", closed)
	}
	// Income statement of the closed year still shows the profit.
	pl := c.must("GET", "/reports/profit-loss?from="+str(fy, "start_date")+"&to="+str(fy, "end_date"), nil)
	if str(pl, "net_profit") != "1000" {
		t.Fatalf("closed-year P&L: %v", pl)
	}
	// The profit now sits in retained earnings and the balance sheet still balances.
	bs := c.must("GET", "/reports/balance-sheet?as_of="+str(fy, "end_date"), nil)
	if bs["balanced"] != true || str(bs, "current_earnings") != "0" || str(bs, "total_equity") != "1000" {
		t.Fatalf("post-closing balance sheet: %v", bs)
	}
	c.expect(http.StatusUnprocessableEntity, "fiscal_year_closed", "POST", "/sales/invoices", map[string]any{"date": today,
		"party_id": id(cust), "post": true, "items": []any{map[string]any{"description": "x", "quantity": "1", "unit_price": "1"}}})
	reopened := c.must("POST", "/company/fiscal-years/"+str(fy, "id")+"/reopen", map[string]any{})
	if str(reopened, "status") != "open" {
		t.Fatalf("reopen: %v", reopened)
	}
	if bs := c.must("GET", "/reports/balance-sheet", nil); str(bs, "current_earnings") != "1000" || bs["balanced"] != true {
		t.Fatalf("after reopen: %v", bs)
	}
}

func TestPermissions(t *testing.T) {
	srv := newApp(t)
	admin := setupCompany(t, srv)
	var viewerRole int64
	for _, r := range items(admin.must("GET", "/roles", nil)) {
		if r["code"] == "viewer" {
			viewerRole = id(r)
		}
	}
	admin.must("POST", "/users", map[string]any{"email": "viewer@example.com", "full_name": "Viewer", "password": "Viewer12345", "role_id": viewerRole})

	v := newClient(t, srv)
	v.expect(http.StatusUnauthorized, "unauthorized", "GET", "/accounts", nil)
	v.expect(http.StatusUnauthorized, "invalid_credentials", "POST", "/auth/login", map[string]any{"email": "viewer@example.com", "password": "wrong-password"})
	v.must("POST", "/auth/login", map[string]any{"email": "viewer@example.com", "password": "Viewer12345"})
	v.must("GET", "/accounts", nil)
	v.must("GET", "/reports/trial-balance", nil)
	v.expect(http.StatusForbidden, "forbidden", "POST", "/parties", map[string]any{"name": "x", "is_customer": true})
	v.expect(http.StatusForbidden, "forbidden", "GET", "/users", nil)
	v.expect(http.StatusForbidden, "forbidden", "GET", "/audit", nil)

	// Missing CSRF token is rejected for state-changing requests.
	req, _ := http.NewRequest("POST", v.base+"/auth/change-password", nil)
	res, err := v.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF token should be rejected, got %d", res.StatusCode)
	}

	// Logout revokes the session.
	v.must("POST", "/auth/logout", nil)
	v.expect(http.StatusUnauthorized, "unauthorized", "GET", "/accounts", nil)

	// Setup is one-shot.
	admin.expect(http.StatusConflict, "already_setup", "POST", "/setup", map[string]any{
		"company": map[string]any{"name": "Other"}, "admin": map[string]any{"full_name": "x", "email": "x@x.io", "password": "12345678"}})
}

func TestChartOfAccountsRules(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	sales := accountByCode(c, "6101")
	parent := accountByCode(c, "61")
	cust := c.must("POST", "/parties", map[string]any{"name": "C", "is_customer": true})
	c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true,
		"items": []any{map[string]any{"description": "Goods", "quantity": "1", "unit_price": "10"}}})
	_ = sales
	// Code must extend the parent's code; type is inherited.
	code, out := c.do("POST", "/accounts", map[string]any{"parent_id": parent, "code": "7199", "name": "Bad"})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["code"] != "code_prefix" {
		t.Fatalf("code prefix: %d %v", code, out)
	}
	next := c.must("GET", "/accounts/next-code?parent_id="+fmt.Sprint(parent), nil)
	created := c.must("POST", "/accounts", map[string]any{"parent_id": parent, "code": next["code"], "name": "Consulting", "type": "asset"})
	if created["type"] != "revenue" || created["level"].(float64) != 3 {
		t.Fatalf("child must inherit type/level: %v", created)
	}
	// An account with postings cannot become a parent; a mapped account cannot be disabled.
	recv := accountByCode(c, "1201")
	code, out = c.do("POST", "/accounts", map[string]any{"parent_id": recv, "code": "120101", "name": "Sub"})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["parent_id"] != "parent_has_postings" {
		t.Fatalf("parent with postings: %d %v", code, out)
	}
	code, out = c.do("PUT", "/accounts/"+fmt.Sprint(recv), map[string]any{"code": "1201", "name": "Trade receivables", "is_active": false})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["is_active"] != "mapped_account" {
		t.Fatalf("disable mapped: %d %v", code, out)
	}
	c.expect(http.StatusUnprocessableEntity, "in_use", "DELETE", "/accounts/"+fmt.Sprint(recv), nil)
}

func TestLetterheadSettingsAndPDF(t *testing.T) {
	srv := newApp(t)
	admin := setupCompany(t, srv)

	// Installed with the official default letterhead.
	got := admin.must("GET", "/settings/letterhead", nil)
	st := sub(got, "settings")
	if st["enabled"] != true || !strings.Contains(str(sub(st, "footer"), "text"), "۸۲۱") {
		t.Fatalf("default letterhead not installed: %v", st)
	}

	// Footer edits persist in the database.
	footer := sub(st, "footer")
	footer["text"] = "{company_name} | تلفن: ۰۲۱۱۲۳۴۵۶۷۸"
	footer["align"] = "right"
	admin.must("PUT", "/settings/letterhead", st)
	again := sub(sub(admin.must("GET", "/settings/letterhead", nil), "settings"), "footer")
	if again["text"] != "{company_name} | تلفن: ۰۲۱۱۲۳۴۵۶۷۸" || again["align"] != "right" {
		t.Fatalf("footer not persisted: %v", again)
	}

	// Invalid values are rejected field by field.
	footer["color"] = "red"
	code, out := admin.do("PUT", "/settings/letterhead", st)
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["footer.color"] == nil {
		t.Fatalf("invalid colour must be rejected: %d %v", code, out)
	}

	// Only administrators may change the letterhead.
	var viewerRole int64
	for _, r := range items(admin.must("GET", "/roles", nil)) {
		if r["code"] == "accountant" {
			viewerRole = id(r)
		}
	}
	admin.must("POST", "/users", map[string]any{"email": "acc@example.com", "full_name": "Acc", "password": "Account123", "role_id": viewerRole})
	acc := newClient(t, srv)
	acc.must("POST", "/auth/login", map[string]any{"email": "acc@example.com", "password": "Account123"})
	acc.expect(http.StatusForbidden, "forbidden", "PUT", "/settings/letterhead", st)

	// Any signed-in user can render documents through the letterhead engine.
	if pdf.FindChrome() == "" {
		t.Skip("chromium not installed; PDF rendering not tested here")
	}
	req, _ := http.NewRequest("POST", acc.base+"/pdf", strings.NewReader(`{"title":"فاکتور فروش","number":"12","tables":[{"columns":[{"label":"کالا","align":"start"}],"rows":[{"cells":["لپ‌تاپ"]}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", acc.csrf())
	res, err := acc.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(body, []byte("%PDF")) {
		t.Fatalf("expected a PDF, got %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
}

// An existing receipt (registered without allocations) can settle invoices later,
// and allocations can be released again without cancelling the receipt.
func TestAllocateExistingReceiptToInvoices(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "C", "is_customer": true})
	other := c.must("POST", "/parties", map[string]any{"name": "D", "is_customer": true})
	cash := c.must("POST", "/treasury/accounts", map[string]any{"kind": "cash", "name": "Cash"})
	inv := func(party map[string]any, amount string) map[string]any {
		return c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(party), "post": true,
			"items": []any{map[string]any{"description": "Consulting", "quantity": "1", "unit_price": amount}}})
	}
	a, b, foreign := inv(cust, "300"), inv(cust, "500"), inv(other, "100")
	rc := c.must("POST", "/treasury/transactions", map[string]any{"kind": "receipt", "date": today, "treasury_account_id": id(cash),
		"party_id": id(cust), "amount": "600"})
	balance := str(c.must("GET", "/parties/"+str(cust, "id"), nil), "balance")
	path := "/treasury/transactions/" + str(rc, "id") + "/allocations"

	// explicit allocation to one invoice; another party's invoice and over-allocation are rejected
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", path, map[string]any{"allocations": []any{map[string]any{"invoice_id": id(foreign), "amount": "50"}}})
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", path, map[string]any{"allocations": []any{map[string]any{"invoice_id": id(b), "amount": "501"}}})
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", path, map[string]any{})
	c.must("POST", path, map[string]any{"allocations": []any{map[string]any{"invoice_id": id(b), "amount": "500"}}})
	if got := str(c.must("GET", "/sales/invoices/"+str(b, "id"), nil), "paid_amount"); got != "500" {
		t.Fatalf("explicit allocation: %s", got)
	}
	// the rest (100) goes oldest first; nothing more can be allocated afterwards
	c.must("POST", path, map[string]any{"auto_allocate": true})
	if got := str(c.must("GET", "/sales/invoices/"+str(a, "id"), nil), "paid_amount"); got != "100" {
		t.Fatalf("auto allocation of the remainder: %s", got)
	}
	c.expect(http.StatusUnprocessableEntity, "fully_allocated", "POST", path, map[string]any{"auto_allocate": true})
	if got := str(c.must("GET", "/parties/"+str(cust, "id"), nil), "balance"); got != balance {
		t.Fatalf("allocating must not change the ledger: %s → %s", balance, got)
	}
	// releasing reopens the invoices so the paid one can be cancelled, receipt stays posted
	c.must("DELETE", path, nil)
	if got := str(c.must("GET", "/sales/invoices/"+str(b, "id"), nil), "paid_amount"); got != "0" {
		t.Fatalf("release: %s", got)
	}
	c.must("POST", "/sales/invoices/"+str(b, "id")+"/cancel", map[string]any{})
	if st := str(c.must("GET", "/treasury/transactions/"+str(rc, "id"), nil), "status"); st != "posted" {
		t.Fatalf("receipt must stay posted: %s", st)
	}
	// transfers and cancelled transactions cannot be allocated
	c.must("POST", "/treasury/transactions/"+str(rc, "id")+"/cancel", map[string]any{})
	c.expect(http.StatusUnprocessableEntity, "already_cancelled", "POST", path, map[string]any{"auto_allocate": true})
}
