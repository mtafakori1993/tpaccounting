package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
)

func writeTools() []Tool {
	return []Tool{
		{Name: "create_receipt", Kind: Write, Permissions: []string{"treasury.create", "treasury.post"}, Run: createTx("receipt")},
		{Name: "create_payment", Kind: Write, Permissions: []string{"treasury.create", "treasury.post"}, Run: createTx("payment")},
		{Name: "create_expense", Kind: Write, Permissions: []string{"expenses.create", "expenses.post"}, Run: createExpense},
		{Name: "create_invoice", Kind: Write, Permissions: []string{"sales.create"}, Run: createInvoice("sales")},
		{Name: "create_purchase", Kind: Write, Permissions: []string{"purchases.create"}, Run: createInvoice("purchases")},
		{Name: "create_journal_entry", Kind: Write, Permissions: []string{"journal.create"}, Run: createJournal},
		{Name: "attach_document", Kind: Write, Run: attachDocument},
		{Name: "generate_pdf", Kind: Write, Run: generatePDF},
		{Name: "cancel_document", Kind: Write, Run: cancelDocument},
		{Name: "create_inventory_receipt", Kind: Write, Permissions: []string{"inventory.create", "inventory.post"}, Run: createInventoryReceipt},
	}
}

func validDate(s string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return fmt.Errorf("%w: date must be YYYY-MM-DD", ErrInvalidInput)
	}
	return nil
}

func validAmount(s string, decimals int) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil || !d.IsPositive() {
		return d, fmt.Errorf("%w: amount must be a positive number", ErrInvalidInput)
	}
	if !d.Equal(d.Round(int32(decimals))) {
		return d, fmt.Errorf("%w: amount has too many decimals", ErrInvalidInput)
	}
	if d.GreaterThan(decimal.New(1, 18)) {
		return d, fmt.Errorf("%w: amount too large", ErrInvalidInput)
	}
	return d, nil
}

func positive(id *int64, field string) error {
	if id != nil && *id <= 0 {
		return fmt.Errorf("%w: %s", ErrInvalidInput, field)
	}
	return nil
}

func cleanText(s string, max int) (string, error) {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' {
			return -1
		}
		return r
	}, s))
	if len([]rune(s)) > max {
		return "", fmt.Errorf("%w: text too long", ErrInvalidInput)
	}
	return s, nil
}

func decimalsOf(env Env) int {
	if env.Me != nil {
		return env.Me.Company.AmountDecimals
	}
	return 0
}

func budgetHeader(confirm bool) map[string]string {
	if confirm {
		return map[string]string{"X-Budget-Confirm": "true"}
	}
	return nil
}

// TxArgs is the input of create_receipt / create_payment.
type TxArgs struct {
	Date              string `json:"date"`
	TreasuryAccountID int64  `json:"treasury_account_id"`
	PartyID           *int64 `json:"party_id"`
	CounterAccountID  *int64 `json:"counter_account_id"`
	CostCenterID      *int64 `json:"cost_center_id"`
	Amount            string `json:"amount"`
	Description       string `json:"description"`
	Reference         string `json:"reference"`
	AutoAllocate      bool   `json:"auto_allocate"`
	BudgetConfirm     bool   `json:"budget_confirm"`
}

func (a *TxArgs) Validate(decimals int) error {
	if err := validDate(a.Date); err != nil {
		return err
	}
	if a.TreasuryAccountID <= 0 {
		return fmt.Errorf("%w: treasury_account_id required", ErrInvalidInput)
	}
	if a.PartyID == nil && a.CounterAccountID == nil {
		return fmt.Errorf("%w: party_id or counter_account_id required", ErrInvalidInput)
	}
	for f, id := range map[string]*int64{"party_id": a.PartyID, "counter_account_id": a.CounterAccountID, "cost_center_id": a.CostCenterID} {
		if err := positive(id, f); err != nil {
			return err
		}
	}
	if _, err := validAmount(a.Amount, decimals); err != nil {
		return err
	}
	var err error
	if a.Description, err = cleanText(a.Description, 500); err != nil {
		return err
	}
	if a.Reference, err = cleanText(a.Reference, 100); err != nil {
		return err
	}
	return nil
}

