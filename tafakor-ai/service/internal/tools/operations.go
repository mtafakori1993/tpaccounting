package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
)

// OpArgs are the arguments of a catalog operation call.
type OpArgs struct {
	Path  Scalars         `json:"path,omitempty"`
	Query Scalars         `json:"query,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
}

// Scalars is a string map that also accepts JSON numbers and booleans (models send page_size: 5).
type Scalars map[string]string

func (s *Scalars) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := Scalars{}
	for k, v := range raw {
		switch x := v.(type) {
		case nil:
			continue
		case string:
			out[k] = x
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(x)
		default:
			return fmt.Errorf("%s must be a string, number or boolean", k)
		}
	}
	*s = out
	return nil
}

var (
	idParam   = regexp.MustCompile(`^[0-9]{1,18}$`)
	wordParam = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,40}$`)
)

// ValidateOpArgs checks args strictly against the operation discovered from the app's code.
func ValidateOpArgs(op *catalog.Operation, a *OpArgs) error {
	for _, p := range op.PathParams {
		v := a.Path[p]
		if v == "" {
			return fmt.Errorf("%w: path parameter %s required", ErrInvalidInput, p)
		}
		re := wordParam
		if p == "id" || strings.HasSuffix(p, "_id") {
			re = idParam
		}
		if !re.MatchString(v) {
			return fmt.Errorf("%w: path parameter %s invalid", ErrInvalidInput, p)
		}
	}
	for k := range a.Path {
		if !contains(op.PathParams, k) {
			return fmt.Errorf("%w: unknown path parameter %s", ErrInvalidInput, k)
		}
	}
	for k, v := range a.Query {
		if !contains(op.QueryParams, k) {
			return fmt.Errorf("%w: unknown query parameter %s for %s", ErrInvalidInput, k, op.ID)
		}
		if len(v) > 120 {
			return fmt.Errorf("%w: query parameter %s too long", ErrInvalidInput, k)
		}
	}
	if len(a.Body) > 0 && string(a.Body) != "null" {
		if op.Method == http.MethodGet {
			return fmt.Errorf("%w: %s takes no body", ErrInvalidInput, op.ID)
		}
		if len(a.Body) > 128<<10 {
			return fmt.Errorf("%w: body too large", ErrInvalidInput)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(a.Body, &obj); err != nil {
			return fmt.Errorf("%w: body must be a JSON object", ErrInvalidInput)
		}
		// Unknown keys are rejected. "Required" markers from the validators are only hints
		// (many are conditional, e.g. transfer vs payment); the application validates them.
		if err := checkFields(obj, op.Body, op.ID); err != nil {
			return err
		}
	}
	return nil
}

// checkFields rejects keys the request struct does not have (also inside arrays of objects).
func checkFields(obj map[string]json.RawMessage, fields []catalog.Field, op string) error {
	if len(fields) == 0 {
		if len(obj) > 0 {
			return fmt.Errorf("%w: %s takes no body fields", ErrInvalidInput, op)
		}
		return nil
	}
	known := map[string]catalog.Field{}
	for _, f := range fields {
		known[f.Name] = f
	}
	for k, v := range obj {
		f, ok := known[k]
		if !ok {
			return fmt.Errorf("%w: unknown field %q for %s", ErrInvalidInput, k, op)
		}
		if len(f.Fields) > 0 {
			trim := bytes.TrimSpace(v)
			switch {
			case len(trim) > 0 && trim[0] == '[':
				var arr []map[string]json.RawMessage
				if err := json.Unmarshal(trim, &arr); err != nil {
					return fmt.Errorf("%w: %s must be a list of objects", ErrInvalidInput, k)
				}
				for _, el := range arr {
					if err := checkFields(el, f.Fields, op+"."+k); err != nil {
						return err
					}
				}
			case len(trim) > 0 && trim[0] == '{':
				var sub map[string]json.RawMessage
				if err := json.Unmarshal(trim, &sub); err == nil {
					if err := checkFields(sub, f.Fields, op+"."+k); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// BuildPath fills the path template.
func BuildPath(op *catalog.Operation, a *OpArgs) string {
	p := op.Path
	for _, k := range op.PathParams {
		p = strings.Replace(p, "{"+k+"}", url.PathEscape(a.Path[k]), 1)
	}
	return p
}

// runOperation calls the operation as the user. Binary responses (exports, PDFs) are saved as files.
func runOperation(op *catalog.Operation) func(context.Context, Env, json.RawMessage) (any, error) {
	return func(ctx context.Context, env Env, raw json.RawMessage) (any, error) {
		var a OpArgs
		if err := decodeStrict(raw, &a); err != nil {
			return nil, err
		}
		if !op.Executable() {
			return nil, fmt.Errorf("%w: %s is %s", ErrNotAllowed, op.ID, op.Classification)
		}
		if err := ValidateOpArgs(op, &a); err != nil {
			return nil, err
		}
		if env.Me != nil {
			if ok, missing := op.Allowed(env.Me.Can); !ok {
				return nil, fmt.Errorf("%w (%s)", ErrNoPermission, missing)
			}
		}
		q := url.Values{}
		for k, v := range a.Query {
			q.Set(k, v)
		}
		call := accounting.Call{Method: op.Method, Path: BuildPath(op, &a), Query: q}
		if len(a.Body) > 0 && string(a.Body) != "null" {
			call.Body = a.Body
		} else if op.Method == http.MethodPost || op.Method == http.MethodPut {
			call.Body = map[string]any{}
		}
		if op.Binary {
			data, ctype, err := env.Acc.Raw(ctx, env.Cred, call)
			if err != nil {
				return nil, err
			}
			if env.Files == nil || env.Me == nil {
				return map[string]any{"bytes": len(data), "content_type": ctype}, nil
			}
			ext := map[bool]string{true: ".pdf", false: ".bin"}[strings.Contains(ctype, "pdf")]
			if strings.Contains(ctype, "spreadsheet") {
				ext = ".xlsx"
			} else if strings.Contains(ctype, "csv") {
				ext = ".csv"
			}
			id, err := env.Files.Save(ctx, env.Me.Company.ID, env.Me.ID, strings.ReplaceAll(op.ID, ".", "-")+ext, ctype, data)
			if err != nil {
				return nil, err
			}
			return map[string]any{"file_id": id, "url": "/ai-api/files/" + id, "content_type": ctype, "bytes": len(data)}, nil
		}
		var out json.RawMessage
		var rawOut []byte
		if err := env.Acc.Do(ctx, env.Cred, call, &rawOut); err != nil {
			return nil, err
		}
		if len(rawOut) == 0 {
			out = json.RawMessage(`{"ok":true}`)
		} else {
			out = rawOut
		}
		return out, nil
	}
}

var ErrNotAllowed = fmt.Errorf("the assistant may not perform this operation")

// RegisterCatalog adds every executable catalog operation to the registry as a tool
// (reads: Read; anything with side effects: Write, so it needs a policy authorization).
func (r *Registry) RegisterCatalog(c *catalog.Catalog) {
	for i := range c.Operations {
		op := &c.Operations[i]
		if !op.Executable() {
			continue
		}
		kind := Write
		if op.IsRead() {
			kind = Read
		}
		r.tools[op.ID] = Tool{Name: op.ID, Kind: kind, Permissions: op.Permissions(), Run: runOperation(op)}
	}
}
