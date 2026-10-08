// Package catalog is the AI's capability registry. It is GENERATED from the
// accounting application's source code by cmd/capmap (routes, permissions,
// request structs, validators, UI usage) — nothing here is hand-invented. Each
// operation becomes an allowlisted tool whose classification decides whether the
// assistant may read it, execute it with confirmation, only with a human's
// approval, or never.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed capabilities.json
var raw []byte

type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
}

type Operation struct {
	ID             string   `json:"id"`
	Module         string   `json:"module"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	Handler        string   `json:"handler"`
	Doc            string   `json:"doc,omitempty"`
	Permission     string   `json:"permission,omitempty"`
	InnerPerms     []string `json:"inner_permissions,omitempty"`
	PathParams     []string `json:"path_params,omitempty"`
	QueryParams    []string `json:"query_params,omitempty"`
	BodyType       string   `json:"body_type,omitempty"`
	Body           []Field  `json:"body,omitempty"`
	Multipart      bool     `json:"multipart,omitempty"`
	Binary         bool     `json:"binary_response,omitempty"`
	Kind           string   `json:"kind"`
	Action         string   `json:"action"`
	Classification string   `json:"classification"`
	Risk           string   `json:"risk"`
	AccountingImp  string   `json:"accounting_impact,omitempty"`
	UIExists       bool     `json:"ui_exists"`
	UIEntryPoints  []string `json:"ui_entry_points,omitempty"`
	Notes          []string `json:"notes,omitempty"`
}

// Classifications.
const (
	FullyAutonomous   = "FULLY_AUTONOMOUS"
	NeedsConfirmation = "REQUIRES_USER_CONFIRMATION"
	NeedsApproval     = "REQUIRES_APPROVAL"
	ReadOnly          = "READ_ONLY"
	NotSupported      = "NOT_SUPPORTED_BY_AI"
	NotSafe           = "NOT_SAFE_TO_AUTOMATE"
)

type Catalog struct {
	Operations []Operation `json:"operations"`
	byID       map[string]*Operation
}

var loaded *Catalog

// Load returns the embedded catalog.
func Load() *Catalog {
	if loaded != nil {
		return loaded
	}
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(fmt.Sprintf("catalog: %v", err))
	}
	c.byID = map[string]*Operation{}
	for i := range c.Operations {
		c.byID[c.Operations[i].ID] = &c.Operations[i]
	}
	loaded = &c
	return loaded
}

func (c *Catalog) Get(id string) (*Operation, bool) {
	o, ok := c.byID[id]
	return o, ok
}

// Executable reports whether the assistant may ever call this operation.
func (o *Operation) Executable() bool {
	return o.Classification != NotSupported && o.Classification != NotSafe && !o.Multipart
}

// IsRead: no side effects.
func (o *Operation) IsRead() bool { return o.Classification == ReadOnly }

// Permissions required at route level (all of them).
func (o *Operation) Permissions() []string {
	if o.Permission == "" {
		return nil
	}
	return strings.Split(o.Permission, "+")
}

// Allowed reports whether a user holding perms can call the operation (same rule as the app).
func (o *Operation) Allowed(can func(string) bool) (bool, string) {
	for _, p := range o.Permissions() {
		if !can(p) {
			return false, p
		}
	}
	if len(o.InnerPerms) > 0 {
		for _, p := range o.InnerPerms {
			if can(p) {
				return true, ""
			}
		}
		return false, strings.Join(o.InnerPerms, " | ")
	}
	return true, ""
}

// readOnlyFields are response-only properties the generated request structs also contain.
var readOnlyFields = map[string]bool{"id": true, "balance": true, "created_at": true, "updated_at": true, "posted_at": true, "on_hand": true,
	"stock_value": true, "has_logo": true, "has_children": true, "postable": true, "account_code": true}

// InputFields lists the writable body fields.
func (o *Operation) InputFields() []Field {
	var out []Field
	for _, f := range o.Body {
		if readOnlyFields[f.Name] || strings.HasSuffix(f.Name, "_name") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Line is the compact one-line description used in the planner's catalog.
func (o *Operation) Line() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s %s", o.ID, o.Method, o.Path)
	if len(o.QueryParams) > 0 {
		b.WriteString(" ?" + strings.Join(o.QueryParams, ","))
	}
	if fs := o.InputFields(); len(fs) > 0 {
		var names []string
		for _, f := range fs {
			n := f.Name
			if f.Required {
				n += "*"
			}
			if len(f.Enum) > 0 && len(f.Enum) <= 8 {
				n += "=" + strings.Join(f.Enum, "|")
			}
			if len(f.Fields) > 0 && strings.HasPrefix(f.Type, "[]") {
				var sub []string
				for _, s := range f.Fields {
					if readOnlyFields[s.Name] || strings.HasSuffix(s.Name, "_name") {
						continue
					}
					sub = append(sub, s.Name)
				}
				n += "[" + strings.Join(sub, ",") + "]"
			}
			names = append(names, n)
		}
		b.WriteString(" {" + strings.Join(names, ",") + "}")
	}
	if o.Classification != ReadOnly {
		b.WriteString(" <" + o.Classification + ">")
	}
	if o.Doc != "" {
		d := o.Doc
		if len(d) > 90 {
			d = d[:90]
		}
		b.WriteString(" — " + d)
	}
	return b.String()
}

// Summary renders the catalog visible to a user (permission-filtered, executable only).
func (c *Catalog) Summary(can func(string) bool) string {
	var lines []string
	for i := range c.Operations {
		o := &c.Operations[i]
		if !o.Executable() {
			continue
		}
		if ok, _ := o.Allowed(can); !ok {
			continue
		}
		lines = append(lines, o.Line())
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Restricted lists operations the user may not use through the assistant (for honest answers).
func (c *Catalog) Restricted() []string {
	var out []string
	for _, o := range c.Operations {
		if !o.Executable() {
			out = append(out, o.ID+" ("+o.Classification+")")
		}
	}
	return out
}
