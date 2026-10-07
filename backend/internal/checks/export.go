package checks

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/export"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

var validStatus = map[string]bool{}

func init() {
	for _, acts := range machine {
		for _, r := range acts {
			validStatus[r.to] = true
			for _, f := range r.from {
				validStatus[f] = true
			}
		}
	}
}

// Summary is the headline of a filtered check set.
type Summary struct {
	Count         int             `json:"count"`
	Amount        decimal.Decimal `json:"amount"`
	Received      Bucket          `json:"received"`
	Issued        Bucket          `json:"issued"`
	Live          Bucket          `json:"live"`
	DueSoon       Bucket          `json:"due_soon"` // live, due within 7 days
	Overdue       Bucket          `json:"overdue"`
	Returned      Bucket          `json:"returned"`
	Cleared       Bucket          `json:"cleared"`
	GeneratedDate civil.Date      `json:"today"`
}

// Summarise aggregates the checks matching lf in one query.
func Summarise(ctx context.Context, q db.DBTX, companyID int64, lf Filter, search string) (*Summary, error) {
	today := civil.Today()
	f := db.NewFilter("c.company_id = $1", companyID)
	lf.apply(f, today)
	addSearch(f, search)
	t := f.Arg(today) + "::date"
	w := f.Arg(today.AddDays(7)) + "::date"
	s := &Summary{GeneratedDate: today}
	err := q.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*), COALESCE(SUM(c.amount), 0),
		       count(*) FILTER (WHERE c.check_type = 'received'), COALESCE(SUM(c.amount) FILTER (WHERE c.check_type = 'received'), 0),
		       count(*) FILTER (WHERE c.check_type = 'issued'), COALESCE(SUM(c.amount) FILTER (WHERE c.check_type = 'issued'), 0),
		       count(*) FILTER (WHERE %[1]s), COALESCE(SUM(c.amount) FILTER (WHERE %[1]s), 0),
		       count(*) FILTER (WHERE %[1]s AND c.due_date BETWEEN %[2]s AND %[3]s), COALESCE(SUM(c.amount) FILTER (WHERE %[1]s AND c.due_date BETWEEN %[2]s AND %[3]s), 0),
		       count(*) FILTER (WHERE %[1]s AND c.due_date < %[2]s), COALESCE(SUM(c.amount) FILTER (WHERE %[1]s AND c.due_date < %[2]s), 0),
		       count(*) FILTER (WHERE c.status = 'returned'), COALESCE(SUM(c.amount) FILTER (WHERE c.status = 'returned'), 0),
		       count(*) FILTER (WHERE c.status = 'cleared'), COALESCE(SUM(c.amount) FILTER (WHERE c.status = 'cleared'), 0)
		FROM checks c JOIN parties p ON p.id = c.party_id WHERE %[4]s`, liveCond, t, w, f.Where()), f.Args...).
		Scan(&s.Count, &s.Amount, &s.Received.Count, &s.Received.Amount, &s.Issued.Count, &s.Issued.Amount, &s.Live.Count, &s.Live.Amount,
			&s.DueSoon.Count, &s.DueSoon.Amount, &s.Overdue.Count, &s.Overdue.Amount, &s.Returned.Count, &s.Returned.Amount,
			&s.Cleared.Count, &s.Cleared.Amount)
	return s, err
}

func addSearch(f *db.Filter, search string) {
	if search != "" {
		f.Add(`(c.check_number ILIKE $N OR c.sayadi_id ILIKE $N OR c.serial_number ILIKE $N OR p.name ILIKE $N OR c.description ILIKE $N
			OR c.bank_name ILIKE $N OR c.account_holder ILIKE $N)`, httpx.Like(search))
	}
}

// labels used in server-side exports, per language.
var exportLabels = map[string]map[string]string{
	"fa": {
		"title": "گزارش چک‌ها", "number": "شماره ثبت", "type": "نوع", "check_number": "شماره چک", "sayadi": "شناسه صیادی", "bank": "بانک",
		"branch": "شعبه", "party": "طرف حساب", "holder": "دارنده فعلی", "issue_date": "تاریخ صدور", "due_date": "سررسید", "amount": "مبلغ",
		"status": "وضعیت", "description": "شرح", "total": "جمع کل", "received_total": "جمع چک‌های دریافتی", "issued_total": "جمع چک‌های پرداختی",
		"f_type": "نوع چک", "f_status": "وضعیت", "f_bank": "بانک", "f_party": "طرف حساب", "f_due_from": "سررسید از", "f_due_to": "سررسید تا",
		"f_min": "حداقل مبلغ", "f_max": "حداکثر مبلغ", "f_q": "جستجو", "f_report": "گزارش", "f_live": "فقط چک‌های جاری", "yes": "بله",
		"received": "دریافتی", "issued": "پرداختی",
		"r_received": "چک‌های دریافتی", "r_issued": "چک‌های پرداختی", "r_due": "چک‌های سررسیدی", "r_overdue": "چک‌های سررسید گذشته",
		"r_returned": "چک‌های برگشتی", "r_in-custody": "چک‌های موجود در صندوق",
	},
	"en": {
		"title": "Checks report", "number": "Register no.", "type": "Type", "check_number": "Check no.", "sayadi": "Sayadi ID", "bank": "Bank",
		"branch": "Branch", "party": "Party", "holder": "Current holder", "issue_date": "Issue date", "due_date": "Due date", "amount": "Amount",
		"status": "Status", "description": "Description", "total": "Total", "received_total": "Received checks total", "issued_total": "Issued checks total",
		"f_type": "Check type", "f_status": "Status", "f_bank": "Bank", "f_party": "Party", "f_due_from": "Due from", "f_due_to": "Due to",
		"f_min": "Minimum amount", "f_max": "Maximum amount", "f_q": "Search", "f_report": "Report", "f_live": "Outstanding only", "yes": "Yes",
		"received": "Received", "issued": "Issued",
		"r_received": "Received checks", "r_issued": "Issued checks", "r_due": "Checks due", "r_overdue": "Overdue checks",
		"r_returned": "Returned checks", "r_in-custody": "Checks in custody",
	},
}

var statusLabels = map[string]map[string]string{
	"fa": {"draft": "پیش‌نویس", "received": "دریافت شده", "in_custody": "در صندوق", "deposited": "در جریان وصول", "cleared": "وصول/پاس شده",
		"transferred": "واگذار شده", "returned": "برگشتی", "cancelled": "باطل شده", "lost": "مفقود", "issued": "صادر شده", "delivered": "تحویل شده",
		"replaced": "تعویض شده"},
	"en": {"draft": "Draft", "received": "Received", "in_custody": "In custody", "deposited": "In collection", "cleared": "Cleared",
		"transferred": "Endorsed", "returned": "Returned", "cancelled": "Cancelled", "lost": "Lost", "issued": "Issued", "delivered": "Delivered",
		"replaced": "Replaced"},
}

// BuildExport loads every check matching the filters (bounded by
// export.MaxRows) and lays them out as a report.
func BuildExport(ctx context.Context, q db.DBTX, companyID int64, lf Filter, lp httpx.ListParams, report, lang string) (*export.Report, error) {
	if lang != "en" {
		lang = "fa"
	}
	L := exportLabels[lang]
	S := statusLabels[lang]
	rep := &export.Report{Name: "checks", Title: L["title"], Lang: lang}
	if report != "" && report != "all" {
		rep.Title = L["r_"+report]
		rep.Name = "checks-" + report
	}
	if err := rep.Prepare(ctx, q, companyID); err != nil {
		return nil, err
	}
	today := civil.Today()
	f := db.NewFilter("c.company_id = $1", companyID)
	lf.apply(f, today)
	addSearch(f, lp.Q)
	order := lp.OrderBy(map[string]string{
		"due_date": "c.due_date", "number": "c.number", "amount": "c.amount", "check_number": "c.check_number", "party": "p.name",
		"status": "c.status", "bank": "c.bank_name", "issue_date": "c.issue_date",
	}, "c.due_date")
	rows, err := q.Query(ctx, `SELECT `+cols+joins+` WHERE `+f.Where()+` ORDER BY `+order+`, c.id DESC LIMIT `+fmt.Sprint(export.MaxRows+1), f.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rep.Columns = []export.Column{
		{Label: L["number"], Kind: export.KindInt}, {Label: L["type"]}, {Label: L["check_number"], Kind: export.KindID},
		{Label: L["sayadi"], Kind: export.KindID}, {Label: L["bank"]}, {Label: L["branch"]}, {Label: L["party"]}, {Label: L["holder"]},
		{Label: L["issue_date"], Kind: export.KindDate}, {Label: L["due_date"], Kind: export.KindDate}, {Label: L["amount"], Kind: export.KindMoney},
		{Label: L["status"]}, {Label: L["description"]},
	}
	var total, recv, iss decimal.Decimal
	for rows.Next() {
		var x Check
		if err := scan(rows, &x, today); err != nil {
			return nil, err
		}
		holder := ""
		if x.HolderPartyName != nil {
			holder = *x.HolderPartyName
		}
		rep.Rows = append(rep.Rows, []string{fmt.Sprint(x.Number), L[x.CheckType], x.CheckNumber, x.SayadiID, x.BankName, x.Branch, x.PartyName,
			holder, x.IssueDate.String(), x.DueDate.String(), x.Amount.String(), S[x.Status], x.Description})
		total = total.Add(x.Amount)
		if x.CheckType == TypeReceived {
			recv = recv.Add(x.Amount)
		} else {
			iss = iss.Add(x.Amount)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if lf.Type != TypeIssued {
		rep.Totals = append(rep.Totals, export.KV{Label: L["received_total"], Value: recv.String()})
	}
	if lf.Type != TypeReceived {
		rep.Totals = append(rep.Totals, export.KV{Label: L["issued_total"], Value: iss.String()})
	}
	rep.Totals = append(rep.Totals, export.KV{Label: L["total"], Value: total.String()})

	// Applied filters, in words.
	add := func(label, value string) { rep.Filters = append(rep.Filters, export.KV{Label: label, Value: value}) }
	if report != "" && report != "all" {
		add(L["f_report"], L["r_"+report])
	}
	if lf.Type != "" {
		add(L["f_type"], L[lf.Type])
	}
	if len(lf.Statuses) > 0 {
		names := make([]string, len(lf.Statuses))
		for i, s := range lf.Statuses {
			names[i] = S[s]
		}
		add(L["f_status"], strings.Join(names, "، "))
	}
	if lf.Live && report != "due" {
		add(L["f_live"], L["yes"])
	}
	if lf.BankID > 0 {
		var name, nameEn string
		if err := q.QueryRow(ctx, `SELECT name, name_en FROM banks WHERE id = $1`, lf.BankID).Scan(&name, &nameEn); err == nil {
			if lang == "en" && nameEn != "" {
				name = nameEn
			}
			add(L["f_bank"], name)
		}
	}
	if lf.PartyID > 0 {
		var name string
		if err := q.QueryRow(ctx, `SELECT name FROM parties WHERE company_id = $1 AND id = $2`, companyID, lf.PartyID).Scan(&name); err == nil {
			add(L["f_party"], name)
		}
	}
	if !lf.DueFrom.IsZero() {
		add(L["f_due_from"], rep.CivilDate(lf.DueFrom))
	}
	if !lf.DueTo.IsZero() {
		add(L["f_due_to"], rep.CivilDate(lf.DueTo))
	}
	if lf.MinAmount.IsPositive() {
		add(L["f_min"], rep.Money(lf.MinAmount.String()))
	}
	if lf.MaxAmount.IsPositive() {
		add(L["f_max"], rep.Money(lf.MaxAmount.String()))
	}
	if lp.Q != "" {
		add(L["f_q"], lp.Q)
	}
	return rep, nil
}
