// Package seed provides the default chart of accounts and master data that a
// new company starts with. Everything here can be changed afterwards.
package seed

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/accounts"
	"github.com/tafakorpardazan/tpaccounting/internal/budgets"
	"github.com/tafakorpardazan/tpaccounting/internal/company"
	"github.com/tafakorpardazan/tpaccounting/internal/costcenters"
	"github.com/tafakorpardazan/tpaccounting/internal/expenses"
	"github.com/tafakorpardazan/tpaccounting/internal/inventory"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/payroll"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

type acc struct {
	code, parent, name, nameEn, typ string
}

// Chart is a compact, bilingual chart based on common Iranian practice:
// group (1 digit) → general (2 digits) → subsidiary (4 digits).
var Chart = []acc{
	{"1", "", "دارایی‌های جاری", "Current assets", "asset"},
	{"11", "1", "موجودی نقد", "Cash and cash equivalents", "asset"},
	{"1101", "11", "صندوق", "Cash on hand", "asset"},
	{"1102", "11", "بانک‌ها", "Bank accounts", "asset"},
	{"12", "1", "حساب‌ها و اسناد دریافتنی", "Receivables", "asset"},
	{"1201", "12", "حساب‌های دریافتنی تجاری", "Trade receivables", "asset"},
	{"1202", "12", "اسناد دریافتنی", "Notes receivable", "asset"},
	{"1203", "12", "سایر حساب‌های دریافتنی", "Other receivables", "asset"},
	{"1204", "12", "اسناد در جریان وصول", "Checks in collection", "asset"},
	{"1205", "12", "مساعده و وام کارکنان", "Employee advances and loans", "asset"},
	{"13", "1", "موجودی کالا", "Inventories", "asset"},
	{"1301", "13", "موجودی کالا", "Merchandise inventory", "asset"},
	{"14", "1", "پیش‌پرداخت‌ها", "Prepayments", "asset"},
	{"1401", "14", "پیش‌پرداخت هزینه‌ها", "Prepaid expenses", "asset"},
	{"1402", "14", "مالیات بر ارزش افزوده خرید", "Input VAT", "asset"},
	{"2", "", "دارایی‌های غیرجاری", "Non-current assets", "asset"},
	{"21", "2", "دارایی‌های ثابت مشهود", "Property and equipment", "asset"},
	{"2101", "21", "ساختمان", "Buildings", "asset"},
	{"2102", "21", "اثاثه و منصوبات", "Furniture and fixtures", "asset"},
	{"2103", "21", "تجهیزات رایانه‌ای", "Computer equipment", "asset"},
	{"2104", "21", "وسایط نقلیه", "Vehicles", "asset"},
	{"22", "2", "استهلاک انباشته", "Accumulated depreciation", "asset"},
	{"2201", "22", "استهلاک انباشته دارایی‌های ثابت", "Accumulated depreciation of fixed assets", "asset"},
	{"3", "", "بدهی‌های جاری", "Current liabilities", "liability"},
	{"31", "3", "حساب‌ها و اسناد پرداختنی", "Payables", "liability"},
	{"3101", "31", "حساب‌های پرداختنی تجاری", "Trade payables", "liability"},
	{"3102", "31", "اسناد پرداختنی", "Notes payable", "liability"},
	{"3103", "31", "سایر حساب‌های پرداختنی", "Other payables", "liability"},
	{"32", "3", "مالیات‌های پرداختنی", "Taxes payable", "liability"},
	{"3201", "32", "مالیات بر ارزش افزوده فروش", "Output VAT", "liability"},
	{"3202", "32", "مالیات عملکرد پرداختنی", "Income tax payable", "liability"},
	{"3203", "32", "مالیات حقوق پرداختنی", "Payroll tax payable", "liability"},
	{"33", "3", "حقوق و مزایای پرداختنی", "Payroll liabilities", "liability"},
	{"3301", "33", "حقوق و دستمزد پرداختنی", "Salaries payable", "liability"},
	{"3302", "33", "بیمه پرداختنی", "Social security payable", "liability"},
	{"34", "3", "پیش‌دریافت‌ها", "Advances received", "liability"},
	{"3401", "34", "پیش‌دریافت از مشتریان", "Customer advances", "liability"},
	{"4", "", "بدهی‌های غیرجاری", "Non-current liabilities", "liability"},
	{"41", "4", "تسهیلات بلندمدت", "Long-term loans", "liability"},
	{"4101", "41", "وام‌های بانکی بلندمدت", "Long-term bank loans", "liability"},
	{"5", "", "حقوق مالکانه", "Equity", "equity"},
	{"51", "5", "سرمایه", "Capital", "equity"},
	{"5101", "51", "سرمایه ثبت‌شده", "Registered capital", "equity"},
	{"52", "5", "اندوخته‌ها", "Reserves", "equity"},
	{"5201", "52", "اندوخته قانونی", "Legal reserve", "equity"},
	{"53", "5", "سود و زیان", "Retained earnings", "equity"},
	{"5301", "53", "سود (زیان) انباشته", "Retained earnings", "equity"},
	{"5302", "53", "تراز افتتاحیه", "Opening balance equity", "equity"},
	{"6", "", "درآمدها", "Revenue", "revenue"},
	{"61", "6", "درآمدهای عملیاتی", "Operating revenue", "revenue"},
	{"6101", "61", "فروش کالا", "Sales of goods", "revenue"},
	{"6102", "61", "درآمد ارائه خدمات", "Service revenue", "revenue"},
	{"6103", "61", "برگشت از فروش", "Sales returns", "revenue"},
	{"6104", "61", "تخفیفات فروش", "Sales discounts", "revenue"},
	{"62", "6", "سایر درآمدها", "Other income", "revenue"},
	{"6201", "62", "سایر درآمدهای غیرعملیاتی", "Other non-operating income", "revenue"},
	{"7", "", "بهای تمام‌شده", "Cost of sales", "cost_of_sales"},
	{"71", "7", "بهای تمام‌شده فروش", "Cost of sales", "cost_of_sales"},
	{"7101", "71", "بهای تمام‌شده کالای فروش‌رفته", "Cost of goods sold", "cost_of_sales"},
	{"7102", "71", "خرید خدمات و کالای غیرانباری", "Purchased services and non-stock items", "cost_of_sales"},
	{"8", "", "هزینه‌ها", "Expenses", "expense"},
	{"81", "8", "هزینه‌های اداری و عمومی", "General and administrative expenses", "expense"},
	{"8101", "81", "حقوق و دستمزد", "Salaries and wages", "expense"},
	{"8102", "81", "اجاره", "Rent", "expense"},
	{"8103", "81", "آب، برق و گاز", "Utilities", "expense"},
	{"8104", "81", "تلفن و اینترنت", "Telephone and internet", "expense"},
	{"8105", "81", "ملزومات اداری", "Office supplies", "expense"},
	{"8106", "81", "استهلاک", "Depreciation", "expense"},
	{"8107", "81", "ایاب و ذهاب", "Transportation", "expense"},
	{"8108", "81", "پذیرایی", "Hospitality", "expense"},
	{"8109", "81", "تعمیر و نگهداری", "Repairs and maintenance", "expense"},
	{"8110", "81", "حق بیمه سهم کارفرما", "Employer social security", "expense"},
	{"8199", "81", "سایر هزینه‌های عمومی", "Other general expenses", "expense"},
	{"82", "8", "هزینه‌های فروش و بازاریابی", "Selling and marketing expenses", "expense"},
	{"8201", "82", "تبلیغات", "Advertising", "expense"},
	{"8202", "82", "پورسانت فروش", "Sales commissions", "expense"},
	{"83", "8", "هزینه‌های مالی", "Finance costs", "expense"},
	{"8301", "83", "کارمزد بانکی", "Bank charges", "expense"},
	{"8302", "83", "هزینه بهره تسهیلات", "Interest expense", "expense"},
	{"84", "8", "سایر هزینه‌ها", "Other expenses", "expense"},
	{"8401", "84", "کسری و اضافی انبار", "Inventory adjustments", "expense"},
}

