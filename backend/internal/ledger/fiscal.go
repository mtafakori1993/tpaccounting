package ledger

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// ResolveFiscalYear returns the open fiscal year that accepts postings on date.
func ResolveFiscalYear(ctx context.Context, q db.DBTX, companyID int64, date civil.Date) (int64, error) {
	var (
		fyID   int64
		status string
		locked bool
	)
	err := q.QueryRow(ctx, `
		SELECT fy.id, fy.status, COALESCE(p.is_locked, FALSE)
		FROM fiscal_years fy
		LEFT JOIN accounting_periods p ON p.fiscal_year_id = fy.id AND $2::date BETWEEN p.start_date AND p.end_date
		WHERE fy.company_id = $1 AND $2::date BETWEEN fy.start_date AND fy.end_date`, companyID, date).Scan(&fyID, &status, &locked)
	if err == pgx.ErrNoRows {
		return 0, apperr.Rule("no_fiscal_year", "no fiscal year covers this date").WithParams("date", date.String())
	}
	if err != nil {
		return 0, err
	}
	if status != "open" {
		return 0, apperr.Rule("fiscal_year_closed", "the fiscal year is closed").WithParams("date", date.String())
	}
	if locked {
		return 0, apperr.Rule("period_locked", "the accounting period is locked").WithParams("date", date.String())
	}
	return fyID, nil
}

// Decimals returns the number of decimal places used for amounts in the company.
func Decimals(ctx context.Context, q db.DBTX, companyID int64) (int32, error) {
	var d int32
	err := q.QueryRow(ctx, `SELECT amount_decimals FROM companies WHERE id = $1`, companyID).Scan(&d)
	return d, err
}

// Mapping keys for automatic accounting. Each key is configured per company.
const (
	MapReceivable          = "receivable"
	MapPayable             = "payable"
	MapSales               = "sales"
	MapServiceRevenue      = "service_revenue"
	MapSalesReturns        = "sales_returns"
	MapSalesDiscount       = "sales_discount"
	MapInventory           = "inventory"
	MapCOGS                = "cogs"
	MapPurchases           = "purchases"
	MapTaxOutput           = "tax_output"
	MapTaxInput            = "tax_input"
	MapOpeningEquity       = "opening_equity"
	MapRetainedEarnings    = "retained_earnings"
	MapInventoryAdjustment = "inventory_adjustment"
	MapCashParent          = "cash_parent"
	MapBankParent          = "bank_parent"

	MapChecksReceivable   = "checks_receivable"
	MapChecksInCollection = "checks_in_collection"
	MapChecksPayable      = "checks_payable"

	MapPayrollSalaryExpense    = "payroll_salary_expense"
	MapPayrollEmployerExpense  = "payroll_employer_expense"
	MapPayrollSalariesPayable  = "payroll_salaries_payable"
	MapPayrollInsurancePayable = "payroll_insurance_payable"
	MapPayrollTaxPayable       = "payroll_tax_payable"
	MapPayrollOtherDeductions  = "payroll_other_deductions"
)

var MappingKeys = []string{
	MapReceivable, MapPayable, MapSales, MapServiceRevenue, MapSalesReturns, MapSalesDiscount,
	MapInventory, MapCOGS, MapPurchases, MapTaxOutput, MapTaxInput, MapOpeningEquity,
	MapRetainedEarnings, MapInventoryAdjustment, MapCashParent, MapBankParent,
	MapChecksReceivable, MapChecksInCollection, MapChecksPayable,
	MapPayrollSalaryExpense, MapPayrollEmployerExpense, MapPayrollSalariesPayable, MapPayrollInsurancePayable,
	MapPayrollTaxPayable, MapPayrollOtherDeductions,
}

// Mapping returns the account configured for key.
func Mapping(ctx context.Context, q db.DBTX, companyID int64, key string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT account_id FROM account_mappings WHERE company_id = $1 AND key = $2`, companyID, key).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, apperr.Rule("mapping_missing", "an automatic-accounting account is not configured").WithParams("key", key)
	}
	return id, err
}

// Mappings loads all configured mappings.
func Mappings(ctx context.Context, q db.DBTX, companyID int64) (map[string]int64, error) {
	rows, err := q.Query(ctx, `SELECT key, account_id FROM account_mappings WHERE company_id = $1`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int64{}
	for rows.Next() {
		var k string
		var id int64
		if err := rows.Scan(&k, &id); err != nil {
			return nil, err
		}
		m[k] = id
	}
	return m, rows.Err()
}

// Require returns the account for key from a loaded mapping set.
func Require(m map[string]int64, key string) (int64, error) {
	if id, ok := m[key]; ok {
		return id, nil
	}
	return 0, apperr.Rule("mapping_missing", "an automatic-accounting account is not configured").WithParams("key", key)
}
