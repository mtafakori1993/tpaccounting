package db

import (
	"strconv"
	"strings"
)

// Filter builds a parameterised WHERE clause. Conditions use "$N" as the
// placeholder for the value being added; values are never interpolated.
type Filter struct {
	conds []string
	Args  []any
}

func NewFilter(base string, args ...any) *Filter {
	return &Filter{conds: []string{base}, Args: args}
}

// Add appends a condition with one bound value.
func (f *Filter) Add(cond string, v any) {
	f.Args = append(f.Args, v)
	f.conds = append(f.conds, strings.ReplaceAll(cond, "$N", "$"+strconv.Itoa(len(f.Args))))
}

// AddRaw appends a condition without a value.
func (f *Filter) AddRaw(cond string) { f.conds = append(f.conds, cond) }

// Arg binds a value and returns its placeholder, for use in SELECT lists.
func (f *Filter) Arg(v any) string {
	f.Args = append(f.Args, v)
	return "$" + strconv.Itoa(len(f.Args))
}

func (f *Filter) Where() string { return strings.Join(f.conds, " AND ") }

// Paging returns a LIMIT/OFFSET clause with bound values.
func (f *Filter) Paging(limit, offset int) string {
	return " LIMIT " + f.Arg(limit) + " OFFSET " + f.Arg(offset)
}

// Clone returns an independent copy of the filter.
func (f *Filter) Clone() *Filter {
	return &Filter{conds: append([]string(nil), f.conds...), Args: append([]any(nil), f.Args...)}
}
