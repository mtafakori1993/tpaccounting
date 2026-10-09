package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

func step(tool, body string) PlanStep {
	return PlanStep{ID: "s1", Tool: tool, Args: tools.OpArgs{Body: json.RawMessage(body)}}
}

func TestGuardRefusesInventedReferencesAndUploads(t *testing.T) {
	st := &State{Uploads: []Upload{{ID: "u-real"}}}
	p := &PlanState{UserTexts: []string{"پرداخت ۳ میلیون تومان به ایرانیکارت"}}
	issues, problems, _ := guardValues(st, p, []PlanStep{step("treasury.transactions.create",
		`{"kind":"payment","party_id":7,"treasury_account_id":2,"amount":"30000000","date":"2025-10-08"}`)}, "2025-10-08", nil)
	if len(issues) != 2 || problems != "" {
		t.Fatalf("unread party/account must be issues: %+v %q", issues, problems)
	}
	// read in this work → grounded; config → grounded
	p.Reads = []ReadRecord{{Tool: "parties.list", Raw: json.RawMessage(`{"items":[{"id":7,"name":"ایرانیکارت"}]}`)}}
	cfg := &tools.CompanyConfig{TreasuryAccounts: []tools.TreasuryAccount{{ID: 2, Name: "بانک ملت"}}}
	if issues, _, _ = guardValues(st, p, []PlanStep{step("treasury.transactions.create",
		`{"kind":"payment","party_id":7,"treasury_account_id":2,"amount":"30000000","date":"2025-10-08"}`)}, "2025-10-08", cfg); len(issues) != 0 {
		t.Fatalf("read/config ids flagged: %+v", issues)
	}
	// a party id seen only in ANOTHER resource's read is not grounded
	p.Reads = []ReadRecord{{Tool: "products.list", Raw: json.RawMessage(`{"items":[{"id":7,"name":"x"}]}`)}}
	if issues, _, _ = guardValues(st, p, []PlanStep{step("expenses.create", `{"party_id":7}`)}, "2025-10-08", nil); len(issues) != 1 {
		t.Fatalf("cross-resource id accepted: %+v", issues)
	}
	_, problems, _ = guardValues(st, p, []PlanStep{step("attachments.create", `{"upload_id":"u-other"}`)}, "2025-10-08", nil)
	if !strings.Contains(problems, "upload_id") {
		t.Fatalf("foreign upload accepted: %q", problems)
	}
}

func TestGuardAsksForCheckValuesTheUserDidNotGive(t *testing.T) {
	st := &State{}
	p := &PlanState{UserTexts: []string{"یک چک به مبلغ ۵ میلیون تومان از محمد تفکری ثبت کن"}, Reads: []ReadRecord{{Tool: "parties.list", Raw: json.RawMessage(`{"items":[{"id":100,"name":"محمد تفکری"}]}`)}}}
	body := `{"check_type":"received","check_number":"123456","party_id":100,"amount":"50000000","issue_date":"2025-10-08","due_date":"2025-12-01","received_date":"2025-10-08"}`
	_, _, ask := guardValues(st, p, []PlanStep{step("checks.create", body)}, "2025-10-08", nil)
	got := strings.Join(ask, ",")
	for _, f := range []string{"check_type", "check_number", "due_date"} {
		if !strings.Contains(got, f) {
			t.Errorf("%s must be asked, asked: %s", f, got)
		}
	}
	if strings.Contains(got, "issue_date") || strings.Contains(got, "received_date") {
		t.Errorf("today's issue/received date is not invented: %s", got)
	}
	// stated by the user: nothing to ask
	p.UserTexts = []string{"چک دریافتی شماره ۱۲۳۴۵۶ به مبلغ ۵ میلیون تومان از محمد تفکری با سررسید ۱۴۰۴/۰۹/۱۰ ثبت کن"}
	if _, problems, ask := guardValues(st, p, []PlanStep{step("checks.create", body)}, "2025-10-08", nil); len(ask) != 0 || problems != "" {
		t.Fatalf("stated values flagged: %v %q", ask, problems)
	}
	// the type the user said decides
	if _, problems, _ := guardValues(st, p, []PlanStep{step("checks.create", strings.Replace(body, `"received"`, `"issued"`, 1))}, "2025-10-08", nil); !strings.Contains(problems, "check_type") {
		t.Fatalf("wrong check type accepted: %q", problems)
	}
}

func TestGuardAsksForInventedNumbers(t *testing.T) {
	st := &State{}
	p := &PlanState{UserTexts: []string{"به اجزای حقوق کمک هزینه سفر اضافه کن"}}
	_, _, ask := guardValues(st, p, []PlanStep{step("payroll.components.create", `{"name":"کمک هزینه سفر","kind":"earning","method":"fixed","rate":"4000000","seq":90}`)}, "2025-10-08", nil)
	if strings.Join(ask, ",") != "rate" {
		t.Fatalf("an invented rate must be asked (seq is layout): %v", ask)
	}
	p.UserTexts = []string{"یک فاکتور فروش ۲ عدد سیستم"}
	_, _, ask = guardValues(st, p, []PlanStep{step("sales.kind.create", `{"items":[{"quantity":"2","tax_rate":"9"}]}`)}, "2025-10-08", nil)
	if strings.Join(ask, ",") != "tax_rate" {
		t.Fatalf("stated quantity ok, invented tax rate asked: %v", ask)
	}
}

func TestNamesFollowTheUsersSpellingAndDatesAreReal(t *testing.T) {
	p := &PlanState{UserTexts: []string{"این واریز به زهرا معتضد بابت مساعده حقوق است؛ ثبتش کن"}}
	_, problems, _ := guardValues(&State{}, p, []PlanStep{step("parties.create", `{"name":"زهرا معتمد","kind":"person"}`)}, "2025-10-08", nil)
	if !strings.Contains(problems, "معتضد") {
		t.Fatalf("misspelled name accepted: %q", problems)
	}
	if _, problems, _ = guardValues(&State{}, p, []PlanStep{step("parties.create", `{"name":"زهرا معتضد","kind":"person"}`)}, "2025-10-08", nil); problems != "" {
		t.Fatalf("correct name flagged: %q", problems)
	}
	if impossibleDate("سررسید ۱۴۰۵/۱۳/۴۰") == "" || impossibleDate("سررسید ۱۴۰۵/۱۲/۲۹") != "" || impossibleDate("2025-02-30") == "" {
		t.Fatal("date validity wrong")
	}
}
