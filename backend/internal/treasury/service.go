// Package treasury manages cash boxes and bank accounts and the money that
// moves through them: receipts, payments and transfers.
package treasury

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/accounts"
	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/invoices"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

type Account struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	Name           string          `json:"name"`
	AccountID      int64           `json:"account_id"`
	AccountCode    string          `json:"account_code"`
	BankName       string          `json:"bank_name"`
	Branch         string          `json:"branch"`
	AccountNumber  string          `json:"account_number"`
	IBAN           string          `json:"iban"`
	CardNumber     string          `json:"card_number"`
	AccountHolder  string          `json:"account_holder"`
	OpeningBalance decimal.Decimal `json:"opening_balance"`
	OpeningDate    *civil.Date     `json:"opening_date"`
	Description    string          `json:"description"`
	IsActive       bool            `json:"is_active"`
	Balance        decimal.Decimal `json:"balance"`
}

const accountCols = `t.id, t.kind, t.name, t.account_id, a.code, t.bank_name, t.branch, t.account_number, t.iban, t.card_number,
	t.account_holder, t.opening_balance, t.opening_date, t.description, t.is_active,
	COALESCE((SELECT SUM(l.debit - l.credit) FROM journal_entry_lines l JOIN journal_entries e ON e.id = l.entry_id
	          WHERE l.account_id = t.account_id AND e.posted_at IS NOT NULL), 0)`

type rowScanner interface{ Scan(...any) error }

func scanAccount(s rowScanner, x *Account) error {
	return s.Scan(&x.ID, &x.Kind, &x.Name, &x.AccountID, &x.AccountCode, &x.BankName, &x.Branch, &x.AccountNumber, &x.IBAN,
		&x.CardNumber, &x.AccountHolder, &x.OpeningBalance, &x.OpeningDate, &x.Description, &x.IsActive, &x.Balance)
}

