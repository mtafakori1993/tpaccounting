package payroll

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

type Allocation struct {
	CostCenterID   int64           `json:"cost_center_id"`
	CostCenterName string          `json:"cost_center_name,omitempty"`
	CostCenterKind string          `json:"cost_center_kind,omitempty"`
	Percent        decimal.Decimal `json:"percent"`
}

type ComponentOverride struct {
	ComponentID int64           `json:"component_id"`
	Rate        decimal.Decimal `json:"rate"`
}

type Employee struct {
	ID                int64               `json:"id"`
	Code              string              `json:"code"`
	FirstName         string              `json:"first_name"`
	LastName          string              `json:"last_name"`
	FullName          string              `json:"full_name"`
	NationalID        string              `json:"national_id"`
	PersonnelNumber   string              `json:"personnel_number"`
	Mobile            string              `json:"mobile"`
	Email             string              `json:"email"`
	Address           string              `json:"address"`
	BankName          string              `json:"bank_name"`
	BankAccountNumber string              `json:"bank_account_number"`
	IBAN              string              `json:"iban"`
	DepartmentID      *int64              `json:"department_id"`
	DepartmentName    *string             `json:"department_name"`
	CostCenterID      *int64              `json:"cost_center_id"`
	CostCenterName    *string             `json:"cost_center_name"`
	Position          string              `json:"position"`
	EmploymentType    string              `json:"employment_type"`
	HireDate          civil.Date          `json:"hire_date"`
	TerminationDate   *civil.Date         `json:"termination_date"`
	BaseSalary        decimal.Decimal     `json:"base_salary"`
	ChildrenCount     int                 `json:"children_count"`
	MaritalStatus     string              `json:"marital_status"`
	InsuranceNumber   string              `json:"insurance_number"`
	InsuranceExempt   bool                `json:"insurance_exempt"`
	TaxExempt         bool                `json:"tax_exempt"`
	IsActive          bool                `json:"is_active"`
	Notes             string              `json:"notes"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	Allocations       []Allocation        `json:"allocations"`
	Components        []ComponentOverride `json:"components"`
}

const empCols = `e.id, e.code, e.first_name, e.last_name, e.national_id, e.personnel_number, e.mobile, e.email, e.address, e.bank_name,
	e.bank_account_number, e.iban, e.department_id, d.name, e.cost_center_id, cc.name, e.position, e.employment_type, e.hire_date,
	e.termination_date, e.base_salary, e.children_count, e.marital_status, e.insurance_number, e.insurance_exempt, e.tax_exempt,
	e.is_active, e.notes, e.created_at, e.updated_at`

const empJoins = ` FROM employees e LEFT JOIN cost_centers d ON d.id = e.department_id LEFT JOIN cost_centers cc ON cc.id = e.cost_center_id`

type rowScanner interface{ Scan(...any) error }

func scanEmployee(s rowScanner, x *Employee) error {
	err := s.Scan(&x.ID, &x.Code, &x.FirstName, &x.LastName, &x.NationalID, &x.PersonnelNumber, &x.Mobile, &x.Email, &x.Address, &x.BankName,
		&x.BankAccountNumber, &x.IBAN, &x.DepartmentID, &x.DepartmentName, &x.CostCenterID, &x.CostCenterName, &x.Position, &x.EmploymentType,
		&x.HireDate, &x.TerminationDate, &x.BaseSalary, &x.ChildrenCount, &x.MaritalStatus, &x.InsuranceNumber, &x.InsuranceExempt,
		&x.TaxExempt, &x.IsActive, &x.Notes, &x.CreatedAt, &x.UpdatedAt)
	x.FullName = strings.TrimSpace(x.FirstName + " " + x.LastName)
	return err
}

func GetEmployee(ctx context.Context, q db.DBTX, companyID, id int64) (*Employee, error) {
	var x Employee
	err := scanEmployee(q.QueryRow(ctx, `SELECT `+empCols+empJoins+` WHERE e.company_id = $1 AND e.id = $2`, companyID, id), &x)
	if db.IsNoRows(err) {
		return nil, apperr.NotFound("employee")
	}
	if err != nil {
		return nil, err
	}
	if x.Allocations, err = loadAllocations(ctx, q, id); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT component_id, rate FROM employee_components WHERE employee_id = $1 ORDER BY component_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	x.Components = []ComponentOverride{}
	for rows.Next() {
		var c ComponentOverride
		if err := rows.Scan(&c.ComponentID, &c.Rate); err != nil {
			return nil, err
		}
		x.Components = append(x.Components, c)
	}
	return &x, rows.Err()
}

func loadAllocations(ctx context.Context, q db.DBTX, employeeID int64) ([]Allocation, error) {
	rows, err := q.Query(ctx, `SELECT a.cost_center_id, c.name, c.kind, a.percent FROM employee_allocations a JOIN cost_centers c ON c.id = a.cost_center_id
		WHERE a.employee_id = $1 ORDER BY a.percent DESC, a.cost_center_id`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Allocation{}
	for rows.Next() {
		var a Allocation
		if err := rows.Scan(&a.CostCenterID, &a.CostCenterName, &a.CostCenterKind, &a.Percent); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type EmployeeFilter struct {
	DepartmentID int64
	Active       *bool
}

func ListEmployees(ctx context.Context, q db.DBTX, companyID int64, ef EmployeeFilter, lp httpx.ListParams) (httpx.Page[Employee], error) {
	f := db.NewFilter("e.company_id = $1", companyID)
	if ef.DepartmentID > 0 {
		f.Add("e.department_id = $N", ef.DepartmentID)
	}
	if ef.Active != nil {
		f.Add("e.is_active = $N", *ef.Active)
	}
	if lp.Q != "" {
		f.Add("(e.code ILIKE $N OR e.first_name ILIKE $N OR e.last_name ILIKE $N OR e.national_id ILIKE $N OR e.personnel_number ILIKE $N OR e.position ILIKE $N)", httpx.Like(lp.Q))
	}
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM employees e WHERE `+f.Where(), f.Args...).Scan(&total); err != nil {
		return httpx.Page[Employee]{}, err
	}
	order := lp.OrderBy(map[string]string{"code": "e.code", "name": "e.last_name", "hire_date": "e.hire_date", "base_salary": "e.base_salary", "department": "d.name"}, "e.code")
	rows, err := q.Query(ctx, `SELECT `+empCols+empJoins+` WHERE `+f.Where()+` ORDER BY `+order+`, e.id`+f.Paging(lp.PageSize, lp.Offset()), f.Args...)
	if err != nil {
		return httpx.Page[Employee]{}, err
	}
	defer rows.Close()
	var items []Employee
	for rows.Next() {
		var x Employee
		if err := scanEmployee(rows, &x); err != nil {
			return httpx.Page[Employee]{}, err
		}
		items = append(items, x)
	}
	return httpx.NewPage(items, total, lp), rows.Err()
}

