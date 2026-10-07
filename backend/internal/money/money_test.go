package money

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestCalcLineTaxOnDiscountedAmount(t *testing.T) {
	r := CalcLine(LineInput{Quantity: d("4"), UnitPrice: d("1500000"), Discount: d("100000"), TaxRate: d("10")}, 0)
	if !r.Gross.Equal(d("6000000")) || !r.Net.Equal(d("5900000")) || !r.Tax.Equal(d("590000")) || !r.Total.Equal(d("6490000")) {
		t.Fatalf("unexpected line: %+v", r)
	}
}

func TestCalcLineRounding(t *testing.T) {
	// 3 × 33.333 = 99.999 → 100.00 at 2 places; 9% tax = 9.00
	r := CalcLine(LineInput{Quantity: d("3"), UnitPrice: d("33.333"), TaxRate: d("9")}, 2)
	if !r.Gross.Equal(d("100")) || !r.Tax.Equal(d("9")) {
		t.Fatalf("unexpected rounding: %+v", r)
	}
	// Rial (0 places): 15 × 10% = 1.5 → rounds half away from zero to 2
	r = CalcLine(LineInput{Quantity: d("1"), UnitPrice: d("15"), TaxRate: d("10")}, 0)
	if !r.Tax.Equal(d("2")) || !r.Total.Equal(d("17")) {
		t.Fatalf("unexpected rial rounding: %+v", r)
	}
}

func TestCalcLineDiscountCappedAtGross(t *testing.T) {
	r := CalcLine(LineInput{Quantity: d("1"), UnitPrice: d("100"), Discount: d("150"), TaxRate: d("10")}, 0)
	if !r.Net.IsZero() || !r.Tax.IsZero() || !r.Total.IsZero() {
		t.Fatalf("discount should be capped: %+v", r)
	}
}

func TestDiscountFromPercent(t *testing.T) {
	if got := DiscountFromPercent(d("3"), d("1000"), d("12.5"), 0); !got.Equal(d("375")) {
		t.Fatalf("got %s", got)
	}
}

func TestSumLines(t *testing.T) {
	lines := []LineResult{
		CalcLine(LineInput{Quantity: d("4"), UnitPrice: d("1500000"), Discount: d("100000"), TaxRate: d("10")}, 0),
		CalcLine(LineInput{Quantity: d("2"), UnitPrice: d("500000"), TaxRate: d("10")}, 0),
	}
	tot := SumLines(lines)
	if !tot.Subtotal.Equal(d("7000000")) || !tot.Discount.Equal(d("100000")) || !tot.Net.Equal(d("6900000")) ||
		!tot.Tax.Equal(d("690000")) || !tot.Total.Equal(d("7590000")) {
		t.Fatalf("unexpected totals: %+v", tot)
	}
}

func TestPaymentStatus(t *testing.T) {
	cases := map[string][2]string{"unpaid": {"100", "0"}, "partial": {"100", "40"}, "paid": {"100", "100"}}
	for want, c := range cases {
		if got := PaymentStatus(d(c[0]), d(c[1])); got != want {
			t.Errorf("PaymentStatus(%s,%s) = %s, want %s", c[0], c[1], got, want)
		}
	}
}

func TestAllocateFIFO(t *testing.T) {
	items := []OpenItem{
		{ID: 3, Date: "2026-03-01", Outstanding: d("300")},
		{ID: 1, Date: "2026-01-01", Outstanding: d("100")},
		{ID: 2, Date: "2026-02-01", Outstanding: d("200")},
		{ID: 4, Date: "2026-02-01", Outstanding: d("0")},
	}
	allocs, rest := AllocateFIFO(d("250"), items)
	if len(allocs) != 2 || allocs[0].ID != 1 || !allocs[0].Amount.Equal(d("100")) || allocs[1].ID != 2 || !allocs[1].Amount.Equal(d("150")) {
		t.Fatalf("unexpected allocations: %+v", allocs)
	}
	if !rest.IsZero() {
		t.Fatalf("unexpected remainder %s", rest)
	}
	allocs, rest = AllocateFIFO(d("1000"), items)
	if len(allocs) != 3 || !rest.Equal(d("400")) {
		t.Fatalf("overpayment: %+v rest %s", allocs, rest)
	}
}

func TestMovingAverageIssue(t *testing.T) {
	// 10 @ 100 + 10 @ 200 = 20 units worth 3000; issuing 5 costs 750.
	if v := IssueValue(d("20"), d("3000"), d("5"), 0); !v.Equal(d("750")) {
		t.Fatalf("got %s", v)
	}
	// Issuing the whole balance takes the whole value, leaving no residue.
	if v := IssueValue(d("3"), d("100"), d("3"), 0); !v.Equal(d("100")) {
		t.Fatalf("got %s", v)
	}
	if v := IssueValue(d("3"), d("100"), d("1"), 0); !v.Equal(d("33")) {
		t.Fatalf("got %s", v)
	}
	if v := AverageCost(d("20"), d("3000")); !v.Equal(d("150")) {
		t.Fatalf("got %s", v)
	}
	if v := IssueValue(d("0"), d("0"), d("1"), 0); !v.IsZero() {
		t.Fatalf("empty stock should cost zero, got %s", v)
	}
}

func TestBalanced(t *testing.T) {
	if !Balanced([]decimal.Decimal{d("100"), d("50")}, []decimal.Decimal{d("150")}) {
		t.Fatal("expected balanced")
	}
	if Balanced([]decimal.Decimal{d("100")}, []decimal.Decimal{d("99.99")}) {
		t.Fatal("expected unbalanced")
	}
	if Balanced(nil, nil) {
		t.Fatal("empty entry is not balanced")
	}
}