func createTx(kind string) func(context.Context, Env, json.RawMessage) (any, error) {
	return func(ctx context.Context, env Env, args json.RawMessage) (any, error) {
		var a TxArgs
		if err := decodeStrict(args, &a); err != nil {
			return nil, err
		}
		if err := a.Validate(decimalsOf(env)); err != nil {
			return nil, err
		}
		body := map[string]any{
			"kind": kind, "date": a.Date, "treasury_account_id": a.TreasuryAccountID, "party_id": a.PartyID,
			"counter_account_id": a.CounterAccountID, "cost_center_id": a.CostCenterID, "amount": a.Amount,
			"description": a.Description, "reference": a.Reference, "auto_allocate": a.AutoAllocate && a.CounterAccountID == nil,
			"allocations": []any{},
		}
		if a.CounterAccountID != nil {
			body["party_id"] = nil // the API books against either a party or an explicit account
			if a.PartyID != nil {
				body["party_id"] = a.PartyID
			}
		}
		var t Transaction
		err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/treasury/transactions", Body: body, Header: budgetHeader(a.BudgetConfirm)}, &t)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
}

// ExpenseArgs is the input of create_expense.
type ExpenseArgs struct {
	Date              string `json:"date"`
	CategoryID        int64  `json:"category_id"`
	Amount            string `json:"amount"`
	Description       string `json:"description"`
	Reference         string `json:"reference"`
	CostCenterID      *int64 `json:"cost_center_id"`
	PaymentMethod     string `json:"payment_method"` // cash | bank | credit
	TreasuryAccountID *int64 `json:"treasury_account_id"`
	PartyID           *int64 `json:"party_id"`
	BudgetConfirm     bool   `json:"budget_confirm"`
}

func (a *ExpenseArgs) Validate(decimals int) error {
	if err := validDate(a.Date); err != nil {
		return err
	}
	if a.CategoryID <= 0 {
		return fmt.Errorf("%w: category_id required", ErrInvalidInput)
	}
	if _, err := validAmount(a.Amount, decimals); err != nil {
		return err
	}
	switch a.PaymentMethod {
	case "cash", "bank":
		if a.TreasuryAccountID == nil || *a.TreasuryAccountID <= 0 {
			return fmt.Errorf("%w: treasury_account_id required", ErrInvalidInput)
		}
	case "credit":
		if a.PartyID == nil || *a.PartyID <= 0 {
			return fmt.Errorf("%w: party_id required for credit", ErrInvalidInput)
		}
		a.TreasuryAccountID = nil
	default:
		return fmt.Errorf("%w: payment_method must be cash, bank or credit", ErrInvalidInput)
	}
	for f, id := range map[string]*int64{"party_id": a.PartyID, "cost_center_id": a.CostCenterID} {
		if err := positive(id, f); err != nil {
			return err
		}
	}
	var err error
	if a.Description, err = cleanText(a.Description, 500); err != nil {
		return err
	}
	if a.Reference, err = cleanText(a.Reference, 100); err != nil {
		return err
	}
	return nil
}

func createExpense(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a ExpenseArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := a.Validate(decimalsOf(env)); err != nil {
		return nil, err
	}
	body := map[string]any{
		"date": a.Date, "category_id": a.CategoryID, "amount": a.Amount, "description": a.Description, "reference": a.Reference,
		"cost_center_id": a.CostCenterID, "payment_method": a.PaymentMethod, "treasury_account_id": a.TreasuryAccountID,
		"party_id": a.PartyID, "post": true,
	}
	var x Expense
	if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/expenses", Body: body, Header: budgetHeader(a.BudgetConfirm)}, &x); err != nil {
		return nil, err
	}
	return &x, nil
}

type InvoiceItem struct {
	ProductID   *int64 `json:"product_id"`
	Description string `json:"description"`
	Quantity    string `json:"quantity"`
	Unit        string `json:"unit"`
	UnitPrice   string `json:"unit_price"`
	TaxRate     string `json:"tax_rate"`
}

type InvoiceArgs struct {
	Date         string        `json:"date"`
	PartyID      int64         `json:"party_id"`
	CostCenterID *int64        `json:"cost_center_id"`
	Reference    string        `json:"reference"`
	Notes        string        `json:"notes"`
	Items        []InvoiceItem `json:"items"`
}

