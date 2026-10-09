package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
)

// Printable documents beyond receipts/payments/expenses/invoices (D42): the app prints
// journal entries and stock documents too (frontend lib/documents.ts journalDocument,
// stockDocument). These builders mirror those, read the record through the user's own
// API access and render through the same /pdf service, so the file the assistant
// returns is the file the app's own "PDF" button produces.

// moreDocuments builds the letterhead document for the extra printable types. ok is
// false for a type it does not handle.
func (a *Agent) moreDocuments(ctx context.Context, u *User, lang string, tg *Target) (doc json.RawMessage, ok bool, err error) {
	switch tg.EntityType {
	case "journal_entry":
		doc, err = a.journalDocument(ctx, u, lang, tg.EntityID)
	case "stock_document":
		doc, err = a.stockDocument(ctx, u, lang, tg.EntityID)
	default:
		return nil, false, nil
	}
	return doc, true, err
}

func docDate(lang, d string) string {
	d = d[:min(10, len(d))]
	if lang == "fa" {
		if t, _, err := normalize.Date(d); err == nil {
			return normalize.FormatJalali(t.Year(), int(t.Month()), t.Day())
		}
	}
	return d
}

func kvs(pairs ...string) []map[string]string {
	var out []map[string]string
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			out = append(out, map[string]string{"label": pairs[i], "value": pairs[i+1]})
		}
	}
	return out
}

func amountCell(d decimal.Decimal) string {
	if d.IsZero() {
		return ""
	}
	return d.String()
}

func (a *Agent) journalDocument(ctx context.Context, u *User, lang string, id int64) (json.RawMessage, error) {
	var e struct {
		Number      int64           `json:"number"`
		Date        string          `json:"date"`
		Description string          `json:"description"`
		Reference   string          `json:"reference"`
		Status      string          `json:"status"`
		SourceType  string          `json:"source_type"`
		TotalDebit  decimal.Decimal `json:"total_debit"`
		TotalCredit decimal.Decimal `json:"total_credit"`
		Lines       []struct {
			AccountCode   string          `json:"account_code"`
			AccountName   string          `json:"account_name"`
			AccountNameEn string          `json:"account_name_en"`
			PartyName     *string         `json:"party_name"`
			Description   string          `json:"description"`
			Debit         decimal.Decimal `json:"debit"`
			Credit        decimal.Decimal `json:"credit"`
		} `json:"lines"`
	}
	if err := a.Acc.Do(ctx, u.Cred, accountingGet(fmt.Sprintf("/journal-entries/%d", id)), &e); err != nil {
		return nil, err
	}
	col := func(l, al string) map[string]string { return map[string]string{"label": l, "align": al} }
	var rows []map[string]any
	for _, l := range e.Lines {
		name, party := l.AccountName, ""
		if lang == "en" && l.AccountNameEn != "" {
			name = l.AccountNameEn
		}
		if l.PartyName != nil {
			party = *l.PartyName
		}
		rows = append(rows, map[string]any{"cells": []string{l.AccountCode, name, party, l.Description, amountCell(l.Debit), amountCell(l.Credit)}})
	}
	var paragraphs []string
	if e.Description != "" {
		paragraphs = []string{e.Description}
	}
	doc := map[string]any{
		"title": tr(lang, "Journal entry ", "سند حسابداری ") + strconv.FormatInt(e.Number, 10), "number": strconv.FormatInt(e.Number, 10),
		"date": docDate(lang, e.Date), "lang": lang,
		"meta":       kvs(tr(lang, "Source", "منبع"), e.SourceType, tr(lang, "Status", "وضعیت"), e.Status, tr(lang, "Reference", "شماره مرجع"), e.Reference),
		"paragraphs": paragraphs,
		"tables": []map[string]any{{"columns": []map[string]string{col(tr(lang, "Code", "کد"), "start"), col(tr(lang, "Account", "حساب"), "start"),
			col(tr(lang, "Party", "طرف حساب"), "start"), col(tr(lang, "Description", "شرح"), "start"), col(tr(lang, "Debit", "بدهکار"), "end"),
			col(tr(lang, "Credit", "بستانکار"), "end")}, "rows": rows,
			"footer": []string{"", "", "", tr(lang, "Total", "جمع"), e.TotalDebit.String(), e.TotalCredit.String()}}},
		"signatures": []string{tr(lang, "Prepared by", "تنظیم‌کننده"), tr(lang, "Approved by", "تاییدکننده")},
	}
	return json.Marshal(doc)
}

