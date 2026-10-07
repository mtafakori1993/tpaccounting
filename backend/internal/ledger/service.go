// Package ledger implements journal entries: the double-entry core every other
// module posts through. All functions take a db.DBTX so callers can compose
// them inside a single database transaction.
package ledger

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/audit"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
)

const (
	StatusDraft     = "draft"
	StatusPosted    = "posted"
	StatusCancelled = "cancelled"

	SourceManual   = "manual"
	SourceOpening  = "opening"
	SourceClosing  = "closing"
	SourceReversal = "reversal"
)

type LineInput struct {
	AccountID    int64           `json:"account_id"`
	PartyID      *int64          `json:"party_id"`
	CostCenterID *int64          `json:"cost_center_id"`
	Description  string          `json:"description"`
	Debit        decimal.Decimal `json:"debit"`
	Credit       decimal.Decimal `json:"credit"`
}

type EntryInput struct {
	Number      int64       `json:"number"` // 0 = assign automatically
	Date        civil.Date  `json:"date"`
	Description string      `json:"description"`
	Reference   string      `json:"reference"`
	Lines       []LineInput `json:"lines"`
	Post        bool        `json:"post"`
}

// Source identifies the business document that generated an entry.
type Source struct {
	Type string
	ID   int64
}

// Validate checks the structural accounting rules of an entry. When posting,
// the entry must have at least two lines and total debit must equal total credit.
func Validate(in EntryInput, places int32, posting bool) error {
	v := apperr.NewValidator()
	v.Check(!in.Date.IsZero(), "date", "required")
	v.MaxLen("description", in.Description, 500)
	v.MaxLen("reference", in.Reference, 100)
	v.Check(in.Number >= 0, "number", "invalid")
	minLines := 1
	if posting {
		minLines = 2
	}
	v.Check(len(in.Lines) >= minLines, "lines", "min_lines")
	v.Check(len(in.Lines) <= 1000, "lines", "too_many")
	for i, l := range in.Lines {
		f := fmt.Sprintf("lines.%d.", i)
		v.RequiredID(f+"account_id", l.AccountID)
		v.Check(!l.Debit.IsNegative() && !l.Credit.IsNegative(), f+"debit", "negative")
		v.Check(l.Debit.IsZero() != l.Credit.IsZero(), f+"debit", "debit_or_credit")
		v.Check(l.Debit.Equal(l.Debit.Round(places)) && l.Credit.Equal(l.Credit.Round(places)), f+"debit", "too_many_decimals")
		v.MaxLen(f+"description", l.Description, 500)
	}
	if err := v.Err(); err != nil {
		return err
	}
	if posting {
		d, c := Totals(in.Lines)
		if !d.Equal(c) {
			return apperr.Rule("unbalanced", "total debit must equal total credit").
				WithParams("debit", d.String(), "credit", c.String(), "difference", d.Sub(c).Abs().String())
		}
	}
	return nil
}

func Totals(lines []LineInput) (debit, credit decimal.Decimal) {
	for _, l := range lines {
		debit = debit.Add(l.Debit)
		credit = credit.Add(l.Credit)
	}
	return
}