// createInvoice creates a DRAFT sales or purchase invoice (posting stays a human action in the app).
func createInvoice(side string) func(context.Context, Env, json.RawMessage) (any, error) {
	return func(ctx context.Context, env Env, args json.RawMessage) (any, error) {
		var a InvoiceArgs
		if err := decodeStrict(args, &a); err != nil {
			return nil, err
		}
		if err := validDate(a.Date); err != nil {
			return nil, err
		}
		if a.PartyID <= 0 || len(a.Items) == 0 || len(a.Items) > 100 {
			return nil, fmt.Errorf("%w: party and 1–100 items required", ErrInvalidInput)
		}
		items := make([]map[string]any, 0, len(a.Items))
		for _, it := range a.Items {
			desc, err := cleanText(it.Description, 300)
			if err != nil {
				return nil, err
			}
			q, err := decimal.NewFromString(it.Quantity)
			if err != nil || !q.IsPositive() {
				return nil, fmt.Errorf("%w: quantity", ErrInvalidInput)
			}
			if _, err := validAmount(it.UnitPrice, 4); err != nil {
				return nil, err
			}
			tax := it.TaxRate
			if tax == "" {
				tax = "0"
			}
			if _, err := decimal.NewFromString(tax); err != nil {
				return nil, fmt.Errorf("%w: tax_rate", ErrInvalidInput)
			}
			if it.ProductID == nil && desc == "" {
				return nil, fmt.Errorf("%w: item needs a product or a description", ErrInvalidInput)
			}
			items = append(items, map[string]any{"product_id": it.ProductID, "description": desc, "quantity": it.Quantity,
				"unit": it.Unit, "unit_price": it.UnitPrice, "discount": "0", "tax_rate": tax})
		}
		ref, err := cleanText(a.Reference, 100)
		if err != nil {
			return nil, err
		}
		notes, err := cleanText(a.Notes, 1000)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"date": a.Date, "party_id": a.PartyID, "cost_center_id": a.CostCenterID, "reference": ref, "notes": notes,
			"items": items, "post": false}
		var inv Invoice
		if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/" + side + "/invoices", Body: body}, &inv); err != nil {
			return nil, err
		}
		return &inv, nil
	}
}

type JournalLine struct {
	AccountID    int64  `json:"account_id"`
	PartyID      *int64 `json:"party_id"`
	CostCenterID *int64 `json:"cost_center_id"`
	Description  string `json:"description"`
	Debit        string `json:"debit"`
	Credit       string `json:"credit"`
}

type JournalArgs struct {
	Date        string        `json:"date"`
	Description string        `json:"description"`
	Reference   string        `json:"reference"`
	Lines       []JournalLine `json:"lines"`
}

// createJournal creates a DRAFT manual journal entry (never posted by the assistant).
func createJournal(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a JournalArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if err := validDate(a.Date); err != nil {
		return nil, err
	}
	if len(a.Lines) < 2 || len(a.Lines) > 200 {
		return nil, fmt.Errorf("%w: 2–200 lines required", ErrInvalidInput)
	}
	dr, cr := decimal.Zero, decimal.Zero
	lines := make([]map[string]any, 0, len(a.Lines))
	for _, l := range a.Lines {
		if l.AccountID <= 0 {
			return nil, fmt.Errorf("%w: account_id", ErrInvalidInput)
		}
		d, e1 := decimal.NewFromString(orZero(l.Debit))
		c, e2 := decimal.NewFromString(orZero(l.Credit))
		if e1 != nil || e2 != nil || d.IsNegative() || c.IsNegative() || (d.IsZero() == c.IsZero()) {
			return nil, fmt.Errorf("%w: each line needs exactly one positive debit or credit", ErrInvalidInput)
		}
		desc, err := cleanText(l.Description, 300)
		if err != nil {
			return nil, err
		}
		dr, cr = dr.Add(d), cr.Add(c)
		lines = append(lines, map[string]any{"account_id": l.AccountID, "party_id": l.PartyID, "cost_center_id": l.CostCenterID,
			"description": desc, "debit": d.String(), "credit": c.String()})
	}
	if !dr.Equal(cr) {
		return nil, fmt.Errorf("%w: debits and credits must balance", ErrInvalidInput)
	}
	desc, err := cleanText(a.Description, 500)
	if err != nil {
		return nil, err
	}
	ref, err := cleanText(a.Reference, 100)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"date": a.Date, "description": desc, "reference": ref, "lines": lines, "post": false}
	var out map[string]any
	if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/journal-entries", Body: body}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func orZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

