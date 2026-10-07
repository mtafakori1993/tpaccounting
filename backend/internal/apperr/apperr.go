// Package apperr defines the application error model. Every error returned to
// clients carries a stable machine-readable code that the frontend translates;
// raw database or internal messages are never exposed.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Params  map[string]any    `json:"params,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WithParams attaches interpolation parameters for the client message.
func (e *Error) WithParams(kv ...any) *Error {
	if e.Params == nil {
		e.Params = map[string]any{}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Params[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return e
}

func NotFound(entity string) *Error {
	return New(http.StatusNotFound, "not_found", entity+" not found").WithParams("entity", entity)
}
func Unauthorized() *Error {
	return New(http.StatusUnauthorized, "unauthorized", "authentication required")
}
func Forbidden() *Error {
	return New(http.StatusForbidden, "forbidden", "you do not have permission for this action")
}
func BadRequest(code, msg string) *Error { return New(http.StatusBadRequest, code, msg) }
func Conflict(code, msg string) *Error   { return New(http.StatusConflict, code, msg) }

// Rule is a violated business rule (HTTP 422).
func Rule(code, msg string) *Error { return New(http.StatusUnprocessableEntity, code, msg) }

// As extracts an *Error from err.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Validator accumulates field-level validation errors.
type Validator struct{ fields map[string]string }

func NewValidator() *Validator { return &Validator{fields: map[string]string{}} }

// Check records code for field when ok is false (first error per field wins).
func (v *Validator) Check(ok bool, field, code string) {
	if !ok {
		if _, exists := v.fields[field]; !exists {
			v.fields[field] = code
		}
	}
}

func (v *Validator) Required(field, value string) {
	v.Check(len([]rune(value)) > 0 && trimmed(value) != "", field, "required")
}

func (v *Validator) MaxLen(field, value string, n int) {
	v.Check(len([]rune(value)) <= n, field, "too_long")
}

func (v *Validator) RequiredID(field string, id int64) { v.Check(id > 0, field, "required") }

func (v *Validator) Valid() bool { return len(v.fields) == 0 }

func (v *Validator) Err() error {
	if v.Valid() {
		return nil
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation", Message: "validation failed", Fields: v.fields}
}

func trimmed(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
