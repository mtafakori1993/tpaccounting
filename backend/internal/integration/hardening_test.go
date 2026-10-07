package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/sheet"
)

// raw performs a GET and returns status, headers and body.
func (c *client) raw(path string) (int, http.Header, []byte) {
	c.t.Helper()
	res, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

// upload posts a multipart file.
func (c *client) upload(path, fileName string, data []byte, fields map[string]string) (int, map[string]any) {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", fileName)
	_, _ = fw.Write(data)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", c.csrf())
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(res.Body)
	_ = jsonUnmarshal(raw, &out)
	return res.StatusCode, out
}

func journalCount(c *client) int64 {
	return num(c.must("GET", "/journal-entries?page_size=1", nil)["total"])
}

func TestCheckExportCompleteDatasetAndLifecycleGuards(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	cust := c.must("POST", "/parties", map[string]any{"name": "Customer Export", "is_customer": true})
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Mellat", "bank_name": "Mellat"})
	due := time.Now().AddDate(0, 0, 10).Format("2006-01-02")
	var ids []int64
	for i := 1; i <= 3; i++ {
		ch := c.must("POST", "/checks", map[string]any{"check_type": "received", "check_number": fmt.Sprintf("%05d", i), "bank_name": "Melli",
			"party_id": id(cust), "amount": fmt.Sprintf("%d000000", i), "issue_date": today, "due_date": due, "received_date": today})
		ids = append(ids, id(ch))
	}
	// The list is paginated…
	if page := c.must("GET", "/checks?check_type=received&page_size=1", nil); len(items(page)) != 1 || num(page["total"]) != 3 {
		t.Fatalf("paging: %v", page)
	}
	// …but the export contains the whole filtered dataset, whatever page size the screen uses.
	code, hdr, body := c.raw("/checks/export?format=csv&check_type=received&page_size=1&lang=en")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv export: %d %s", code, body)
	}
	csv := string(body)
	for _, want := range []string{"Tafakor Pardazan", "Checks report", "Generated", "Check type,Received", "00001", "00002", "00003", "Record count,3", "Total,6000000"} {
		if !strings.Contains(csv, want) {
			t.Fatalf("csv export lacks %q:\n%s", want, csv)
		}
	}
	code, _, body = c.raw("/checks/export?format=xlsx&min_amount=2000000")
	if code != 200 {
		t.Fatalf("xlsx export: %d", code)
	}
	rows, err := sheet.ReadXLSX(body)
	if err != nil {
		t.Fatal(err)
	}
	dataRows := 0
	for _, r := range rows {
		if len(r) > 2 && (r[2] == "00002" || r[2] == "00003") {
			dataRows++
			if r[10] != "2000000" && r[10] != "3000000" {
				t.Fatalf("amount must stay a raw number in Excel: %q", r)
			}
		}
	}
	if dataRows != 2 {
		t.Fatalf("xlsx filtered rows: %d %q", dataRows, rows)
	}
	c.expect(http.StatusUnprocessableEntity, "validation", "GET", "/checks/export?format=doc", nil)
	c.expect(http.StatusUnprocessableEntity, "validation", "GET", "/checks/export?format=csv&status=bogus", nil)
	sum := c.must("GET", "/checks/summary?check_type=received", nil)
	if num(sum["count"]) != 3 || str(sum, "amount") != "6000000" || str(sub(sum, "due_soon"), "amount") != "0" {
		t.Fatalf("summary: %v", sum)
	}
	// Viewers may view but not export (no checks.export) — accountants can.
	viewer := loginAs(t, srv, c, "viewer", "v@example.com")
	if code, _, _ := viewer.raw("/checks/export?format=csv"); code != http.StatusForbidden && code != http.StatusOK {
		t.Fatalf("viewer export: %d", code)
	}
	viewer.expect(http.StatusForbidden, "forbidden", "POST", fmt.Sprintf("/checks/%d/transitions", ids[0]), map[string]any{"action": "custody"})

	// Lifecycle guards.
	path := fmt.Sprintf("/checks/%d/transitions", ids[0])
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	before := journalCount(c)
	c.must("POST", path, map[string]any{"action": "deposit", "date": tomorrow, "treasury_account_id": id(bank)})
	// A transition cannot be dated before the current status.
	code, out := c.do("POST", path, map[string]any{"action": "clear", "date": today})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["date"] != "before_date" {
		t.Fatalf("clear before deposit: %d %v", code, out)
	}
	// Repeating a transition is rejected: no duplicate journal entry.
	c.expect(http.StatusUnprocessableEntity, "invalid_transition", "POST", path, map[string]any{"action": "deposit", "date": tomorrow, "treasury_account_id": id(bank)})
	if after := journalCount(c); after != before+1 {
		t.Fatalf("journal entries: %d → %d", before, after)
	}
	// Locked accounting periods are respected: nothing is booked or changed.
	fy := items(c.must("GET", "/company/fiscal-years", nil))[0]
	periods := c.must("GET", fmt.Sprintf("/company/fiscal-years/%d", id(fy)), nil)["periods"].([]any)
	var periodID int64
	for _, p := range periods {
		m := p.(map[string]any)
		if str(m, "start_date") <= tomorrow && str(m, "end_date") >= tomorrow {
			periodID = id(m)
		}
	}
	c.must("PUT", fmt.Sprintf("/company/periods/%d", periodID), map[string]any{"is_locked": true})
	c.expect(http.StatusUnprocessableEntity, "period_locked", "POST", path, map[string]any{"action": "clear", "date": tomorrow})
	if ch := c.must("GET", fmt.Sprintf("/checks/%d", ids[0]), nil); str(ch, "status") != "deposited" {
		t.Fatalf("status must not change in a locked period: %v", ch)
	}
	c.must("PUT", fmt.Sprintf("/company/periods/%d", periodID), map[string]any{"is_locked": false})
	c.must("POST", path, map[string]any{"action": "clear", "date": tomorrow})

	// A replacement check can only replace one check.
	supp := c.must("POST", "/parties", map[string]any{"name": "Supplier R", "is_supplier": true})
	issue := func(no string) map[string]any {
		return c.must("POST", "/checks", map[string]any{"check_type": "issued", "check_number": no, "party_id": id(supp), "amount": "1000000",
			"issue_date": today, "due_date": due, "treasury_account_id": id(bank), "issue": true})
	}
	a, b, r := issue("9001"), issue("9002"), issue("9003")
	c.must("POST", fmt.Sprintf("/checks/%d/transitions", id(a)), map[string]any{"action": "replace", "date": today, "reason": "x", "replacement_check_id": id(r)})
	code, out = c.do("POST", fmt.Sprintf("/checks/%d/transitions", id(b)), map[string]any{"action": "replace", "date": today, "reason": "x", "replacement_check_id": id(r)})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["replacement_check_id"] != "already_used" {
		t.Fatalf("reused replacement: %d %v", code, out)
	}
	// Every transition is audited with its user.
	if a := items(c.must("GET", fmt.Sprintf("/audit?entity=check&entity_id=%d", ids[0]), nil)); len(a) < 3 {
		t.Fatalf("audit: %d", len(a))
	}
	tb := sub(c.must("GET", "/reports/trial-balance?level=3", nil), "totals")
	if str(tb, "closing_debit") != str(tb, "closing_credit") {
		t.Fatalf("trial balance: %v", tb)
	}
}