// AttachEntities: every record type the application's attachments API accepts
// (backend attachments.entityTables).
var AttachEntities = map[string]bool{"treasury_transaction": true, "expense": true, "invoice": true, "journal_entry": true, "party": true,
	"stock_document": true, "check": true, "employee": true, "payroll_period": true, "budget": true}

// typeExt: the extension the application expects for each document type it accepts.
var typeExt = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif", "application/pdf": ".pdf"}

// AttachmentName is the name the original file is stored under in the application. The
// app accepts a file only by its extension, so a pasted or camera image named "blob" or
// "image" gets the extension of its real (sniffed) type.
func AttachmentName(name string, content []byte) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	ct := http.DetectContentType(content)
	if i := strings.Index(ct, ";"); i > 0 {
		ct = ct[:i]
	}
	want, known := typeExt[ct]
	ext := strings.ToLower(filepath.Ext(name))
	if !known || ext == want || (want == ".jpg" && ext == ".jpeg") {
		return name
	}
	if name == "" || name == "." || name == "/" {
		name = "document"
	}
	return name + want
}

type AttachArgs struct {
	EntityType string `json:"entity_type"`
	EntityID   int64  `json:"entity_id"`
	UploadID   string `json:"upload_id"`
}

func attachDocument(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a AttachArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if !AttachEntities[a.EntityType] || a.EntityID <= 0 || a.UploadID == "" {
		return nil, fmt.Errorf("%w: entity and upload required", ErrInvalidInput)
	}
	if env.Files == nil || env.Me == nil {
		return nil, fmt.Errorf("%w: no file store", ErrInvalidInput)
	}
	name, content, err := env.Files.Read(ctx, env.Me.Company.ID, env.Me.ID, a.UploadID)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := env.Acc.Upload(ctx, env.Cred, a.EntityType, a.EntityID, AttachmentName(name, content), content, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PDFArgs renders a printable document through the app's own PDF service (letterhead included).
type PDFArgs struct {
	Document json.RawMessage `json:"document"`
	FileName string          `json:"file_name"`
}

func generatePDF(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a PDFArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	if len(a.Document) == 0 || len(a.Document) > 200_000 {
		return nil, fmt.Errorf("%w: document required", ErrInvalidInput)
	}
	var doc map[string]any
	if err := json.Unmarshal(a.Document, &doc); err != nil {
		return nil, fmt.Errorf("%w: document", ErrInvalidInput)
	}
	pdf, ctype, err := env.Acc.Raw(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: "/pdf", Body: doc})
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(ctype, "application/pdf") {
		return nil, fmt.Errorf("unexpected PDF response")
	}
	if env.Files == nil || env.Me == nil {
		return nil, fmt.Errorf("%w: no file store", ErrInvalidInput)
	}
	name := strings.TrimSpace(a.FileName)
	if name == "" {
		name = "document"
	}
	id, err := env.Files.Save(ctx, env.Me.Company.ID, env.Me.ID, filepath.Base(name)+".pdf", "application/pdf", pdf)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file_id": id, "url": "/ai-api/files/" + id, "size": len(pdf)}, nil
}

// CancelArgs undoes a registered document with the app's own cancel-by-reversal.
type CancelArgs struct {
	EntityType string `json:"entity_type"` // treasury_transaction | expense
	EntityID   int64  `json:"entity_id"`
}

func cancelDocument(ctx context.Context, env Env, args json.RawMessage) (any, error) {
	var a CancelArgs
	if err := decodeStrict(args, &a); err != nil {
		return nil, err
	}
	var path, perm string
	switch a.EntityType {
	case "treasury_transaction":
		path, perm = fmt.Sprintf("/treasury/transactions/%d/cancel", a.EntityID), "treasury.cancel"
	case "expense":
		path, perm = fmt.Sprintf("/expenses/%d/cancel", a.EntityID), "expenses.cancel"
	default:
		return nil, fmt.Errorf("%w: entity_type", ErrInvalidInput)
	}
	if a.EntityID <= 0 {
		return nil, fmt.Errorf("%w: entity_id", ErrInvalidInput)
	}
	if env.Me != nil && !env.Me.Can(perm) {
		return nil, fmt.Errorf("%w (%s)", ErrNoPermission, perm)
	}
	var out map[string]any
	if err := env.Acc.Do(ctx, env.Cred, accounting.Call{Method: http.MethodPost, Path: path, Body: map[string]any{}}, &out); err != nil {
		return nil, err
	}
	return out, nil
}
