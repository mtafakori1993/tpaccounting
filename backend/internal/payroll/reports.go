package payroll

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/ledger"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// ReportFilter selects payrolls by period start date. Draft payrolls are never reported.
type ReportFilter struct {
	From, To     civil.Date
	EmployeeID   int64
	DepartmentID int64
	Statuses     []string
}

func (rf ReportFilter) where(f *db.Filter) {
	if len(rf.Statuses) > 0 {
		f.Add("p.status = ANY($N)", rf.Statuses)
	} else {
		f.AddRaw("p.status <> 'draft'")
	}
	if !rf.From.IsZero() {
		f.Add("p.start_date >= $N", rf.From)
	}
	if !rf.To.IsZero() {
		f.Add("p.start_date <= $N", rf.To)
	}
	if rf.EmployeeID > 0 {
		f.Add("s.employee_id = $N", rf.EmployeeID)
	}
	if rf.DepartmentID > 0 {
		f.Add("s.department_id = $N", rf.DepartmentID)
	}
}

// Row is a generic report row: a key, a label and named amounts.
type Row struct {
	ID      *int64                     `json:"id"`
	Key     string                     `json:"key"`
	Label   string                     `json:"label"`
	Extra   map[string]string          `json:"extra,omitempty"`
	Count   int                        `json:"count"`
	Amounts map[string]decimal.Decimal `json:"amounts"`
}

type Report struct {
	Name    string                     `json:"name"`
	Columns []string                   `json:"columns"`
	Rows    []Row                      `json:"rows"`
	Totals  map[string]decimal.Decimal `json:"totals"`
}

func newReport(name string, cols ...string) *Report {
	return &Report{Name: name, Columns: cols, Rows: []Row{}, Totals: map[string]decimal.Decimal{}}
}

func (r *Report) add(row Row) {
	for _, c := range r.Columns {
		r.Totals[c] = r.Totals[c].Add(row.Amounts[c])
	}
	r.Rows = append(r.Rows, row)
}

// Build runs a named payroll report.
func Build(ctx context.Context, q db.DBTX, companyID int64, name string, rf ReportFilter) (*Report, error) {
	f := db.NewFilter("p.company_id = $1", companyID)
	rf.where(f)
	from := ` FROM payslips s JOIN payroll_periods p ON p.id = s.period_id`
	switch name {
	case "summary":
		out := newReport(name, "gross", "deductions", "net", "employer", "total_cost")
		rows, err := q.Query(ctx, `SELECT p.id, p.year, p.month, p.name, p.status, count(*), SUM(s.gross), SUM(s.total_deductions), SUM(s.net), SUM(s.employer_cost)`+
			from+` WHERE `+f.Where()+` GROUP BY p.id ORDER BY p.year, p.month`, f.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var y, m, n int
			var label, status string
			var g, d, nt, e decimal.Decimal
			if err := rows.Scan(&id, &y, &m, &label, &status, &n, &g, &d, &nt, &e); err != nil {
				return nil, err
			}
			out.add(Row{ID: &id, Key: label, Label: label, Count: n, Extra: map[string]string{"status": status},
				Amounts: map[string]decimal.Decimal{"gross": g, "deductions": d, "net": nt, "employer": e, "total_cost": g.Add(e)}})
		}
		return out, rows.Err()
	case "employee":
		out := newReport(name, "gross", "deductions", "net", "employer", "taxable", "insurable")
		group, label, key := "s.employee_id, s.employee_code, s.employee_name", "s.employee_code || ' - ' || s.employee_name", "s.employee_id"
		order := "s.employee_code"
		if rf.EmployeeID > 0 {
			group, label, key, order = "p.id, p.name, p.year, p.month", "p.name", "p.id", "p.year, p.month"
		}
		rows, err := q.Query(ctx, `SELECT `+key+`, `+label+`, count(*), SUM(s.gross), SUM(s.total_deductions), SUM(s.net), SUM(s.employer_cost),
			SUM(s.taxable_income), SUM(s.insurable_income)`+from+` WHERE `+f.Where()+` GROUP BY `+group+` ORDER BY `+order, f.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var l string
			var n int
			var g, d, nt, e, tx, ins decimal.Decimal
			if err := rows.Scan(&id, &l, &n, &g, &d, &nt, &e, &tx, &ins); err != nil {
				return nil, err
			}
			out.add(Row{ID: &id, Key: l, Label: l, Count: n,
				Amounts: map[string]decimal.Decimal{"gross": g, "deductions": d, "net": nt, "employer": e, "taxable": tx, "insurable": ins}})
		}
		return out, rows.Err()
	case "by-department":
		out := newReport(name, "gross", "employer", "total_cost", "net")
		rows, err := q.Query(ctx, `SELECT d.id, COALESCE(d.name, ''), count(DISTINCT s.employee_id), SUM(s.gross), SUM(s.employer_cost), SUM(s.net)`+from+`
			LEFT JOIN cost_centers d ON d.id = s.department_id WHERE `+f.Where()+` GROUP BY d.id, d.name ORDER BY 2`, f.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id *int64
			var l string
			var n int
			var g, e, nt decimal.Decimal
			if err := rows.Scan(&id, &l, &n, &g, &e, &nt); err != nil {
				return nil, err
			}
			out.add(Row{ID: id, Key: l, Label: l, Count: n, Amounts: map[string]decimal.Decimal{"gross": g, "employer": e, "total_cost": g.Add(e), "net": nt}})
		}
		return out, rows.Err()
	case "by-cost-center", "by-project":
		return allocatedCost(ctx, q, companyID, name, f, from)
	case "tax", "insurance", "employer-costs":
		return componentReport(ctx, q, name, f, from)
	case "net-payable", "payments":
		out := newReport(name, "net")
		statuses := "('posted')"
		if name == "payments" {
			statuses = "('paid', 'closed')"
		}
		rows, err := q.Query(ctx, `SELECT s.id, p.name, s.employee_code || ' - ' || s.employee_name, s.iban, s.bank_account_number,
			COALESCE(p.payment_date::text, ''), COALESCE(ta.name, ''), s.net`+from+`
			LEFT JOIN treasury_accounts ta ON ta.id = p.treasury_account_id
			WHERE `+f.Where()+` AND p.status IN `+statuses+` ORDER BY p.year, p.month, s.employee_code`, f.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var period, l, iban, acct, paid, treas string
			var nt decimal.Decimal
			if err := rows.Scan(&id, &period, &l, &iban, &acct, &paid, &treas, &nt); err != nil {
				return nil, err
			}
			out.add(Row{ID: &id, Key: period, Label: l, Count: 1,
				Extra:   map[string]string{"period": period, "iban": iban, "bank_account_number": acct, "payment_date": paid, "treasury_account": treas},
				Amounts: map[string]decimal.Decimal{"net": nt}})
		}
		return out, rows.Err()
	case "journal":
		return journalReport(ctx, q, companyID, rf)
	}
	return nil, apperr.NotFound("report")
}