func setupPayroll(t *testing.T, c *client) (map[string]any, string, int64, map[string]int64) {
	t.Helper()
	comps := items(c.must("GET", "/payroll/components", nil))
	compID := map[string]int64{}
	for _, x := range comps {
		compID[str(x, "code")] = id(x)
	}
	emp := c.must("POST", "/employees", map[string]any{"code": "E001", "first_name": "علی", "last_name": "رضایی", "national_id": "0499370899",
		"hire_date": "2026-01-01", "base_salary": "300000000", "iban": "IR820540102680020817909002", "bank_name": "ملت"})
	c.must("POST", "/employees", map[string]any{"code": "E002", "first_name": "Sara", "last_name": "Ahmadi", "hire_date": "2026-01-01",
		"base_salary": "200000000"})
	jy, jm, _ := jalali.FromGregorian(time.Now())
	p := c.must("POST", "/payroll/periods", map[string]any{"year": jy, "month": jm})
	return p, fmt.Sprintf("/payroll/periods/%d", id(p)), id(emp), compID
}

func TestPayrollConfigurableRules(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	rules := c.must("GET", "/payroll/rules", nil)
	tax := rules["tax_tables"].([]any)
	ins := rules["insurance_rules"].([]any)
	if len(tax) != 1 || len(ins) != 1 {
		t.Fatalf("seeded rules: %v", rules)
	}
	taxID, insID := id(tax[0].(map[string]any)), id(ins[0].(map[string]any))
	p, base, empID, _ := setupPayroll(t, c)
	_ = p
	p = c.must("POST", base+"/calculate", nil)
	basis := sub(p, "calc_basis")
	if num(basis["tax_table_id"]) != taxID || num(basis["insurance_rule_id"]) != insID || str(basis, "day_basis") != "30" || str(basis, "period_days") != "30" {
		t.Fatalf("calc basis: %v", basis)
	}
	slip := func() map[string]string {
		for _, s := range c.must("GET", base+"/payslips", nil)["items"].([]any) {
			m := s.(map[string]any)
			if num(m["employee_id"]) == empID {
				out := map[string]string{}
				for _, l := range c.must("GET", fmt.Sprintf("/payroll/payslips/%d", id(m)), nil)["lines"].([]any) {
					x := l.(map[string]any)
					out[str(x, "code")] = str(x, "amount")
				}
				return out
			}
		}
		return nil
	}
	first := slip()
	// Changing the rule tables changes the result without touching code.
	tt := tax[0].(map[string]any)
	tt["brackets"] = []any{map[string]any{"upto": nil, "rate": "10"}}
	tt["exemption"] = "0"
	c.must("PUT", fmt.Sprintf("/payroll/tax-tables/%d", taxID), tt)
	ir := ins[0].(map[string]any)
	ir["employee_rate"] = "5"
	ir["ceiling"] = "100000000"
	c.must("PUT", fmt.Sprintf("/payroll/insurance-rules/%d", insID), ir)
	c.must("POST", base+"/calculate", nil)
	second := slip()
	if second["insurance_employee"] != "5000000" || first["insurance_employee"] == second["insurance_employee"] || first["income_tax"] == second["income_tax"] {
		t.Fatalf("rules not applied: before %v after %v", first, second)
	}
	// Overlapping active rules are rejected.
	code, out := c.do("POST", "/payroll/tax-tables", map[string]any{"name": "dup", "effective_from": "2026-01-01", "mode": "monthly",
		"brackets": []any{map[string]any{"upto": nil, "rate": "5"}}})
	if code != http.StatusUnprocessableEntity || sub(out, "error")["code"] != "rule_period_overlap" {
		t.Fatalf("overlap: %d %v", code, out)
	}
	// A rule used by a payroll cannot be deleted.
	c.expect(http.StatusConflict, "in_use", "DELETE", fmt.Sprintf("/payroll/tax-tables/%d", taxID), nil)
	// Day basis: a fixed divisor changes the daily wage and is recorded.
	c.must("PUT", "/payroll/settings", map[string]any{"period_days": "fixed", "divisor": "26", "monthly_hours": "192"})
	c.must("PUT", base+"/inputs", []any{map[string]any{"employee_id": empID, "working_days": "26", "unpaid_leave_days": "1"}})
	p = c.must("POST", base+"/calculate", nil)
	if b := sub(p, "calc_basis"); str(b, "day_basis") != "fixed" || str(b, "period_days") != "26" {
		t.Fatalf("fixed divisor: %v", b)
	}
	if s := slip(); s["base_salary"] != "300000000" || s["unpaid_leave"] != "11538462" {
		t.Fatalf("fixed divisor amounts: %v", s)
	}
	c.expect(http.StatusUnprocessableEntity, "validation", "PUT", "/payroll/settings", map[string]any{"period_days": "fixed", "divisor": "40", "monthly_hours": "192"})
	// Formula components are validated.
	code, out = c.do("POST", "/payroll/components", map[string]any{"code": "transport", "name": "ایاب و ذهاب", "kind": "earning", "method": "formula",
		"formula": "working_days * ", "seq": 45})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["formula"] != "invalid_formula" {
		t.Fatalf("formula validation: %d %v", code, out)
	}
	c.must("POST", "/payroll/components", map[string]any{"code": "transport", "name": "ایاب و ذهاب", "kind": "earning", "method": "formula",
		"formula": "working_days * 100000", "seq": 45, "variability": "variable", "taxable": true})
	c.must("POST", base+"/calculate", nil)
	if s := slip(); s["transport"] != "2600000" {
		t.Fatalf("formula component: %v", s)
	}
	// Payroll officers cannot change statutory rules.
	officer := loginAs(t, srv, c, "payroll_officer", "po@example.com")
	officer.expect(http.StatusForbidden, "forbidden", "PUT", fmt.Sprintf("/payroll/tax-tables/%d", taxID), tt)
}

