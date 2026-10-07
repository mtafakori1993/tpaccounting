package payroll

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/export"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

// Payment instructions list what each employee must be paid for a payroll —
// the dataset a bank transfer (or a bank's group-payment upload) is built
// from. Producing them never touches the ledger: the salary payment itself
// remains the single aggregated entry booked by Pay.

// PaymentRow is one employee's transfer instruction.
type PaymentRow struct {
	EmployeeID    int64           `json:"employee_id"`
	EmployeeCode  string          `json:"employee_code"`
	EmployeeName  string          `json:"employee_name"`
	NationalID    string          `json:"national_id"`
	BankName      string          `json:"bank_name"`
	IBAN          string          `json:"iban"`
	AccountNumber string          `json:"account_number"`
	Amount        decimal.Decimal `json:"amount"`
	Description   string          `json:"description"`
	Issues        []string        `json:"issues"`
}

type PaymentBatch struct {
	PeriodID    int64           `json:"period_id"`
	PeriodName  string          `json:"period_name"`
	Status      string          `json:"status"`
	PaymentDate civil.Date      `json:"payment_date"`
	Rows        []PaymentRow    `json:"rows"`
	Total       decimal.Decimal `json:"total"`
	Count       int             `json:"count"`
	IssueCount  int             `json:"issue_count"`
	Layouts     []LayoutInfo    `json:"layouts"`
}

// PaymentLayout turns instructions into a file layout. Bank-specific layouts
// (column order, headers, date and amount formats) are added by registering
// another implementation; nothing else changes.
type PaymentLayout interface {
	Code() string
	// Plain layouts contain only a header row and data rows (bank upload files).
	Plain() bool
	Columns(lang string) []export.Column
	Row(b *PaymentBatch, r PaymentRow) []string
}

type LayoutInfo struct {
	Code  string `json:"code"`
	Plain bool   `json:"plain"`
}

var paymentLayouts = map[string]PaymentLayout{}

// RegisterPaymentLayout adds a layout.
func RegisterPaymentLayout(l PaymentLayout) { paymentLayouts[l.Code()] = l }

func init() {
	RegisterPaymentLayout(genericLayout{plain: true})
	RegisterPaymentLayout(genericLayout{plain: false})
}

// genericLayout is a bank-neutral transfer dataset. "generic" is the plain
// upload file; "report" adds the company header, filters and totals.
type genericLayout struct{ plain bool }

func (g genericLayout) Code() string {
	if g.plain {
		return "generic"
	}
	return "report"
}
func (g genericLayout) Plain() bool { return g.plain }

func (genericLayout) Columns(lang string) []export.Column {
	l := map[string][]string{
		"fa": {"کد پرسنلی", "نام کارمند", "کد ملی", "بانک", "شبا", "شماره حساب", "مبلغ", "تاریخ پرداخت", "دوره حقوق", "شرح"},
		"en": {"Employee code", "Employee", "National ID", "Bank", "IBAN", "Account number", "Amount", "Payment date", "Payroll period", "Description"},
	}[lang]
	kinds := []export.Kind{export.KindID, export.KindText, export.KindID, export.KindText, export.KindID, export.KindID, export.KindMoney,
		export.KindDate, export.KindText, export.KindText}
	out := make([]export.Column, len(l))
	for i := range l {
		out[i] = export.Column{Label: l[i], Kind: kinds[i]}
	}
	return out
}

func (genericLayout) Row(b *PaymentBatch, r PaymentRow) []string {
	return []string{r.EmployeeCode, r.EmployeeName, r.NationalID, r.BankName, r.IBAN, r.AccountNumber, r.Amount.String(), b.PaymentDate.String(),
		b.PeriodName, r.Description}
}

// ValidIBAN checks an Iranian IBAN (IR + 24 digits) with the ISO 7064 mod-97 check.
func ValidIBAN(iban string) bool {
	if !ibanRe.MatchString(iban) {
		return false
	}
	rearranged := iban[4:] + "1827" + iban[2:4] // I=18, R=27
	n, ok := new(big.Int).SetString(rearranged, 10)
	return ok && new(big.Int).Mod(n, big.NewInt(97)).Int64() == 1
}

