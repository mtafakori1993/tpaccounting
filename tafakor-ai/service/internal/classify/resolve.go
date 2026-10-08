package classify

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tafakor-ai/internal/extract"
	"github.com/tafakorpardazan/tafakor-ai/internal/learning"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
	"github.com/tafakorpardazan/tafakor-ai/internal/tools"
)

type Option struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Question is ONE targeted question; the recommended option comes first.
type Question struct {
	ID       string   `json:"id"`
	Field    string   `json:"field"`
	Text     string   `json:"text"`
	Options  []Option `json:"options,omitempty"`
	FreeText bool     `json:"free_text,omitempty"`
	Remember bool     `json:"remember,omitempty"`  // offer "remember this answer"
	RuleKind string   `json:"rule_kind,omitempty"` // learning kind used when remembered
	Key      string   `json:"key,omitempty"`       // learning pattern key when remembered
}

type EntryLine struct {
	Side       string `json:"side"` // debit | credit
	Account    string `json:"account"`
	Party      string `json:"party,omitempty"`
	CostCenter string `json:"cost_center,omitempty"`
	Amount     string `json:"amount"`
}

type Display struct {
	DocumentType   string `json:"document_type"`
	Party          string `json:"party,omitempty"`
	Project        string `json:"project,omitempty"`
	Treasury       string `json:"treasury,omitempty"`
	CounterAccount string `json:"counter_account,omitempty"`
	Category       string `json:"category,omitempty"`
	PaymentMethod  string `json:"payment_method,omitempty"`
	Amount         string `json:"amount"`
	Currency       string `json:"currency"`
	OriginalAmount string `json:"original_amount,omitempty"`
	Date           string `json:"date"`
	DateJalali     string `json:"date_jalali"`
	Reference      string `json:"reference,omitempty"`
	Description    string `json:"description,omitempty"`
	Attachment     string `json:"attachment,omitempty"`
	Product        string `json:"product,omitempty"`
	ProductCode    string `json:"product_code,omitempty"`
	Quantity       string `json:"quantity,omitempty"`
	Unit           string `json:"unit,omitempty"`
	UnitValue      string `json:"unit_value,omitempty"`
	Warehouse      string `json:"warehouse,omitempty"`
}

type Flags struct {
	Ambiguous        bool `json:"ambiguous"`
	Conflict         bool `json:"conflict"`
	LowConfidence    bool `json:"low_confidence"`
	ForeignCurrency  bool `json:"foreign_currency"`
	NotRequested     bool `json:"not_requested"`
	FromDocumentText bool `json:"from_document_text"`
	LearnedSuggest   bool `json:"learned_suggest"`
	AssumedDate      bool `json:"assumed_date"`
}

// Choice records what the assistant picked for a field (used to detect user corrections).
type Choice struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Proposal struct {
	Action    string                      `json:"action"`
	EventType string                      `json:"event_type"`
	Tx        *tools.TxArgs               `json:"tx,omitempty"`
	Expense   *tools.ExpenseArgs          `json:"expense,omitempty"`
	Invoice   *tools.InvoiceArgs          `json:"invoice,omitempty"`
	Inventory *tools.InventoryReceiptArgs `json:"inventory,omitempty"`
	Inferred  []string                    `json:"inferred,omitempty"` // fields the assistant derived (shown, never asked)
	Display   Display                     `json:"display"`
	Preview   []EntryLine                 `json:"preview"`
	Question  *Question                   `json:"question,omitempty"`
	Flags     Flags                       `json:"flags"`
	Notes     []string                    `json:"notes,omitempty"`
	UsedRules []int64                     `json:"used_rules,omitempty"`
	Suggested map[string]Choice           `json:"suggested"`
	Stop      string                      `json:"stop,omitempty"`      // cannot proceed; plain-language reason
	StopCode  string                      `json:"stop_code,omitempty"` // set: the fixed flow cannot finish this; the general planner takes over (missing_record, planner)
	Subject   string                      `json:"subject,omitempty"`
	PartyID   int64                       `json:"party_id,omitempty"`
}

// Args returns the tool arguments of the proposal.
func (p *Proposal) Args() any {
	switch {
	case p.Tx != nil:
		return p.Tx
	case p.Expense != nil:
		return p.Expense
	case p.Invoice != nil:
		return p.Invoice
	case p.Inventory != nil:
		return p.Inventory
	}
	return nil
}

// Context is everything resolution needs; all lists come from the API as the user.
type Context struct {
	Lang        string
	Today       string
	UserText    string
	Interp      *Interpretation
	Extractions []*extract.Extraction
	Config      *tools.CompanyConfig
	Parties     []tools.Party
	Accounts    []tools.Account // postable ledger accounts
	Aliases     []store.Alias
	Learn       *learning.Service
	Scope       store.Scope
	Answers     map[string]string
	UploadName  string
	Frame       *Frame // slot statuses (explicit "none" answers)
	Products    []tools.Product
	Warehouses  []tools.Warehouse
}

func (c *Context) tr(en, fa string) string {
	if c.Lang == "fa" {
		return fa
	}
	return en
}

var errQuestion = errors.New("question")

// Resolve builds a proposal. It stops at the first open question.
func Resolve(ctx context.Context, c *Context) (*Proposal, error) {
	if c.Answers == nil {
		c.Answers = map[string]string{}
	}
	ev := c.Interp.Event
	p := &Proposal{EventType: ev.Type, Suggested: map[string]Choice{}, Subject: ev.Subject}
	p.Flags.NotRequested = !c.Interp.UserRequestedRegistration
	for _, x := range c.Extractions {
		if x.Instructions {
			p.Flags.FromDocumentText = true
			p.Notes = append(p.Notes, c.tr("The document contains text that looks like instructions; it was ignored.", "سند حاوی متنی شبیه دستور بود که نادیده گرفته شد."))
		}
	}
	r := &resolver{c: c, p: p, ev: ev}
	if len(c.Extractions) > 0 {
		r.x = c.Extractions[0]
	}
	err := r.run(ctx)
	if errors.Is(err, errQuestion) {
		return p, nil
	}
	return p, err
}

type resolver struct {
	c      *Context
	p      *Proposal
	ev     Event
	x      *extract.Extraction
	amount decimal.Decimal
	date   string
}

func (r *resolver) ask(q Question) error {
	if q.ID == "" {
		q.ID = q.Field
	}
	// recommended option first
	sort.SliceStable(q.Options, func(i, j int) bool { return q.Options[i].Recommended && !q.Options[j].Recommended })
	r.p.Question = &q
	return errQuestion
}

func (r *resolver) answer(field string) (string, bool) {
	v, ok := r.c.Answers[field]
	return v, ok
}

