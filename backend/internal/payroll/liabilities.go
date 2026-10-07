package payroll

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/treasury"
)

// Payroll liabilities are the amounts a posted payroll owes to the tax office,
// social security and other third parties. They are created when the payroll
// is posted (from the same payslip lines as the accrual entry) and settled by
// ordinary treasury payments (Dr liability / Cr cash or bank) — there is no
// separate payment mechanism. The paid amount is always derived from the live
// treasury transactions, so cancelling a payment in treasury reopens the
// liability.

const (
	LiabilityEmployeeTax       = "employee_tax"
	LiabilityEmployeeInsurance = "employee_insurance"
	LiabilityEmployerInsurance = "employer_insurance"
	LiabilityOther             = "other"
)

type LiabilityPayment struct {
	TransactionID     int64           `json:"transaction_id"`
	TransactionNumber int64           `json:"transaction_number"`
	Date              civil.Date      `json:"date"`
	Amount            decimal.Decimal `json:"amount"`
	TreasuryAccount   string          `json:"treasury_account"`
	JournalEntryID    *int64          `json:"journal_entry_id"`
	Status            string          `json:"status"`
	UserName          *string         `json:"user_name"`
	CreatedAt         time.Time       `json:"created_at"`
}

type Liability struct {
	ID             int64              `json:"id"`
	PeriodID       int64              `json:"period_id"`
	PeriodName     string             `json:"period_name"`
	Kind           string             `json:"kind"`
	AccountID      int64              `json:"account_id"`
	AccountCode    string             `json:"account_code"`
	AccountName    string             `json:"account_name"`
	Amount         decimal.Decimal    `json:"amount"`
	Paid           decimal.Decimal    `json:"paid"`
	Outstanding    decimal.Decimal    `json:"outstanding"`
	Status         string             `json:"status"` // open | partial | paid
	JournalEntryID *int64             `json:"journal_entry_id"`
	CreatedAt      time.Time          `json:"created_at"`
	Payments       []LiabilityPayment `json:"payments,omitempty"`
}

type liabilityKey struct {
	kind    string
	account int64
}