// checkReferences verifies that accounts are postable and that parties and cost
// centers belong to the company and are active.
func checkReferences(ctx context.Context, q db.DBTX, companyID int64, lines []LineInput) error {
	accIDs, partyIDs, ccIDs := []int64{}, []int64{}, []int64{}
	for _, l := range lines {
		accIDs = append(accIDs, l.AccountID)
		if l.PartyID != nil {
			partyIDs = append(partyIDs, *l.PartyID)
		}
		if l.CostCenterID != nil {
			ccIDs = append(ccIDs, *l.CostCenterID)
		}
	}
	type accState struct{ active, postable bool }
	accounts := map[int64]accState{}
	rows, err := q.Query(ctx, `
		SELECT a.id, a.is_active, a.level >= 2 AND NOT EXISTS (SELECT 1 FROM accounts c WHERE c.parent_id = a.id)
		FROM accounts a WHERE a.company_id = $1 AND a.id = ANY($2)`, companyID, accIDs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var s accState
		if err := rows.Scan(&id, &s.active, &s.postable); err != nil {
			rows.Close()
			return err
		}
		accounts[id] = s
	}
	rows.Close()
	active := func(table string, ids []int64) (map[int64]bool, error) {
		out := map[int64]bool{}
		if len(ids) == 0 {
			return out, nil
		}
		rows, err := q.Query(ctx, `SELECT id, is_active FROM `+table+` WHERE company_id = $1 AND id = ANY($2)`, companyID, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var a bool
			if err := rows.Scan(&id, &a); err != nil {
				return nil, err
			}
			out[id] = a
		}
		return out, rows.Err()
	}
	parties, err := active("parties", partyIDs)
	if err != nil {
		return err
	}
	ccs, err := active("cost_centers", ccIDs)
	if err != nil {
		return err
	}
	v := apperr.NewValidator()
	for i, l := range lines {
		f := fmt.Sprintf("lines.%d.", i)
		s, ok := accounts[l.AccountID]
		v.Check(ok, f+"account_id", "invalid")
		v.Check(!ok || s.active, f+"account_id", "inactive_account")
		v.Check(!ok || s.postable, f+"account_id", "not_postable")
		if l.PartyID != nil {
			a, ok := parties[*l.PartyID]
			v.Check(ok, f+"party_id", "invalid")
			v.Check(!ok || a, f+"party_id", "inactive")
		}
		if l.CostCenterID != nil {
			a, ok := ccs[*l.CostCenterID]
			v.Check(ok, f+"cost_center_id", "invalid")
			v.Check(!ok || a, f+"cost_center_id", "inactive")
		}
	}
	return v.Err()
}

