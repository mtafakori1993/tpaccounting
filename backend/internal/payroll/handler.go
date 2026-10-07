package payroll

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/export"
	"github.com/tafakorpardazan/tpaccounting/internal/pdf"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/sheet"
)

type Handler struct {
	Pool *pgxpool.Pool
	PDF  *pdf.Service
}

// EmployeeRoutes is mounted at /employees. Employee data is payroll data.
func (h *Handler) EmployeeRoutes(r chi.Router) {
	r.With(identity.Require("payroll.view")).Get("/", httpx.Handler(h.listEmployees))
	r.With(identity.Require("payroll.view")).Get("/{id}", httpx.Handler(h.getEmployee))
	r.With(identity.Require("payroll.create")).Post("/", httpx.Handler(h.saveEmployee))
	r.With(identity.Require("payroll.edit")).Put("/{id}", httpx.Handler(h.saveEmployee))
	r.With(identity.Require("payroll.delete")).Delete("/{id}", httpx.Handler(h.deleteEmployee))
}

// Routes is mounted at /payroll.
func (h *Handler) Routes(r chi.Router) {
	view := identity.Require("payroll.view")
	r.With(view).Get("/settings", httpx.Handler(h.getSettings))
	r.With(identity.Require("payroll.settings")).Put("/settings", httpx.Handler(h.putSettings))

	// Statutory rules with effective dates.
	r.With(view).Get("/rules", httpx.Handler(h.rules))
	r.With(identity.Require("payroll.settings")).Post("/tax-tables", httpx.Handler(h.saveTaxTable))
	r.With(identity.Require("payroll.settings")).Put("/tax-tables/{id}", httpx.Handler(h.saveTaxTable))
	r.With(identity.Require("payroll.settings")).Delete("/tax-tables/{id}", httpx.Handler(h.deleteRule("payroll_tax_tables")))
	r.With(identity.Require("payroll.settings")).Post("/insurance-rules", httpx.Handler(h.saveInsuranceRule))
	r.With(identity.Require("payroll.settings")).Put("/insurance-rules/{id}", httpx.Handler(h.saveInsuranceRule))
	r.With(identity.Require("payroll.settings")).Delete("/insurance-rules/{id}", httpx.Handler(h.deleteRule("payroll_insurance_rules")))

	// Liabilities (tax / insurance) and their settlement through treasury.
	r.With(view).Get("/liabilities", httpx.Handler(h.listLiabilities))
	r.With(view).Get("/liabilities/{id}", httpx.Handler(h.getLiability))
	r.With(identity.Require("payroll.pay")).Post("/liabilities/{id}/pay", httpx.Handler(h.payLiability))

	// Attendance import.
	r.With(view).Get("/attendance/template", httpx.Handler(h.attendanceTemplate))
	r.With(view).Get("/periods/{id}/attendance/imports", httpx.Handler(h.listImports))
	r.With(identity.Require("payroll.edit")).Post("/periods/{id}/attendance/import", httpx.Handler(h.importAttendance))

	// Payment instructions (bank transfer dataset); no accounting effect.
	r.With(view).Get("/periods/{id}/payments", httpx.Handler(h.payments))
	r.With(identity.Require("payroll.export")).Get("/periods/{id}/payments/export", httpx.Handler(h.exportPayments))

	r.With(view).Get("/components", httpx.Handler(h.listComponents))
	r.With(identity.Require("payroll.create")).Post("/components", httpx.Handler(h.saveComponent))
	r.With(identity.Require("payroll.create")).Post("/components/defaults", httpx.Handler(h.installDefaults))
	r.With(identity.Require("payroll.edit")).Put("/components/{id}", httpx.Handler(h.saveComponent))

	r.With(view).Get("/periods", httpx.Handler(h.listPeriods))
	r.With(identity.Require("payroll.create")).Post("/periods", httpx.Handler(h.createPeriod))
	r.With(view).Get("/periods/{id}", httpx.Handler(h.getPeriod))
	r.With(identity.Require("payroll.delete")).Delete("/periods/{id}", httpx.Handler(h.deletePeriod))
	r.With(view).Get("/periods/{id}/inputs", httpx.Handler(h.getInputs))
	r.With(identity.Require("payroll.edit")).Put("/periods/{id}/inputs", httpx.Handler(h.putInputs))
	r.With(view).Get("/periods/{id}/payslips", httpx.Handler(h.listPayslips))
	r.With(view).Get("/payslips/{id}", httpx.Handler(h.getPayslip))
	r.With(view).Get("/reports/{report}", httpx.Handler(h.report))

	// Workflow actions; the permission depends on the action.
	for _, prefix := range []string{"/{id}", "/periods/{id}"} {
		for action := range periodMachine {
			r.Post(prefix+"/"+action, httpx.Handler(h.action(action)))
		}
	}
	r.With(view).Get("/", httpx.Handler(h.listPeriods))
}

