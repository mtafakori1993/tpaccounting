package ledger

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
)

func amt(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestValidateRequiresBalanceWhenPosting(t *testing.T) {
	in := EntryInput{Date: civil.New(2026, 1, 1), Lines: []LineInput{
		{AccountID: 1, Debit: amt("100")}, {AccountID: 2, Credit: amt("90")},
	}}
	if err := Validate(in, 0, false); err != nil {
		t.Fatalf("drafts may be unbalanced: %v", err)
	}
	err := Validate(in, 0, true)
	if code(err) != "unbalanced" {
		t.Fatalf("expected unbalanced, got %v", err)
	}
	if e, _ := apperr.As(err); e.Params["difference"] != "10" {
		t.Fatalf("expected difference param, got %+v", e.Params)
	}
	in.Lines[1].Credit = amt("100")
	if err := Validate(in, 0, true); err != nil {
		t.Fatalf("balanced entry rejected: %v", err)
	}
}

func TestValidateLineRules(t *testing.T) {
	cases := map[string]LineInput{
		"debit_or_credit":   {AccountID: 1, Debit: amt("10"), Credit: amt("10")},
		"negative":          {AccountID: 1, Debit: amt("-10")},
		"too_many_decimals": {AccountID: 1, Debit: amt("10.5")},
	}
	for want, line := range cases {
		err := Validate(EntryInput{Date: civil.New(2026, 1, 1), Lines: []LineInput{line, {AccountID: 2, Credit: amt("10")}}}, 0, false)
		e, ok := apperr.As(err)
		if !ok || e.Fields["lines.0.debit"] != want {
			t.Errorf("%s: got %v", want, err)
		}
	}
	err := Validate(EntryInput{Date: civil.New(2026, 1, 1), Lines: []LineInput{{AccountID: 1, Debit: amt("1")}}}, 0, true)
	if e, ok := apperr.As(err); !ok || e.Fields["lines"] != "min_lines" {
		t.Fatalf("posting needs two lines, got %v", err)
	}
	err = Validate(EntryInput{Lines: []LineInput{{Debit: amt("1")}}}, 0, false)
	if e, ok := apperr.As(err); !ok || e.Fields["date"] != "required" || e.Fields["lines.0.account_id"] != "required" {
		t.Fatalf("expected required fields, got %v", err)
	}
}

func TestBuilderMergesAndOrders(t *testing.T) {
	var b Builder
	party := int64(7)
	b.Credit(10, nil, nil, "", amt("100"))
	b.Debit(20, &party, nil, "", amt("150"))
	b.Credit(10, nil, nil, "", amt("50"))
	b.Credit(30, nil, nil, "", amt("0"))   // ignored
	b.Credit(40, nil, nil, "", amt("-20")) // flips to debit
	lines := b.Lines()
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %+v", len(lines), lines)
	}
	if lines[0].AccountID != 20 || lines[1].AccountID != 40 || !lines[1].Debit.Equal(amt("20")) {
		t.Fatalf("debits must come first: %+v", lines)
	}
	if lines[2].AccountID != 10 || !lines[2].Credit.Equal(amt("150")) {
		t.Fatalf("credits must merge: %+v", lines[2])
	}
	rev := Reversed(lines)
	if !rev[0].Credit.Equal(amt("150")) || !rev[2].Debit.Equal(amt("150")) {
		t.Fatalf("reversal must swap sides: %+v", rev)
	}
}