func insertLines(ctx context.Context, q db.DBTX, entryID int64, lines []LineInput) error {
	for i, l := range lines {
		if _, err := q.Exec(ctx, `
			INSERT INTO journal_entry_lines (entry_id, line_no, account_id, party_id, cost_center_id, description, debit, credit)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			entryID, i+1, l.AccountID, l.PartyID, l.CostCenterID, strings.TrimSpace(l.Description), l.Debit, l.Credit); err != nil {
			return err
		}
	}
	return nil
}

func normalise(in *EntryInput) {
	in.Description = strings.TrimSpace(in.Description)
	in.Reference = strings.TrimSpace(in.Reference)
}

// Create inserts a new entry (draft, or posted when in.Post is set) and returns its id.
func Create(ctx context.Context, q db.DBTX, companyID, userID int64, in EntryInput, src Source) (int64, error) {
	normalise(&in)
	places, err := Decimals(ctx, q, companyID)
	if err != nil {
		return 0, err
	}
	if err := Validate(in, places, in.Post); err != nil {
		return 0, err
	}
	fyID, err := ResolveFiscalYear(ctx, q, companyID, in.Date)
	if err != nil {
		return 0, err
	}
	if err := checkReferences(ctx, q, companyID, in.Lines); err != nil {
		return 0, err
	}
	number := in.Number
	if number > 0 {
		if err := db.BumpNumber(ctx, q, companyID, "journal", fyID, number); err != nil {
			return 0, err
		}
	} else if number, err = db.NextNumber(ctx, q, companyID, "journal", fyID); err != nil {
		return 0, err
	}
	if src.Type == "" {
		src.Type = SourceManual
	}
	var srcID *int64
	if src.ID > 0 {
		srcID = &src.ID
	}
	d, c := Totals(in.Lines)
	var id int64
	if err := q.QueryRow(ctx, `
		INSERT INTO journal_entries (company_id, fiscal_year_id, number, date, description, reference, status,
		                             source_type, source_id, total_debit, total_credit, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, 'draft', $7, $8, $9, $10, $11) RETURNING id`,
		companyID, fyID, number, in.Date, in.Description, in.Reference, src.Type, srcID, d, c, userID).Scan(&id); err != nil {
		return 0, err
	}
	if err := insertLines(ctx, q, id, in.Lines); err != nil {
		return 0, err
	}
	if in.Post {
		if err := markPosted(ctx, q, companyID, id, userID, d, c, src); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// OnPost, when set, runs inside the posting transaction right after an entry
// is posted (budget control). Returning an error rejects the posting.
var OnPost func(ctx context.Context, q db.DBTX, companyID, entryID int64, src Source) error

func markPosted(ctx context.Context, q db.DBTX, companyID, id, userID int64, d, c decimal.Decimal, src Source) error {
	if _, err := q.Exec(ctx, `
		UPDATE journal_entries SET status = 'posted', posted_at = now(), posted_by = $2,
		       total_debit = $3, total_credit = $4, updated_at = now()
		WHERE id = $1`, id, userID, d, c); err != nil {
		return err
	}
	if OnPost != nil {
		return OnPost(ctx, q, companyID, id, src)
	}
	return nil
}

type header struct {
	Status       string
	SourceType   string
	FiscalYearID int64
	Number       int64
	Date         civil.Date
	ReversalOf   *int64
	Description  string
	Reference    string
}

func lockHeader(ctx context.Context, q db.DBTX, companyID, id int64) (header, error) {
	var h header
	err := q.QueryRow(ctx, `
		SELECT status, source_type, fiscal_year_id, number, date, reversal_of_id, description, reference
		FROM journal_entries WHERE id = $1 AND company_id = $2 FOR UPDATE`, id, companyID).
		Scan(&h.Status, &h.SourceType, &h.FiscalYearID, &h.Number, &h.Date, &h.ReversalOf, &h.Description, &h.Reference)
	if db.IsNoRows(err) {
		return h, apperr.NotFound("journal_entry")
	}
	return h, err
}

func loadLineInputs(ctx context.Context, q db.DBTX, id int64) ([]LineInput, error) {
	rows, err := q.Query(ctx, `
		SELECT account_id, party_id, cost_center_id, description, debit, credit
		FROM journal_entry_lines WHERE entry_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LineInput
	for rows.Next() {
		var l LineInput
		if err := rows.Scan(&l.AccountID, &l.PartyID, &l.CostCenterID, &l.Description, &l.Debit, &l.Credit); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Update replaces a manual draft entry. Posted entries can never be edited.
func Update(ctx context.Context, q db.DBTX, companyID, userID, id int64, in EntryInput) error {
	normalise(&in)
	h, err := lockHeader(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if h.Status != StatusDraft {
		return apperr.Rule("not_draft", "only draft entries can be edited")
	}
	if h.SourceType != SourceManual {
		return apperr.Rule("edit_via_source", "this entry is generated by another document")
	}
	places, err := Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	if err := Validate(in, places, in.Post); err != nil {
		return err
	}
	fyID, err := ResolveFiscalYear(ctx, q, companyID, in.Date)
	if err != nil {
		return err
	}
	if err := checkReferences(ctx, q, companyID, in.Lines); err != nil {
		return err
	}
	number := h.Number
	switch {
	case in.Number > 0 && (in.Number != h.Number || fyID != h.FiscalYearID):
		number = in.Number
		if err := db.BumpNumber(ctx, q, companyID, "journal", fyID, number); err != nil {
			return err
		}
	case fyID != h.FiscalYearID:
		if number, err = db.NextNumber(ctx, q, companyID, "journal", fyID); err != nil {
			return err
		}
	}
	d, c := Totals(in.Lines)
	if _, err := q.Exec(ctx, `
		UPDATE journal_entries SET fiscal_year_id = $2, number = $3, date = $4, description = $5, reference = $6,
		       total_debit = $7, total_credit = $8, updated_at = now()
		WHERE id = $1`, id, fyID, number, in.Date, in.Description, in.Reference, d, c); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `DELETE FROM journal_entry_lines WHERE entry_id = $1`, id); err != nil {
		return err
	}
	if err := insertLines(ctx, q, id, in.Lines); err != nil {
		return err
	}
	if in.Post {
		return markPosted(ctx, q, companyID, id, userID, d, c, Source{Type: SourceManual})
	}
	return nil
}

// Post validates and posts a draft entry.
func Post(ctx context.Context, q db.DBTX, companyID, userID, id int64) error {
	h, err := lockHeader(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if h.Status != StatusDraft {
		return apperr.Rule("not_draft", "only draft entries can be posted")
	}
	lines, err := loadLineInputs(ctx, q, id)
	if err != nil {
		return err
	}
	places, err := Decimals(ctx, q, companyID)
	if err != nil {
		return err
	}
	in := EntryInput{Date: h.Date, Lines: lines}
	if err := Validate(in, places, true); err != nil {
		return err
	}
	if _, err := ResolveFiscalYear(ctx, q, companyID, h.Date); err != nil {
		return err
	}
	if err := checkReferences(ctx, q, companyID, lines); err != nil {
		return err
	}
	d, c := Totals(lines)
	return markPosted(ctx, q, companyID, id, userID, d, c, Source{Type: h.SourceType})
}

// Cancel cancels an entry. A draft is simply marked cancelled; a posted entry
// is kept and neutralised by a posted reversal entry dated `date` (defaults to
// the original date). fromSource must be true when the caller is the module
// that generated the entry. Returns the reversal entry id (0 for drafts).
func Cancel(ctx context.Context, q db.DBTX, companyID, userID, id int64, date civil.Date, fromSource bool) (int64, error) {
	h, err := lockHeader(ctx, q, companyID, id)
	if err != nil {
		return 0, err
	}
	switch {
	case h.Status == StatusCancelled:
		return 0, apperr.Rule("already_cancelled", "the entry is already cancelled")
	case h.ReversalOf != nil:
		return 0, apperr.Rule("cannot_cancel_reversal", "a reversal entry cannot be cancelled")
	case !fromSource && h.SourceType != SourceManual && h.SourceType != SourceOpening:
		return 0, apperr.Rule("cancel_via_source", "cancel the source document instead")
	}
	if h.Status == StatusDraft {
		_, err := q.Exec(ctx, `UPDATE journal_entries SET status = 'cancelled', cancelled_at = now(), cancelled_by = $2, updated_at = now() WHERE id = $1`, id, userID)
		return 0, err
	}
	if date.IsZero() {
		date = h.Date
	}
	lines, err := loadLineInputs(ctx, q, id)
	if err != nil {
		return 0, err
	}
	// Reversals of closing entries stay tagged as closing so income statements ignore both.
	srcType := SourceReversal
	if h.SourceType == SourceClosing {
		srcType = SourceClosing
	}
	revID, err := Create(ctx, q, companyID, userID, EntryInput{
		Date: date, Description: h.Description, Reference: h.Reference, Lines: Reversed(lines), Post: true,
	}, Source{Type: srcType, ID: id})
	if err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `UPDATE journal_entries SET reversal_of_id = $2 WHERE id = $1`, revID, id); err != nil {
		return 0, err
	}
	if _, err := q.Exec(ctx, `
		UPDATE journal_entries SET status = 'cancelled', cancelled_at = now(), cancelled_by = $2, reversed_by_id = $3, updated_at = now()
		WHERE id = $1`, id, userID, revID); err != nil {
		return 0, err
	}
	return revID, nil
}

// DeleteDraft removes a manual draft. Posted entries are never deleted.
func DeleteDraft(ctx context.Context, q db.DBTX, companyID, id int64) error {
	h, err := lockHeader(ctx, q, companyID, id)
	if err != nil {
		return err
	}
	if h.Status != StatusDraft || h.SourceType != SourceManual {
		return apperr.Rule("not_deletable", "only manual draft entries can be deleted; cancel posted entries instead")
	}
	_, err = q.Exec(ctx, `DELETE FROM journal_entries WHERE id = $1`, id)
	return err
}

// PostAuto creates and posts an automatically generated entry, recording an audit trail.
func PostAuto(ctx context.Context, q db.DBTX, companyID, userID int64, date civil.Date, desc, ref string, lines []LineInput, src Source) (int64, error) {
	id, err := Create(ctx, q, companyID, userID, EntryInput{Date: date, Description: desc, Reference: ref, Lines: lines, Post: true}, src)
	if err != nil {
		return 0, err
	}
	return id, audit.Record(ctx, q, "post", "journal_entry", id, nil, map[string]any{"source_type": src.Type, "source_id": src.ID, "lines": lines})
}