func (r *resolver) run(ctx context.Context) error {
	switch r.ev.Type {
	case EvInventoryIn:
		r.p.Action = "create_inventory_receipt"
		return r.inventory(ctx)
	case EvExpense:
		r.p.Action = "create_expense"
	case EvLoanReceived, EvOwnerCapital, EvCustomerReceipt, EvCustomerAdvance:
		r.p.Action = "create_receipt"
	case EvSupplierPayment:
		r.p.Action = "create_payment"
	case EvPurchase:
		r.p.Action = "create_purchase"
	case EvSalesInvoice:
		r.p.Action = "create_invoice"
	case EvTransfer:
		r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
		r.p.Stop = r.c.tr("Transfer between your own accounts.", "انتقال بین حساب‌های خودتان.")
		return nil
	default:
		if v, ok := r.answer("event_type"); ok && validEvents[v] && v != EvOther {
			r.ev.Type = v
			r.p.EventType = v
			return r.run(ctx)
		}
		return r.ask(Question{Field: "event_type", Text: r.c.tr("What kind of transaction is this?", "این چه نوع تراکنشی است؟"), Options: []Option{
			{Value: EvExpense, Label: r.c.tr("An expense the company paid", "هزینه‌ای که شرکت پرداخت کرده")},
			{Value: EvCustomerReceipt, Label: r.c.tr("Money received from a customer", "دریافت از مشتری")},
			{Value: EvCustomerAdvance, Label: r.c.tr("Advance payment from a customer", "پیش‌دریافت از مشتری")},
			{Value: EvLoanReceived, Label: r.c.tr("A loan the company received", "وام/قرضی که شرکت گرفته")},
			{Value: EvSupplierPayment, Label: r.c.tr("Payment to a supplier", "پرداخت به تامین‌کننده")},
		}})
	}
	if err := r.money(); err != nil {
		return err
	}
	switch r.p.Action {
	case "create_expense":
		return r.expense(ctx)
	case "create_receipt", "create_payment":
		return r.treasuryTx(ctx)
	default:
		return r.invoice(ctx)
	}
}

// ---------- amount, currency, date ----------

func (r *resolver) money() error {
	base := strings.ToUpper(r.c.Config.BaseCurrency)
	if base == "" {
		base = "IRR"
	}
	var userAmt, docAmt decimal.Decimal
	userCur := strings.ToUpper(r.ev.Currency)
	if a, err := normalize.Amount(r.ev.Amount); err == nil {
		userAmt = a
	}
	docCur := ""
	if r.x != nil && r.x.Amount.Valid {
		docAmt, _ = decimal.NewFromString(r.x.Amount.Value)
		docCur = strings.ToUpper(r.x.Currency.Value)
	}
	toBase := func(a decimal.Decimal, cur string) (decimal.Decimal, bool) {
		switch {
		case cur == "" || cur == base:
			return a, true
		case cur == "IRT" && base == "IRR":
			return a.Mul(decimal.NewFromInt(10)), true
		case cur == "IRR" && base == "IRT":
			return a.Div(decimal.NewFromInt(10)), true
		}
		return a, false
	}
	// conflict between instruction and document?
	if userAmt.IsPositive() && docAmt.IsPositive() {
		ub, ok1 := toBase(userAmt, userCur)
		db, ok2 := toBase(docAmt, docCur)
		if ok1 && ok2 && !ub.Equal(db) {
			if v, ok := r.answer("amount_conflict"); ok {
				if v == "user" {
					docAmt = decimal.Zero
				} else {
					userAmt = decimal.Zero
				}
			} else {
				r.p.Flags.Conflict = true
				return r.ask(Question{Field: "amount_conflict",
					Text: r.c.tr(fmt.Sprintf("The document says %s %s but you said %s %s. Which amount is correct?", docAmt, docCur, userAmt, userCur),
						fmt.Sprintf("مبلغ سند %s %s است اما شما %s %s گفتید. کدام درست است؟", docAmt, docCur, userAmt, userCur)),
					Options: []Option{{Value: "document", Label: r.c.tr("The document: ", "مبلغ سند: ") + docAmt.String() + " " + docCur, Recommended: true},
						{Value: "user", Label: r.c.tr("What I said: ", "مبلغی که گفتم: ") + userAmt.String() + " " + userCur}}})
			}
			r.p.Flags.Conflict = true // stays true: a human must see it even after answering
		}
	}
	amt, cur := docAmt, docCur
	if userAmt.IsPositive() && (!docAmt.IsPositive()) {
		amt, cur = userAmt, userCur
	} else if docAmt.IsPositive() && r.x.Amount.Low() && !userAmt.IsPositive() {
		r.p.Flags.LowConfidence = true
	}
	if v, ok := r.answer("amount"); ok {
		if a, err := normalize.Amount(v); err == nil && a.IsPositive() {
			amt, cur = a, base
		}
	}
	if !amt.IsPositive() {
		return r.ask(Question{Field: "amount", FreeText: true, Text: r.c.tr(fmt.Sprintf("What is the amount (in %s)?", base), fmt.Sprintf("مبلغ چقدر است (به %s)؟", base))})
	}
	if cur == "" {
		cur = base
	}
	r.p.Display.Currency = base
	baseAmt, ok := toBase(amt, cur)
	if !ok {
		r.p.Flags.ForeignCurrency = true
		r.p.Display.OriginalAmount = amt.String() + " " + cur
		rate := r.ev.FXRate
		if v, ok := r.answer("fx_rate"); ok {
			rate = v
		}
		rd, err := normalize.Amount(rate)
		if err != nil || !rd.IsPositive() {
			return r.ask(Question{Field: "fx_rate", FreeText: true, Text: r.c.tr(
				fmt.Sprintf("The amount is %s %s. What exchange rate should I use (1 %s = ? %s)?", amt, cur, cur, base),
				fmt.Sprintf("مبلغ %s %s است. با چه نرخی تبدیل کنم (هر %s = چند %s)؟", amt, cur, cur, base))})
		}
		baseAmt = amt.Mul(rd)
		r.p.Notes = append(r.p.Notes, fmt.Sprintf("%s %s × %s = %s %s", amt, cur, rd, baseAmt.Round(int32(r.c.Config.AmountDecimals)), base))
	} else if cur != base {
		r.p.Notes = append(r.p.Notes, r.c.tr(fmt.Sprintf("Converted %s %s to %s %s.", amt, cur, baseAmt, base), fmt.Sprintf("%s %s به %s %s تبدیل شد.", amt, cur, baseAmt, base)))
	}
	r.amount = baseAmt.Round(int32(r.c.Config.AmountDecimals))
	r.p.Display.Amount = r.amount.String()

	// date
	date := ""
	if v, ok := r.answer("date"); ok {
		if t, _, err := normalize.Date(v); err == nil {
			date = t.Format("2006-01-02")
		}
	}
	if date == "" && r.ev.Date != "" {
		if t, _, err := normalize.Date(r.ev.Date); err == nil {
			date = t.Format("2006-01-02")
		}
	}
	if r.x != nil && r.x.Date.Valid {
		if date != "" && date != r.x.Date.Value && !hasAnswer(r.c.Answers, "date_conflict") {
			r.p.Flags.Conflict = true
			return r.ask(Question{Field: "date_conflict", Text: r.c.tr("The document date differs from the date you gave. Which one?", "تاریخ سند با تاریخی که گفتید فرق دارد. کدام؟"),
				Options: []Option{{Value: r.x.Date.Value, Label: r.c.tr("Document date ", "تاریخ سند ") + jal(r.x.Date.Value), Recommended: true},
					{Value: date, Label: r.c.tr("My date ", "تاریخ من ") + jal(date)}}})
		}
		if v, ok := r.answer("date_conflict"); ok {
			date = v
			r.p.Flags.Conflict = true
		}
		if date == "" {
			date = r.x.Date.Value
			if r.x.Date.Low() {
				r.p.Flags.LowConfidence = true
			}
		}
	}
	if date == "" {
		date = r.c.Today
		r.p.Flags.AssumedDate = true
		r.p.Notes = append(r.p.Notes, r.c.tr("No date was given; using today.", "تاریخی ذکر نشد؛ تاریخ امروز استفاده شد."))
	}
	r.date = date
	r.p.Display.Date, r.p.Display.DateJalali = date, jal(date)
	if r.x != nil {
		r.p.Display.Attachment = r.x.FileName
	}
	return nil
}