var (
	ibanRe   = regexp.MustCompile(`^IR[0-9]{24}$`)
	digitsRe = regexp.MustCompile(`^[0-9]*$`)
)

func asciiDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '۰' && r <= '۹':
			r = '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			r = '0' + (r - '٠')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ValidNationalID checks an Iranian national code (10 digits with check digit).
func ValidNationalID(s string) bool {
	if len(s) != 10 || !digitsRe.MatchString(s) || strings.Count(s, s[:1]) == 10 {
		return false
	}
	sum := 0
	for i := 0; i < 9; i++ {
		sum += int(s[i]-'0') * (10 - i)
	}
	r := sum % 11
	c := int(s[9] - '0')
	return (r < 2 && c == r) || (r >= 2 && c == 11-r)
}

func normaliseEmployee(in *Employee) {
	for _, s := range []*string{&in.Code, &in.FirstName, &in.LastName, &in.NationalID, &in.PersonnelNumber, &in.Mobile, &in.Email, &in.Address,
		&in.BankName, &in.BankAccountNumber, &in.IBAN, &in.Position, &in.InsuranceNumber, &in.Notes} {
		*s = strings.TrimSpace(*s)
	}
	in.NationalID = asciiDigits(in.NationalID)
	in.Mobile = asciiDigits(in.Mobile)
	in.IBAN = strings.ToUpper(strings.ReplaceAll(asciiDigits(in.IBAN), " ", ""))
	if in.IBAN != "" && !strings.HasPrefix(in.IBAN, "IR") {
		in.IBAN = "IR" + in.IBAN
	}
	if in.EmploymentType == "" {
		in.EmploymentType = "full_time"
	}
	if in.MaritalStatus == "" {
		in.MaritalStatus = "single"
	}
}

