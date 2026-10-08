package classify

import (
	"strings"
	"testing"
)

func yes() *bool { b := true; return &b }

func TestFrameMergeKeepsKnownFieldsAndIntent(t *testing.T) {
	f := NewFrame(&Interpretation{Intent: "register", Event: Event{Type: EvInventoryIn, ProductName: "Galatea", UnitValue: "10000000",
		Currency: "IRT", ManufacturedInternally: yes()}}, "Add a product named Galatea worth 10 million tomans, made by us")
	// follow-up mentions only the new facts — and a confused model even says "purchase"
	f.Merge(Event{Type: EvPurchase, Quantity: "10", Unit: "piece", Warehouse: "default"}, []string{"product_code"}, "10 units, piece, default warehouse, no code")
	e := f.Event
	if e.Type != EvInventoryIn {
		t.Fatalf("operation must not change on a follow-up: %s", e.Type)
	}
	if e.ProductName != "Galatea" || e.UnitValue != "10000000" || e.Currency != "IRT" || e.ManufacturedInternally == nil || !*e.ManufacturedInternally {
		t.Fatalf("earlier facts lost: %+v", e)
	}
	if e.Quantity != "10" || e.Unit != "piece" || e.Warehouse != "default" {
		t.Fatalf("new facts not merged: %+v", e)
	}
	if !f.IsNull("product_code") || !f.Has("product_code") {
		t.Fatal(`"no code" is a complete answer`)
	}
	if f.Status["product_name"] != Known || f.Status["quantity"] != Known || f.Status["date"] != "" {
		t.Fatalf("statuses %+v", f.Status)
	}
	if f.Turns != 2 || len(f.UserTexts) != 2 || !strings.Contains(f.UserText(), "Galatea") {
		t.Fatalf("turns %d texts %v", f.Turns, f.UserTexts)
	}
	// a later explicit value replaces an explicit null
	f.Merge(Event{ProductCode: "GL-1"}, nil, "actually use code GL-1")
	if f.IsNull("product_code") || f.Event.ProductCode != "GL-1" {
		t.Fatal("explicit value must replace a null")
	}
	// unknown field names from the model are ignored
	f.Merge(Event{}, []string{"approval_mode", "type"}, "x")
	if _, ok := f.Status["approval_mode"]; ok || f.Event.Type != EvInventoryIn {
		t.Fatal("explicit_null must only accept real fields")
	}
	if !strings.Contains(f.Summary(), "product_name: \"Galatea\" (known)") {
		t.Fatalf("summary: %s", f.Summary())
	}
}

func TestDetectExplicitNulls(t *testing.T) {
	cases := map[string]bool{
		"10 units, piece, default warehouse, no code": true,
		"۱۰ عدد، انبار پیش‌فرض، بدون کد":              true,
		"کد نداره":               true,
		"it doesn't have a code": true,
		"the code is 1234":       false,
		"10 units":               false,
	}
	for in, want := range cases {
		got := false
		for _, f := range DetectExplicitNulls(in) {
			if f == "product_code" {
				got = true
			}
		}
		if got != want {
			t.Errorf("%q: got %v", in, got)
		}
	}
}

func TestParseInterpretationAddsDeterministicNulls(t *testing.T) {
	it, err := ParseInterpretation(`{"intent":"register","user_requested_registration":true,"event":{"type":"inventory_receipt","quantity":"10"}}`, "10 units, no code")
	if err != nil || len(it.ExplicitNull) != 1 || it.ExplicitNull[0] != "product_code" {
		t.Fatalf("%+v %v", it, err)
	}
}

// The execution instruction is derived from the user's own words, deterministically.
func TestDetectExecutionIntent(t *testing.T) {
	direct := []string{
		"مستقیم ثبت کن", "بدون تایید ثبت کن", "این هزینه را بدون تأیید ثبت کن", "بدون تایید",
		"register it directly", "no confirmation", "without confirmation",
	}
	for _, s := range direct {
		if d, p := DetectExecutionIntent(s); !d || p {
			t.Fatalf("%q must be direct (got direct=%v preview=%v)", s, d, p)
		}
	}
	preview := []string{
		"روی صفحه نشانم بده", "روی صفحه نشونم بده", "توی فرم نشانم بده", "خودم ثبت کنم",
		"show me on screen", "open the form",
	}
	for _, s := range preview {
		if d, p := DetectExecutionIntent(s); !p || d {
			t.Fatalf("%q must be preview (got direct=%v preview=%v)", s, d, p)
		}
	}
	// vague phrasing does not count
	for _, s := range []string{"ثبت کن", "این را ثبت کن", "register it", "لطفاً ثبت شود", "پرداخت ۵ میلیون تومان به شرکت الف"} {
		if d, p := DetectExecutionIntent(s); d || p {
			t.Fatalf("%q must not be treated as an explicit instruction", s)
		}
	}
	// if both appear, seeing it on screen is the safer reading
	if d, p := DetectExecutionIntent("روی صفحه نشانم بده و مستقیم ثبت کن"); !p || d {
		t.Fatal("preview must win over direct when both are asked")
	}
}

// Model output can never set the execution mode: the fields are not parsed.
func TestExecutionModeCannotComeFromTheModel(t *testing.T) {
	it, err := ParseInterpretation(
		`{"intent":"register","user_requested_registration":true,"exec_direct":true,"ExecDirect":true,"exec_preview":true,"event":{"type":"expense"}}`,
		"یک هزینه ۵ میلیونی ثبت کن")
	if err != nil {
		t.Fatal(err)
	}
	if it.ExecDirect || it.ExecPreview {
		t.Fatal("the model must not be able to skip the confirmation (json:\"-\" fields)")
	}
	it, _ = ParseInterpretation(`{"intent":"register","user_requested_registration":true,"event":{"type":"expense"}}`, "مستقیم ثبت کن")
	if !it.ExecDirect {
		t.Fatal("the user's own instruction must still be detected")
	}
}