// Mappings assigns default accounts (by code) to automatic-accounting roles.
var Mappings = map[string]string{
	ledger.MapReceivable: "1201", ledger.MapPayable: "3101", ledger.MapSales: "6101", ledger.MapServiceRevenue: "6102",
	ledger.MapSalesReturns: "6103", ledger.MapSalesDiscount: "6104", ledger.MapInventory: "1301", ledger.MapCOGS: "7101",
	ledger.MapPurchases: "7102", ledger.MapTaxOutput: "3201", ledger.MapTaxInput: "1402", ledger.MapOpeningEquity: "5302",
	ledger.MapRetainedEarnings: "5301", ledger.MapInventoryAdjustment: "8401", ledger.MapCashParent: "1101", ledger.MapBankParent: "1102",
	ledger.MapChecksReceivable: "1202", ledger.MapChecksInCollection: "1204", ledger.MapChecksPayable: "3102",
	ledger.MapPayrollSalaryExpense: "8101", ledger.MapPayrollEmployerExpense: "8110", ledger.MapPayrollSalariesPayable: "3301",
	ledger.MapPayrollInsurancePayable: "3302", ledger.MapPayrollTaxPayable: "3203", ledger.MapPayrollOtherDeductions: "1205",
}

var costCenters = []costcenters.CostCenter{
	{Code: "10", Kind: "department", Name: "توسعه", NameEn: "Development"},
	{Code: "20", Kind: "department", Name: "فروش", NameEn: "Sales"},
	{Code: "30", Kind: "department", Name: "بازاریابی", NameEn: "Marketing"},
	{Code: "40", Kind: "department", Name: "اداری", NameEn: "Administration"},
	{Code: "P01", Kind: "project", Name: "پروژه نمونه", NameEn: "Sample project"},
}

