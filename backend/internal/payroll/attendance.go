package payroll

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/jalali"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/sheet"
)

// Attendance import: a CSV or Excel file with one row per employee and day
// is parsed, validated as a whole and previewed; only a file without a single
// error can be imported, and the import is one transaction (never partial).
// Imported days are kept in attendance_records and summed into the period
// inputs (source "import"); manual amounts (bonus, loan…) are untouched.

const maxImportRows = 20000

// Import columns. Each accepts English and Persian header aliases.
var importColumns = []struct {
	key     string
	aliases []string
}{
	{"employee_code", []string{"employee_code", "employeecode", "code", "personnel_code", "کدپرسنلی", "کدکارمند", "کد"}},
	{"date", []string{"date", "day", "تاریخ"}},
	{"working_hours", []string{"working_hours", "workinghours", "hours", "work_hours", "ساعتکار", "ساعاتکار", "کارکرد"}},
	{"overtime_hours", []string{"overtime_hours", "overtime", "اضافهکار", "ساعتاضافهکار"}},
	{"absence", []string{"absence", "absence_days", "absent", "غیبت"}},
	{"leave", []string{"leave", "paid_leave", "leave_days", "مرخصی", "مرخصیاستحقاقی"}},
	{"unpaid_leave", []string{"unpaid_leave", "unpaidleave", "مرخصیبدونحقوق"}},
	{"mission", []string{"mission", "mission_days", "ماموریت", "مأموریت"}},
	{"holiday_work", []string{"holiday_work", "holiday_work_hours", "holidaywork", "تعطیلکاری", "ساعتتعطیلکاری"}},
}

func normHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(asciiDigits(s)))
	return strings.NewReplacer(" ", "", "\u200c", "", "-", "", "ـ", "", "ي", "ی", "ك", "ک", "\ufeff", "").Replace(s)
}

type ImportIssue struct {
	Row      int    `json:"row"` // 1-based line in the file (0 = not tied to a row)
	Column   string `json:"column,omitempty"`
	Code     string `json:"code"`
	Value    string `json:"value,omitempty"`
	Employee string `json:"employee,omitempty"`
}

type ImportEmployee struct {
	EmployeeID   int64  `json:"employee_id"`
	EmployeeCode string `json:"employee_code"`
	EmployeeName string `json:"employee_name"`
	Days         int    `json:"days"`
	Attendance
}

type ImportSummary struct {
	TotalRows    int `json:"total_rows"`
	ValidRows    int `json:"valid_rows"`
	InvalidRows  int `json:"invalid_rows"`
	Warnings     int `json:"warnings"`
	ImportedRows int `json:"imported_rows"`
	Employees    int `json:"employees"`
}

type ImportResult struct {
	FileName  string           `json:"file_name"`
	Format    string           `json:"format"`
	Summary   ImportSummary    `json:"summary"`
	Errors    []ImportIssue    `json:"errors"`
	Warnings  []ImportIssue    `json:"warnings"`
	Employees []ImportEmployee `json:"employees"`
	CanImport bool             `json:"can_import"`
	Imported  bool             `json:"imported"`
	ImportID  *int64           `json:"import_id,omitempty"`
}

type dayRow struct {
	line       int
	employeeID int64
	date       civil.Date
	working    decimal.Decimal
	overtime   decimal.Decimal
	absence    decimal.Decimal
	leave      decimal.Decimal
	unpaid     decimal.Decimal
	mission    decimal.Decimal
	holiday    decimal.Decimal
}

