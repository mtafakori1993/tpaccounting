// Package money holds the pure accounting arithmetic used across modules:
// rounding, invoice line calculation, payment allocation and inventory costing.
// It has no I/O so every rule here is covered by unit tests.
package money

import (
	"sort"

	"github.com/shopspring/decimal"
)

var (
	Zero    = decimal.Zero
	hundred = decimal.NewFromInt(100)
)

// Round rounds half away from zero to the given number of decimal places.
func Round(d decimal.Decimal, places int32) decimal.Decimal { return d.Round(places) }

// LineInput is one invoice row as entered by the user.
type LineInput struct {
	Quantity  decimal.Decimal
	UnitPrice decimal.Decimal
	Discount  decimal.Decimal // absolute amount for the whole line
	TaxRate   decimal.Decimal // percent, e.g. 10 for 10%
}

type LineResult struct {
	Gross    decimal.Decimal // quantity × unit price
	Discount decimal.Decimal
	Net      decimal.Decimal // gross − discount
	Tax      decimal.Decimal // net × rate
	Total    decimal.Decimal // net + tax
}

// CalcLine computes one invoice line. Tax is charged on the discounted amount.
func CalcLine(in LineInput, places int32) LineResult {
	gross := Round(in.Quantity.Mul(in.UnitPrice), places)
	disc := Round(in.Discount, places)
	if disc.GreaterThan(gross) {
		disc = gross
	}
	net := gross.Sub(disc)
	tax := Round(net.Mul(in.TaxRate).Div(hundred), places)
	return LineResult{Gross: gross, Discount: disc, Net: net, Tax: tax, Total: net.Add(tax)}
}

// DiscountFromPercent converts a percentage discount into an amount on qty × price.
func DiscountFromPercent(qty, price, percent decimal.Decimal, places int32) decimal.Decimal {
	return Round(qty.Mul(price).Mul(percent).Div(hundred), places)
}

type Totals struct {
	Subtotal decimal.Decimal // sum of gross
	Discount decimal.Decimal
	Net      decimal.Decimal
	Tax      decimal.Decimal
	Total    decimal.Decimal
}

// SumLines adds up computed lines into invoice totals.
func SumLines(lines []LineResult) Totals {
	var t Totals
	for _, l := range lines {
		t.Subtotal = t.Subtotal.Add(l.Gross)
		t.Discount = t.Discount.Add(l.Discount)
		t.Net = t.Net.Add(l.Net)
		t.Tax = t.Tax.Add(l.Tax)
		t.Total = t.Total.Add(l.Total)
	}
	return t
}

// PaymentStatus derives the settlement state of a document.
func PaymentStatus(total, paid decimal.Decimal) string {
	switch {
	case paid.IsZero() && total.IsPositive():
		return "unpaid"
	case paid.LessThan(total):
		return "partial"
	default:
		return "paid"
	}
}

// OpenItem is an outstanding document that can receive an allocation.
type OpenItem struct {
	ID          int64
	Date        string // YYYY-MM-DD, used for FIFO ordering
	Outstanding decimal.Decimal
}

type Allocation struct {
	ID     int64
	Amount decimal.Decimal
}

// AllocateFIFO spreads amount over open items, oldest first. It returns the
// allocations and any unallocated remainder (an advance / on-account amount).
func AllocateFIFO(amount decimal.Decimal, items []OpenItem) ([]Allocation, decimal.Decimal) {
	sorted := append([]OpenItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date < sorted[j].Date
		}
		return sorted[i].ID < sorted[j].ID
	})
	remaining := amount
	var out []Allocation
	for _, it := range sorted {
		if !remaining.IsPositive() {
			break
		}
		if !it.Outstanding.IsPositive() {
			continue
		}
		a := decimal.Min(remaining, it.Outstanding)
		out = append(out, Allocation{ID: it.ID, Amount: a})
		remaining = remaining.Sub(a)
	}
	return out, remaining
}

// IssueValue returns the cost of issuing qty units from a stock balance
// valued at moving average. Issuing the entire balance takes the entire value
// so no rounding residue is left behind.
func IssueValue(balQty, balValue, qty decimal.Decimal, places int32) decimal.Decimal {
	if !balQty.IsPositive() {
		return Zero
	}
	if qty.Equal(balQty) {
		return balValue
	}
	return Round(balValue.Div(balQty).Mul(qty), places)
}

// AverageCost is the current moving-average unit cost of a balance.
func AverageCost(balQty, balValue decimal.Decimal) decimal.Decimal {
	if !balQty.IsPositive() {
		return Zero
	}
	return balValue.Div(balQty).Round(2)
}

// Balanced reports whether debits equal credits and are non-zero.
func Balanced(debits, credits []decimal.Decimal) bool {
	d, c := Zero, Zero
	for _, v := range debits {
		d = d.Add(v)
	}
	for _, v := range credits {
		c = c.Add(v)
	}
	return d.Equal(c) && d.IsPositive()
}