// sampleBudget is a draft operating-expense budget to start from: account
// code, cost center code ("" = all) and annual amount, spread equally.
var sampleBudget = []struct{ account, costCenter, annual string }{
	{"8101", "", "12000000000"},
	{"8102", "40", "2400000000"},
	{"8103", "40", "360000000"},
	{"8201", "30", "1800000000"},
}

var expenseCategories = []struct{ name, nameEn, code string }{
	{"حقوق و دستمزد", "Salaries", "8101"},
	{"اجاره", "Rent", "8102"},
	{"قبوض آب، برق و گاز", "Utilities", "8103"},
	{"تلفن و اینترنت", "Telephone and internet", "8104"},
	{"ملزومات اداری", "Office supplies", "8105"},
	{"ایاب و ذهاب", "Transportation", "8107"},
	{"پذیرایی", "Hospitality", "8108"},
	{"تبلیغات", "Advertising", "8201"},
	{"کارمزد بانکی", "Bank charges", "8301"},
	{"سایر", "Other", "8199"},
}

// Company seeds the default chart, posting rules and master data.
func Company(ctx context.Context, q db.DBTX, companyID int64) error {
	ids := map[string]int64{}
	for _, a := range Chart {
		in := accounts.Input{Code: a.code, Name: a.name, NameEn: a.nameEn, Type: a.typ}
		if a.parent != "" {
			pid, ok := ids[a.parent]
			if !ok {
				return fmt.Errorf("seed: parent %s not found", a.parent)
			}
			in.ParentID = &pid
		}
		id, err := accounts.Create(ctx, q, companyID, in)
		if err != nil {
			return fmt.Errorf("seed account %s: %w", a.code, err)
		}
		ids[a.code] = id
	}
	m := map[string]int64{}
	for key, code := range Mappings {
		m[key] = ids[code]
	}
	if err := company.SetMappings(ctx, q, companyID, m); err != nil {
		return fmt.Errorf("seed mappings: %w", err)
	}
	ccIDs := map[string]int64{}
	for _, c := range costCenters {
		id, err := costcenters.Create(ctx, q, companyID, c)
		if err != nil {
			return err
		}
		ccIDs[c.Code] = id
	}
	for _, c := range expenseCategories {
		if _, err := expenses.CreateCategory(ctx, q, companyID, expenses.Category{Name: c.name, NameEn: c.nameEn, AccountID: ids[c.code]}); err != nil {
			return err
		}
	}
	if _, err := inventory.CreateWarehouse(ctx, q, companyID, inventory.Warehouse{Code: "01", Name: "انبار مرکزی", IsDefault: true}); err != nil {
		return err
	}
	if _, err := payroll.InstallDefaults(ctx, q, companyID); err != nil {
		return fmt.Errorf("seed salary components: %w", err)
	}
	if err := payroll.InstallDefaultRules(ctx, q, companyID, 0); err != nil {
		return fmt.Errorf("seed payroll rules: %w", err)
	}
	var fyID int64
	if err := q.QueryRow(ctx, `SELECT id FROM fiscal_years WHERE company_id = $1 ORDER BY start_date DESC LIMIT 1`, companyID).Scan(&fyID); err != nil {
		if db.IsNoRows(err) {
			return nil
		}
		return err
	}
	in := budgets.Input{FiscalYearID: fyID, Code: "OPEX", Name: "بودجه هزینه‌های عملیاتی", NameEn: "Operating expense budget",
		Description: "نمونه؛ پیش از فعال‌سازی مبالغ را اصلاح کنید."}
	for _, l := range sampleBudget {
		line := budgets.LineInput{AccountID: ids[l.account], AllocationMethod: budgets.MethodEqual, AnnualAmount: decimal.RequireFromString(l.annual)}
		if l.costCenter != "" {
			cc := ccIDs[l.costCenter]
			line.CostCenterID = &cc
		}
		in.Lines = append(in.Lines, line)
	}
	if _, err := budgets.Create(ctx, q, companyID, 0, in); err != nil {
		return fmt.Errorf("seed budget: %w", err)
	}
	return nil
}