func GetAccount(ctx context.Context, q db.DBTX, companyID, id int64) (*Account, error) {
	var x Account
	err := scanAccount(q.QueryRow(ctx, `SELECT `+accountCols+` FROM treasury_accounts t JOIN accounts a ON a.id = t.account_id
		WHERE t.company_id = $1 AND t.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("treasury_account")
	}
	return &x, err
}

func ListAccounts(ctx context.Context, q db.DBTX, companyID int64, kind string) ([]Account, error) {
	f := db.NewFilter("t.company_id = $1", companyID)
	if kind != "" {
		f.Add("t.kind = $N", kind)
	}
	rows, err := q.Query(ctx, `SELECT `+accountCols+` FROM treasury_accounts t JOIN accounts a ON a.id = t.account_id
		WHERE `+f.Where()+` ORDER BY t.kind, t.name`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		var x Account
		if err := scanAccount(rows, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func normaliseAccount(in *Account) error {
	for _, s := range []*string{&in.Name, &in.BankName, &in.Branch, &in.AccountNumber, &in.IBAN, &in.CardNumber, &in.AccountHolder, &in.Description} {
		*s = strings.TrimSpace(*s)
	}
	in.IBAN = strings.ToUpper(strings.ReplaceAll(in.IBAN, " ", ""))
	in.CardNumber = strings.ReplaceAll(strings.ReplaceAll(in.CardNumber, " ", ""), "-", "")
	v := apperr.NewValidator()
	v.Check(in.Kind == "cash" || in.Kind == "bank", "kind", "invalid")
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 150)
	v.Check(in.IBAN == "" || (len(in.IBAN) >= 15 && len(in.IBAN) <= 34), "iban", "invalid_iban")
	v.Check(in.CardNumber == "" || len(in.CardNumber) == 16, "card_number", "invalid")
	v.Check(!in.OpeningBalance.IsNegative(), "opening_balance", "negative")
	if in.Kind == "bank" {
		v.Required("bank_name", in.BankName)
	}
	return v.Err()
}

// CreateAccount creates a cash box or bank account, its own ledger account and
// (if any) the opening balance entry.
func CreateAccount(ctx context.Context, q db.DBTX, companyID, userID int64, in Account) (int64, error) {
	if err := normaliseAccount(&in); err != nil {
		return 0, err
	}
	parentKey := ledger.MapCashParent
	if in.Kind == "bank" {
		parentKey = ledger.MapBankParent
	}
	parentID, err := ledger.Mapping(ctx, q, companyID, parentKey)
	if err != nil {
		return 0, err
	}
	code, err := accounts.NextCode(ctx, q, companyID, &parentID)
	if err != nil {
		return 0, err
	}
	glName := in.Name
	if in.Kind == "bank" && in.AccountNumber != "" {
		glName = fmt.Sprintf("%s - %s", in.Name, in.AccountNumber)
	}
	glID, err := accounts.Create(ctx, q, companyID, accounts.Input{ParentID: &parentID, Code: code, Name: glName})
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == "validation" {
			return 0, apperr.Rule("treasury_parent_invalid", "the parent account for cash/bank is not usable")
		}
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO treasury_accounts (company_id, kind, name, account_id, bank_name, branch, account_number, iban, card_number,
		                               account_holder, opening_balance, opening_date, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		companyID, in.Kind, in.Name, glID, in.BankName, in.Branch, in.AccountNumber, in.IBAN, in.CardNumber, in.AccountHolder,
		in.OpeningBalance, in.OpeningDate, in.Description).Scan(&id); err != nil {
		return 0, err
	}
	if in.OpeningBalance.IsPositive() {
		date := civil.Today()
		if in.OpeningDate != nil && !in.OpeningDate.IsZero() {
			date = *in.OpeningDate
		}
		equity, err := ledger.Mapping(ctx, q, companyID, ledger.MapOpeningEquity)
		if err != nil {
			return 0, err
		}
		var b ledger.Builder
		b.Debit(glID, nil, nil, "", in.OpeningBalance)
		b.Credit(equity, nil, nil, "", in.OpeningBalance)
		eid, err := ledger.PostAuto(ctx, q, companyID, userID, date, "Opening balance - "+in.Name, "", b.Lines(),
			ledger.Source{Type: ledger.SourceOpening, ID: id})
		if err != nil {
			return 0, err
		}
		if _, err := q.Exec(ctx, `UPDATE treasury_accounts SET opening_entry_id = $2, opening_date = $3 WHERE id = $1`, id, eid, date); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func UpdateAccount(ctx context.Context, q db.DBTX, companyID, id int64, in Account) error {
	cur, err := GetAccount(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	in.Kind = cur.Kind
	in.OpeningBalance = cur.OpeningBalance
	if err := normaliseAccount(&in); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `
		UPDATE treasury_accounts SET name = $3, bank_name = $4, branch = $5, account_number = $6, iban = $7, card_number = $8,
		       account_holder = $9, description = $10, is_active = $11
		WHERE company_id = $1 AND id = $2`, companyID, id, in.Name, in.BankName, in.Branch, in.AccountNumber, in.IBAN, in.CardNumber,
		in.AccountHolder, in.Description, in.IsActive); err != nil {
		return err
	}
	_, err = q.Exec(ctx, `UPDATE accounts SET name = $2, is_active = $3, updated_at = now() WHERE id = $1`, cur.AccountID, in.Name, in.IsActive)
	return err
}

type Transaction struct {
	ID                    int64           `json:"id"`
	Kind                  string          `json:"kind"`
	Number                int64           `json:"number"`
	Date                  civil.Date      `json:"date"`
	TreasuryAccountID     int64           `json:"treasury_account_id"`
	TreasuryAccountName   string          `json:"treasury_account_name"`
	ToTreasuryAccountID   *int64          `json:"to_treasury_account_id"`
	ToTreasuryAccountName *string         `json:"to_treasury_account_name"`
	PartyID               *int64          `json:"party_id"`
	PartyName             *string         `json:"party_name"`
	CounterAccountID      *int64          `json:"counter_account_id"`
	CounterAccountName    *string         `json:"counter_account_name"`
	CostCenterID          *int64          `json:"cost_center_id"`
	Amount                decimal.Decimal `json:"amount"`
	Allocated             decimal.Decimal `json:"allocated"`
	Description           string          `json:"description"`
	Reference             string          `json:"reference"`
	Status                string          `json:"status"`
	JournalEntryID        *int64          `json:"journal_entry_id"`
	JournalNumber         *int64          `json:"journal_number"`
	CreatedAt             time.Time       `json:"created_at"`
	Allocations           []AllocationRow `json:"allocations,omitempty"`
}

type AllocationRow struct {
	InvoiceID int64           `json:"invoice_id"`
	Side      string          `json:"side"`
	Kind      string          `json:"kind"`
	Number    int64           `json:"number"`
	Amount    decimal.Decimal `json:"amount"`
}

type TxInput struct {
	Kind                string                     `json:"kind"`
	Date                civil.Date                 `json:"date"`
	TreasuryAccountID   int64                      `json:"treasury_account_id"`
	ToTreasuryAccountID *int64                     `json:"to_treasury_account_id"`
	PartyID             *int64                     `json:"party_id"`
	CounterAccountID    *int64                     `json:"counter_account_id"`
	CostCenterID        *int64                     `json:"cost_center_id"`
	Amount              decimal.Decimal            `json:"amount"`
	Description         string                     `json:"description"`
	Reference           string                     `json:"reference"`
	Allocations         []invoices.AllocationInput `json:"allocations"`
	AutoAllocate        bool                       `json:"auto_allocate"`
}

const txCols = `t.id, t.kind, t.number, t.date, t.treasury_account_id, ta.name, t.to_treasury_account_id, tt.name, t.party_id, p.name,
	t.counter_account_id, ca.code || ' - ' || ca.name, t.cost_center_id, t.amount,
	COALESCE((SELECT SUM(amount) FROM payment_allocations pa WHERE pa.transaction_id = t.id), 0),
	t.description, t.reference, t.status, t.journal_entry_id, je.number, t.created_at`

const txJoins = ` FROM treasury_transactions t JOIN treasury_accounts ta ON ta.id = t.treasury_account_id
	LEFT JOIN treasury_accounts tt ON tt.id = t.to_treasury_account_id
	LEFT JOIN parties p ON p.id = t.party_id
	LEFT JOIN accounts ca ON ca.id = t.counter_account_id
	LEFT JOIN journal_entries je ON je.id = t.journal_entry_id`

func scanTx(s rowScanner, x *Transaction) error {
	return s.Scan(&x.ID, &x.Kind, &x.Number, &x.Date, &x.TreasuryAccountID, &x.TreasuryAccountName, &x.ToTreasuryAccountID,
		&x.ToTreasuryAccountName, &x.PartyID, &x.PartyName, &x.CounterAccountID, &x.CounterAccountName, &x.CostCenterID, &x.Amount,
		&x.Allocated, &x.Description, &x.Reference, &x.Status, &x.JournalEntryID, &x.JournalNumber, &x.CreatedAt)
}

func GetTx(ctx context.Context, q db.DBTX, companyID, id int64) (*Transaction, error) {
	var x Transaction
	err := scanTx(q.QueryRow(ctx, `SELECT `+txCols+txJoins+` WHERE t.company_id = $1 AND t.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("treasury_transaction")
	}
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT i.id, i.side, i.kind, i.number, a.amount FROM payment_allocations a JOIN invoices i ON i.id = a.invoice_id
		WHERE a.transaction_id = $1 ORDER BY a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	x.Allocations = []AllocationRow{}
	for rows.Next() {
		var a AllocationRow
		if err := rows.Scan(&a.InvoiceID, &a.Side, &a.Kind, &a.Number, &a.Amount); err != nil {
			return nil, err
		}
		x.Allocations = append(x.Allocations, a)
	}
	return &x, rows.Err()
}

type TxFilter struct {
	Kind              string
	TreasuryAccountID int64
	PartyID           int64
	Status            string
	From, To          civil.Date
}

func ListTx(ctx context.Context, q db.DBTX, companyID int64, tf TxFilter, lp httpx.ListParams) (httpx.Page[Transaction], error) {
	f := db.NewFilter("t.company_id = $1", companyID)
	if tf.Kind != "" {
		f.Add("t.kind = $N", tf.Kind)
	}
	if tf.TreasuryAccountID > 0 {
		f.Add("(t.treasury_account_id = $N OR t.to_treasury_account_id = $N)", tf.TreasuryAccountID)
	}
	if tf.PartyID > 0 {
		f.Add("t.party_id = $N", tf.PartyID)
	}
	if tf.Status != "" {
		f.Add("t.status = $N", tf.Status)
	}
	if !tf.From.IsZero() {
		f.Add("t.date >= $N", tf.From)
	}
	if !tf.To.IsZero() {
		f.Add("t.date <= $N", tf.To)
	}
	if lp.Q != "" {
		f.Add("(t.description ILIKE $N OR t.reference ILIKE $N OR p.name ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM treasury_transactions t LEFT JOIN parties p ON p.id = t.party_id WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return httpx.Page[Transaction]{}, err
	}
	order := lp.OrderBy(map[string]string{"date": "t.date", "number": "t.number", "amount": "t.amount", "kind": "t.kind"}, "t.date")
	rows, err := q.Query(ctx, `SELECT `+txCols+txJoins+` WHERE `+f.Where()+` ORDER BY `+order+`, t.id DESC`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return httpx.Page[Transaction]{}, err
	}
	defer rows.Close()
	var items []Transaction
	for rows.Next() {
		var x Transaction
		if err := scanTx(rows, &x); err != nil {
			return httpx.Page[Transaction]{}, err
		}
		items = append(items, x)
	}
	return httpx.NewPage(items, total, lp), rows.Err()
}

// PartyCounterAccount chooses the receivable or payable account a party transaction hits.
func PartyCounterAccount(ctx context.Context, q db.DBTX, companyID, partyID int64, kind string) (int64, error) {
	var isCustomer, isSupplier, active bool
	var recv, pay *int64
	err := q.QueryRow(ctx, `SELECT is_customer, is_supplier, is_active, receivable_account_id, payable_account_id FROM parties WHERE company_id = $1 AND id = $2`,
		companyID, partyID).Scan(&isCustomer, &isSupplier, &active, &recv, &pay)
	if db.IsNoRows(err) {
		v := apperr.NewValidator()
		v.Check(false, "party_id", "invalid")
		return 0, v.Err()
	}
	if err != nil {
		return 0, err
	}
	useReceivable := (kind == "receipt" && (isCustomer || !isSupplier)) || (kind == "payment" && !isSupplier)
	if useReceivable {
		if recv != nil {
			return *recv, nil
		}
		return ledger.Mapping(ctx, q, companyID, ledger.MapReceivable)
	}
	if pay != nil {
		return *pay, nil
	}
	return ledger.Mapping(ctx, q, companyID, ledger.MapPayable)
}

// CreateTx records and posts a receipt, payment or transfer atomically with
// its journal entry and invoice allocations.
func CreateTx(ctx context.Context, q db.DBTX, companyID, userID int64, in TxInput) (int64, error) {
	in.Description, in.Reference = strings.TrimSpace(in.Description), strings.TrimSpace(in.Reference)
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return 0, err
	}
	v := apperr.NewValidator()
	v.Check(in.Kind == "receipt" || in.Kind == "payment" || in.Kind == "transfer", "kind", "invalid")
	v.Check(!in.Date.IsZero(), "date", "required")
	v.RequiredID("treasury_account_id", in.TreasuryAccountID)
	v.Check(in.Amount.IsPositive(), "amount", "positive")
	v.Check(in.Amount.Equal(in.Amount.Round(places)), "amount", "too_many_decimals")
	v.MaxLen("description", in.Description, 500)
	if in.Kind == "transfer" {
		v.Check(in.ToTreasuryAccountID != nil && *in.ToTreasuryAccountID != in.TreasuryAccountID, "to_treasury_account_id", "required")
		in.PartyID, in.CounterAccountID, in.Allocations, in.AutoAllocate = nil, nil, nil, false
	} else {
		in.ToTreasuryAccountID = nil
		v.Check(in.PartyID != nil || in.CounterAccountID != nil, "party_id", "party_or_account")
		v.Check(in.PartyID != nil || len(in.Allocations) == 0, "allocations", "party_required")
	}
	if err := v.Err(); err != nil {
		return 0, err
	}
	from, err := GetAccount(ctx, q, companyID, in.TreasuryAccountID)
	if err != nil {
		return 0, err
	}
	if !from.IsActive {
		return 0, apperr.Rule("treasury_inactive", "the cash/bank account is disabled")
	}
	var to *Account
	if in.ToTreasuryAccountID != nil {
		if to, err = GetAccount(ctx, q, companyID, *in.ToTreasuryAccountID); err != nil {
			return 0, err
		}
		if !to.IsActive {
			return 0, apperr.Rule("treasury_inactive", "the cash/bank account is disabled")
		}
	}
	// Cash cannot go below zero.
	if in.Kind != "receipt" && from.Kind == "cash" && from.Balance.LessThan(in.Amount) {
		return 0, apperr.Rule("insufficient_funds", "the cash balance is not sufficient").WithParams("balance", from.Balance.String())
	}
	var b ledger.Builder
	switch in.Kind {
	case "transfer":
		b.Debit(to.AccountID, nil, nil, in.Description, in.Amount)
		b.Credit(from.AccountID, nil, nil, in.Description, in.Amount)
	default:
		counter := int64(0)
		if in.CounterAccountID != nil {
			counter = *in.CounterAccountID
		} else if counter, err = PartyCounterAccount(ctx, q, companyID, *in.PartyID, in.Kind); err != nil {
			return 0, err
		}
		if in.Kind == "receipt" {
			b.Debit(from.AccountID, nil, nil, in.Description, in.Amount)
			b.Credit(counter, in.PartyID, in.CostCenterID, in.Description, in.Amount)
		} else {
			b.Debit(counter, in.PartyID, in.CostCenterID, in.Description, in.Amount)
			b.Credit(from.AccountID, nil, nil, in.Description, in.Amount)
		}
	}
	number, err := db.NextNumber(ctx, q, companyID, "treasury_"+in.Kind, 0)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO treasury_transactions (company_id, kind, number, date, treasury_account_id, to_treasury_account_id, party_id,
		                                   counter_account_id, cost_center_id, amount, description, reference, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13) RETURNING id`,
		companyID, in.Kind, number, in.Date, in.TreasuryAccountID, in.ToTreasuryAccountID, in.PartyID, in.CounterAccountID,
		in.CostCenterID, in.Amount, in.Description, in.Reference, userID).Scan(&id); err != nil {
		return 0, err
	}
	desc := in.Description
	if desc == "" {
		desc = fmt.Sprintf("%s #%d", in.Kind, number)
	}
	prefix := map[string]string{"receipt": "RCV", "payment": "PAY", "transfer": "TRF"}[in.Kind]
	entryID, err := ledger.PostAuto(ctx, q, companyID, userID, in.Date, desc, fmt.Sprintf("%s-%d", prefix, number), b.Lines(),
		ledger.Source{Type: "treasury_" + in.Kind, ID: id})
	if err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `UPDATE treasury_transactions SET journal_entry_id = $2 WHERE id = $1`, id, entryID); err != nil {
		return 0, err
	}
	if in.PartyID != nil && in.CounterAccountID == nil {
		if err := invoices.AllocatePayment(ctx, q, companyID, *in.PartyID, id, in.Kind, in.Amount, in.Allocations, in.AutoAllocate); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// CancelTx reverses a transaction and releases its invoice allocations.
func CancelTx(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	var status string
	var entryID *int64
	if err := q.QueryRow(ctx, `SELECT status, journal_entry_id FROM treasury_transactions WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).
		Scan(&status, &entryID); err != nil {
		if db.IsNoRows(err) {
			return apperr.NotFound("treasury_transaction")
		}
		return err
	}
	if status == "cancelled" {
		return apperr.Rule("already_cancelled", "the transaction is already cancelled")
	}
	if err := invoices.ReleasePayment(ctx, q, id); err != nil {
		return err
	}
	if entryID != nil {
		if _, err := ledger.Cancel(ctx, q, companyID, userID, *entryID, civil.Date{}, true); err != nil {
			return err
		}
	}
	_, err := q.Exec(ctx, `UPDATE treasury_transactions SET status = 'cancelled', cancelled_at = now() WHERE id = $1`, id)
	return err
}