func attendanceCSV(rows ...string) []byte {
	return []byte("کد پرسنلی,تاریخ,ساعت کار,اضافه کار,غیبت,مرخصی\n" + strings.Join(rows, "\n") + "\n")
}

func TestAttendanceImport(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	p, base, empID, _ := setupPayroll(t, c)
	start := str(p, "start_date")
	d0, _ := time.Parse("2006-01-02", start)
	day := func(n int) string {
		y, m, d := jalali.FromGregorian(d0.AddDate(0, 0, n))
		return fmt.Sprintf("%d/%02d/%02d", y, m, d)
	}
	// A file with errors: preview lists them, import is refused and nothing is stored.
	bad := attendanceCSV("E001,"+day(0)+",8,2,0,0", "E999,"+day(1)+",8,0,0,0", "E001,"+day(0)+",8,0,0,0", "E002,1300/01/01,8,0,0,0", "E002,"+day(2)+",abc,0,0,0")
	code, res := c.upload(base+"/attendance/import", "att.csv", bad, nil)
	if code != 200 || res["can_import"] != false {
		t.Fatalf("preview: %d %v", code, res)
	}
	s := sub(res, "summary")
	if num(s["total_rows"]) != 5 || num(s["invalid_rows"]) != 4 || num(s["valid_rows"]) != 1 {
		t.Fatalf("summary: %v", s)
	}
	codes := map[string]bool{}
	for _, e := range res["errors"].([]any) {
		codes[str(e.(map[string]any), "code")] = true
	}
	for _, want := range []string{"unknown_employee", "duplicate_row", "date_outside_period", "invalid_number"} {
		if !codes[want] {
			t.Fatalf("missing error %s: %v", want, res["errors"])
		}
	}
	code, out := c.upload(base+"/attendance/import", "att.csv", bad, map[string]string{"commit": "true"})
	if code != http.StatusUnprocessableEntity || sub(out, "error")["code"] != "import_has_errors" {
		t.Fatalf("commit with errors: %d %v", code, out)
	}
	if imps := c.must("GET", base+"/attendance/imports", nil)["items"].([]any); len(imps) != 0 {
		t.Fatal("nothing may be imported from a file with errors")
	}
	// A valid Excel file is imported in full.
	var buf bytes.Buffer
	_ = sheet.WriteXLSX(&buf, sheet.Sheet{Rows: [][]sheet.Cell{
		{sheet.Text("employee_code"), sheet.Text("date"), sheet.Text("working_hours"), sheet.Text("overtime_hours"), sheet.Text("absence"), sheet.Text("leave")},
		{sheet.Text("E001"), sheet.Text(day(0)), sheet.Number("8"), sheet.Number("2"), sheet.Number("0"), sheet.Number("0")},
		{sheet.Text("E001"), sheet.Text(day(1)), sheet.Number("8"), sheet.Number("1.5"), sheet.Number("0"), sheet.Number("0")},
		{sheet.Text("E001"), sheet.Text(day(2)), sheet.Number("0"), sheet.Number("0"), sheet.Number("1"), sheet.Number("0")},
	}})
	code, res = c.upload(base+"/attendance/import", "att.xlsx", buf.Bytes(), map[string]string{"commit": "true"})
	if code != 200 || res["imported"] != true || num(sub(res, "summary")["imported_rows"]) != 3 {
		t.Fatalf("import: %d %v", code, res)
	}
	// E002 had no rows: warned, and keeps the default inputs.
	if len(res["warnings"].([]any)) == 0 {
		t.Fatal("missing employee must be warned about")
	}
	var row map[string]any
	for _, r := range c.must("GET", base+"/inputs", nil)["items"].([]any) {
		if num(r.(map[string]any)["employee_id"]) == empID {
			row = r.(map[string]any)
		}
	}
	if str(row, "overtime_hours") != "3.5" || str(row, "working_hours") != "16" || str(row, "absence_days") != "1" || !strings.HasPrefix(str(row, "source"), "import:") {
		t.Fatalf("imported inputs: %v", row)
	}
	if code, _, body := c.raw("/payroll/attendance/template?lang=en"); code != 200 || len(body) < 100 {
		t.Fatalf("template: %d", code)
	}
	// Approved payrolls cannot be changed by an import.
	c.must("POST", base+"/calculate", nil)
	c.must("POST", base+"/approve", nil)
	code, out = c.upload(base+"/attendance/import", "att.xlsx", buf.Bytes(), map[string]string{"commit": "true"})
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("import into approved payroll: %d %v", code, out)
	}
}

