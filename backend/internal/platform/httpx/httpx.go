// Package httpx contains HTTP helpers shared by all modules: JSON encoding,
// request decoding, error translation and list parameters.
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/civil"
)

const maxBodyBytes = 4 << 20

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func OK(w http.ResponseWriter, v any)      { JSON(w, http.StatusOK, v) }
func Created(w http.ResponseWriter, v any) { JSON(w, http.StatusCreated, v) }
func NoContent(w http.ResponseWriter)      { w.WriteHeader(http.StatusNoContent) }

// Decode reads a JSON request body into v.
func Decode(r *http.Request, v any) error {
	body := http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return apperr.BadRequest("invalid_body", "request body is empty")
		}
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) && ute.Field != "" {
			e := apperr.NewValidator()
			e.Check(false, ute.Field, "invalid")
			return e.Err()
		}
		return apperr.BadRequest("invalid_body", "request body is not valid JSON")
	}
	return nil
}

// constraintCodes maps database constraint names to client error codes.
var constraintCodes = map[string]string{
	"journal_entries_number_unique":       "duplicate_document_number",
	"invoices_number_unique":              "duplicate_document_number",
	"stock_documents_number_unique":       "duplicate_document_number",
	"treasury_transactions_number_unique": "duplicate_document_number",
	"expenses_number_unique":              "duplicate_document_number",
	"accounts_code_unique":                "duplicate_code",
	"parties_code_unique":                 "duplicate_code",
	"products_sku_unique":                 "duplicate_code",
	"warehouses_code_unique":              "duplicate_code",
	"cost_centers_code_unique":            "duplicate_code",
	"product_categories_name_unique":      "duplicate_name",
	"expense_categories_name_unique":      "duplicate_name",
	"users_email_unique":                  "duplicate_email",
	"roles_code_key":                      "duplicate_code",
	"fiscal_years_company_id_name_key":    "duplicate_name",
	"posted_entry_immutable":              "posted_immutable",
	"journal_entries_balanced":            "unbalanced",
	"checks_number_unique":                "duplicate_document_number",
	"checks_sayadi_unique":                "duplicate_check",
	"checks_issued_unique":                "duplicate_check",
	"checks_received_unique":              "duplicate_check",
	"employees_code_unique":               "duplicate_code",
	"employees_national_id_unique":        "duplicate_national_id",
	"salary_components_code_unique":       "duplicate_code",
	"payroll_periods_month_unique":        "duplicate_payroll_period",
	"payroll_locked":                      "payroll_locked",
	"budgets_code_unique":                 "duplicate_code",
	"budgets_one_active":                  "budget_already_active",
	"budget_lines_unique":                 "duplicate_budget_line",
	"budget_locked":                       "budget_locked",
}

// Error writes err as a structured JSON error. Unknown errors are logged and
// reported generically so internal details never reach the client.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok {
		JSON(w, e.Status, map[string]any{"error": e})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		JSON(w, http.StatusNotFound, map[string]any{"error": apperr.NotFound("record")})
		return
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		code := constraintCodes[pg.ConstraintName]
		switch pg.Code {
		case "23505":
			if code == "" {
				code = "duplicate"
			}
			JSON(w, http.StatusConflict, map[string]any{"error": apperr.Conflict(code, "duplicate value")})
			return
		case "23503":
			c := "invalid_reference"
			if r.Method == http.MethodDelete {
				c = "in_use"
			}
			JSON(w, http.StatusConflict, map[string]any{"error": apperr.Conflict(c, "record is referenced")})
			return
		case "23514", "22003", "22P02", "23502":
			JSON(w, http.StatusUnprocessableEntity, map[string]any{"error": apperr.Rule("invalid_data", "data violates a rule")})
			return
		case "P0001":
			if code == "" {
				code = "invalid_data"
			}
			JSON(w, http.StatusUnprocessableEntity, map[string]any{"error": apperr.Rule(code, "operation not allowed")})
			return
		case "40001", "40P01":
			JSON(w, http.StatusConflict, map[string]any{"error": apperr.Conflict("concurrent_update", "please retry")})
			return
		}
	}
	slog.Error("unhandled error", "method", r.Method, "path", r.URL.Path, "err", err)
	JSON(w, http.StatusInternalServerError, map[string]any{"error": apperr.New(http.StatusInternalServerError, "internal", "unexpected server error")})
}

// Handler adapts a handler that returns an error.
func Handler(fn func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			Error(w, r, err)
		}
	}
}

func IDParam(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.NotFound("record")
	}
	return id, nil
}

// QueryInt64 returns a positive integer query parameter or 0.
func QueryInt64(r *http.Request, name string) int64 {
	v, err := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func QueryBool(r *http.Request, name string) *bool {
	v, err := strconv.ParseBool(r.URL.Query().Get(name))
	if err != nil {
		return nil
	}
	return &v
}

// QueryDate parses an optional YYYY-MM-DD parameter.
func QueryDate(r *http.Request, name string) (civil.Date, error) {
	s := strings.TrimSpace(r.URL.Query().Get(name))
	if s == "" {
		return civil.Date{}, nil
	}
	d, err := civil.Parse(s)
	if err != nil {
		v := apperr.NewValidator()
		v.Check(false, name, "invalid_date")
		return d, v.Err()
	}
	return d, nil
}

// ListParams are the common pagination/sorting/search parameters.
type ListParams struct {
	Page     int
	PageSize int
	Sort     string
	Desc     bool
	Q        string
}

func (p ListParams) Offset() int { return (p.Page - 1) * p.PageSize }

func ParseList(r *http.Request, defaultSort string, defaultDesc bool) ListParams {
	q := r.URL.Query()
	p := ListParams{Page: 1, PageSize: 50, Sort: defaultSort, Desc: defaultDesc, Q: strings.TrimSpace(q.Get("q"))}
	if v, err := strconv.Atoi(q.Get("page")); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(q.Get("page_size")); err == nil && v > 0 {
		p.PageSize = min(v, 500)
	}
	if s := q.Get("sort"); s != "" {
		p.Sort = s
		p.Desc = q.Get("order") == "desc"
	}
	return p
}

// OrderBy returns a safe ORDER BY clause using a whitelist of sortable columns.
func (p ListParams) OrderBy(allowed map[string]string, fallback string) string {
	col, ok := allowed[p.Sort]
	if !ok {
		col = fallback
	}
	dir := "ASC"
	if p.Desc {
		dir = "DESC"
	}
	return col + " " + dir
}

type Page[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func NewPage[T any](items []T, total int, p ListParams) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize}
}

// Like escapes a search term for ILIKE.
func Like(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}