// liabilityAmounts derives the liabilities of a payroll from its payslip
// lines, with the same account resolution as AccrualLines.
func liabilityAmounts(ctx context.Context, q db.DBTX, companyID, periodID int64) (map[liabilityKey]decimal.Decimal, error) {
	m, err := ledger.Mappings(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT l.kind, l.category, l.account_id, SUM(l.amount) FROM payslip_lines l JOIN payslips s ON s.id = l.payslip_id
		WHERE s.period_id = $1 GROUP BY l.kind, l.category, l.account_id`, periodID)
	if err != nil {
		return nil, err
	}
	type line struct {
		kind, category string
		account        *int64
		amount         decimal.Decimal
	}
	var lines []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.kind, &l.category, &l.account, &l.amount); err != nil {
			rows.Close()
			return nil, err
		}
		lines = append(lines, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[liabilityKey]decimal.Decimal{}
	add := func(kind, mapKey string, override *int64, amount decimal.Decimal) error {
		acc := int64(0)
		if override != nil {
			acc = *override
		} else if acc, err = ledger.Require(m, mapKey); err != nil {
			return err
		}
		out[liabilityKey{kind, acc}] = out[liabilityKey{kind, acc}].Add(amount)
		return nil
	}
	for _, l := range lines {
		if !l.amount.IsPositive() {
			continue
		}
		switch {
		case l.kind == KindDeduction && l.category == "tax":
			err = add(LiabilityEmployeeTax, ledger.MapPayrollTaxPayable, nil, l.amount)
		case l.kind == KindDeduction && l.category == "insurance":
			err = add(LiabilityEmployeeInsurance, ledger.MapPayrollInsurancePayable, nil, l.amount)
		case l.kind == KindEmployer:
			err = add(LiabilityEmployerInsurance, ledger.MapPayrollInsurancePayable, nil, l.amount)
		case l.kind == KindDeduction && l.category != "unpaid_leave":
			// Other deductions are a liability only when credited to a liability
			// account (e.g. union dues); loans and advances recover an asset.
			acc := l.account
			if acc == nil {
				a, err := ledger.Require(m, ledger.MapPayrollOtherDeductions)
				if err != nil {
					return nil, err
				}
				acc = &a
			}
			var typ string
			if err := q.QueryRow(ctx, `SELECT type FROM accounts WHERE id = $1`, *acc).Scan(&typ); err != nil {
				return nil, err
			}
			if typ == "liability" {
				err = add(LiabilityOther, "", acc, l.amount)
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// createLiabilities records the liabilities of a payroll being posted.
func createLiabilities(ctx context.Context, q db.DBTX, companyID, periodID, entryID int64) error {
	amounts, err := liabilityAmounts(ctx, q, companyID, periodID)
	if err != nil {
		return err
	}
	for k, a := range amounts {
		if _, err := q.Exec(ctx, `INSERT INTO payroll_liabilities (company_id, period_id, kind, account_id, amount, journal_entry_id)
			VALUES ($1, $2, $3, $4, $5, $6)`, companyID, periodID, k.kind, k.account, a, entryID); err != nil {
			return err
		}
	}
	return nil
}

// removeLiabilities deletes the liabilities of a payroll being unposted. It
// fails while any of them has a live payment.
func removeLiabilities(ctx context.Context, q db.DBTX, periodID int64) error {
	var live bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payroll_liability_payments lp JOIN payroll_liabilities l ON l.id = lp.liability_id
		JOIN treasury_transactions t ON t.id = lp.treasury_transaction_id WHERE l.period_id = $1 AND t.status <> 'cancelled')`, periodID).Scan(&live); err != nil {
		return err
	}
	if live {
		return apperr.Rule("liabilities_paid", "tax or insurance of this payroll has been paid; cancel those payments first")
	}
	if _, err := q.Exec(ctx, `DELETE FROM payroll_liability_payments WHERE liability_id IN (SELECT id FROM payroll_liabilities WHERE period_id = $1)`, periodID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM payroll_liabilities WHERE period_id = $1`, periodID)
	return err
}

const liabilityCols = `l.id, l.period_id, p.name, l.kind, l.account_id, a.code, a.name, l.amount,
	COALESCE((SELECT SUM(lp.amount) FROM payroll_liability_payments lp JOIN treasury_transactions t ON t.id = lp.treasury_transaction_id
	          WHERE lp.liability_id = l.id AND t.status <> 'cancelled'), 0),
	l.journal_entry_id, l.created_at`

const liabilityJoins = ` FROM payroll_liabilities l JOIN payroll_periods p ON p.id = l.period_id JOIN accounts a ON a.id = l.account_id`

func scanLiability(s rowScanner, x *Liability) error {
	if err := s.Scan(&x.ID, &x.PeriodID, &x.PeriodName, &x.Kind, &x.AccountID, &x.AccountCode, &x.AccountName, &x.Amount, &x.Paid,
		&x.JournalEntryID, &x.CreatedAt); err != nil {
		return err
	}
	x.Outstanding = x.Amount.Sub(x.Paid)
	switch {
	case !x.Outstanding.IsPositive():
		x.Status = "paid"
	case x.Paid.IsPositive():
		x.Status = "partial"
	default:
		x.Status = "open"
	}
	return nil
}

type LiabilityFilter struct {
	PeriodID int64
	Kind     string
	OpenOnly bool
}

func ListLiabilities(ctx context.Context, q db.DBTX, companyID int64, lf LiabilityFilter) ([]Liability, error) {
	f := db.NewFilter("l.company_id = $1", companyID)
	if lf.PeriodID > 0 {
		f.Add("l.period_id = $N", lf.PeriodID)
	}
	if lf.Kind != "" {
		f.Add("l.kind = $N", lf.Kind)
	}
	rows, err := q.Query(ctx, `SELECT `+liabilityCols+liabilityJoins+` WHERE `+f.Where()+` ORDER BY p.start_date DESC, l.kind, l.id`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Liability{}
	for rows.Next() {
		var x Liability
		if err := scanLiability(rows, &x); err != nil {
			return nil, err
		}
		if lf.OpenOnly && x.Status == "paid" {
			continue
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func GetLiability(ctx context.Context, q db.DBTX, companyID, id int64) (*Liability, error) {
	var x Liability
	err := scanLiability(q.QueryRow(ctx, `SELECT `+liabilityCols+liabilityJoins+` WHERE l.company_id = $1 AND l.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("payroll_liability")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT t.id, t.number, t.date, lp.amount, ta.name, t.journal_entry_id, t.status, u.full_name, lp.created_at
		FROM payroll_liability_payments lp JOIN treasury_transactions t ON t.id = lp.treasury_transaction_id
		JOIN treasury_accounts ta ON ta.id = t.treasury_account_id LEFT JOIN users u ON u.id = lp.created_by
		WHERE lp.liability_id = $1 ORDER BY lp.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	x.Payments = []LiabilityPayment{}
	for rows.Next() {
		var p LiabilityPayment
		if err := rows.Scan(&p.TransactionID, &p.TransactionNumber, &p.Date, &p.Amount, &p.TreasuryAccount, &p.JournalEntryID, &p.Status,
			&p.UserName, &p.CreatedAt); err != nil {
			return nil, err
		}
		x.Payments = append(x.Payments, p)
	}
	return &x, rows.Err()
}

type LiabilityPayInput struct {
	Date              civil.Date      `json:"date"`
	TreasuryAccountID int64           `json:"treasury_account_id"`
	Amount            decimal.Decimal `json:"amount"`
	Reference         string          `json:"reference"`
	Description       string          `json:"description"`
}

var liabilityNames = map[string]string{
	LiabilityEmployeeTax: "Payroll tax", LiabilityEmployeeInsurance: "Employee social security",
	LiabilityEmployerInsurance: "Employer social security", LiabilityOther: "Payroll deductions",
}

// PayLiability settles (part of) a liability with a treasury payment.
func PayLiability(ctx context.Context, q db.DBTX, companyID, userID, id int64, in LiabilityPayInput) (int64, error) {
	var dummy int64
	if err := q.QueryRow(ctx, `SELECT id FROM payroll_liabilities WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&dummy); err != nil {
		if db.IsNoRows(err) {
			return 0, apperr.NotFound("payroll_liability")
		}
		return 0, err
	}
	l, err := GetLiability(ctx, q, companyID, id)
	if err != nil {
		return 0, err
	}
	if l.Status == "paid" {
		return 0, apperr.Rule("liability_paid", "this liability is fully paid")
	}
	if in.Amount.IsZero() {
		in.Amount = l.Outstanding
	}
	if in.Date.IsZero() {
		in.Date = civil.Today()
	}
	var periodEnd civil.Date
	if err := q.QueryRow(ctx, `SELECT end_date FROM payroll_periods WHERE id = $1`, l.PeriodID).Scan(&periodEnd); err != nil {
		return 0, err
	}
	v := apperr.NewValidator()
	v.RequiredID("treasury_account_id", in.TreasuryAccountID)
	v.Check(in.Amount.IsPositive(), "amount", "positive")
	v.Check(!in.Amount.GreaterThan(l.Outstanding), "amount", "exceeds_outstanding")
	v.Check(!in.Date.Before(periodEnd), "date", "before_date")
	v.MaxLen("reference", in.Reference, 100)
	v.MaxLen("description", in.Description, 500)
	if err := v.Err(); err != nil {
		return 0, err
	}
	desc := strings.TrimSpace(in.Description)
	if desc == "" {
		desc = fmt.Sprintf("%s - %s", liabilityNames[l.Kind], l.PeriodName)
	}
	txID, err := treasury.CreateTx(ctx, q, companyID, userID, treasury.TxInput{
		Kind: "payment", Date: in.Date, TreasuryAccountID: in.TreasuryAccountID, CounterAccountID: &l.AccountID, Amount: in.Amount,
		Description: desc, Reference: in.Reference,
	})
	if err != nil {
		return 0, err
	}
	_, err = q.Exec(ctx, `INSERT INTO payroll_liability_payments (liability_id, treasury_transaction_id, amount, created_by) VALUES ($1, $2, $3, $4)`,
		id, txID, in.Amount, userID)
	return txID, err
}