// PaymentInstructions lists the transfers of an approved (or later) payroll.
func PaymentInstructions(ctx context.Context, q db.DBTX, companyID, periodID int64, date civil.Date) (*PaymentBatch, error) {
	p, err := GetPeriod(ctx, q, companyID, periodID)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDraft || p.Status == StatusCalculated {
		return nil, apperr.Rule("payroll_not_approved", "approve the payroll before preparing payments")
	}
	b := &PaymentBatch{PeriodID: p.ID, PeriodName: p.Name, Status: p.Status, Rows: []PaymentRow{}}
	switch {
	case !date.IsZero():
		b.PaymentDate = date
	case p.PaymentDate != nil:
		b.PaymentDate = *p.PaymentDate
	default:
		b.PaymentDate = civil.Today()
	}
	rows, err := q.Query(ctx, `SELECT s.employee_id, s.employee_code, s.employee_name, e.national_id, e.bank_name, s.iban, s.bank_account_number, s.net
		FROM payslips s JOIN employees e ON e.id = s.employee_id WHERE s.period_id = $1 AND s.net > 0 ORDER BY s.employee_code, s.id`, periodID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r PaymentRow
		if err := rows.Scan(&r.EmployeeID, &r.EmployeeCode, &r.EmployeeName, &r.NationalID, &r.BankName, &r.IBAN, &r.AccountNumber, &r.Amount); err != nil {
			return nil, err
		}
		r.Description = "Salary " + p.Name
		r.Issues = []string{}
		switch {
		case r.IBAN == "" && r.AccountNumber == "":
			r.Issues = append(r.Issues, "missing_bank_account")
		case r.IBAN != "" && !ValidIBAN(r.IBAN):
			r.Issues = append(r.Issues, "invalid_iban")
		}
		if r.NationalID == "" {
			r.Issues = append(r.Issues, "missing_national_id")
		}
		if len(r.Issues) > 0 {
			b.IssueCount++
		}
		b.Total = b.Total.Add(r.Amount)
		b.Rows = append(b.Rows, r)
	}
	b.Count = len(b.Rows)
	for _, l := range paymentLayouts {
		b.Layouts = append(b.Layouts, LayoutInfo{Code: l.Code(), Plain: l.Plain()})
	}
	sort.Slice(b.Layouts, func(i, j int) bool { return b.Layouts[i].Code < b.Layouts[j].Code })
	if !b.Total.Equal(p.NetTotal) {
		// Every payslip with a positive net is listed, so this can only mean corruption.
		return nil, fmt.Errorf("payment instructions %s do not match the payroll net %s", b.Total, p.NetTotal)
	}
	return b, rows.Err()
}

// PaymentReport lays a batch out in a registered layout.
func PaymentReport(ctx context.Context, q db.DBTX, companyID int64, b *PaymentBatch, layout, lang string) (*export.Report, error) {
	l, ok := paymentLayouts[layout]
	if !ok {
		v := apperr.NewValidator()
		v.Check(false, "layout", "invalid")
		return nil, v.Err()
	}
	if lang != "en" {
		lang = "fa"
	}
	title := map[string]string{"fa": "دستور پرداخت حقوق", "en": "Salary payment instructions"}[lang]
	rep := &export.Report{Name: fmt.Sprintf("salary-payments-%s", strings.ReplaceAll(b.PeriodName, " ", "-")), Title: title + " — " + b.PeriodName,
		Lang: lang, Plain: l.Plain(), Columns: l.Columns(lang)}
	if err := rep.Prepare(ctx, q, companyID); err != nil {
		return nil, err
	}
	for _, r := range b.Rows {
		rep.Rows = append(rep.Rows, l.Row(b, r))
	}
	rep.Totals = []export.KV{{Label: map[string]string{"fa": "جمع خالص پرداختی", "en": "Total net pay"}[lang], Value: b.Total.String()}}
	rep.Filters = []export.KV{
		{Label: map[string]string{"fa": "دوره حقوق", "en": "Payroll period"}[lang], Value: b.PeriodName},
		{Label: map[string]string{"fa": "تاریخ پرداخت", "en": "Payment date"}[lang], Value: rep.CivilDate(b.PaymentDate)},
	}
	return rep, nil
}

// LogPaymentExport records that instructions were exported (no accounting effect).
func LogPaymentExport(ctx context.Context, q db.DBTX, companyID, userID int64, b *PaymentBatch, layout, format string) error {
	_, err := q.Exec(ctx, `INSERT INTO payroll_payment_exports (company_id, period_id, layout, format, row_count, total_amount, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, companyID, b.PeriodID, layout, format, b.Count, b.Total, userID)
	return err
}