func (h *Handler) listEmployees(w http.ResponseWriter, r *http.Request) error {
	page, err := ListEmployees(r.Context(), h.Pool, identity.From(r.Context()).CompanyID,
		EmployeeFilter{DepartmentID: httpx.QueryInt64(r, "department_id"), Active: httpx.QueryBool(r, "active")}, httpx.ParseList(r, "code", false))
	if err != nil {
		return err
	}
	httpx.OK(w, page)
	return nil
}

func (h *Handler) getEmployee(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	e, err := GetEmployee(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, e)
	return nil
}

func (h *Handler) saveEmployee(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Employee
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	create := r.Method == http.MethodPost
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if create {
			var err error
			if id, err = CreateEmployee(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
				return err
			}
			after, err := GetEmployee(r.Context(), tx, p.CompanyID, id)
			if err != nil {
				return err
			}
			return audit.Record(r.Context(), tx, "create", "employee", id, nil, after)
		}
		var err error
		if id, err = httpx.IDParam(r, "id"); err != nil {
			return err
		}
		before, err := GetEmployee(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := UpdateEmployee(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		after, err := GetEmployee(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := audit.Record(r.Context(), tx, "update", "employee", id, before, after); err != nil {
			return err
		}
		if !before.BaseSalary.Equal(after.BaseSalary) {
			return audit.Record(r.Context(), tx, "salary_change", "employee", id,
				map[string]any{"base_salary": before.BaseSalary}, map[string]any{"base_salary": after.BaseSalary})
		}
		return nil
	})
	if err != nil {
		return err
	}
	e, err := GetEmployee(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	if create {
		httpx.Created(w, e)
	} else {
		httpx.OK(w, e)
	}
	return nil
}

func (h *Handler) deleteEmployee(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetEmployee(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM payslips WHERE employee_id = $1) OR EXISTS (SELECT 1 FROM payroll_inputs WHERE employee_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return apperr.Conflict("in_use", "the employee has payroll history; deactivate instead")
		}
		if _, err := tx.Exec(r.Context(), `DELETE FROM employees WHERE id = $1`, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "employee", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) error {
	s, err := LoadSettings(r.Context(), h.Pool, identity.From(r.Context()).CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, s)
	return nil
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in PayrollSettings
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := LoadSettings(r.Context(), tx, p.CompanyID)
		if err != nil {
			return err
		}
		if err := SaveSettings(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update", "payroll_settings", 0, before, in)
	})
	if err != nil {
		return err
	}
	return h.getSettings(w, r)
}

func (h *Handler) listComponents(w http.ResponseWriter, r *http.Request) error {
	items, err := ListComponents(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, false)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) saveComponent(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in Component
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	if r.Method == http.MethodPut {
		var err error
		if id, err = httpx.IDParam(r, "id"); err != nil {
			return err
		}
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		action := "update"
		if id == 0 {
			action = "create"
		}
		var err error
		if id, err = SaveComponent(r.Context(), tx, p.CompanyID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, action, "salary_component", id, nil, in)
	})
	if err != nil {
		return err
	}
	in.ID = id
	httpx.OK(w, in)
	return nil
}

func (h *Handler) installDefaults(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var n int
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if n, err = InstallDefaults(r.Context(), tx, p.CompanyID); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "salary_component", 0, nil, map[string]any{"defaults": n})
	})
	if err != nil {
		return err
	}
	return h.listComponents(w, r)
}

func (h *Handler) listPeriods(w http.ResponseWriter, r *http.Request) error {
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	items, err := ListPeriods(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, year)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) getPeriod(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	x, err := GetPeriod(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, x)
	return nil
}

func (h *Handler) createPeriod(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in PeriodInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if id, err = CreatePeriod(r.Context(), tx, p.CompanyID, p.UserID, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "create", "payroll_period", id, nil, in)
	})
	if err != nil {
		return err
	}
	x, err := GetPeriod(r.Context(), h.Pool, p.CompanyID, id)
	if err != nil {
		return err
	}
	httpx.Created(w, x)
	return nil
}

func (h *Handler) deletePeriod(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetPeriod(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		if err := DeletePeriod(r.Context(), tx, p.CompanyID, id); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "delete", "payroll_period", id, before, nil)
	})
	if err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

func (h *Handler) getInputs(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	rows, err := Inputs(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, rows)
	return nil
}