var stockKinds = map[string][2]string{"receipt": {"Stock receipt", "رسید انبار"}, "issue": {"Stock issue", "حواله انبار"},
	"transfer": {"Warehouse transfer", "انتقال بین انبارها"}, "adjustment": {"Stock adjustment", "تعدیل موجودی"}}

func (a *Agent) stockDocument(ctx context.Context, u *User, lang string, id int64) (json.RawMessage, error) {
	var d struct {
		Kind               string  `json:"kind"`
		Number             int64   `json:"number"`
		Date               string  `json:"date"`
		WarehouseName      string  `json:"warehouse_name"`
		ToWarehouseName    *string `json:"to_warehouse_name"`
		CounterAccountName *string `json:"counter_account_name"`
		Description        string  `json:"description"`
		Reference          string  `json:"reference"`
		Status             string  `json:"status"`
		Lines              []struct {
			ProductName string          `json:"product_name"`
			ProductSKU  string          `json:"product_sku"`
			Unit        string          `json:"unit"`
			Quantity    decimal.Decimal `json:"quantity"`
			UnitCost    decimal.Decimal `json:"unit_cost"`
			Description string          `json:"description"`
		} `json:"lines"`
	}
	if err := a.Acc.Do(ctx, u.Cred, accountingGet(fmt.Sprintf("/inventory/documents/%d", id)), &d); err != nil {
		return nil, err
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	showCost := d.Kind == "receipt" || d.Kind == "adjustment"
	col := func(l, al string) map[string]string { return map[string]string{"label": l, "align": al} }
	cols := []map[string]string{col("#", "center"), col(tr(lang, "SKU", "کد کالا"), "start"), col(tr(lang, "Item", "کالا"), "start"),
		col(tr(lang, "Quantity", "مقدار"), "end")}
	if showCost {
		cols = append(cols, col(tr(lang, "Unit cost", "بهای واحد"), "end"))
	}
	cols = append(cols, col(tr(lang, "Description", "شرح"), "start"))
	var rows []map[string]any
	for i, l := range d.Lines {
		cells := []string{strconv.Itoa(i + 1), l.ProductSKU, l.ProductName, l.Quantity.String() + " " + l.Unit}
		if showCost {
			cells = append(cells, amountCell(l.UnitCost))
		}
		rows = append(rows, map[string]any{"cells": append(cells, l.Description)})
	}
	title := d.Kind
	if n, ok := stockKinds[d.Kind]; ok {
		title = pick(lang, n)
	}
	var paragraphs []string
	if d.Description != "" {
		paragraphs = []string{d.Description}
	}
	doc := map[string]any{"title": title, "number": strconv.FormatInt(d.Number, 10), "date": docDate(lang, d.Date), "lang": lang,
		"meta": kvs(tr(lang, "Warehouse", "انبار"), d.WarehouseName, tr(lang, "To warehouse", "به انبار"), str(d.ToWarehouseName),
			tr(lang, "Counter account", "حساب مقابل"), str(d.CounterAccountName), tr(lang, "Reference", "شماره مرجع"), d.Reference,
			tr(lang, "Status", "وضعیت"), d.Status),
		"paragraphs": paragraphs,
		"tables":     []map[string]any{{"columns": cols, "rows": rows}},
		"signatures": []string{tr(lang, "Delivered by", "تحویل‌دهنده"), tr(lang, "Received by", "تحویل‌گیرنده")}}
	return json.Marshal(doc)
}