// AllocationsInput settles open invoices with an already registered receipt or payment.
type AllocationsInput struct {
	Allocations  []invoices.AllocationInput `json:"allocations"`
	AutoAllocate bool                       `json:"auto_allocate"`
}

// AllocateTx allocates the still unallocated part of a posted receipt or payment
// to the counterparty's open documents (explicitly, or oldest first). The ledger
// is not touched: the transaction already settled the party's account; this
// only records which invoices it paid.
func AllocateTx(ctx context.Context, q db.DBTX, companyID, id int64, in AllocationsInput) error {
	kind, partyID, amount, err := lockAllocatable(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if len(in.Allocations) == 0 && !in.AutoAllocate {
		v := apperr.NewValidator()
		v.Check(false, "allocations", "required")
		return v.Err()
	}
	var allocated decimal.Decimal
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM payment_allocations WHERE transaction_id = $1`, id).Scan(&allocated); err != nil {
		return err
	}
	remaining := amount.Sub(allocated)
	if !remaining.IsPositive() {
		return apperr.Rule("fully_allocated", "the whole amount of this transaction is already allocated")
	}
	return invoices.AllocatePayment(ctx, q, companyID, partyID, id, kind, remaining, in.Allocations, in.AutoAllocate)
}

// ReleaseTxAllocations removes a posted transaction's invoice allocations. The
// money stays on the counterparty's account (as an advance) and can be
// allocated again; the invoices become open again.
func ReleaseTxAllocations(ctx context.Context, q db.DBTX, companyID, id int64) error {
	if _, _, _, err := lockAllocatable(ctx, q, companyID, id); err != nil {
		return err
	}
	return invoices.ReleasePayment(ctx, q, id)
}

func lockAllocatable(ctx context.Context, q db.DBTX, companyID, id int64) (string, int64, decimal.Decimal, error) {
	var kind, status string
	var partyID, counter *int64
	var amount decimal.Decimal
	if err := q.QueryRow(ctx, `SELECT kind, status, party_id, counter_account_id, amount FROM treasury_transactions
		WHERE company_id = $1 AND id = $2 FOR UPDATE`, companyID, id).Scan(&kind, &status, &partyID, &counter, &amount); err != nil {
		if db.IsNoRows(err) {
			return "", 0, amount, apperr.NotFound("treasury_transaction")
		}
		return "", 0, amount, err
	}
	if status == "cancelled" {
		return "", 0, amount, apperr.Rule("already_cancelled", "the transaction is cancelled")
	}
	if kind == "transfer" || partyID == nil || counter != nil {
		return "", 0, amount, apperr.Rule("not_allocatable", "only receipts and payments of a counterparty can settle invoices")
	}
	return kind, *partyID, amount, nil
}