// parseImportDate accepts YYYY/MM/DD or YYYY-MM-DD in the Jalali (year < 1700)
// or Gregorian calendar, with Persian or Latin digits.
func parseImportDate(s string) (civil.Date, bool) {
	s = strings.TrimSpace(asciiDigits(s))
	if len(s) >= 10 && strings.Contains(s, "T") {
		s = s[:10]
	}
	s = strings.NewReplacer("-", "/", ".", "/").Replace(s)
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return civil.Date{}, false
	}
	y, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	d, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || m < 1 || m > 12 || d < 1 {
		return civil.Date{}, false
	}
	if y >= 1300 && y < 1700 {
		if d > jalali.MonthLength(y, m) {
			return civil.Date{}, false
		}
		return civil.Date{Time: jalali.ToGregorian(y, m, d)}, true
	}
	if y < 1900 || y > 2200 {
		return civil.Date{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d {
		return civil.Date{}, false
	}
	return civil.New(y, time.Month(m), d), true
}

var truthy = map[string]string{"yes": "1", "true": "1", "y": "1", "x": "1", "✓": "1", "بله": "1", "دارد": "1", "no": "0", "false": "0", "n": "0", "خیر": "0", "ندارد": "0"}

func parseQty(s string) (decimal.Decimal, bool) {
	s = strings.TrimSpace(asciiDigits(s))
	if s == "" {
		return decimal.Zero, true
	}
	if v, ok := truthy[strings.ToLower(s)]; ok {
		s = v
	}
	s = strings.NewReplacer("٫", ".", "/", ".").Replace(s)
	// HH:MM durations.
	if h, mm, ok := strings.Cut(s, ":"); ok {
		hi, e1 := strconv.Atoi(h)
		mi, e2 := strconv.Atoi(mm)
		if e1 != nil || e2 != nil || mi < 0 || mi >= 60 || hi < 0 {
			return decimal.Zero, false
		}
		return decimal.NewFromInt(int64(hi)).Add(decimal.NewFromInt(int64(mi)).Div(decimal.NewFromInt(60))).Round(2), true
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// ParseAttendance reads, validates and aggregates an attendance file for a
// period. It never writes.
func ParseAttendance(ctx context.Context, q db.DBTX, companyID, periodID int64, fileName string, data []byte) (*ImportResult, []dayRow, error) {
	p, err := GetPeriod(ctx, q, companyID, periodID)
	if err != nil {
		return nil, nil, err
	}
	res := &ImportResult{FileName: fileName, Errors: []ImportIssue{}, Warnings: []ImportIssue{}, Employees: []ImportEmployee{}}
	lower := strings.ToLower(fileName)
	var table [][]string
	switch {
	case strings.HasSuffix(lower, ".xlsx"):
		res.Format = "xlsx"
		table, err = sheet.ReadXLSX(data)
	case strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".txt"):
		res.Format = "csv"
		table, err = sheet.ReadCSV(data)
	default:
		return nil, nil, apperr.Rule("unsupported_file", "upload a CSV or Excel (.xlsx) file")
	}
	if err != nil {
		return nil, nil, apperr.Rule("unreadable_file", "the file could not be read")
	}
	// Header: the first non-empty row.
	hi := -1
	for i, r := range table {
		if strings.TrimSpace(strings.Join(r, "")) != "" {
			hi = i
			break
		}
	}
	if hi < 0 {
		return nil, nil, apperr.Rule("empty_file", "the file has no rows")
	}
	col := map[string]int{}
	for i, h := range table[hi] {
		n := normHeader(h)
		for _, c := range importColumns {
			for _, a := range c.aliases {
				if n == normHeader(a) {
					if _, dup := col[c.key]; !dup {
						col[c.key] = i
					}
				}
			}
		}
	}
	var missing []string
	for _, k := range []string{"employee_code", "date"} {
		if _, ok := col[k]; !ok {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, nil, apperr.Rule("missing_columns", "required columns are missing").WithParams("columns", strings.Join(missing, ", "))
	}
	if _, ok := col["working_hours"]; !ok {
		res.Warnings = append(res.Warnings, ImportIssue{Code: "no_working_hours_column"})
	}
	if len(table)-hi-1 > maxImportRows {
		return nil, nil, apperr.Rule("file_too_large", "too many rows").WithParams("max", fmt.Sprint(maxImportRows))
	}
	s, err := LoadSettings(ctx, q, companyID)
	if err != nil {
		return nil, nil, err
	}
	emps, err := eligibleEmployees(ctx, q, companyID, p, s)
	if err != nil {
		return nil, nil, err
	}
	eligibleByID := map[int64]eligible{}
	for _, e := range emps {
		eligibleByID[e.ID] = e
	}
	// All employees by code, to tell "unknown" from "not employed in this period".
	codes := map[string]Employee{}
	erows, err := q.Query(ctx, `SELECT `+empCols+empJoins+` WHERE e.company_id = $1`, companyID)
	if err != nil {
		return nil, nil, err
	}
	for erows.Next() {
		var e Employee
		if err := scanEmployee(erows, &e); err != nil {
			erows.Close()
			return nil, nil, err
		}
		codes[strings.ToLower(e.Code)] = e
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return nil, nil, err
	}

	cell := func(r []string, key string) (string, bool) {
		i, ok := col[key]
		if !ok || i >= len(r) {
			return "", ok
		}
		return strings.TrimSpace(r[i]), true
	}
	seen := map[string]int{}
	var days []dayRow
	badRows := map[int]bool{}
	for i := hi + 1; i < len(table); i++ {
		r := table[i]
		if strings.TrimSpace(strings.Join(r, "")) == "" {
			continue
		}
		line := i + 1
		res.Summary.TotalRows++
		fail := func(column, code, value string) {
			res.Errors = append(res.Errors, ImportIssue{Row: line, Column: column, Code: code, Value: value})
			badRows[line] = true
		}
		code, _ := cell(r, "employee_code")
		code = asciiDigits(code)
		var d dayRow
		d.line = line
		emp, known := codes[strings.ToLower(code)]
		switch {
		case code == "":
			fail("employee_code", "required", "")
		case !known:
			fail("employee_code", "unknown_employee", code)
		default:
			if _, ok := eligibleByID[emp.ID]; !ok {
				fail("employee_code", "employee_not_in_period", code)
			}
			d.employeeID = emp.ID
		}
		ds, _ := cell(r, "date")
		date, ok := parseImportDate(ds)
		switch {
		case ds == "":
			fail("date", "required", "")
		case !ok:
			fail("date", "invalid_date", ds)
		case date.Before(p.StartDate) || date.After(p.EndDate):
			fail("date", "date_outside_period", ds)
		case known && (date.Before(emp.HireDate) || (emp.TerminationDate != nil && date.After(*emp.TerminationDate))):
			fail("date", "date_outside_employment", ds)
		}
		d.date = date
		num := func(key string, dst *decimal.Decimal, max decimal.Decimal) {
			v, present := cell(r, key)
			if !present || v == "" {
				return
			}
			x, ok := parseQty(v)
			switch {
			case !ok:
				fail(key, "invalid_number", v)
			case x.IsNegative():
				fail(key, "negative", v)
			case x.GreaterThan(max):
				fail(key, "out_of_range", v)
			default:
				*dst = x
			}
		}
		h24, one := decimal.NewFromInt(24), decimal.NewFromInt(1)
		num("working_hours", &d.working, h24)
		num("overtime_hours", &d.overtime, h24)
		num("absence", &d.absence, one)
		num("leave", &d.leave, one)
		num("unpaid_leave", &d.unpaid, one)
		num("mission", &d.mission, one)
		num("holiday_work", &d.holiday, h24)
		if d.working.Add(d.overtime).Add(d.holiday).GreaterThan(h24) {
			fail("working_hours", "hours_exceed_day", "")
		}
		if d.absence.Add(d.leave).Add(d.unpaid).GreaterThan(one) {
			fail("absence", "day_overbooked", "")
		}
		if d.employeeID > 0 && ok {
			k := fmt.Sprintf("%d|%s", d.employeeID, date.String())
			if first, dup := seen[k]; dup {
				fail("date", "duplicate_row", fmt.Sprint(first))
			} else {
				seen[k] = line
			}
		}
		if !badRows[line] {
			if d.absence.IsPositive() && d.working.IsPositive() {
				res.Warnings = append(res.Warnings, ImportIssue{Row: line, Code: "absence_with_hours", Employee: emp.FullName})
			}
			days = append(days, d)
		}
	}
	res.Summary.InvalidRows = len(badRows)
	res.Summary.ValidRows = res.Summary.TotalRows - res.Summary.InvalidRows
	if res.Summary.TotalRows == 0 {
		return nil, nil, apperr.Rule("empty_file", "the file has no data rows")
	}
	// Aggregate per employee.
	agg := map[int64]*ImportEmployee{}
	for _, d := range days {
		a := agg[d.employeeID]
		if a == nil {
			e := eligibleByID[d.employeeID]
			a = &ImportEmployee{EmployeeID: e.ID, EmployeeCode: e.Code, EmployeeName: e.FullName, Attendance: Attendance{WorkingDays: e.defaultDays}}
			agg[d.employeeID] = a
		}
		a.Days++
		a.WorkingHours = a.WorkingHours.Add(d.working)
		a.OvertimeHours = a.OvertimeHours.Add(d.overtime)
		a.AbsenceDays = a.AbsenceDays.Add(d.absence)
		a.PaidLeaveDays = a.PaidLeaveDays.Add(d.leave)
		a.UnpaidLeaveDays = a.UnpaidLeaveDays.Add(d.unpaid)
		a.MissionDays = a.MissionDays.Add(d.mission)
		a.HolidayWorkHours = a.HolidayWorkHours.Add(d.holiday)
	}
	for _, a := range agg {
		res.Employees = append(res.Employees, *a)
	}
	sort.Slice(res.Employees, func(i, j int) bool { return res.Employees[i].EmployeeCode < res.Employees[j].EmployeeCode })
	for _, e := range emps {
		if agg[e.ID] == nil {
			res.Warnings = append(res.Warnings, ImportIssue{Code: "employee_missing", Employee: e.FullName, Value: e.Code})
		}
	}
	res.Summary.Employees = len(res.Employees)
	res.Summary.Warnings = len(res.Warnings)
	editable := p.Status == StatusDraft || p.Status == StatusCalculated
	if !editable {
		res.Errors = append(res.Errors, ImportIssue{Code: "payroll_locked"})
	}
	res.CanImport = len(res.Errors) == 0 && len(days) > 0
	return res, days, nil
}

// ImportAttendance validates the file and, when it has no errors, stores it
// atomically. With commit false it only previews.
func ImportAttendance(ctx context.Context, q db.DBTX, companyID, userID, periodID int64, fileName string, data []byte, commit bool) (*ImportResult, error) {
	if commit {
		// Serialise with other changes to this payroll before validating.
		if _, err := lockPeriod(ctx, q, companyID, periodID); err != nil {
			return nil, err
		}
	}
	res, days, err := ParseAttendance(ctx, q, companyID, periodID, fileName, data)
	if err != nil || !commit {
		return res, err
	}
	if !res.CanImport {
		return res, apperr.Rule("import_has_errors", "the file has errors; nothing was imported").WithParams("errors", fmt.Sprint(len(res.Errors)))
	}
	var importID int64
	if err := q.QueryRow(ctx, `INSERT INTO payroll_attendance_imports (company_id, period_id, file_name, file_format, total_rows, valid_rows, invalid_rows,
		warning_count, imported_rows, employee_count, created_by) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		companyID, periodID, fileName, res.Format, res.Summary.TotalRows, res.Summary.ValidRows, res.Summary.InvalidRows, res.Summary.Warnings,
		len(days), len(res.Employees), userID).Scan(&importID); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(res.Employees))
	for _, e := range res.Employees {
		ids = append(ids, e.EmployeeID)
	}
	// The file replaces earlier attendance of the employees it covers.
	if _, err := q.Exec(ctx, `DELETE FROM attendance_records WHERE period_id = $1 AND employee_id = ANY($2)`, periodID, ids); err != nil {
		return nil, err
	}
	for _, d := range days {
		if _, err := q.Exec(ctx, `INSERT INTO attendance_records (period_id, import_id, employee_id, date, working_hours, overtime_hours, absence_days,
			paid_leave_days, unpaid_leave_days, mission_days, holiday_work_hours) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			periodID, importID, d.employeeID, d.date, d.working, d.overtime, d.absence, d.leave, d.unpaid, d.mission, d.holiday); err != nil {
			return nil, err
		}
	}
	src := fmt.Sprintf("import:%d", importID)
	for _, e := range res.Employees {
		if _, err := q.Exec(ctx, `
			INSERT INTO payroll_inputs (period_id, employee_id, working_days, working_hours, absence_days, unpaid_leave_days, paid_leave_days,
			                            overtime_hours, mission_days, holiday_work_hours, source)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (period_id, employee_id) DO UPDATE SET working_days = EXCLUDED.working_days, working_hours = EXCLUDED.working_hours,
			       absence_days = EXCLUDED.absence_days, unpaid_leave_days = EXCLUDED.unpaid_leave_days, paid_leave_days = EXCLUDED.paid_leave_days,
			       overtime_hours = EXCLUDED.overtime_hours, mission_days = EXCLUDED.mission_days, holiday_work_hours = EXCLUDED.holiday_work_hours,
			       source = EXCLUDED.source`,
			periodID, e.EmployeeID, e.WorkingDays, e.WorkingHours, e.AbsenceDays, e.UnpaidLeaveDays, e.PaidLeaveDays, e.OvertimeHours, e.MissionDays,
			e.HolidayWorkHours, src); err != nil {
			return nil, err
		}
	}
	if _, err := q.Exec(ctx, `UPDATE payroll_periods SET status = 'draft', updated_at = now() WHERE id = $1 AND status = 'calculated'`, periodID); err != nil {
		return nil, err
	}
	res.Imported, res.ImportID = true, &importID
	res.Summary.ImportedRows = len(days)
	return res, nil
}

type ImportLog struct {
	ID           int64     `json:"id"`
	FileName     string    `json:"file_name"`
	Format       string    `json:"file_format"`
	TotalRows    int       `json:"total_rows"`
	ImportedRows int       `json:"imported_rows"`
	Warnings     int       `json:"warning_count"`
	Employees    int       `json:"employee_count"`
	UserName     *string   `json:"user_name"`
	CreatedAt    time.Time `json:"created_at"`
}

func ListImports(ctx context.Context, q db.DBTX, companyID, periodID int64) ([]ImportLog, error) {
	rows, err := q.Query(ctx, `SELECT i.id, i.file_name, i.file_format, i.total_rows, i.imported_rows, i.warning_count, i.employee_count, u.full_name, i.created_at
		FROM payroll_attendance_imports i LEFT JOIN users u ON u.id = i.created_by WHERE i.company_id = $1 AND i.period_id = $2 ORDER BY i.id DESC`, companyID, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImportLog{}
	for rows.Next() {
		var x ImportLog
		if err := rows.Scan(&x.ID, &x.FileName, &x.Format, &x.TotalRows, &x.ImportedRows, &x.Warnings, &x.Employees, &x.UserName, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// AttendanceTemplate is a sample import file (header + example rows).
func AttendanceTemplate(lang string) [][]sheet.Cell {
	h := []string{"employee_code", "date", "working_hours", "overtime_hours", "absence", "leave", "unpaid_leave", "mission", "holiday_work"}
	if lang != "en" {
		h = []string{"کد پرسنلی", "تاریخ", "ساعت کار", "اضافه کار", "غیبت", "مرخصی", "مرخصی بدون حقوق", "ماموریت", "تعطیل کاری"}
	}
	rows := [][]sheet.Cell{{}}
	for _, x := range h {
		rows[0] = append(rows[0], sheet.Bold(x))
	}
	rows = append(rows,
		[]sheet.Cell{sheet.Text("E001"), sheet.Text("1405/07/01"), sheet.Number("8"), sheet.Number("2"), sheet.Number("0"), sheet.Number("0"), sheet.Number("0"), sheet.Number("0"), sheet.Number("0")},
		[]sheet.Cell{sheet.Text("E001"), sheet.Text("1405/07/02"), sheet.Number("0"), sheet.Number("0"), sheet.Number("0"), sheet.Number("1"), sheet.Number("0"), sheet.Number("0"), sheet.Number("0")},
	)
	return rows
}