func validateEmployee(ctx context.Context, q db.DBTX, companyID int64, in *Employee) error {
	normaliseEmployee(in)
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	v := apperr.NewValidator()
	v.Required("code", in.Code)
	v.MaxLen("code", in.Code, 30)
	v.Required("first_name", in.FirstName)
	v.Required("last_name", in.LastName)
	v.MaxLen("first_name", in.FirstName, 100)
	v.MaxLen("last_name", in.LastName, 100)
	v.Check(in.NationalID == "" || ValidNationalID(in.NationalID), "national_id", "invalid_national_id")
	v.Check(in.IBAN == "" || ibanRe.MatchString(in.IBAN), "iban", "invalid_iban")
	v.Check(in.Email == "" || strings.Contains(in.Email, "@"), "email", "invalid_email")
	v.Check(map[string]bool{"full_time": true, "part_time": true, "contract": true, "hourly": true, "intern": true}[in.EmploymentType], "employment_type", "invalid")
	v.Check(in.MaritalStatus == "single" || in.MaritalStatus == "married", "marital_status", "invalid")
	v.Check(!in.HireDate.IsZero(), "hire_date", "required")
	v.Check(in.TerminationDate == nil || in.TerminationDate.IsZero() || !in.TerminationDate.Before(in.HireDate), "termination_date", "end_before_start")
	v.Check(!in.BaseSalary.IsNegative(), "base_salary", "negative")
	v.Check(in.BaseSalary.Equal(in.BaseSalary.Round(places)), "base_salary", "too_many_decimals")
	v.Check(in.ChildrenCount >= 0 && in.ChildrenCount <= 20, "children_count", "invalid")
	v.MaxLen("notes", in.Notes, 2000)
	if in.TerminationDate != nil && in.TerminationDate.IsZero() {
		in.TerminationDate = nil
	}
	sum := decimal.Zero
	seen := map[int64]bool{}
	for i, a := range in.Allocations {
		f := fmt.Sprintf("allocations.%d.", i)
		v.Check(a.CostCenterID > 0 && !seen[a.CostCenterID], f+"cost_center_id", "invalid")
		v.Check(a.Percent.IsPositive() && a.Percent.LessThanOrEqual(hundred), f+"percent", "invalid_rate")
		seen[a.CostCenterID] = true
		sum = sum.Add(a.Percent)
	}
	v.Check(len(in.Allocations) == 0 || sum.Equal(hundred), "allocations", "allocation_total")
	for i, c := range in.Components {
		v.Check(!c.Rate.IsNegative(), fmt.Sprintf("components.%d.rate", i), "negative")
	}
	if err := v.Err(); err != nil {
		return err
	}
	ccIDs := []int64{}
	for _, id := range []*int64{in.DepartmentID, in.CostCenterID} {
		if id != nil {
			ccIDs = append(ccIDs, *id)
		}
	}
	for _, a := range in.Allocations {
		ccIDs = append(ccIDs, a.CostCenterID)
	}
	if len(ccIDs) > 0 {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(DISTINCT id) FROM cost_centers WHERE company_id = $1 AND id = ANY($2)`, companyID, ccIDs).Scan(&n); err != nil {
			return err
		}
		distinct := map[int64]bool{}
		for _, id := range ccIDs {
			distinct[id] = true
		}
		v.Check(n == len(distinct), "cost_center_id", "invalid")
	}
	for i, c := range in.Components {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM salary_components WHERE company_id = $1 AND id = $2)`, companyID, c.ComponentID).Scan(&ok); err != nil {
			return err
		}
		v.Check(ok, fmt.Sprintf("components.%d.component_id", i), "invalid")
	}
	return v.Err()
}

