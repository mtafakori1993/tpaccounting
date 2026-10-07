package payroll

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// Statutory payroll parameters (income-tax tables and social-security rates)
// are data with an effective period, never constants in code: the rules in
// force on a payroll's period end date are used, and the payroll records which
// rule it used.

const (
	ModeMonthly = "monthly"
	ModeAnnual  = "annual"
)

type TaxTable struct {
	ID            int64       `json:"id"`
	Name          string      `json:"name"`
	EffectiveFrom civil.Date  `json:"effective_from"`
	EffectiveTo   *civil.Date `json:"effective_to"`
	// Mode tells whether the exemption and brackets are monthly or annual amounts.
	// Annual tables are applied to the monthly taxable income × 12 and divided by 12.
	Mode      string          `json:"mode"`
	Exemption decimal.Decimal `json:"exemption"`
	Brackets  []Bracket       `json:"brackets"`
	Notes     string          `json:"notes"`
	IsActive  bool            `json:"is_active"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type InsuranceRule struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	EffectiveFrom    civil.Date      `json:"effective_from"`
	EffectiveTo      *civil.Date     `json:"effective_to"`
	EmployeeRate     decimal.Decimal `json:"employee_rate"`
	EmployerRate     decimal.Decimal `json:"employer_rate"`
	UnemploymentRate decimal.Decimal `json:"unemployment_rate"`
	// Ceiling caps the monthly insurable base (0 = no ceiling).
	Ceiling decimal.Decimal `json:"ceiling"`
	// EligibleCodes lists the earning components that form the insurable base;
	// empty means "every earning component marked insurable".
	EligibleCodes []string  `json:"eligible_codes"`
	Notes         string    `json:"notes"`
	IsActive      bool      `json:"is_active"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Tax computes the income tax on a monthly taxable amount.
func (t *TaxTable) Tax(monthlyTaxable decimal.Decimal) (base, tax decimal.Decimal) {
	twelve := decimal.NewFromInt(12)
	if t.Mode == ModeAnnual {
		annual := monthlyTaxable.Mul(twelve).Sub(t.Exemption)
		if annual.IsNegative() {
			annual = decimal.Zero
		}
		return annual.Div(twelve), ProgressiveTax(annual, t.Brackets).Div(twelve)
	}
	base = monthlyTaxable.Sub(t.Exemption)
	if base.IsNegative() {
		base = decimal.Zero
	}
	return base, ProgressiveTax(base, t.Brackets)
}

// Rate returns the percentage for one insurance share.
func (r *InsuranceRule) Rate(share string) decimal.Decimal {
	switch share {
	case "employee":
		return r.EmployeeRate
	case "employer":
		return r.EmployerRate
	case "unemployment":
		return r.UnemploymentRate
	}
	return decimal.Zero
}

const taxCols = `id, name, effective_from, effective_to, mode, exemption, brackets, notes, is_active, updated_at`

func scanTax(s rowScanner, t *TaxTable) error {
	var raw []byte
	if err := s.Scan(&t.ID, &t.Name, &t.EffectiveFrom, &t.EffectiveTo, &t.Mode, &t.Exemption, &raw, &t.Notes, &t.IsActive, &t.UpdatedAt); err != nil {
		return err
	}
	t.Brackets = []Bracket{}
	return json.Unmarshal(raw, &t.Brackets)
}

func ListTaxTables(ctx context.Context, q db.DBTX, companyID int64) ([]TaxTable, error) {
	rows, err := q.Query(ctx, `SELECT `+taxCols+` FROM payroll_tax_tables WHERE company_id = $1 ORDER BY effective_from DESC, id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaxTable{}
	for rows.Next() {
		var t TaxTable
		if err := scanTax(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TaxTableFor returns the active table in force on date (nil if none).
func TaxTableFor(ctx context.Context, q db.DBTX, companyID int64, date civil.Date) (*TaxTable, error) {
	var t TaxTable
	err := scanTax(q.QueryRow(ctx, `SELECT `+taxCols+` FROM payroll_tax_tables WHERE company_id = $1 AND is_active
		AND effective_from <= $2 AND (effective_to IS NULL OR effective_to >= $2) ORDER BY effective_from DESC, id DESC LIMIT 1`, companyID, date), &t)
	if db.IsNoRows(err) {
		return nil, nil
	}
	return &t, err
}

func validateBrackets(v *apperr.Validator, brackets []Bracket) {
	var prev *decimal.Decimal
	for i, b := range brackets {
		f := fmt.Sprintf("brackets.%d.", i)
		v.Check(!b.Rate.IsNegative() && b.Rate.LessThanOrEqual(hundred), f+"rate", "invalid_rate")
		if b.Upto != nil {
			v.Check(b.Upto.IsPositive() && (prev == nil || b.Upto.GreaterThan(*prev)), f+"upto", "invalid")
			prev = b.Upto
		} else {
			v.Check(i == len(brackets)-1, f+"upto", "invalid")
		}
	}
}

func checkEffective(v *apperr.Validator, from civil.Date, to *civil.Date) *civil.Date {
	v.Check(!from.IsZero(), "effective_from", "required")
	if to != nil && to.IsZero() {
		to = nil
	}
	v.Check(to == nil || !to.Before(from), "effective_to", "end_before_start")
	return to
}

// overlap reports whether another active row of table overlaps [from, to].
func overlap(ctx context.Context, q db.DBTX, table string, companyID, id int64, from civil.Date, to *civil.Date) (bool, error) {
	// Serialise saves per company so two concurrent saves cannot both pass the check.
	if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1), $2::int)`, table, companyID); err != nil {
		return false, err
	}
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE company_id = $1 AND id <> $2 AND is_active
		AND effective_from <= COALESCE($4::date, 'infinity'::date) AND COALESCE(effective_to, 'infinity'::date) >= $3)`,
		companyID, id, from, to).Scan(&exists)
	return exists, err
}

func SaveTaxTable(ctx context.Context, q db.DBTX, companyID, userID, id int64, in TaxTable) (int64, error) {
	in.Name, in.Notes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Notes)
	if in.Mode == "" {
		in.Mode = ModeMonthly
	}
	if in.Brackets == nil {
		in.Brackets = []Bracket{}
	}
	v := apperr.NewValidator()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 150)
	v.MaxLen("notes", in.Notes, 2000)
	v.Check(in.Mode == ModeMonthly || in.Mode == ModeAnnual, "mode", "invalid")
	v.Check(!in.Exemption.IsNegative(), "exemption", "negative")
	v.Check(len(in.Brackets) > 0 && len(in.Brackets) <= 20, "brackets", "required")
	validateBrackets(v, in.Brackets)
	in.EffectiveTo = checkEffective(v, in.EffectiveFrom, in.EffectiveTo)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if in.IsActive || id == 0 {
		clash, err := overlap(ctx, q, "payroll_tax_tables", companyID, id, in.EffectiveFrom, in.EffectiveTo)
		if err != nil {
			return 0, err
		}
		if clash {
			return 0, apperr.Rule("rule_period_overlap", "another active table is in force in this period")
		}
	}
	b, _ := json.Marshal(in.Brackets)
	if id == 0 {
		err := q.QueryRow(ctx, `INSERT INTO payroll_tax_tables (company_id, name, effective_from, effective_to, mode, exemption, brackets, notes, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9::bigint, 0)) RETURNING id`, companyID, in.Name, in.EffectiveFrom, in.EffectiveTo, in.Mode, in.Exemption, b,
			in.Notes, userID).Scan(&id)
		return id, err
	}
	tag, err := q.Exec(ctx, `UPDATE payroll_tax_tables SET name = $3, effective_from = $4, effective_to = $5, mode = $6, exemption = $7, brackets = $8,
		notes = $9, is_active = $10, updated_at = now() WHERE company_id = $1 AND id = $2`, companyID, id, in.Name, in.EffectiveFrom, in.EffectiveTo,
		in.Mode, in.Exemption, b, in.Notes, in.IsActive)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, apperr.NotFound("tax_table")
	}
	return id, nil
}

const insCols = `id, name, effective_from, effective_to, employee_rate, employer_rate, unemployment_rate, ceiling, eligible_codes, notes, is_active, updated_at`

func scanIns(s rowScanner, r *InsuranceRule) error {
	err := s.Scan(&r.ID, &r.Name, &r.EffectiveFrom, &r.EffectiveTo, &r.EmployeeRate, &r.EmployerRate, &r.UnemploymentRate, &r.Ceiling,
		&r.EligibleCodes, &r.Notes, &r.IsActive, &r.UpdatedAt)
	if r.EligibleCodes == nil {
		r.EligibleCodes = []string{}
	}
	return err
}

func ListInsuranceRules(ctx context.Context, q db.DBTX, companyID int64) ([]InsuranceRule, error) {
	rows, err := q.Query(ctx, `SELECT `+insCols+` FROM payroll_insurance_rules WHERE company_id = $1 ORDER BY effective_from DESC, id DESC`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InsuranceRule{}
	for rows.Next() {
		var r InsuranceRule
		if err := scanIns(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InsuranceRuleFor returns the active rule in force on date (nil if none).
func InsuranceRuleFor(ctx context.Context, q db.DBTX, companyID int64, date civil.Date) (*InsuranceRule, error) {
	var r InsuranceRule
	err := scanIns(q.QueryRow(ctx, `SELECT `+insCols+` FROM payroll_insurance_rules WHERE company_id = $1 AND is_active
		AND effective_from <= $2 AND (effective_to IS NULL OR effective_to >= $2) ORDER BY effective_from DESC, id DESC LIMIT 1`, companyID, date), &r)
	if db.IsNoRows(err) {
		return nil, nil
	}
	return &r, err
}

func SaveInsuranceRule(ctx context.Context, q db.DBTX, companyID, userID, id int64, in InsuranceRule) (int64, error) {
	in.Name, in.Notes = strings.TrimSpace(in.Name), strings.TrimSpace(in.Notes)
	if in.EligibleCodes == nil {
		in.EligibleCodes = []string{}
	}
	v := apperr.NewValidator()
	v.Required("name", in.Name)
	v.MaxLen("name", in.Name, 150)
	v.MaxLen("notes", in.Notes, 2000)
	for _, f := range []struct {
		name string
		d    decimal.Decimal
	}{{"employee_rate", in.EmployeeRate}, {"employer_rate", in.EmployerRate}, {"unemployment_rate", in.UnemploymentRate}} {
		v.Check(!f.d.IsNegative() && f.d.LessThanOrEqual(hundred), f.name, "invalid_rate")
	}
	v.Check(!in.Ceiling.IsNegative(), "ceiling", "negative")
	in.EffectiveTo = checkEffective(v, in.EffectiveFrom, in.EffectiveTo)
	if err := v.Err(); err != nil {
		return 0, err
	}
	if len(in.EligibleCodes) > 0 {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(DISTINCT code) FROM salary_components WHERE company_id = $1 AND kind = 'earning' AND code = ANY($2)`,
			companyID, in.EligibleCodes).Scan(&n); err != nil {
			return 0, err
		}
		distinct := map[string]bool{}
		for _, c := range in.EligibleCodes {
			distinct[c] = true
		}
		v.Check(n == len(distinct), "eligible_codes", "invalid")
		if err := v.Err(); err != nil {
			return 0, err
		}
	}
	if in.IsActive || id == 0 {
		clash, err := overlap(ctx, q, "payroll_insurance_rules", companyID, id, in.EffectiveFrom, in.EffectiveTo)
		if err != nil {
			return 0, err
		}
		if clash {
			return 0, apperr.Rule("rule_period_overlap", "another active rule is in force in this period")
		}
	}
	if id == 0 {
		err := q.QueryRow(ctx, `INSERT INTO payroll_insurance_rules (company_id, name, effective_from, effective_to, employee_rate, employer_rate,
			unemployment_rate, ceiling, eligible_codes, notes, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11::bigint, 0)) RETURNING id`,
			companyID, in.Name, in.EffectiveFrom, in.EffectiveTo, in.EmployeeRate, in.EmployerRate, in.UnemploymentRate, in.Ceiling, in.EligibleCodes,
			in.Notes, userID).Scan(&id)
		return id, err
	}
	tag, err := q.Exec(ctx, `UPDATE payroll_insurance_rules SET name = $3, effective_from = $4, effective_to = $5, employee_rate = $6, employer_rate = $7,
		unemployment_rate = $8, ceiling = $9, eligible_codes = $10, notes = $11, is_active = $12, updated_at = now() WHERE company_id = $1 AND id = $2`,
		companyID, id, in.Name, in.EffectiveFrom, in.EffectiveTo, in.EmployeeRate, in.EmployerRate, in.UnemploymentRate, in.Ceiling, in.EligibleCodes,
		in.Notes, in.IsActive)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, apperr.NotFound("insurance_rule")
	}
	return id, nil
}