func (h *Handler) putInputs(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in []InputRow
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		if err := SaveInputs(r.Context(), tx, p.CompanyID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "update_inputs", "payroll_period", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.getInputs(w, r)
}

func (h *Handler) listPayslips(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	items, err := ListPayslips(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) getPayslip(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	s, err := GetPayslip(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, s)
	return nil
}

func (h *Handler) action(name string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p := identity.From(r.Context())
		if err := identity.Check(r.Context(), PeriodPermission(name)); err != nil {
			return err
		}
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		var in ActionInput
		if r.ContentLength > 0 {
			if err := httpx.Decode(r, &in); err != nil {
				return err
			}
		}
		err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
			before, err := GetPeriod(r.Context(), tx, p.CompanyID, id)
			if err != nil {
				return err
			}
			ctx := r.Context()
			fns := map[string]func(context.Context, db.DBTX, int64, int64, int64) error{
				"calculate": CalculatePeriod, "approve": Approve, "reopen": Reopen, "post": Post, "close": Close,
			}
			switch name {
			case "pay":
				err = Pay(ctx, tx, p.CompanyID, p.UserID, id, in)
			case "unpost":
				err = Unpost(ctx, tx, p.CompanyID, p.UserID, id, in.Date)
			case "unpay":
				err = Unpay(ctx, tx, p.CompanyID, p.UserID, id, in.Date)
			default:
				err = fns[name](ctx, tx, p.CompanyID, p.UserID, id)
			}
			if err != nil {
				return err
			}
			after, err := GetPeriod(ctx, tx, p.CompanyID, id)
			if err != nil {
				return err
			}
			return audit.Record(ctx, tx, "payroll_"+name, "payroll_period", id,
				map[string]any{"status": before.Status, "net_total": before.NetTotal},
				map[string]any{"status": after.Status, "gross_total": after.GrossTotal, "net_total": after.NetTotal, "employer_total": after.EmployerTotal,
					"journal_entry_id": after.JournalEntryID, "payment_entry_id": after.PaymentEntryID, "reason": in.Reason})
		})
		if err != nil {
			return err
		}
		return h.getPeriod(w, r)
	}
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) error {
	from, err := httpx.QueryDate(r, "from")
	if err != nil {
		return err
	}
	to, err := httpx.QueryDate(r, "to")
	if err != nil {
		return err
	}
	rf := ReportFilter{From: from, To: to, EmployeeID: httpx.QueryInt64(r, "employee_id"), DepartmentID: httpx.QueryInt64(r, "department_id")}
	if s := r.URL.Query().Get("status"); s != "" {
		rf.Statuses = []string{s}
	}
	out, err := Build(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, chi.URLParam(r, "report"), rf)
	if err != nil {
		return err
	}
	httpx.OK(w, out)
	return nil
}

func (h *Handler) rules(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	tax, err := ListTaxTables(r.Context(), h.Pool, p.CompanyID)
	if err != nil {
		return err
	}
	ins, err := ListInsuranceRules(r.Context(), h.Pool, p.CompanyID)
	if err != nil {
		return err
	}
	s, err := LoadSettings(r.Context(), h.Pool, p.CompanyID)
	if err != nil {
		return err
	}
	httpx.OK(w, map[string]any{"tax_tables": tax, "insurance_rules": ins, "settings": s, "formula_variables": FormulaVariables})
	return nil
}

func (h *Handler) saveTaxTable(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in TaxTable
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	if r.Method == http.MethodPut {
		var err error
		if id, err = httpx.IDParam(r, "id"); err != nil {
			return err
		}
	} else {
		in.IsActive = true
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		action := "update"
		if id == 0 {
			action = "create"
		}
		var err error
		if id, err = SaveTaxTable(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, action, "payroll_tax_table", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.rules(w, r)
}

func (h *Handler) saveInsuranceRule(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	var in InsuranceRule
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	var id int64
	if r.Method == http.MethodPut {
		var err error
		if id, err = httpx.IDParam(r, "id"); err != nil {
			return err
		}
	} else {
		in.IsActive = true
	}
	err := db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		action := "update"
		if id == 0 {
			action = "create"
		}
		var err error
		if id, err = SaveInsuranceRule(r.Context(), tx, p.CompanyID, p.UserID, id, in); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, action, "payroll_insurance_rule", id, nil, in)
	})
	if err != nil {
		return err
	}
	return h.rules(w, r)
}

func (h *Handler) deleteRule(table string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p := identity.From(r.Context())
		id, err := httpx.IDParam(r, "id")
		if err != nil {
			return err
		}
		err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
			if err := DeleteRule(r.Context(), tx, p.CompanyID, table, id); err != nil {
				return err
			}
			return audit.Record(r.Context(), tx, "delete", strings.TrimSuffix(table, "s"), id, nil, nil)
		})
		if err != nil {
			return err
		}
		return h.rules(w, r)
	}
}