// componentReport totals payslip lines of tax, insurance or employer components per employee (or per component).
func componentReport(ctx context.Context, q db.DBTX, name string, f *db.Filter, from string) (*Report, error) {
	var cond string
	var cols []string
	switch name {
	case "tax":
		cond, cols = "l.category = 'tax' AND l.kind = 'deduction'", []string{"taxable", "tax"}
	case "insurance":
		cond, cols = "l.category = 'insurance'", []string{"insurable", "employee_share", "employer_share", "total"}
	default:
		cond, cols = "l.kind = 'employer'", []string{"amount"}
	}
	out := newReport(name, cols...)
	if name == "employer-costs" {
		rows, err := q.Query(ctx, `SELECT l.component_id, l.name, count(DISTINCT s.employee_id), SUM(l.amount)`+from+`
			JOIN payslip_lines l ON l.payslip_id = s.id WHERE `+f.Where()+` AND `+cond+` GROUP BY l.component_id, l.name ORDER BY 2`, f.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var l string
			var n int
			var a decimal.Decimal
			if err := rows.Scan(&id, &l, &n, &a); err != nil {
				return nil, err
			}
			out.add(Row{ID: &id, Key: l, Label: l, Count: n, Amounts: map[string]decimal.Decimal{"amount": a}})
		}
		return out, rows.Err()
	}
	rows, err := q.Query(ctx, `WITH sl AS (
			SELECT s.id, s.employee_id, s.employee_code, s.employee_name, s.taxable_income, s.insurable_income,
			       COALESCE(SUM(l.amount) FILTER (WHERE l.kind = 'deduction'), 0) AS ded,
			       COALESCE(SUM(l.amount) FILTER (WHERE l.kind = 'employer'), 0) AS emp`+from+`
			JOIN payslip_lines l ON l.payslip_id = s.id WHERE `+f.Where()+` AND `+cond+` GROUP BY s.id)
		SELECT employee_id, employee_code || ' - ' || employee_name, count(*), SUM(taxable_income), SUM(insurable_income), SUM(ded), SUM(emp)
		FROM sl GROUP BY employee_id, employee_code, employee_name ORDER BY employee_code`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var l string
		var n int
		var taxable, insurable, ded, emp decimal.Decimal
		if err := rows.Scan(&id, &l, &n, &taxable, &insurable, &ded, &emp); err != nil {
			return nil, err
		}
		amounts := map[string]decimal.Decimal{}
		if name == "tax" {
			amounts["taxable"], amounts["tax"] = taxable, ded
		} else {
			amounts["insurable"], amounts["employee_share"], amounts["employer_share"], amounts["total"] = insurable, ded, emp, ded.Add(emp)
		}
		out.add(Row{ID: &id, Key: l, Label: l, Count: n, Amounts: amounts})
	}
	return out, rows.Err()
}