// DeleteRule removes a tax table or insurance rule that no payroll has used.
func DeleteRule(ctx context.Context, q db.DBTX, companyID int64, table string, id int64) error {
	key := map[string]string{"payroll_tax_tables": "tax_table_id", "payroll_insurance_rules": "insurance_rule_id"}[table]
	var used bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM payroll_periods WHERE company_id = $1 AND (calc_basis->>'`+key+`')::bigint = $2)`, companyID, id).Scan(&used); err != nil {
		return err
	}
	if used {
		return apperr.Conflict("in_use", "a payroll was calculated with this rule; deactivate it or set an end date instead")
	}
	tag, err := q.Exec(ctx, `DELETE FROM `+table+` WHERE company_id = $1 AND id = $2`, companyID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("payroll_rule")
	}
	return nil
}

// DefaultTaxTable and DefaultInsuranceRule are illustrative starting values
// for a new company. They are not official: the administrator must replace
// them with the rates published for the year.
func DefaultTaxTable(from civil.Date) TaxTable {
	return TaxTable{Name: "جدول مالیات حقوق (نمونه — با جدول رسمی سال جایگزین شود)", EffectiveFrom: from, Mode: ModeMonthly,
		Notes: "Illustrative example values. Replace with the official table for the year before running payroll.",
		Brackets: []Bracket{
			{Upto: decp("240000000"), Rate: dec("0")},
			{Upto: decp("300000000"), Rate: dec("10")},
			{Upto: decp("380000000"), Rate: dec("15")},
			{Upto: decp("500000000"), Rate: dec("20")},
			{Upto: nil, Rate: dec("30")},
		}, IsActive: true}
}

func DefaultInsuranceRule(from civil.Date) InsuranceRule {
	return InsuranceRule{Name: "بیمه تأمین اجتماعی (نمونه — نرخ‌ها بررسی شود)", EffectiveFrom: from, EmployeeRate: dec("7"), EmployerRate: dec("20"),
		UnemploymentRate: dec("3"), EligibleCodes: []string{}, IsActive: true,
		Notes: "Illustrative example rates. Verify against the current Social Security regulations and set the insurable ceiling."}
}

// InstallDefaultRules adds an example tax table and insurance rule when the company has none.
func InstallDefaultRules(ctx context.Context, q db.DBTX, companyID, userID int64) error {
	var from civil.Date
	if err := q.QueryRow(ctx, `SELECT COALESCE(min(start_date), CURRENT_DATE) FROM fiscal_years WHERE company_id = $1`, companyID).Scan(&from); err != nil {
		return err
	}
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM payroll_tax_tables WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := SaveTaxTable(ctx, q, companyID, userID, 0, DefaultTaxTable(from)); err != nil {
			return err
		}
	}
	if err := q.QueryRow(ctx, `SELECT count(*) FROM payroll_insurance_rules WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := SaveInsuranceRule(ctx, q, companyID, userID, 0, DefaultInsuranceRule(from)); err != nil {
			return err
		}
	}
	return nil
}
