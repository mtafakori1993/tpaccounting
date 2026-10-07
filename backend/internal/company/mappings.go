package company

import (
	"context"
	"slices"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// mappingRules lists, per automatic-accounting role, the account types allowed
// and whether the account must be a parent (for auto-created sub-accounts).
var mappingRules = map[string]struct {
	types  []string
	parent bool
}{
	ledger.MapReceivable:          {[]string{"asset"}, false},
	ledger.MapPayable:             {[]string{"liability"}, false},
	ledger.MapSales:               {[]string{"revenue"}, false},
	ledger.MapServiceRevenue:      {[]string{"revenue"}, false},
	ledger.MapSalesReturns:        {[]string{"revenue"}, false},
	ledger.MapSalesDiscount:       {[]string{"revenue"}, false},
	ledger.MapInventory:           {[]string{"asset"}, false},
	ledger.MapCOGS:                {[]string{"cost_of_sales"}, false},
	ledger.MapPurchases:           {[]string{"cost_of_sales", "expense"}, false},
	ledger.MapTaxOutput:           {[]string{"liability"}, false},
	ledger.MapTaxInput:            {[]string{"asset"}, false},
	ledger.MapOpeningEquity:       {[]string{"equity"}, false},
	ledger.MapRetainedEarnings:    {[]string{"equity"}, false},
	ledger.MapInventoryAdjustment: {[]string{"expense", "cost_of_sales"}, false},
	ledger.MapCashParent:          {[]string{"asset"}, true},
	ledger.MapBankParent:          {[]string{"asset"}, true},

	ledger.MapChecksReceivable:        {[]string{"asset"}, false},
	ledger.MapChecksInCollection:      {[]string{"asset"}, false},
	ledger.MapChecksPayable:           {[]string{"liability"}, false},
	ledger.MapPayrollSalaryExpense:    {[]string{"expense", "cost_of_sales"}, false},
	ledger.MapPayrollEmployerExpense:  {[]string{"expense", "cost_of_sales"}, false},
	ledger.MapPayrollSalariesPayable:  {[]string{"liability"}, false},
	ledger.MapPayrollInsurancePayable: {[]string{"liability"}, false},
	ledger.MapPayrollTaxPayable:       {[]string{"liability"}, false},
	ledger.MapPayrollOtherDeductions:  {[]string{"asset", "liability"}, false},
}

type Mapping struct {
	Key           string   `json:"key"`
	AccountID     *int64   `json:"account_id"`
	AccountCode   *string  `json:"account_code"`
	AccountName   *string  `json:"account_name"`
	AccountNameEn *string  `json:"account_name_en"`
	Types         []string `json:"allowed_types"`
	Parent        bool     `json:"parent"`
}

func listMappings(ctx context.Context, q db.DBTX, companyID int64) ([]Mapping, error) {
	rows, err := q.Query(ctx, `
		SELECT m.key, a.id, a.code, a.name, a.name_en FROM account_mappings m JOIN accounts a ON a.id = m.account_id
		WHERE m.company_id = $1`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]Mapping{}
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.Key, &m.AccountID, &m.AccountCode, &m.AccountName, &m.AccountNameEn); err != nil {
			return nil, err
		}
		found[m.Key] = m
	}
	out := make([]Mapping, 0, len(ledger.MappingKeys))
	for _, k := range ledger.MappingKeys {
		m := found[k]
		m.Key = k
		m.Types = mappingRules[k].types
		m.Parent = mappingRules[k].parent
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMappings validates and stores mapping assignments (key → account id).
func SetMappings(ctx context.Context, q db.DBTX, companyID int64, in map[string]int64) error {
	v := apperr.NewValidator()
	for key, accID := range in {
		rule, ok := mappingRules[key]
		if !ok {
			v.Check(false, key, "invalid")
			continue
		}
		var typ string
		var level int
		var hasChildren bool
		err := q.QueryRow(ctx, `
			SELECT type, level, EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)
			FROM accounts a WHERE a.company_id = $1 AND a.id = $2 AND a.is_active`, companyID, accID).Scan(&typ, &level, &hasChildren)
		if db.IsNoRows(err) {
			v.Check(false, key, "invalid")
			continue
		}
		if err != nil {
			return err
		}
		v.Check(slices.Contains(rule.types, typ), key, "wrong_account_type")
		if rule.parent {
			v.Check(level < 4, key, "not_parent")
		} else {
			v.Check(!hasChildren && level >= 2, key, "not_postable")
		}
	}
	if err := v.Err(); err != nil {
		return err
	}
	for key, accID := range in {
		if _, err := q.Exec(ctx, `
			INSERT INTO account_mappings (company_id, key, account_id) VALUES ($1, $2, $3)
			ON CONFLICT (company_id, key) DO UPDATE SET account_id = EXCLUDED.account_id`, companyID, key, accID); err != nil {
			return err
		}
	}
	return nil
}