func (h *Handler) listLiabilities(w http.ResponseWriter, r *http.Request) error {
	items, err := ListLiabilities(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, LiabilityFilter{
		PeriodID: httpx.QueryInt64(r, "period_id"), Kind: r.URL.Query().Get("kind"), OpenOnly: r.URL.Query().Get("open") == "true"})
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) getLiability(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	l, err := GetLiability(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, l)
	return nil
}

func (h *Handler) payLiability(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	// The settlement is a treasury payment, so it needs the treasury permission too.
	if err := identity.Check(r.Context(), "treasury.post"); err != nil {
		return err
	}
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	var in LiabilityPayInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		before, err := GetLiability(r.Context(), tx, p.CompanyID, id)
		if err != nil {
			return err
		}
		txID, err := PayLiability(r.Context(), tx, p.CompanyID, p.UserID, id, in)
		if err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "pay_liability", "payroll_liability", id,
			map[string]any{"paid": before.Paid, "outstanding": before.Outstanding},
			map[string]any{"treasury_transaction_id": txID, "amount": in.Amount, "date": in.Date, "treasury_account_id": in.TreasuryAccountID})
	})
	if err != nil {
		return err
	}
	return h.getLiability(w, r)
}

const maxImportBytes = 5 << 20

func (h *Handler) importAttendance(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+64<<10)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		return apperr.Rule("file_too_large", "the file is too large").WithParams("max_mb", "5")
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		v := apperr.NewValidator()
		v.Check(false, "file", "required")
		return v.Err()
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImportBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxImportBytes {
		return apperr.Rule("file_too_large", "the file is too large").WithParams("max_mb", "5")
	}
	commit := r.FormValue("commit") == "true"
	if !commit {
		res, err := ImportAttendance(r.Context(), h.Pool, p.CompanyID, p.UserID, id, fh.Filename, data, false)
		if err != nil {
			return err
		}
		httpx.OK(w, res)
		return nil
	}
	var res *ImportResult
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		var err error
		if res, err = ImportAttendance(r.Context(), tx, p.CompanyID, p.UserID, id, fh.Filename, data, true); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "import_attendance", "payroll_period", id, nil,
			map[string]any{"import_id": res.ImportID, "file": fh.Filename, "summary": res.Summary})
	})
	if err != nil {
		return err
	}
	httpx.OK(w, res)
	return nil
}

func (h *Handler) listImports(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	items, err := ListImports(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id)
	if err != nil {
		return err
	}
	httpx.OK(w, items)
	return nil
}

func (h *Handler) attendanceTemplate(w http.ResponseWriter, r *http.Request) error {
	var buf bytes.Buffer
	lang := r.URL.Query().Get("lang")
	if err := sheet.WriteXLSX(&buf, sheet.Sheet{Name: "attendance", RTL: lang != "en", Rows: AttendanceTemplate(lang), FreezeRow: 1}); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="attendance-template.xlsx"`)
	_, err := w.Write(buf.Bytes())
	return err
}

func (h *Handler) payments(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	date, err := httpx.QueryDate(r, "payment_date")
	if err != nil {
		return err
	}
	b, err := PaymentInstructions(r.Context(), h.Pool, identity.From(r.Context()).CompanyID, id, date)
	if err != nil {
		return err
	}
	httpx.OK(w, b)
	return nil
}

func (h *Handler) exportPayments(w http.ResponseWriter, r *http.Request) error {
	p := identity.From(r.Context())
	id, err := httpx.IDParam(r, "id")
	if err != nil {
		return err
	}
	date, err := httpx.QueryDate(r, "payment_date")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	format, layout := q.Get("format"), q.Get("layout")
	if layout == "" {
		layout = "generic"
	}
	var rep *export.Report
	err = db.WithTx(r.Context(), h.Pool, func(tx pgx.Tx) error {
		b, err := PaymentInstructions(r.Context(), tx, p.CompanyID, id, date)
		if err != nil {
			return err
		}
		if rep, err = PaymentReport(r.Context(), tx, p.CompanyID, b, layout, q.Get("lang")); err != nil {
			return err
		}
		if err := LogPaymentExport(r.Context(), tx, p.CompanyID, p.UserID, b, layout, format); err != nil {
			return err
		}
		return audit.Record(r.Context(), tx, "export_payments", "payroll_period", id, nil,
			map[string]any{"layout": layout, "format": format, "rows": b.Count, "total": b.Total})
	})
	if err != nil {
		return err
	}
	return export.Write(r.Context(), w, h.PDF, p.CompanyID, rep, format)
}