// allocatedCost spreads each payslip's salary cost (gross + employer) over its cost-center allocation.
func allocatedCost(ctx context.Context, q db.DBTX, companyID int64, name string, f *db.Filter, from string) (*Report, error) {
	places, err := ledger.Decimals(ctx, q, companyID)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT s.employee_id, s.gross, s.employer_cost, s.allocations`+from+` WHERE `+f.Where(), f.Args...)
	if err != nil {
		return nil, err
	}
	type acc struct {
		gross, employer decimal.Decimal
		employees       map[int64]bool
	}
	byCC := map[int64]*acc{}
	get := func(id int64) *acc {
		if byCC[id] == nil {
			byCC[id] = &acc{employees: map[int64]bool{}}
		}
		return byCC[id]
	}
	ccIDs := map[int64]bool{}
	type slip struct {
		emp             int64
		gross, employer decimal.Decimal
		allocs          []Allocation
	}
	var slips []slip
	for rows.Next() {
		var s slip
		var raw []byte
		if err := rows.Scan(&s.emp, &s.gross, &s.employer, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(raw, &s.allocs); err != nil {
			rows.Close()
			return nil, err
		}
		for _, a := range s.allocs {
			ccIDs[a.CostCenterID] = true
		}
		slips = append(slips, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	kinds := map[int64]string{}
	names := map[int64]string{}
	ids := make([]int64, 0, len(ccIDs))
	for id := range ccIDs {
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		crows, err := q.Query(ctx, `SELECT id, kind, name FROM cost_centers WHERE company_id = $1 AND id = ANY($2)`, companyID, ids)
		if err != nil {
			return nil, err
		}
		for crows.Next() {
			var id int64
			var k, n string
			if err := crows.Scan(&id, &k, &n); err != nil {
				crows.Close()
				return nil, err
			}
			kinds[id], names[id] = k, n
		}
		crows.Close()
	}
	wantProject := name == "by-project"
	for _, s := range slips {
		var allocs []Allocation
		for _, a := range s.allocs {
			if (kinds[a.CostCenterID] == "project") == wantProject {
				allocs = append(allocs, a)
			}
		}
		pcts := make([]decimal.Decimal, len(allocs))
		total := decimal.Zero
		for i, a := range allocs {
			pcts[i] = a.Percent
			total = total.Add(a.Percent)
		}
		// The share not allocated to this dimension is reported as unassigned.
		if rest := hundred.Sub(total); rest.IsPositive() {
			allocs = append(allocs, Allocation{CostCenterID: 0})
			pcts = append(pcts, rest)
		}
		gs, es := Split(s.gross, pcts, places), Split(s.employer, pcts, places)
		for i, a := range allocs {
			x := get(a.CostCenterID)
			x.gross = x.gross.Add(gs[i])
			x.employer = x.employer.Add(es[i])
			x.employees[s.emp] = true
		}
	}
	out := newReport(name, "gross", "employer", "total_cost")
	keys := make([]int64, 0, len(byCC))
	for k := range byCC {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == 0) != (keys[j] == 0) {
			return keys[j] == 0
		}
		return names[keys[i]] < names[keys[j]]
	})
	for _, k := range keys {
		x := byCC[k]
		var id *int64
		if k != 0 {
			kk := k
			id = &kk
		}
		out.add(Row{ID: id, Key: names[k], Label: names[k], Count: len(x.employees),
			Amounts: map[string]decimal.Decimal{"gross": x.gross, "employer": x.employer, "total_cost": x.gross.Add(x.employer)}})
	}
	return out, nil
}

func journalReport(ctx context.Context, q db.DBTX, companyID int64, rf ReportFilter) (*Report, error) {
	f := db.NewFilter("e.company_id = $1 AND e.source_type IN ('payroll', 'payroll_payment', 'reversal')", companyID)
	f.AddRaw(`(e.source_type <> 'reversal' OR EXISTS (SELECT 1 FROM journal_entries o WHERE o.id = e.reversal_of_id AND o.source_type IN ('payroll', 'payroll_payment')))`)
	if !rf.From.IsZero() {
		f.Add("e.date >= $N", rf.From)
	}
	if !rf.To.IsZero() {
		f.Add("e.date <= $N", rf.To)
	}
	rows, err := q.Query(ctx, `SELECT e.id, e.number, e.date, e.description, e.source_type, e.status, e.total_debit
		FROM journal_entries e WHERE `+f.Where()+` ORDER BY e.date, e.number`, f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := newReport("journal", "amount")
	for rows.Next() {
		var id, number int64
		var date civil.Date
		var desc, src, status string
		var amt decimal.Decimal
		if err := rows.Scan(&id, &number, &date, &desc, &src, &status, &amt); err != nil {
			return nil, err
		}
		out.add(Row{ID: &id, Key: date.String(), Label: desc, Count: 1,
			Extra:   map[string]string{"number": decimal.NewFromInt(number).String(), "date": date.String(), "source_type": src, "status": status},
			Amounts: map[string]decimal.Decimal{"amount": amt}})
	}
	return out, rows.Err()
}
