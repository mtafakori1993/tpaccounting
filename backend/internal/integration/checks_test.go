package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestReceivedCheckLifecycleAndAccounting(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "Customer A", "is_customer": true})
	other := c.must("POST", "/parties", map[string]any{"name": "Supplier X", "is_supplier": true})
	svc := c.must("POST", "/products", map[string]any{"name": "Consulting", "kind": "service"})
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Mellat", "bank_name": "Mellat"})
	inv := c.must("POST", "/sales/invoices", map[string]any{"date": today, "party_id": id(cust), "post": true,
		"items": []any{map[string]any{"product_id": id(svc), "quantity": "1", "unit_price": "50000000"}}})
	recvChecks, inCollection, receivable := accountByCode(c, "1202"), accountByCode(c, "1204"), accountByCode(c, "1201")

	var banks []map[string]any
	banks = items(c.must("GET", "/checks/banks", nil))
	if len(banks) < 20 {
		t.Fatalf("bank catalogue: %d", len(banks))
	}
	melli := id(banks[0])

	due := time.Now().AddDate(0, 0, 20).Format("2006-01-02")
	body := map[string]any{"check_type": "received", "check_number": "۱۲۳۴۵۶", "sayadi_id": "1234567890123456", "bank_id": melli,
		"account_number": "1001", "party_id": id(cust), "amount": "30000000", "issue_date": today, "due_date": due, "received_date": today,
		"invoice_id": id(inv)}
	ch := c.must("POST", "/checks", body)
	if str(ch, "status") != "received" || str(ch, "check_number") != "123456" || ch["journal_entry_id"] == nil {
		t.Fatalf("received check: %v", ch)
	}
	je := entry(c, ch["journal_entry_id"])
	if str(je, "source_type") != "check" || str(je, "total_debit") != "30000000" {
		t.Fatalf("check entry: %v", je)
	}
	if balance(c, recvChecks) != "30000000" || partyBalance(c, id(cust)) != "20000000" {
		t.Fatalf("after receipt: checks %s customer %s", balance(c, recvChecks), partyBalance(c, id(cust)))
	}
	if inv = c.must("GET", fmt.Sprintf("/sales/invoices/%d", id(inv)), nil); str(inv, "payment_status") != "partial" {
		t.Fatalf("invoice must be partly settled by the check: %v", inv)
	}

	// Duplicate checks are rejected (same Sayadi ID, or same bank/account/number).
	c.expect(http.StatusConflict, "duplicate_check", "POST", "/checks", body)
	body2 := map[string]any{}
	for k, v := range body {
		body2[k] = v
	}
	body2["sayadi_id"] = ""
	c.expect(http.StatusConflict, "duplicate_check", "POST", "/checks", body2)

	// Received → in custody → deposited → cleared.
	path := fmt.Sprintf("/checks/%d/transitions", id(ch))
	c.must("POST", path, map[string]any{"action": "custody", "date": today})
	c.expect(http.StatusUnprocessableEntity, "invalid_transition", "POST", path, map[string]any{"action": "clear", "date": today})
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", path, map[string]any{"action": "deposit", "date": today})
	ch = c.must("POST", path, map[string]any{"action": "deposit", "date": today, "treasury_account_id": id(bank)})
	if str(ch, "status") != "deposited" || balance(c, recvChecks) != "0" || balance(c, inCollection) != "30000000" {
		t.Fatalf("deposit: %v / %s / %s", str(ch, "status"), balance(c, recvChecks), balance(c, inCollection))
	}
	ch = c.must("POST", path, map[string]any{"action": "clear", "date": today})
	if str(ch, "status") != "cleared" || balance(c, inCollection) != "0" {
		t.Fatalf("clear: %v", ch)
	}
	if b := str(c.must("GET", fmt.Sprintf("/treasury/accounts/%d", id(bank)), nil), "balance"); b != "30000000" {
		t.Fatalf("bank after clearing: %s", b)
	}
	if n := len(ch["transitions"].([]any)); n != 4 {
		t.Fatalf("every transition is recorded: %d", n)
	}
	c.expect(http.StatusUnprocessableEntity, "invalid_transition", "POST", path, map[string]any{"action": "return", "reason": "x", "date": today})

	// Audit trail of the status changes.
	audit := items(c.must("GET", fmt.Sprintf("/audit?entity=check&entity_id=%d", id(ch)), nil))
	if len(audit) < 4 {
		t.Fatalf("audit entries: %d", len(audit))
	}

	// A second check is returned (bounced): every entry is reversed and the invoice reopened.
	ch2 := c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": "777", "bank_name": "بانک سامان", "party_id": id(cust),
		"amount": "10000000", "issue_date": today, "due_date": due, "received_date": today, "auto_allocate": true})
	if partyBalance(c, id(cust)) != "10000000" {
		t.Fatalf("customer after 2nd check: %s", partyBalance(c, id(cust)))
	}
	p2 := fmt.Sprintf("/checks/%d/transitions", id(ch2))
	c.must("POST", p2, map[string]any{"action": "deposit", "date": today, "treasury_account_id": id(bank)})
	code, out := c.do("POST", p2, map[string]any{"action": "return", "date": today})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["reason"] != "required" {
		t.Fatalf("return needs a reason: %d %v", code, out)
	}
	ch2 = c.must("POST", p2, map[string]any{"action": "return", "date": today, "reason": "insufficient funds"})
	trs := ch2["transitions"].([]any)
	last := trs[len(trs)-1].(map[string]any)
	if str(ch2, "status") != "returned" || len(last["reversal_entry_ids"].([]any)) != 2 {
		t.Fatalf("returned check: %v", ch2)
	}
	if partyBalance(c, id(cust)) != "20000000" || balance(c, inCollection) != "0" || balance(c, recvChecks) != "0" || balance(c, receivable) != "20000000" {
		t.Fatalf("after return: customer %s collection %s", partyBalance(c, id(cust)), balance(c, inCollection))
	}
	if str(ch2, "allocated") != "0" {
		t.Fatalf("allocations must be released: %v", ch2)
	}

	// Endorsement to a supplier settles our payable to them.
	ch3 := c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": "888", "bank_name": "Tejarat", "party_id": id(cust),
		"amount": "5000000", "issue_date": today, "due_date": due, "received_date": today})
	c.must("POST", fmt.Sprintf("/checks/%d/transitions", id(ch3)), map[string]any{"action": "transfer", "date": today, "party_id": id(other)})
	if partyBalance(c, id(other)) != "5000000" {
		t.Fatalf("endorsed check must debit the supplier: %s", partyBalance(c, id(other)))
	}

	// Cancellation of a wrongly registered check reverses it.
	ch4 := c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": "999", "bank_name": "Tejarat", "party_id": id(cust),
		"amount": "1000000", "issue_date": today, "due_date": due, "received_date": today})
	ch4 = c.must("POST", fmt.Sprintf("/checks/%d/transitions", id(ch4)), map[string]any{"action": "cancel", "date": today})
	if str(ch4, "status") != "cancelled" || ch4["cancelled_at"] == nil || partyBalance(c, id(cust)) != "15000000" {
		t.Fatalf("cancel: %v customer %s", ch4, partyBalance(c, id(cust)))
	}
	// Booked checks cannot be deleted; cancelled checks stay on record.
	c.expect(http.StatusUnprocessableEntity, "not_deletable", "DELETE", fmt.Sprintf("/checks/%d", id(ch4)), nil)

	// Trial balance stays balanced through all of it.
	tb := sub(c.must("GET", "/reports/trial-balance?level=3", nil), "totals")
	if str(tb, "closing_debit") != str(tb, "closing_credit") {
		t.Fatalf("trial balance: %v", tb)
	}
}

