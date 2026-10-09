package tools

import (
	"github.com/shopspring/decimal"
)

// DTOs mirror the subset of accounting API responses the assistant uses.

type Party struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	IsCustomer bool   `json:"is_customer"`
	IsSupplier bool   `json:"is_supplier"`
	IsOther    bool   `json:"is_other"`
	Mobile     string `json:"mobile"`
	Phone      string `json:"phone"`
	IsActive   bool   `json:"is_active"`
}

type CostCenter struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	NameEn   string `json:"name_en"`
	IsActive bool   `json:"is_active"`
}

type TreasuryAccount struct {
	ID            int64           `json:"id"`
	Kind          string          `json:"kind"` // cash | bank
	Name          string          `json:"name"`
	AccountID     int64           `json:"account_id"`
	AccountCode   string          `json:"account_code"`
	BankName      string          `json:"bank_name"`
	AccountNumber string          `json:"account_number"`
	CardNumber    string          `json:"card_number"`
	IsActive      bool            `json:"is_active"`
	Balance       decimal.Decimal `json:"balance"`
}

type Account struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	NameEn   string `json:"name_en"`
	Type     string `json:"type"`
	Level    int    `json:"level"`
	IsActive bool   `json:"is_active"`
	Postable bool   `json:"postable"`
}

type ExpenseCategory struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	NameEn      string `json:"name_en"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	IsActive    bool   `json:"is_active"`
}

type Transaction struct {
	ID                  int64           `json:"id"`
	Kind                string          `json:"kind"`
	Number              int64           `json:"number"`
	Date                string          `json:"date"`
	TreasuryAccountID   int64           `json:"treasury_account_id"`
	TreasuryAccountName string          `json:"treasury_account_name"`
	PartyID             *int64          `json:"party_id"`
	PartyName           *string         `json:"party_name"`
	CounterAccountID    *int64          `json:"counter_account_id"`
	CounterAccountName  *string         `json:"counter_account_name"`
	CostCenterID        *int64          `json:"cost_center_id"`
	Amount              decimal.Decimal `json:"amount"`
	Description         string          `json:"description"`
	Reference           string          `json:"reference"`
	Status              string          `json:"status"`
	JournalEntryID      *int64          `json:"journal_entry_id"`
	JournalNumber       *int64          `json:"journal_number"`
	CreatedAt           string          `json:"created_at"`
}

type Expense struct {
	ID                  int64           `json:"id"`
	Number              int64           `json:"number"`
	Date                string          `json:"date"`
	CategoryID          int64           `json:"category_id"`
	CategoryName        string          `json:"category_name"`
	AccountID           int64           `json:"account_id"`
	AccountName         string          `json:"account_name"`
	Amount              decimal.Decimal `json:"amount"`
	Description         string          `json:"description"`
	Reference           string          `json:"reference"`
	CostCenterID        *int64          `json:"cost_center_id"`
	PaymentMethod       string          `json:"payment_method"`
	TreasuryAccountID   *int64          `json:"treasury_account_id"`
	TreasuryAccountName *string         `json:"treasury_account_name"`
	PartyID             *int64          `json:"party_id"`
	PartyName           *string         `json:"party_name"`
	Status              string          `json:"status"`
	JournalEntryID      *int64          `json:"journal_entry_id"`
	JournalNumber       *int64          `json:"journal_number"`
	CreatedAt           string          `json:"created_at"`
}

type Invoice struct {
	ID            int64           `json:"id"`
	Side          string          `json:"side"`
	Kind          string          `json:"kind"`
	Number        int64           `json:"number"`
	Date          string          `json:"date"`
	DueDate       *string         `json:"due_date"`
	PartyID       int64           `json:"party_id"`
	PartyName     string          `json:"party_name"`
	Status        string          `json:"status"`
	Total         decimal.Decimal `json:"total"`
	Outstanding   decimal.Decimal `json:"outstanding"`
	PaymentStatus string          `json:"payment_status"`
	Reference     string          `json:"reference"`
}

type FiscalYear struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
	Status    string   `json:"status"`
	Periods   []Period `json:"periods,omitempty"`
}

type Period struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	IsLocked  bool   `json:"is_locked"`
}

type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// CompanyConfig is the compact snapshot of the company's own configuration the
// classifier is allowed to choose from. It never contains invented accounts.
type CompanyConfig struct {
	CompanyName       string            `json:"company_name"`
	BaseCurrency      string            `json:"base_currency"`
	AmountDecimals    int               `json:"amount_decimals"`
	Calendar          string            `json:"calendar"`
	TreasuryAccounts  []TreasuryAccount `json:"treasury_accounts"`
	ExpenseCategories []ExpenseCategory `json:"expense_categories"`
	Projects          []CostCenter      `json:"projects"`
	CostCenters       []CostCenter      `json:"cost_centers"`
	FiscalYears       []FiscalYear      `json:"fiscal_years"`
	BudgetsActive     bool              `json:"budgets_active"`
}