func hasAnswer(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func jal(iso string) string {
	t, _, err := normalize.Date(iso)
	if err != nil {
		return iso
	}
	return normalize.FormatJalali(t.Year(), int(t.Month()), t.Day())
}

func (r *resolver) reference() string {
	if r.x != nil && r.x.Reference.Valid && r.x.Reference.Value != "" {
		if r.x.Reference.Low() {
			r.p.Flags.LowConfidence = true
		}
		return r.x.Reference.Value
	}
	return normalize.Digits(strings.TrimSpace(r.ev.Reference))
}

func (r *resolver) description() string {
	parts := []string{}
	for _, s := range []string{r.ev.Description, r.ev.Purpose} {
		if s != "" && !contains(parts, s) {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 && r.ev.Subject != "" {
		parts = append(parts, r.ev.Subject)
	}
	d := strings.Join(parts, " — ")
	if r.p.Display.OriginalAmount != "" {
		d = strings.TrimSpace(d + " (" + r.p.Display.OriginalAmount + ")")
	}
	d = normalize.MaskSensitive(d)
	if len([]rune(d)) > 480 {
		d = string([]rune(d)[:480])
	}
	r.p.Display.Description = d
	return d
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- parties ----------

type partyHit struct {
	p     tools.Party
	exact bool
}

func (r *resolver) findParties(name string) []partyHit {
	n := normalize.Text(name)
	if n == "" {
		return nil
	}
	for _, a := range r.c.Aliases {
		if a.Kind == "party" && normalize.Text(a.Alias) == n {
			for _, p := range r.c.Parties {
				if p.ID == a.TargetID {
					return []partyHit{{p, true}}
				}
			}
		}
	}
	var exact, fuzzy []partyHit
	for _, p := range r.c.Parties {
		pn := normalize.Text(p.Name)
		switch {
		case pn == n:
			exact = append(exact, partyHit{p, true})
		case strings.Contains(pn, n) || strings.Contains(n, pn) || similar(pn, n) >= 0.8:
			fuzzy = append(fuzzy, partyHit{p, false})
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return fuzzy
}

// similar is a normalised Levenshtein similarity in [0,1].
func similar(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return 1 - float64(prev[len(rb)])/float64(max(len(ra), len(rb)))
}

func partyLabel(p tools.Party) string {
	roles := []string{}
	if p.IsCustomer {
		roles = append(roles, "customer")
	}
	if p.IsSupplier {
		roles = append(roles, "supplier")
	}
	return fmt.Sprintf("%s (%s %s)", p.Name, p.Code, strings.Join(roles, "/"))
}

// party resolves a party for field; required=false allows "none".
func (r *resolver) party(field, name, role string, required bool) (*tools.Party, error) {
	if v, ok := r.answer(field); ok {
		if v == "none" && !required {
			return nil, nil
		}
		id, _ := strconv.ParseInt(v, 10, 64)
		for _, p := range r.c.Parties {
			if p.ID == id {
				pp := p
				return &pp, nil
			}
		}
	}
	hits := r.findParties(name)
	if role != "" && len(hits) > 1 {
		var filtered []partyHit
		for _, h := range hits {
			if (role == "customer" && h.p.IsCustomer) || (role == "supplier" && h.p.IsSupplier) {
				filtered = append(filtered, h)
			}
		}
		if len(filtered) > 0 && len(filtered) < len(hits) && filtered[0].exact {
			hits = filtered
		}
	}
	if len(hits) == 1 && hits[0].exact {
		pp := hits[0].p
		r.p.Suggested[field] = Choice{pp.ID, pp.Name}
		return &pp, nil
	}
	r.p.Flags.Ambiguous = true
	var opts []Option
	for i, h := range hits {
		if i >= 8 {
			break
		}
		opts = append(opts, Option{Value: strconv.FormatInt(h.p.ID, 10), Label: partyLabel(h.p), Recommended: i == 0 && h.exact && len(hits) == 1})
	}
	if !required {
		opts = append(opts, Option{Value: "none", Label: r.c.tr("No party", "بدون طرف حساب")})
	}
	var text string
	switch {
	case name == "":
		text = r.c.tr("Who is the other party of this transaction?", "طرف حساب این تراکنش کیست؟")
		for i, p := range r.c.Parties {
			if i >= 12 {
				break
			}
			if role == "" || (role == "customer" && p.IsCustomer) || (role == "supplier" && p.IsSupplier) || role == "person" {
				opts = append([]Option{{Value: strconv.FormatInt(p.ID, 10), Label: partyLabel(p)}}, opts...)
			}
		}
	case len(hits) > 1:
		text = r.c.tr(fmt.Sprintf("There are several parties matching %q. Which one?", name), fmt.Sprintf("چند طرف حساب با نام «%s» وجود دارد. کدام؟", name))
	case len(hits) == 1:
		text = r.c.tr(fmt.Sprintf("Did you mean %s?", hits[0].p.Name), fmt.Sprintf("منظورتان «%s» است؟", hits[0].p.Name))
		opts[0].Recommended = true
	default:
		if required && len(opts) == 0 {
			r.p.StopCode = "missing_record"
			r.p.Stop = r.c.tr(fmt.Sprintf("%q is not among your parties yet; I'll create it as part of this work.", name),
				fmt.Sprintf("«%s» هنوز در طرف‌حساب‌ها نیست؛ آن را در همین کار می‌سازم.", name))
			return nil, errQuestion
		}
		text = r.c.tr(fmt.Sprintf("I couldn't find %q among your parties. Which party is it?", name), fmt.Sprintf("«%s» را در طرف‌حساب‌ها پیدا نکردم. کدام طرف حساب است؟", name))
	}
	return nil, r.ask(Question{Field: field, Text: text, Options: opts, Remember: name != "", RuleKind: learning.PartyAlias, Key: name})
}

// ---------- projects ----------

func (r *resolver) project() (*tools.CostCenter, error) {
	if v, ok := r.answer("project"); ok {
		if v == "none" {
			return nil, nil
		}
		id, _ := strconv.ParseInt(v, 10, 64)
		for _, p := range append(r.c.Config.Projects, r.c.Config.CostCenters...) {
			if p.ID == id {
				pp := p
				return &pp, nil
			}
		}
	}
	name := normalize.Text(r.ev.Project)
	if name == "" {
		return nil, nil
	}
	var hits []tools.CostCenter
	for _, p := range r.c.Config.Projects {
		pn, pe := normalize.Text(p.Name), normalize.Text(p.NameEn)
		if strings.Contains(pn, name) || strings.Contains(name, pn) || (pe != "" && (strings.Contains(pe, name) || strings.Contains(name, pe))) ||
			similar(pn, name) > 0.75 || (pe != "" && similar(pe, name) > 0.75) || keywordOverlap(name, pn+" "+pe) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		r.p.Suggested["project"] = Choice{hits[0].ID, hits[0].Name}
		return &hits[0], nil
	}
	r.p.Flags.Ambiguous = true
	var opts []Option
	src := hits
	if len(src) == 0 {
		src = r.c.Config.Projects
	}
	for _, p := range src {
		opts = append(opts, Option{Value: strconv.FormatInt(p.ID, 10), Label: p.Name})
	}
	opts = append(opts, Option{Value: "none", Label: r.c.tr("No project", "بدون پروژه")})
	return nil, r.ask(Question{Field: "project", Text: r.c.tr(fmt.Sprintf("Which project is %q?", r.ev.Project), fmt.Sprintf("«%s» کدام پروژه است؟", r.ev.Project)), Options: opts})
}

// keywordOverlap: a meaningful word (≥4 letters) of the query appears in the target.
func keywordOverlap(q, target string) bool {
	stop := map[string]bool{"project": true, "پروژه": true, "the": true, "for": true}
	for _, w := range strings.Fields(q) {
		if len([]rune(w)) >= 4 && !stop[w] && strings.Contains(target, w) {
			return true
		}
	}
	return false
}

// ---------- treasury ----------

func last4(s string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if len(d) < 4 {
		return ""
	}
	return d[len(d)-4:]
}

func (r *resolver) treasury(ctx context.Context, field, kind string, cardHint string) (*tools.TreasuryAccount, error) {
	var active []tools.TreasuryAccount
	for _, t := range r.c.Config.TreasuryAccounts {
		if t.IsActive && (kind == "" || t.Kind == kind) {
			active = append(active, t)
		}
	}
	if v, ok := r.answer(field); ok {
		id, _ := strconv.ParseInt(v, 10, 64)
		for _, t := range active {
			if t.ID == id {
				tt := t
				return &tt, nil
			}
		}
	}
	pick := func(t tools.TreasuryAccount) *tools.TreasuryAccount {
		r.p.Suggested[field] = Choice{t.ID, t.Name}
		return &t
	}
	// card/account digits from the document
	if l4 := last4(cardHint); l4 != "" {
		for _, t := range active {
			if last4(t.CardNumber) == l4 || last4(t.AccountNumber) == l4 {
				return pick(t), nil
			}
		}
	}
	// learned default for this payment method
	if r.c.Learn != nil && kind != "" {
		m, err := r.c.Learn.Lookup(ctx, r.c.Scope, learning.PaymentDefault, field+":"+kind, "")
		if err == nil && m != nil && m.Stage == "apply" {
			for _, t := range active {
				if t.ID == m.Result.ID {
					r.p.UsedRules = append(r.p.UsedRules, m.Rule.ID)
					return pick(t), nil
				}
			}
		}
	}
	// name hint
	if h := normalize.Text(r.ev.TreasuryHint); h != "" {
		var hits []tools.TreasuryAccount
		for _, t := range active {
			if strings.Contains(normalize.Text(t.Name+" "+t.BankName), h) {
				hits = append(hits, t)
			}
		}
		if len(hits) == 1 {
			return pick(hits[0]), nil
		}
	}
	if len(active) == 1 {
		r.p.Notes = append(r.p.Notes, r.c.tr("Using your only "+kindName(kind, "en")+" account.", "تنها حساب "+kindName(kind, "fa")+" شما استفاده شد."))
		return pick(active[0]), nil
	}
	if len(active) == 0 {
		r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
		r.p.Stop = r.c.tr("You have no active "+kindName(kind, "en")+" account (or no permission to see it).", "هیچ حساب "+kindName(kind, "fa")+" فعالی ندارید (یا مجوز دیدن آن را ندارید).")
		return nil, errQuestion
	}
	// suggest-stage learned default as recommendation
	rec := int64(0)
	if r.c.Learn != nil && kind != "" {
		if m, err := r.c.Learn.Lookup(ctx, r.c.Scope, learning.PaymentDefault, field+":"+kind, ""); err == nil && m != nil {
			rec = m.Result.ID
		}
	}
	var opts []Option
	for _, t := range active {
		label := t.Name
		if t.CardNumber != "" {
			label += " · " + t.CardNumber
		}
		opts = append(opts, Option{Value: strconv.FormatInt(t.ID, 10), Label: label, Recommended: t.ID == rec})
	}
	text := r.c.tr("Which cash/bank account received the money?", "پول به کدام حساب نقد/بانک واریز شد؟")
	if field == "source_account" {
		text = r.c.tr("Which account was the money paid from?", "پول از کدام حساب پرداخت شد؟")
	}
	return nil, r.ask(Question{Field: field, Text: text, Options: opts, Remember: true, RuleKind: learning.PaymentDefault, Key: field + ":" + kind})
}

func kindName(kind, lang string) string {
	switch {
	case kind == "bank" && lang == "fa":
		return "بانکی"
	case kind == "cash" && lang == "fa":
		return "نقدی"
	case lang == "fa":
		return "نقد/بانک"
	case kind == "":
		return "cash/bank"
	}
	return kind
}

// ---------- events ----------

func (r *resolver) treasuryTx(ctx context.Context) error {
	ev := r.ev
	a := &tools.TxArgs{Date: r.date, Amount: r.amount.String()}
	var party *tools.Party
	var err error
	switch ev.Type {
	case EvCustomerReceipt, EvCustomerAdvance:
		name := ev.Counterparty
		if name == "" && r.x != nil {
			name = r.x.Sender.Value
		}
		if party, err = r.party("party", name, "customer", true); err != nil || party == nil {
			return orStop(err)
		}
		a.PartyID = &party.ID
		a.AutoAllocate = ev.Type == EvCustomerReceipt
		if ev.Type == EvCustomerAdvance {
			// An advance is a liability, not a settlement: use the company's own
			// "customer advances" account when exactly one exists (party stays on the line).
			var adv []tools.Account
			for _, acc := range r.c.Accounts {
				n := normalize.Text(acc.Name + " " + acc.NameEn)
				if acc.IsActive && acc.Postable && acc.Type == "liability" && (strings.Contains(n, normalize.Text("پیش‌دریافت")) || strings.Contains(n, "پیش دریافت") || strings.Contains(n, "advance")) {
					adv = append(adv, acc)
				}
			}
			if len(adv) == 1 {
				a.CounterAccountID = &adv[0].ID
				a.AutoAllocate = false
				r.p.Display.CounterAccount = adv[0].Code + " " + adv[0].Name
			} else {
				r.p.Notes = append(r.p.Notes, r.c.tr("No customer-advances account found; the advance stays as a credit on the customer's account.", "حساب پیش‌دریافت پیدا نشد؛ پیش‌دریافت به‌صورت بستانکاری در حساب مشتری می‌ماند."))
			}
		}
	case EvSupplierPayment:
		name := ev.Counterparty
		if name == "" && r.x != nil {
			name = firstNonEmpty(r.x.Recipient.Value, r.x.Merchant.Value)
		}
		if party, err = r.party("party", name, "supplier", true); err != nil || party == nil {
			return orStop(err)
		}
		a.PartyID = &party.ID
		a.AutoAllocate = true
	case EvLoanReceived, EvOwnerCapital:
		name := ev.Counterparty
		if name == "" && r.x != nil {
			name = r.x.Sender.Value
		}
		if party, err = r.party("party", name, "person", false); err != nil {
			return err
		}
		if party != nil {
			a.PartyID = &party.ID
		}
		acc, err := r.counterAccount(ctx, party)
		if err != nil || acc == nil {
			return orStop(err)
		}
		a.CounterAccountID = &acc.ID
		r.p.Display.CounterAccount = acc.Code + " " + acc.Name
	}
	if party != nil {
		r.p.Display.Party = party.Name
		r.p.PartyID = party.ID
	}
	// conflict: document sender/recipient resolves to a different party than the instruction
	if party != nil && r.x != nil && ev.Counterparty != "" {
		docName := r.x.Sender.Value
		if r.p.Action == "create_payment" {
			docName = r.x.Recipient.Value
		}
		if hits := r.findParties(docName); len(hits) == 1 && hits[0].exact && hits[0].p.ID != party.ID && !hasAnswer(r.c.Answers, "party") {
			r.p.Flags.Conflict = true
			return r.ask(Question{Field: "party", Text: r.c.tr(fmt.Sprintf("You said %s but the document shows %s. Which party?", party.Name, hits[0].p.Name),
				fmt.Sprintf("شما «%s» گفتید اما سند «%s» را نشان می‌دهد. کدام؟", party.Name, hits[0].p.Name)),
				Options: []Option{{Value: strconv.FormatInt(party.ID, 10), Label: party.Name, Recommended: true}, {Value: strconv.FormatInt(hits[0].p.ID, 10), Label: hits[0].p.Name}}})
		}
	}
	proj, err := r.project()
	if err != nil {
		return err
	}
	if proj != nil {
		a.CostCenterID = &proj.ID
		r.p.Display.Project = proj.Name
	}
	field, hint := "dest_account", ""
	if r.x != nil {
		hint = firstNonEmpty(r.x.Account.Value, r.x.Card.Value)
	}
	if r.p.Action == "create_payment" {
		field = "source_account"
	}
	t, err := r.treasury(ctx, field, "", hint)
	if err != nil || t == nil {
		return orStop(err)
	}
	a.TreasuryAccountID = t.ID
	r.p.Display.Treasury = t.Name
	a.Reference = r.reference()
	a.Description = r.description()
	r.p.Display.Reference = a.Reference
	r.p.Tx = a
	r.p.Display.DocumentType = map[string]string{"create_receipt": r.c.tr("Treasury receipt", "دریافت (خزانه‌داری)"), "create_payment": r.c.tr("Treasury payment", "پرداخت (خزانه‌داری)")}[r.p.Action]
	partyName, cc := r.p.Display.Party, r.p.Display.Project
	counter := r.c.tr("Receivable — ", "حساب دریافتنی — ") + partyName
	if r.p.Action == "create_payment" {
		counter = r.c.tr("Payable — ", "حساب پرداختنی — ") + partyName
	}
	if a.CounterAccountID != nil {
		counter = r.p.Display.CounterAccount
	}
	treasuryLine := t.AccountCode + " " + t.Name
	if r.p.Action == "create_receipt" {
		r.p.Preview = []EntryLine{{Side: "debit", Account: treasuryLine, Amount: a.Amount}, {Side: "credit", Account: counter, Party: partyName, CostCenter: cc, Amount: a.Amount}}
	} else {
		r.p.Preview = []EntryLine{{Side: "debit", Account: counter, Party: partyName, CostCenter: cc, Amount: a.Amount}, {Side: "credit", Account: treasuryLine, Amount: a.Amount}}
	}
	return nil
}

func orStop(err error) error {
	if err == nil {
		return errQuestion
	}
	return err
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// counterAccount picks the ledger account for loans / capital. Never guessed: an
// applied learned rule, the user's answer, or a question with real candidates.
func (r *resolver) counterAccount(ctx context.Context, party *tools.Party) (*tools.Account, error) {
	byID := func(id int64) *tools.Account {
		for _, a := range r.c.Accounts {
			if a.ID == id && a.IsActive && a.Postable {
				aa := a
				return &aa
			}
		}
		return nil
	}
	if v, ok := r.answer("counter_account"); ok {
		id, _ := strconv.ParseInt(v, 10, 64)
		if a := byID(id); a != nil {
			return a, nil
		}
	}
	keys := []string{r.ev.Type}
	if party != nil {
		keys = append([]string{fmt.Sprintf("%s|party:%d", r.ev.Type, party.ID)}, keys...)
	}
	rec := int64(0)
	if r.c.Learn != nil {
		for _, k := range keys {
			m, err := r.c.Learn.Lookup(ctx, r.c.Scope, learning.EventAccount, k, "")
			if errors.Is(err, learning.ErrConflict) {
				r.p.Flags.Ambiguous = true
				var opts []Option
				for _, c := range m.Conflict {
					opts = append(opts, Option{Value: strconv.FormatInt(c.ID, 10), Label: c.Name})
				}
				return nil, r.ask(Question{Field: "counter_account", Text: r.c.tr("Your saved rules disagree about the account. Which one?", "قواعد ذخیره‌شده درباره حساب اختلاف دارند. کدام؟"), Options: opts, Remember: true, RuleKind: learning.EventAccount, Key: keys[0]})
			}
			if m != nil {
				if a := byID(m.Result.ID); a != nil {
					if m.Stage == "apply" {
						r.p.UsedRules = append(r.p.UsedRules, m.Rule.ID)
						r.p.Suggested["counter_account"] = Choice{a.ID, a.Name}
						return a, nil
					}
					rec = a.ID
					r.p.Flags.LearnedSuggest = true
					break
				}
			}
		}
	}
	typ, words := "liability", []string{"وام", "قرض", "تسهیلات", "شرکا", "سهامدار", "جاری", "سایر حساب‌های پرداختنی", "loan", "borrow", "shareholder", "partner", "director", "other payables"}
	if r.ev.Type == EvOwnerCapital {
		typ, words = "equity", []string{"سرمایه", "شرکا", "جاری", "capital", "partner", "owner"}
	}
	var cands []tools.Account
	for _, a := range r.c.Accounts {
		if !a.IsActive || !a.Postable || a.Type != typ {
			continue
		}
		n := normalize.Text(a.Name + " " + a.NameEn)
		for _, w := range words {
			if strings.Contains(n, normalize.Text(w)) {
				cands = append(cands, a)
				break
			}
		}
	}
	if len(cands) == 0 && rec == 0 {
		r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
		r.p.Stop = r.c.tr("Your chart of accounts has no suitable account for this (e.g. \"Loans from shareholders\"). I won't guess: please create one in Chart of accounts, then ask me again and I'll remember it.",
			"در سرفصل حساب‌ها حساب مناسبی برای این مورد (مثلاً «وام دریافتی از شرکا») وجود ندارد. حدس نمی‌زنم: لطفاً آن را در سرفصل حساب‌ها بسازید و دوباره از من بخواهید تا به خاطر بسپارم.")
		return nil, errQuestion
	}
	r.p.Flags.Ambiguous = rec == 0 || r.p.Flags.Ambiguous
	var opts []Option
	if rec != 0 {
		if a := byID(rec); a != nil {
			opts = append(opts, Option{Value: strconv.FormatInt(a.ID, 10), Label: r.c.tr("Last time: ", "دفعه قبل: ") + a.Code + " " + a.Name, Recommended: true})
		}
	}
	for _, a := range cands {
		if a.ID != rec {
			opts = append(opts, Option{Value: strconv.FormatInt(a.ID, 10), Label: a.Code + " " + a.Name})
		}
	}
	what := r.c.tr("this loan", "این وام")
	if r.ev.Type == EvOwnerCapital {
		what = r.c.tr("this capital contribution", "این آورده سرمایه")
	}
	return nil, r.ask(Question{Field: "counter_account", Text: r.c.tr("Which account should "+what+" be credited to?", "این مبلغ را در کدام حساب برای "+what+" بستانکار کنم؟"), Options: opts, Remember: true, RuleKind: learning.EventAccount, Key: keys[0]})
}

func (r *resolver) expense(ctx context.Context) error {
	ev := r.ev
	a := &tools.ExpenseArgs{Date: r.date, Amount: r.amount.String()}
	cats := map[int64]tools.ExpenseCategory{}
	for _, c := range r.c.Config.ExpenseCategories {
		if c.IsActive {
			cats[c.ID] = c
		}
	}
	if len(cats) == 0 {
		r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
		r.p.Stop = r.c.tr("There are no expense categories you can use.", "دسته هزینه‌ای برای استفاده وجود ندارد.")
		return nil
	}
	var cat *tools.ExpenseCategory
	setCat := func(c tools.ExpenseCategory) { cc := c; cat = &cc }
	if v, ok := r.answer("category"); ok {
		id, _ := strconv.ParseInt(v, 10, 64)
		if c, ok := cats[id]; ok {
			setCat(c)
		}
	}
	text := strings.Join([]string{ev.Subject, ev.Description, ev.Purpose, r.c.UserText}, " ")
	if cat == nil && r.c.Learn != nil {
		m, err := r.c.Learn.Lookup(ctx, r.c.Scope, learning.ExpenseCategory, "", text)
		if errors.Is(err, learning.ErrConflict) {
			r.p.Flags.Ambiguous = true
			var opts []Option
			for _, c := range m.Conflict {
				opts = append(opts, Option{Value: strconv.FormatInt(c.ID, 10), Label: c.Name})
			}
			return r.ask(Question{Field: "category", Text: r.c.tr("Your saved rules disagree about the category. Which one?", "قواعد ذخیره‌شده درباره دسته هزینه اختلاف دارند. کدام؟"), Options: opts, Remember: ev.Subject != "", RuleKind: learning.ExpenseCategory, Key: ev.Subject})
		}
		if m != nil {
			if c, ok := cats[m.Result.ID]; ok {
				if m.Stage == "apply" {
					setCat(c)
					r.p.UsedRules = append(r.p.UsedRules, m.Rule.ID)
					r.p.Notes = append(r.p.Notes, r.c.tr("Category from your learned rule.", "دسته هزینه طبق قاعده آموخته‌شده."))
				} else if model, ok := cats[ev.CategoryID]; !ok || model.ID != c.ID {
					// suggest stage: ask "last time you chose X, same?"
					r.p.Flags.LearnedSuggest = true
					opts := []Option{{Value: strconv.FormatInt(c.ID, 10), Label: c.Name, Recommended: true}}
					if ok {
						opts = append(opts, Option{Value: strconv.FormatInt(model.ID, 10), Label: model.Name})
					}
					r.p.UsedRules = append(r.p.UsedRules, m.Rule.ID)
					return r.ask(Question{Field: "category", Text: r.c.tr(fmt.Sprintf("Last time you chose %q for this. Same again?", c.Name), fmt.Sprintf("دفعه قبل «%s» را انتخاب کردید. همان؟", c.Name)), Options: opts})
				} else {
					setCat(c)
					r.p.Flags.LearnedSuggest = true
					r.p.UsedRules = append(r.p.UsedRules, m.Rule.ID)
				}
			}
		}
	}
	if cat == nil {
		if c, ok := cats[ev.CategoryID]; ok && r.c.Interp.Confidence >= 0.6 {
			setCat(c)
		}
	}
	if cat == nil {
		r.p.Flags.Ambiguous = true
		var opts []Option
		seen := map[int64]bool{}
		for i, id := range append([]int64{ev.CategoryID}, ev.CategoryAltIDs...) {
			if c, ok := cats[id]; ok && !seen[id] {
				seen[id] = true
				opts = append(opts, Option{Value: strconv.FormatInt(id, 10), Label: c.Name, Recommended: i == 0})
			}
		}
		ids := make([]int64, 0, len(cats))
		for id := range cats {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if !seen[id] {
				opts = append(opts, Option{Value: strconv.FormatInt(id, 10), Label: cats[id].Name})
			}
		}
		return r.ask(Question{Field: "category", Text: r.c.tr("Which expense category is this?", "این هزینه در کدام دسته است؟"), Options: opts, Remember: ev.Subject != "", RuleKind: learning.ExpenseCategory, Key: ev.Subject})
	}
	if _, ok := r.p.Suggested["category"]; !ok {
		r.p.Suggested["category"] = Choice{cat.ID, cat.Name}
	}
	a.CategoryID = cat.ID
	r.p.Display.Category = cat.Name

	// who paid?
	paidBy := ev.PaidBy
	if v, ok := r.answer("paid_by"); ok {
		paidBy = v
	}
	cardHint := ""
	if r.x != nil {
		cardHint = r.x.Card.Value
	}
	if (paidBy == "" || paidBy == "unknown") && cardHint != "" {
		for _, t := range r.c.Config.TreasuryAccounts {
			if t.IsActive && last4(cardHint) != "" && (last4(t.CardNumber) == last4(cardHint) || last4(t.AccountNumber) == last4(cardHint)) {
				paidBy = "bank"
			}
		}
	}
	switch paidBy {
	case "company_card", "bank":
		a.PaymentMethod = "bank"
		t, err := r.treasury(ctx, "source_account", "bank", cardHint)
		if err != nil || t == nil {
			return orStop(err)
		}
		a.TreasuryAccountID = &t.ID
		r.p.Display.Treasury = t.Name
	case "cash":
		a.PaymentMethod = "cash"
		t, err := r.treasury(ctx, "source_account", "cash", "")
		if err != nil || t == nil {
			return orStop(err)
		}
		a.TreasuryAccountID = &t.ID
		r.p.Display.Treasury = t.Name
	case "personal":
		a.PaymentMethod = "credit"
		p, err := r.party("paid_by_party", ev.PaidByPerson, "person", true)
		if err != nil || p == nil {
			return orStop(err)
		}
		a.PartyID = &p.ID
		r.p.Display.Party = p.Name
		r.p.PartyID = p.ID
	default:
		rec := ""
		if r.c.Learn != nil {
			if m, _ := r.c.Learn.Lookup(ctx, r.c.Scope, learning.PaymentDefault, "paid_by", ""); m != nil {
				rec = m.Result.Name
			}
		}
		opts := []Option{
			{Value: "company_card", Label: r.c.tr("Company card / bank account", "کارت یا حساب بانکی شرکت"), Recommended: rec == "company_card" || rec == ""},
			{Value: "cash", Label: r.c.tr("Company cash box", "صندوق شرکت"), Recommended: rec == "cash"},
			{Value: "personal", Label: r.c.tr("Someone paid personally (the company owes them)", "شخصی از جیب خود پرداخت کرد (شرکت بدهکار اوست)"), Recommended: rec == "personal"},
		}
		return r.ask(Question{Field: "paid_by", Text: r.c.tr("Who paid for this?", "این هزینه را چه کسی پرداخت کرد؟"), Options: opts, Remember: true, RuleKind: learning.PaymentDefault, Key: "paid_by"})
	}
	r.p.Display.PaymentMethod = a.PaymentMethod
	proj, err := r.project()
	if err != nil {
		return err
	}
	if proj != nil {
		a.CostCenterID = &proj.ID
		r.p.Display.Project = proj.Name
	}
	a.Reference = r.reference()
	a.Description = r.description()
	r.p.Display.Reference = a.Reference
	r.p.Expense = a
	r.p.Display.DocumentType = r.c.tr("Expense (posted)", "هزینه (قطعی)")
	credit := r.p.Display.Treasury
	if a.PaymentMethod == "credit" {
		credit = r.c.tr("Payable — ", "حساب پرداختنی — ") + r.p.Display.Party
	}
	r.p.Preview = []EntryLine{
		{Side: "debit", Account: cat.AccountCode + " " + cat.AccountName, CostCenter: r.p.Display.Project, Amount: a.Amount},
		{Side: "credit", Account: credit, Party: r.p.Display.Party, Amount: a.Amount},
	}
	return nil
}

func (r *resolver) invoice(ctx context.Context) error {
	role := "customer"
	if r.p.Action == "create_purchase" {
		role = "supplier"
	}
	name := r.ev.Counterparty
	if name == "" && r.x != nil {
		name = firstNonEmpty(r.x.Merchant.Value, r.x.Sender.Value)
	}
	party, err := r.party("party", name, role, true)
	if err != nil || party == nil {
		return orStop(err)
	}
	proj, err := r.project()
	if err != nil {
		return err
	}
	desc := firstNonEmpty(r.ev.Subject, r.ev.Description, r.ev.Purpose, r.c.tr("Services", "خدمات"))
	a := &tools.InvoiceArgs{Date: r.date, PartyID: party.ID, Reference: r.reference(),
		Items: []tools.InvoiceItem{{Description: desc, Quantity: "1", UnitPrice: r.amount.String(), TaxRate: "0"}}}
	if proj != nil {
		a.CostCenterID = &proj.ID
		r.p.Display.Project = proj.Name
	}
	r.p.Invoice = a
	r.p.Display.Party, r.p.PartyID = party.Name, party.ID
	r.p.Display.Description = desc
	r.p.Display.DocumentType = map[string]string{"create_invoice": r.c.tr("Sales invoice (draft)", "فاکتور فروش (پیش‌نویس)"), "create_purchase": r.c.tr("Purchase invoice (draft)", "فاکتور خرید (پیش‌نویس)")}[r.p.Action]
	r.p.Notes = append(r.p.Notes, r.c.tr("Invoices are created as drafts; review the lines and tax, then post them in the app.", "فاکتورها به‌صورت پیش‌نویس ساخته می‌شوند؛ اقلام و مالیات را بررسی و سپس در برنامه قطعی کنید."))
	return nil
}

// ---------- inventory: goods added to stock (e.g. manufactured by the company) ----------

var invLabels = map[string][2]string{
	"product_name": {"the product name", "نام کالا"},
	"quantity":     {"how many units", "تعداد"},
	"unit":         {"the unit (e.g. piece, kg)", "واحد (مثلاً عدد یا کیلوگرم)"},
	"unit_value":   {"the value per unit", "ارزش هر واحد"},
}

func (r *resolver) has(field string) bool {
	return r.c.Frame.Has(field)
}

func (r *resolver) inventory(ctx context.Context) error {
	ev := r.ev
	base := strings.ToUpper(r.c.Config.BaseCurrency)
	if base == "" {
		base = "IRR"
	}
	if ev.ManufacturedInternally != nil && !*ev.ManufacturedInternally {
		r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
		r.p.Stop = r.c.tr("Goods bought from a supplier are registered with a purchase invoice (it updates stock and the supplier's balance). Tell me the supplier and I'll prepare the purchase invoice.",
			"کالایی که از تأمین‌کننده خریده شده با فاکتور خرید ثبت می‌شود (موجودی و حساب تأمین‌کننده را به‌روز می‌کند). نام تأمین‌کننده را بگویید تا فاکتور خرید را آماده کنم.")
		return nil
	}
	var missing []string
	name := strings.TrimSpace(firstNonEmpty(ev.ProductName, ev.Subject))
	if name == "" {
		missing = append(missing, "product_name")
	}
	// existing product?
	var product *tools.Product
	if name != "" {
		var hits []tools.Product
		for _, p := range r.c.Products {
			if p.IsActive && normalize.Text(p.Name) == normalize.Text(name) {
				hits = append(hits, p)
			}
		}
		if v, ok := r.answer("product"); ok {
			for _, p := range hits {
				if strconv.FormatInt(p.ID, 10) == v {
					pp := p
					product = &pp
				}
			}
		}
		if product == nil && len(hits) > 1 {
			var opts []Option
			for _, p := range hits {
				opts = append(opts, Option{Value: strconv.FormatInt(p.ID, 10), Label: p.Name + " (" + p.SKU + ")"})
			}
			r.p.Flags.Ambiguous = true
			return r.ask(Question{Field: "product", Text: r.c.tr("Several products have this name. Which one?", "چند کالا با این نام وجود دارد. کدام؟"), Options: opts})
		}
		if product == nil && len(hits) == 1 {
			product = &hits[0]
			r.p.Inferred = append(r.p.Inferred, "product")
			r.p.Notes = append(r.p.Notes, r.c.tr("Using the existing product "+product.Name+".", "از کالای موجود «"+product.Name+"» استفاده می‌شود."))
		}
	}
	qty, qerr := normalize.Amount(ev.Quantity)
	if qerr != nil || !qty.IsPositive() {
		if d, ok := normalize.WordsToNumber(ev.Quantity); ok && d.IsPositive() {
			qty, qerr = d, nil
		}
	}
	if qerr != nil || !qty.IsPositive() {
		missing = append(missing, "quantity")
	}
	unit := strings.TrimSpace(ev.Unit)
	if unit == "" && product != nil && product.Unit != "" {
		unit = product.Unit
		r.p.Inferred = append(r.p.Inferred, "unit")
	}
	if unit == "" && !r.c.Frame.IsNull("unit") {
		missing = append(missing, "unit")
	}
	// value: per unit as stated, or a total divided by the quantity
	cur := strings.ToUpper(ev.Currency)
	toBase := func(d decimal.Decimal) decimal.Decimal {
		switch {
		case cur == "IRT" && base == "IRR":
			return d.Mul(decimal.NewFromInt(10))
		case cur == "IRR" && base == "IRT":
			return d.Div(decimal.NewFromInt(10))
		}
		return d
	}
	var unitValue decimal.Decimal
	if uv, err := normalize.Amount(ev.UnitValue); err == nil && uv.IsPositive() {
		unitValue = toBase(uv)
	} else if tv, err := normalize.Amount(ev.TotalValue); err == nil && tv.IsPositive() && qty.IsPositive() {
		unitValue = toBase(tv).Div(qty)
	} else if uv, ok := normalize.WordsToNumber(ev.UnitValue); ok && uv.IsPositive() {
		unitValue = toBase(uv)
	}
	if !unitValue.IsPositive() {
		missing = append(missing, "unit_value")
	}
	if cur != "" && cur != base && cur != "IRT" && cur != "IRR" {
		r.p.Stop = r.c.tr("Please give the value in "+base+".", "لطفاً ارزش را به "+base+" بگویید.")
		return nil
	}
	if len(missing) > 0 {
		var parts []string
		for _, m := range missing {
			l := invLabels[m]
			parts = append(parts, r.c.tr(l[0], l[1]))
		}
		text := r.c.tr("To add it to stock I still need: "+strings.Join(parts, ", ")+".", "برای افزودن به انبار فقط این موارد را لازم دارم: "+strings.Join(parts, "، ")+".")
		// one question about ONLY the missing fields; the reply is merged into the operation
		return r.ask(Question{ID: "details:" + strings.Join(missing, ","), Field: "details", FreeText: true, Text: text})
	}
	// warehouse: named, "default", or (not mentioned) the default warehouse — inferred, shown in the preview
	var wh *tools.Warehouse
	var actives []tools.Warehouse
	for _, w := range r.c.Warehouses {
		if w.IsActive {
			actives = append(actives, w)
		}
	}
	if v, ok := r.answer("warehouse"); ok {
		for _, w := range actives {
			if strconv.FormatInt(w.ID, 10) == v {
				ww := w
				wh = &ww
			}
		}
	}
	wn := normalize.Text(ev.Warehouse)
	isDefault := wn == "" || wn == "default" || strings.Contains(wn, "پیش فرض") || strings.Contains(wn, "پیشفرض") || strings.Contains(wn, "اصلی") || strings.Contains(wn, "main")
	if wh == nil && !isDefault {
		for _, w := range actives {
			if strings.Contains(normalize.Text(w.Name), wn) || normalize.Text(w.Code) == wn {
				ww := w
				wh = &ww
				break
			}
		}
	}
	if wh == nil {
		for _, w := range actives {
			if w.IsDefault {
				ww := w
				wh = &ww
			}
		}
		if wh == nil && len(actives) == 1 {
			wh = &actives[0]
		}
		if wh != nil && ev.Warehouse == "" {
			r.p.Inferred = append(r.p.Inferred, "warehouse")
		}
	}
	if wh == nil {
		if len(actives) == 0 {
			r.p.StopCode = "planner" // the general planner can do this (create the missing record, transfer, invoice…)
			r.p.Stop = r.c.tr("There is no active warehouse (or you can't see it).", "هیچ انبار فعالی وجود ندارد (یا مجوز دیدن آن را ندارید).")
			return nil
		}
		var opts []Option
		for _, w := range actives {
			opts = append(opts, Option{Value: strconv.FormatInt(w.ID, 10), Label: w.Name})
		}
		return r.ask(Question{Field: "warehouse", Text: r.c.tr("Which warehouse?", "کدام انبار؟"), Options: opts})
	}
	// date
	date := r.c.Today
	if t, _, err := normalize.Date(ev.Date); err == nil {
		date = t.Format("2006-01-02")
	} else {
		r.p.Inferred = append(r.p.Inferred, "date")
	}
	r.date = date
	a := &tools.InventoryReceiptArgs{Date: date, WarehouseID: wh.ID, Quantity: qty.String(), UnitCost: unitValue.Round(4).String()}
	if product != nil {
		a.ProductID = &product.ID
	} else {
		code := strings.TrimSpace(normalize.Digits(ev.ProductCode))
		if code == "" && !r.c.Frame.IsNull("product_code") {
			r.p.Inferred = append(r.p.Inferred, "product_code")
		}
		a.NewProduct = &tools.NewProduct{Name: name, SKU: code, Unit: unit}
	}
	desc := r.c.tr("Production receipt: ", "رسید تولید: ") + name
	if ev.ManufacturedInternally == nil {
		desc = r.c.tr("Stock receipt: ", "رسید انبار: ") + name
	}
	a.Description = desc
	r.p.Inventory = a
	total := qty.Mul(unitValue).Round(int32(r.c.Config.AmountDecimals))
	r.amount = total
	r.p.Display = Display{DocumentType: r.c.tr("Stock receipt (goods produced by the company)", "رسید انبار (کالای تولید داخلی)"),
		Product: name, Quantity: qty.String(), Unit: unit, UnitValue: unitValue.Round(int32(r.c.Config.AmountDecimals)).String(),
		Amount: total.String(), Currency: base, Warehouse: wh.Name, Date: date, DateJalali: jal(date), Description: desc}
	if product != nil {
		r.p.Display.ProductCode = product.SKU // the existing product's own code
	}
	if product == nil {
		switch {
		case r.c.Frame.IsNull("product_code"):
			r.p.Display.ProductCode = r.c.tr("none (numbered by the app)", "ندارد (شماره‌گذاری خودکار)")
		case a.NewProduct.SKU != "":
			r.p.Display.ProductCode = a.NewProduct.SKU
		default:
			r.p.Display.ProductCode = r.c.tr("numbered by the app", "شماره‌گذاری خودکار")
		}
		r.p.Notes = append(r.p.Notes, r.c.tr("A new product \""+name+"\" will be created (goods, stock tracked).", "کالای جدید «"+name+"» ساخته می‌شود (کالا، با کنترل موجودی)."))
	}
	if ev.ManufacturedInternally == nil {
		r.p.Notes = append(r.p.Notes, r.c.tr("Recorded as goods added to stock (not a purchase).", "به‌عنوان ورود کالا به انبار ثبت می‌شود (نه خرید)."))
	}
	if contains(r.p.Inferred, "warehouse") {
		r.p.Notes = append(r.p.Notes, r.c.tr("Warehouse: the default warehouse ("+wh.Name+").", "انبار: انبار پیش‌فرض («"+wh.Name+"»)."))
	}
	r.p.Preview = []EntryLine{
		{Side: "debit", Account: r.c.tr("Inventory (posting rule)", "موجودی کالا (طبق قواعد ثبت)"), Amount: total.String()},
		{Side: "credit", Account: r.c.tr("Inventory adjustment / production (posting rule)", "تعدیل موجودی / تولید (طبق قواعد ثبت)"), Amount: total.String()},
	}
	r.p.PartyID = 0
	r.p.Subject = name
	return nil
}