func TestIssuedCheckAndReports(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	supp := c.must("POST", "/parties", map[string]any{"name": "Supplier B", "is_supplier": true})
	cust := c.must("POST", "/parties", map[string]any{"name": "Customer C", "is_customer": true})
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Saman", "bank_name": "Saman", "account_number": "55-1",
		"opening_balance": "100000000", "opening_date": today})
	cash := c.must("POST", "/treasury/accounts", map[string]any{"kind": "cash", "name": "Cash"})
	payable := accountByCode(c, "3102")

	// Issued checks must be drawn on a bank account.
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", "/checks", map[string]any{"check_type": "issued", "check_number": "1",
		"party_id": id(supp), "amount": "100", "issue_date": today, "due_date": today, "treasury_account_id": id(cash)})

	due := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	draft := c.must("POST", "/checks", map[string]any{"check_type": "issued", "check_number": "500100", "party_id": id(supp), "amount": "40000000",
		"issue_date": today, "due_date": due, "treasury_account_id": id(bank)})
	if str(draft, "status") != "draft" || draft["journal_entry_id"] != nil || str(draft, "bank_name") != "Saman" {
		t.Fatalf("draft issued check: %v", draft)
	}
	// Drafts are editable and deletable; nothing is booked.
	draft["amount"] = "45000000"
	draft = c.must("PUT", fmt.Sprintf("/checks/%d", id(draft)), draft)
	if str(draft, "amount") != "45000000" || balance(c, payable) != "0" {
		t.Fatalf("draft edit: %v", draft)
	}
	path := fmt.Sprintf("/checks/%d/transitions", id(draft))
	issued := c.must("POST", path, map[string]any{"action": "issue", "date": today})
	if str(issued, "status") != "issued" || balance(c, payable) != "-45000000" || partyBalance(c, id(supp)) != "45000000" {
		t.Fatalf("issue: %v payable %s supplier %s", issued, balance(c, payable), partyBalance(c, id(supp)))
	}
	// Same check number on the same account cannot be issued twice.
	c.expect(http.StatusConflict, "duplicate_check", "POST", "/checks", map[string]any{"check_type": "issued", "check_number": "500100",
		"party_id": id(supp), "amount": "1", "issue_date": today, "due_date": due, "treasury_account_id": id(bank)})
	c.must("POST", path, map[string]any{"action": "deliver", "date": today})
	cleared := c.must("POST", path, map[string]any{"action": "clear", "date": today})
	if str(cleared, "status") != "cleared" || balance(c, payable) != "0" {
		t.Fatalf("clear issued: %v", cleared)
	}
	if b := str(c.must("GET", fmt.Sprintf("/treasury/accounts/%d", id(bank)), nil), "balance"); b != "55000000" {
		t.Fatalf("bank after issued check cleared: %s", b)
	}

	// Issue immediately, then cancel through reversal.
	now := c.must("POST", "/checks", map[string]any{"check_type": "issued", "check_number": "500101", "party_id": id(supp), "amount": "7000000",
		"issue_date": today, "due_date": due, "treasury_account_id": id(bank), "issue": true})
	if str(now, "status") != "issued" {
		t.Fatalf("issue at once: %v", now)
	}
	// A replacement check takes its place.
	repl := c.must("POST", "/checks", map[string]any{"check_type": "issued", "check_number": "500102", "party_id": id(supp), "amount": "7000000",
		"issue_date": today, "due_date": due, "treasury_account_id": id(bank)})
	replaced := c.must("POST", fmt.Sprintf("/checks/%d/transitions", id(now)), map[string]any{"action": "replace", "date": today,
		"reason": "date corrected", "replacement_check_id": id(repl)})
	if str(replaced, "status") != "replaced" || num(replaced["replaced_by_id"]) != id(repl) || partyBalance(c, id(supp)) != "45000000" {
		t.Fatalf("replace: %v supplier %s", replaced, partyBalance(c, id(supp)))
	}

	// Received checks for reports: one due soon, one overdue.
	overdueDate := time.Now().AddDate(0, 0, -5).Format("2006-01-02")
	issueDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": "1", "bank_name": "Melli", "party_id": id(cust),
		"amount": "2000000", "issue_date": today, "due_date": due, "received_date": today})
	c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": "2", "bank_name": "Melli", "party_id": id(cust),
		"amount": "3000000", "issue_date": issueDate, "due_date": overdueDate, "received_date": issueDate})

	dash := c.must("GET", "/checks/dashboard", nil)
	if str(sub(dash, "received_live"), "amount") != "5000000" || str(sub(dash, "overdue_received"), "amount") != "3000000" ||
		str(sub(dash, "due_week_received"), "amount") != "2000000" || str(sub(dash, "in_custody"), "amount") != "5000000" {
		t.Fatalf("dashboard: %v", dash)
	}
	if n := num(sub(dash, "issued_live")["count"]); n != 0 {
		t.Fatalf("no issued check is outstanding: %v", sub(dash, "issued_live"))
	}
	if got := c.must("GET", "/checks/reports/overdue", nil); num(got["total"]) != 1 || str(got, "total_amount") != "3000000" {
		t.Fatalf("overdue report: %v", got)
	}
	if got := c.must("GET", "/checks/reports/due", nil); num(got["total"]) != 1 {
		t.Fatalf("due report: %v", got)
	}
	if got := c.must("GET", "/checks/reports/issued?status=cleared", nil); num(got["total"]) != 1 {
		t.Fatalf("issued report filter: %v", got)
	}
	if got := c.must("GET", "/checks?check_type=received&min_amount=2500000&sort=amount&order=desc", nil); num(got["total"]) != 1 {
		t.Fatalf("amount filter: %v", got)
	}
	if got := c.must("GET", "/checks?q=500102", nil); num(got["total"]) != 1 {
		t.Fatalf("search: %v", got)
	}
	byParty := c.must("GET", "/checks/reports/by-party", nil)
	if len(byParty["rows"].([]any)) != 2 {
		t.Fatalf("by party: %v", byParty)
	}
	flow := c.must("GET", "/checks/reports/cash-flow?group_by=day", nil)
	if str(flow, "overdue_net") != "3000000" || str(flow, "inflow") != "2000000" {
		t.Fatalf("cash flow: %v", flow)
	}

	// Viewers can read but not register checks.
	viewer := loginAs(t, srv, c, "viewer", "viewer@example.com")
	viewer.must("GET", "/checks", nil)
	viewer.expect(http.StatusForbidden, "forbidden", "POST", "/checks", map[string]any{"check_type": "received"})
	viewer.expect(http.StatusForbidden, "forbidden", "POST", path, map[string]any{"action": "return", "reason": "x"})
}