func saveChildren(ctx context.Context, q db.DBTX, id int64, in Employee) error {
	if _, err := q.Exec(ctx, `DELETE FROM employee_allocations WHERE employee_id = $1`, id); err != nil {
		return err
	}
	for _, a := range in.Allocations {
		if _, err := q.Exec(ctx, `INSERT INTO employee_allocations (employee_id, cost_center_id, percent) VALUES ($1, $2, $3)`, id, a.CostCenterID, a.Percent); err != nil {
			return err
		}
	}
	if _, err := q.Exec(ctx, `DELETE FROM employee_components WHERE employee_id = $1`, id); err != nil {
		return err
	}
	for _, c := range in.Components {
		if _, err := q.Exec(ctx, `INSERT INTO employee_components (employee_id, component_id, rate) VALUES ($1, $2, $3)
			ON CONFLICT (employee_id, component_id) DO UPDATE SET rate = EXCLUDED.rate`, id, c.ComponentID, c.Rate); err != nil {
			return err
		}
	}
	return nil
}

func CreateEmployee(ctx context.Context, q db.DBTX, companyID, userID int64, in Employee) (int64, error) {
	if err := validateEmployee(ctx, q, companyID, &in); err != nil {
		return 0, err
	}
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO employees (company_id, code, first_name, last_name, national_id, personnel_number, mobile, email, address, bank_name,
		                       bank_account_number, iban, department_id, cost_center_id, position, employment_type, hire_date, termination_date,
		                       base_salary, children_count, marital_status, insurance_number, insurance_exempt, tax_exempt, notes, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $26)
		RETURNING id`,
		companyID, in.Code, in.FirstName, in.LastName, in.NationalID, in.PersonnelNumber, in.Mobile, in.Email, in.Address, in.BankName,
		in.BankAccountNumber, in.IBAN, in.DepartmentID, in.CostCenterID, in.Position, in.EmploymentType, in.HireDate, in.TerminationDate,
		in.BaseSalary, in.ChildrenCount, in.MaritalStatus, in.InsuranceNumber, in.InsuranceExempt, in.TaxExempt, in.Notes, userID).Scan(&id); err != nil {
		return 0, err
	}
	return id, saveChildren(ctx, q, id, in)
}

func UpdateEmployee(ctx context.Context, q db.DBTX, companyID, userID, id int64, in Employee) error {
	if err := validateEmployee(ctx, q, companyID, &in); err != nil {
		return err
	}
	tag, err := q.Exec(ctx, `
		UPDATE employees SET code = $3, first_name = $4, last_name = $5, national_id = $6, personnel_number = $7, mobile = $8, email = $9,
		       address = $10, bank_name = $11, bank_account_number = $12, iban = $13, department_id = $14, cost_center_id = $15, position = $16,
		       employment_type = $17, hire_date = $18, termination_date = $19, base_salary = $20, children_count = $21, marital_status = $22,
		       insurance_number = $23, insurance_exempt = $24, tax_exempt = $25, is_active = $26, notes = $27, updated_by = $28, updated_at = now()
		WHERE company_id = $1 AND id = $2`,
		companyID, id, in.Code, in.FirstName, in.LastName, in.NationalID, in.PersonnelNumber, in.Mobile, in.Email, in.Address, in.BankName,
		in.BankAccountNumber, in.IBAN, in.DepartmentID, in.CostCenterID, in.Position, in.EmploymentType, in.HireDate, in.TerminationDate,
		in.BaseSalary, in.ChildrenCount, in.MaritalStatus, in.InsuranceNumber, in.InsuranceExempt, in.TaxExempt, in.IsActive, in.Notes, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apperr.NotFound("employee")
	}
	return saveChildren(ctx, q, id, in)
}

// ---- Components ----

const compCols = `id, code, name, name_en, kind, category, method, rate, quantity_field, brackets, exemption, ceiling, taxable, insurable,
	account_id, seq, is_active, variability, formula, insurance_share, effective_from, effective_to`

func scanComponent(s rowScanner, c *Component) error {
	var brackets []byte
	if err := s.Scan(&c.ID, &c.Code, &c.Name, &c.NameEn, &c.Kind, &c.Category, &c.Method, &c.Rate, &c.QuantityField, &brackets, &c.Exemption,
		&c.Ceiling, &c.Taxable, &c.Insurable, &c.AccountID, &c.Seq, &c.IsActive, &c.Variability, &c.Formula, &c.InsuranceShare,
		&c.EffectiveFrom, &c.EffectiveTo); err != nil {
		return err
	}
	c.Brackets = []Bracket{}
	return json.Unmarshal(brackets, &c.Brackets)
}

func ListComponents(ctx context.Context, q db.DBTX, companyID int64, activeOnly bool) ([]Component, error) {
	sql := `SELECT ` + compCols + ` FROM salary_components WHERE company_id = $1`
	if activeOnly {
		sql += ` AND is_active`
	}
	rows, err := q.Query(ctx, sql+` ORDER BY kind = 'employer', kind = 'deduction', seq, id`, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Component{}
	for rows.Next() {
		var c Component
		if err := scanComponent(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var (
	codeRe     = regexp.MustCompile(`^[a-z0-9_]+$`)
	methods    = map[string]bool{"fixed": true, "base_percent": true, "daily_base": true, "hourly_base": true, "per_unit": true, "per_child": true, "percent_of_insurable": true, "percent_of_gross": true, "tax_brackets": true, "manual": true, "formula": true, "tax_table": true, "insurance_rule": true}
	categories = map[string]bool{"salary": true, "allowance": true, "overtime": true, "bonus": true, "insurance": true, "tax": true, "loan": true, "advance": true, "unpaid_leave": true, "other": true}
	qtyFields  = map[string]bool{"": true, "working_days": true, "working_hours": true, "absence_days": true, "unpaid_leave_days": true, "paid_leave_days": true, "overtime_hours": true, "mission_days": true, "holiday_work_hours": true}
)

func validateComponent(ctx context.Context, q db.DBTX, companyID int64, c *Component) error {
	c.Code, c.Name, c.NameEn = strings.ToLower(strings.TrimSpace(c.Code)), strings.TrimSpace(c.Name), strings.TrimSpace(c.NameEn)
	if c.Category == "" {
		c.Category = "other"
	}
	if c.Kind != KindEarning {
		c.Taxable, c.Insurable = false, false
	}
	if c.Brackets == nil {
		c.Brackets = []Bracket{}
	}
	c.Formula = strings.TrimSpace(c.Formula)
	if c.Variability == "" {
		c.Variability = "fixed"
	}
	if c.Method != "formula" {
		c.Formula = ""
	}
	if c.Method != "insurance_rule" {
		c.InsuranceShare = ""
	}
	if c.EffectiveFrom != nil && c.EffectiveFrom.IsZero() {
		c.EffectiveFrom = nil
	}
	if c.EffectiveTo != nil && c.EffectiveTo.IsZero() {
		c.EffectiveTo = nil
	}
	v := apperr.NewValidator()
	v.Check(c.Variability == "fixed" || c.Variability == "variable", "variability", "invalid")
	v.Check(c.EffectiveTo == nil || c.EffectiveFrom == nil || !c.EffectiveTo.Before(*c.EffectiveFrom), "effective_to", "end_before_start")
	if c.Method == "formula" {
		if err := CheckFormula(c.Formula); err != nil {
			v.Check(false, "formula", "invalid_formula")
		}
	}
	v.Check(c.Method != "insurance_rule" || c.InsuranceShare == "employee" || c.InsuranceShare == "employer" || c.InsuranceShare == "unemployment",
		"insurance_share", "required")
	v.Check(c.Method != "tax_table" || c.Kind == KindDeduction, "method", "invalid")
	v.Check(c.Method != "insurance_rule" || (c.Kind == KindDeduction && c.InsuranceShare == "employee") || (c.Kind == KindEmployer && c.InsuranceShare != "employee"),
		"insurance_share", "invalid")
	v.Check(codeRe.MatchString(c.Code), "code", "invalid")
	v.Required("name", c.Name)
	v.Check(c.Kind == KindEarning || c.Kind == KindDeduction || c.Kind == KindEmployer, "kind", "invalid")
	v.Check(categories[c.Category], "category", "invalid")
	v.Check(methods[c.Method], "method", "invalid")
	v.Check(qtyFields[c.QuantityField], "quantity_field", "invalid")
	v.Check(!c.Rate.IsNegative(), "rate", "negative")
	v.Check(!c.Exemption.IsNegative(), "exemption", "negative")
	v.Check(!c.Ceiling.IsNegative(), "ceiling", "negative")
	needsQty := c.Method == "daily_base" || c.Method == "hourly_base" || c.Method == "per_unit"
	v.Check(!needsQty || c.QuantityField != "", "quantity_field", "required")
	v.Check(c.Method != "tax_brackets" || len(c.Brackets) > 0, "brackets", "required")
	v.Check(c.Kind == KindEarning || (c.Method != "base_percent" && c.Method != "per_child") || c.Category == "unpaid_leave", "method", "invalid")
	v.Check(c.Kind != KindEarning || (c.Method != "percent_of_insurable" && c.Method != "tax_brackets" && c.Method != "percent_of_gross" &&
		c.Method != "tax_table" && c.Method != "insurance_rule"), "method", "invalid")
	validateBrackets(v, c.Brackets)
	if err := v.Err(); err != nil {
		return err
	}
	if c.AccountID != nil {
		var typ string
		var postable bool
		err := q.QueryRow(ctx, `SELECT type, level >= 2 AND NOT EXISTS (SELECT 1 FROM accounts x WHERE x.parent_id = a.id) FROM accounts a
			WHERE a.company_id = $1 AND a.id = $2 AND a.is_active`, companyID, *c.AccountID).Scan(&typ, &postable)
		if db.IsNoRows(err) {
			v.Check(false, "account_id", "invalid")
			return v.Err()
		}
		if err != nil {
			return err
		}
		v.Check(postable, "account_id", "not_postable")
		if c.Kind == KindDeduction && c.Category != "unpaid_leave" {
			v.Check(typ == "asset" || typ == "liability", "account_id", "wrong_account_type")
		} else {
			v.Check(typ == "expense" || typ == "cost_of_sales", "account_id", "wrong_account_type")
		}
	}
	return v.Err()
}

func SaveComponent(ctx context.Context, q db.DBTX, companyID int64, id int64, c Component) (int64, error) {
	if err := validateComponent(ctx, q, companyID, &c); err != nil {
		return 0, err
	}
	brackets, _ := json.Marshal(c.Brackets)
	if id == 0 {
		err := q.QueryRow(ctx, `
			INSERT INTO salary_components (company_id, code, name, name_en, kind, category, method, rate, quantity_field, brackets, exemption,
			                               ceiling, taxable, insurable, account_id, seq, is_active, variability, formula, insurance_share, effective_from, effective_to)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, TRUE, $17, $18, $19, $20, $21) RETURNING id`,
			companyID, c.Code, c.Name, c.NameEn, c.Kind, c.Category, c.Method, c.Rate, c.QuantityField, brackets, c.Exemption, c.Ceiling,
			c.Taxable, c.Insurable, c.AccountID, c.Seq, c.Variability, c.Formula, c.InsuranceShare, c.EffectiveFrom, c.EffectiveTo).Scan(&id)
		return id, err
	}
	tag, err := q.Exec(ctx, `
		UPDATE salary_components SET code = $3, name = $4, name_en = $5, kind = $6, category = $7, method = $8, rate = $9, quantity_field = $10,
		       brackets = $11, exemption = $12, ceiling = $13, taxable = $14, insurable = $15, account_id = $16, seq = $17, is_active = $18,
		       variability = $19, formula = $20, insurance_share = $21, effective_from = $22, effective_to = $23
		WHERE company_id = $1 AND id = $2`, companyID, id, c.Code, c.Name, c.NameEn, c.Kind, c.Category, c.Method, c.Rate, c.QuantityField,
		brackets, c.Exemption, c.Ceiling, c.Taxable, c.Insurable, c.AccountID, c.Seq, c.IsActive, c.Variability, c.Formula, c.InsuranceShare,
		c.EffectiveFrom, c.EffectiveTo)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, apperr.NotFound("salary_component")
	}
	return id, nil
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func decp(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

// DefaultComponents is a starting salary structure based on common Iranian
// practice. Amounts are examples and every value is editable; statutory rates
// (income tax, social security) are not held here but in the dated tax tables
// and insurance rules (rules.go).
func DefaultComponents() []Component {
	e := func(seq int, code, name, nameEn, cat, method, rate, qty string, taxable, insurable bool) Component {
		c := Component{Seq: seq, Code: code, Name: name, NameEn: nameEn, Kind: KindEarning, Category: cat, Method: method, Rate: dec(rate),
			QuantityField: qty, Taxable: taxable, Insurable: insurable, Variability: "fixed"}
		if method == "manual" || method == "hourly_base" || method == "daily_base" {
			c.Variability = "variable"
		}
		return c
	}
	d := func(seq int, code, name, nameEn, cat, method, rate, qty string) Component {
		v := "variable"
		if method == "tax_table" || method == "insurance_rule" {
			v = "fixed"
		}
		return Component{Seq: seq, Code: code, Name: name, NameEn: nameEn, Kind: KindDeduction, Category: cat, Method: method, Rate: dec(rate),
			QuantityField: qty, Variability: v}
	}
	insurance := d(220, "insurance_employee", "حق بیمه سهم کارمند", "Employee social security", "insurance", "insurance_rule", "0", "")
	insurance.InsuranceShare = "employee"
	employer := func(seq int, code, name, nameEn, share string) Component {
		return Component{Seq: seq, Code: code, Name: name, NameEn: nameEn, Kind: KindEmployer, Category: "insurance", Method: "insurance_rule",
			InsuranceShare: share, Variability: "fixed"}
	}
	return []Component{
		e(10, "base_salary", "حقوق پایه", "Base salary", "salary", "base_percent", "100", "working_days", true, true),
		e(20, "housing", "حق مسکن", "Housing allowance", "allowance", "fixed", "9000000", "", true, true),
		e(30, "food", "بن خواربار", "Food allowance", "allowance", "fixed", "22000000", "", true, true),
		e(40, "child", "حق اولاد", "Child allowance", "allowance", "per_child", "7166184", "", true, false),
		e(50, "seniority", "پایه سنوات", "Seniority", "allowance", "fixed", "0", "", true, true),
		e(60, "overtime", "اضافه‌کار", "Overtime", "overtime", "hourly_base", "140", "overtime_hours", true, true),
		e(70, "holiday_work", "تعطیل‌کاری", "Holiday work", "overtime", "hourly_base", "140", "holiday_work_hours", true, true),
		e(80, "mission", "فوق‌العاده مأموریت", "Mission allowance", "allowance", "daily_base", "100", "mission_days", false, false),
		e(90, "bonus", "پاداش", "Bonus", "bonus", "manual", "0", "", true, false),
		e(100, "commission", "پورسانت", "Commission", "bonus", "manual", "0", "", true, true),
		e(110, "other_earnings", "سایر مزایا", "Other earnings", "other", "manual", "0", "", true, true),
		d(200, "unpaid_leave", "کسر مرخصی بدون حقوق", "Unpaid leave", "unpaid_leave", "daily_base", "100", "unpaid_leave_days"),
		d(205, "absence", "کسر غیبت", "Absence", "unpaid_leave", "daily_base", "100", "absence_days"),
		insurance,
		d(230, "income_tax", "مالیات حقوق", "Income tax", "tax", "tax_table", "0", ""),
		d(240, "loan", "قسط وام", "Loan installment", "loan", "manual", "0", ""),
		d(250, "advance", "مساعده", "Salary advance", "advance", "manual", "0", ""),
		d(260, "other_deductions", "سایر کسور", "Other deductions", "other", "manual", "0", ""),
		employer(300, "insurance_employer", "حق بیمه سهم کارفرما", "Employer social security", "employer"),
		employer(310, "unemployment", "بیمه بیکاری", "Unemployment insurance", "unemployment"),
	}
}

// InstallDefaults adds the default components when a company has none.
func InstallDefaults(ctx context.Context, q db.DBTX, companyID int64) (int, error) {
	var n int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM salary_components WHERE company_id = $1`, companyID).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	for _, c := range DefaultComponents() {
		if _, err := SaveComponent(ctx, q, companyID, 0, c); err != nil {
			return 0, fmt.Errorf("component %s: %w", c.Code, err)
		}
		n++
	}
	return n, nil
}