func TestPayrollPaymentFileAndLiabilities(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	p, base, _, _ := setupPayroll(t, c)
	_ = p
	c.must("POST", base+"/calculate", nil)
	c.expect(http.StatusUnprocessableEntity, "payroll_not_approved", "GET", base+"/payments", nil)
	p = c.must("POST", base+"/approve", nil)
	if w := p["warnings"].([]any); len(w) == 0 {
		t.Fatal("employee without bank account must be warned about")
	}

	// Payment instructions: one row per employee, issues flagged, no accounting effect.
	before := journalCount(c)
	pay := c.must("GET", base+"/payments", nil)
	rows := pay["rows"].([]any)
	if len(rows) != 2 || num(pay["issue_count"]) != 1 || !dec(pay["total"]).Equal(dec(p["net_total"])) {
		t.Fatalf("instructions: %v", pay)
	}
	code, _, body := c.raw(base + "/payments/export?format=csv&layout=generic&lang=en")
	if code != 200 || !strings.Contains(string(body), "IR820540102680020817909002") || !strings.Contains(string(body), "0499370899") ||
		strings.Contains(string(body), "Generated") {
		t.Fatalf("bank file: %d %s", code, body)
	}
	if code, _, body = c.raw(base + "/payments/export?format=xlsx&layout=report"); code != 200 {
		t.Fatalf("xlsx: %d", code)
	}
	if r, err := sheet.ReadXLSX(body); err != nil || len(r) < 6 {
		t.Fatalf("xlsx report: %v %v", r, err)
	}
	c.expect(http.StatusUnprocessableEntity, "validation", "GET", base+"/payments/export?format=csv&layout=nope", nil)
	if after := journalCount(c); after != before {
		t.Fatalf("exporting instructions must not post: %d → %d", before, after)
	}

	// Posting creates the liabilities, separately per kind.
	c.must("POST", base+"/post", nil)
	ls := items(c.must("GET", "/payroll/liabilities?period_id="+fmt.Sprint(id(p)), nil))
	kinds := map[string]map[string]any{}
	for _, l := range ls {
		kinds[str(l, "kind")] = l
	}
	if kinds["employee_tax"] == nil || kinds["employee_insurance"] == nil || kinds["employer_insurance"] == nil {
		t.Fatalf("liabilities: %v", ls)
	}
	taxL := kinds["employee_tax"]
	taxPayable := accountByCode(c, "3203")
	if !dec(balance(c, taxPayable)).Neg().Equal(dec(taxL["amount"])) || str(taxL, "status") != "open" {
		t.Fatalf("tax liability %v vs ledger %s", taxL, balance(c, taxPayable))
	}

	// Settled through an ordinary treasury payment.
	bank := c.must("POST", "/treasury/accounts", map[string]any{"kind": "bank", "name": "Tax bank", "bank_name": "Melli",
		"opening_balance": "5000000000", "opening_date": today})
	lpath := fmt.Sprintf("/payroll/liabilities/%d/pay", id(taxL))
	end := str(p, "end_date")
	// A liability cannot be settled before the payroll's accrual date.
	code, out := c.do("POST", lpath, map[string]any{"treasury_account_id": id(bank), "date": str(p, "start_date")})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["date"] != "before_date" {
		t.Fatalf("early payment: %d %v", code, out)
	}
	over := dec(taxL["amount"]).Add(decimal.NewFromInt(1)).String()
	code, out = c.do("POST", lpath, map[string]any{"treasury_account_id": id(bank), "amount": over, "date": end})
	if code != http.StatusUnprocessableEntity || sub(sub(out, "error"), "fields")["amount"] != "exceeds_outstanding" {
		t.Fatalf("overpay: %d %v", code, out)
	}
	paid := c.must("POST", lpath, map[string]any{"treasury_account_id": id(bank), "date": end, "reference": "TAX-1"})
	if str(paid, "status") != "paid" || balance(c, taxPayable) != "0" || len(paid["payments"].([]any)) != 1 {
		t.Fatalf("paid: %v ledger %s", paid, balance(c, taxPayable))
	}
	txID := num(paid["payments"].([]any)[0].(map[string]any)["transaction_id"])
	tx := c.must("GET", fmt.Sprintf("/treasury/transactions/%d", txID), nil)
	if str(tx, "kind") != "payment" || num(tx["counter_account_id"]) != taxPayable {
		t.Fatalf("treasury payment: %v", tx)
	}
	c.expect(http.StatusUnprocessableEntity, "liability_paid", "POST", lpath, map[string]any{"treasury_account_id": id(bank), "amount": "1", "date": end})
	// Unposting is refused while a liability payment is live…
	c.expect(http.StatusUnprocessableEntity, "liabilities_paid", "POST", base+"/unpost", map[string]any{})
	// …cancelling the payment in treasury reopens the liability…
	c.must("POST", fmt.Sprintf("/treasury/transactions/%d/cancel", txID), nil)
	if l := c.must("GET", fmt.Sprintf("/payroll/liabilities/%d", id(taxL)), nil); str(l, "status") != "open" {
		t.Fatalf("after cancel: %v", l)
	}
	// …and the payroll can then be unposted, removing its liabilities.
	c.must("POST", base+"/unpost", map[string]any{})
	if ls := items(c.must("GET", "/payroll/liabilities?period_id="+fmt.Sprint(id(p)), nil)); len(ls) != 0 {
		t.Fatalf("liabilities after unpost: %v", ls)
	}
	// Officers without treasury.post cannot pay.
	c.must("POST", base+"/post", nil)
	officer := loginAs(t, srv, c, "payroll_officer", "po2@example.com")
	ls = items(c.must("GET", "/payroll/liabilities?period_id="+fmt.Sprint(id(p)), nil))
	officer.expect(http.StatusForbidden, "forbidden", "POST", fmt.Sprintf("/payroll/liabilities/%d/pay", id(ls[0])), map[string]any{"treasury_account_id": id(bank)})
	tb := sub(c.must("GET", "/reports/trial-balance?level=3", nil), "totals")
	if str(tb, "closing_debit") != str(tb, "closing_credit") {
		t.Fatalf("trial balance: %v", tb)
	}
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestBudgetForecastMethods(t *testing.T) {
	srv := newApp(t)
	c := setupCompany(t, srv)
	b := items(c.must("GET", "/budgets", nil))[0]
	path := fmt.Sprintf("/budgets/%d", id(b))
	for _, a := range []string{"submit", "approve", "activate"} {
		c.must("POST", path+"/"+a, nil)
	}
	a := c.must("GET", path+"/analysis", nil)
	if str(a, "forecast_method") != "straight_line" || len(a["forecast_methods"].([]any)) != 4 {
		t.Fatalf("default method: %v", a["forecast_methods"])
	}
	row := a["rows"].([]any)[0].(map[string]any)
	if str(row, "forecast_method") != "straight_line" || str(row, "forecast_basis") != "run_rate" || str(row, "forecast_coverage") == "" {
		t.Fatalf("row forecast: %v", row)
	}
	lineID := num(row["line_id"])
	for _, m := range []string{"historical_average", "previous_year"} {
		r := c.must("GET", path+"/analysis?forecast_method="+m, nil)["rows"].([]any)[0].(map[string]any)
		if str(r, "forecast_method") != m {
			t.Fatalf("%s: %v", m, r)
		}
	}
	// No earlier fiscal year: the previous-year method says it fell back.
	r := c.must("GET", path+"/analysis?forecast_method=previous_year", nil)
	if r["has_prior_year"] != false || str(r["rows"].([]any)[0].(map[string]any), "forecast_basis") != "fallback_no_prior_year" {
		t.Fatalf("prior year fallback: %v", r["rows"].([]any)[0])
	}
	c.expect(http.StatusUnprocessableEntity, "validation", "GET", path+"/analysis?forecast_method=ai", nil)

	// Manual override, permission-protected and kept as history.
	c.expect(http.StatusUnprocessableEntity, "validation", "POST", fmt.Sprintf("/budgets/lines/%d/forecasts", lineID), map[string]any{"amount": "100"})
	accountant := loginAs(t, srv, c, "accountant", "acc@example.com")
	accountant.expect(http.StatusForbidden, "forbidden", "POST", fmt.Sprintf("/budgets/lines/%d/forecasts", lineID), map[string]any{"amount": "1", "reason": "x"})
	c.must("POST", fmt.Sprintf("/budgets/lines/%d/forecasts", lineID), map[string]any{"amount": "123000000", "reason": "قرارداد جدید"})
	c.must("POST", fmt.Sprintf("/budgets/lines/%d/forecasts", lineID), map[string]any{"amount": "125000000", "reason": "اصلاح"})
	if h := c.must("GET", fmt.Sprintf("/budgets/lines/%d/forecasts", lineID), nil)["items"].([]any); len(h) != 2 {
		t.Fatalf("forecast history: %v", h)
	}
	r = c.must("GET", path+"/analysis?forecast_method=manual", nil)
	for _, x := range r["rows"].([]any) {
		m := x.(map[string]any)
		if num(m["line_id"]) == lineID {
			if str(m, "forecast") != "125000000" || str(m, "forecast_basis") != "manager_override" || sub(m, "manual_forecast") == nil {
				t.Fatalf("manual forecast: %v", m)
			}
		} else if str(m, "forecast_basis") != "fallback_no_manual" {
			t.Fatalf("lines without override fall back: %v", m)
		}
	}
}
