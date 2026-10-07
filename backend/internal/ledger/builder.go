package ledger

import "github.com/shopspring/decimal"

// Builder assembles journal lines for automatic accounting, merging lines that
// hit the same account/party/cost center on the same side.
type Builder struct {
	lines []LineInput
	index map[builderKey]int
}

type builderKey struct {
	account, party, cc int64
	debit              bool
	desc               string
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func (b *Builder) add(accountID int64, partyID, ccID *int64, desc string, amount decimal.Decimal, debit bool) {
	if amount.IsZero() {
		return
	}
	if amount.IsNegative() {
		// A negative amount on one side is a positive amount on the other.
		amount, debit = amount.Neg(), !debit
	}
	if b.index == nil {
		b.index = map[builderKey]int{}
	}
	k := builderKey{accountID, val(partyID), val(ccID), debit, desc}
	if i, ok := b.index[k]; ok {
		if debit {
			b.lines[i].Debit = b.lines[i].Debit.Add(amount)
		} else {
			b.lines[i].Credit = b.lines[i].Credit.Add(amount)
		}
		return
	}
	l := LineInput{AccountID: accountID, PartyID: partyID, CostCenterID: ccID, Description: desc}
	if debit {
		l.Debit = amount
	} else {
		l.Credit = amount
	}
	b.index[k] = len(b.lines)
	b.lines = append(b.lines, l)
}

func (b *Builder) Debit(accountID int64, partyID, ccID *int64, desc string, amount decimal.Decimal) {
	b.add(accountID, partyID, ccID, desc, amount, true)
}

func (b *Builder) Credit(accountID int64, partyID, ccID *int64, desc string, amount decimal.Decimal) {
	b.add(accountID, partyID, ccID, desc, amount, false)
}

// Lines returns debit lines first, then credit lines.
func (b *Builder) Lines() []LineInput {
	out := make([]LineInput, 0, len(b.lines))
	for _, l := range b.lines {
		if l.Debit.IsPositive() {
			out = append(out, l)
		}
	}
	for _, l := range b.lines {
		if l.Credit.IsPositive() {
			out = append(out, l)
		}
	}
	return out
}

// Reversed returns lines with debit and credit swapped.
func Reversed(lines []LineInput) []LineInput {
	out := make([]LineInput, len(lines))
	for i, l := range lines {
		l.Debit, l.Credit = l.Credit, l.Debit
		out[i] = l
	}
	return out
}
