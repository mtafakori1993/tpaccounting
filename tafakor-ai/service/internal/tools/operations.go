package tools

import (
	"bytes"
	"context"
	"crypto/rand"
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
		if op.Multipart {
			return runMultipart(ctx, env, op, &a)
		}
		secrets, err := generateSecrets(&a)
		if err != nil {
			return nil, err
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
		if len(secrets) > 0 {
			return WithSecrets{Result: out, Secrets: secrets}, nil
		}
		return out, nil
	}
}

// SecretGenerate is the only value the assistant may put in a password field: the
// service generates a strong temporary password at execution time. The approval
// (payload hash), the plan state, the audit log and the model only ever see this
// placeholder; the generated value is shown once to the user.
const SecretGenerate = "$generate"

// SecretFields are body fields that carry credentials.
var SecretFields = map[string]bool{"password": true, "new_password": true}

// WithSecrets is a result that carries generated credentials. Marshalling it yields
// only the result, so secrets cannot leak into stored state by accident.
type WithSecrets struct {
	Result  json.RawMessage
	Secrets map[string]string
}

func (w WithSecrets) MarshalJSON() ([]byte, error) { return w.Result, nil }

func generateSecrets(a *OpArgs) (map[string]string, error) {
	if len(a.Body) == 0 {
		return nil, nil
	}
	var obj map[string]any
	if json.Unmarshal(a.Body, &obj) != nil {
		return nil, nil
	}
	var secrets map[string]string
	for k, v := range obj {
		if !SecretFields[k] {
			continue
		}
		sv, _ := v.(string)
		switch sv {
		case "":
			continue
		case SecretGenerate:
			pw := newPassword()
			obj[k] = pw
			if secrets == nil {
				secrets = map[string]string{}
			}
			secrets[k] = pw
		default:
			return nil, fmt.Errorf("%w: %s must be %q (a temporary password is generated and shown once to the user)", ErrInvalidInput, k, SecretGenerate)
		}
	}
	if secrets != nil {
		a.Body, _ = json.Marshal(obj)
	}
	return secrets, nil
}

func newPassword() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:12]) + "-" + string(b[12:])
}

var uploadIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

// runMultipart sends a file the user uploaded in the assistant (upload_id) to an
// upload endpoint of the application, with the remaining body keys as form fields.
func runMultipart(ctx context.Context, env Env, op *catalog.Operation, a *OpArgs) (any, error) {
	var obj map[string]any
	if len(a.Body) > 0 {
		if err := json.Unmarshal(a.Body, &obj); err != nil {
			return nil, fmt.Errorf("%w: body must be a JSON object", ErrInvalidInput)
		}
	}
	uid, _ := obj["upload_id"].(string)
	if !uploadIDRe.MatchString(uid) {
		return nil, fmt.Errorf("%w: upload_id (a file the user attached in this conversation) is required for %s", ErrInvalidInput, op.ID)
	}
	if env.Files == nil || env.Me == nil {
		return nil, fmt.Errorf("%w: no file store", ErrInvalidInput)
	}
	name, content, err := env.Files.Read(ctx, env.Me.Company.ID, env.Me.ID, uid)
	if err != nil {
		return nil, fmt.Errorf("%w: upload_id not found among this user's files", ErrInvalidInput)
	}
	fields := map[string]string{}
	for k, v := range obj {
		if k == "upload_id" || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			fields[k] = x
		case float64:
			fields[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			fields[k] = strconv.FormatBool(x)
		default:
			return nil, fmt.Errorf("%w: form field %s must be a string, number or boolean", ErrInvalidInput, k)
		}
	}
	var rawOut []byte
	if err := env.Acc.Multipart(ctx, env.Cred, accounting.Call{Method: op.Method, Path: BuildPath(op, a)}, fields, name, content, &rawOut); err != nil {
		return nil, err
	}
	if len(rawOut) == 0 {
		return json.RawMessage(`{"ok":true}`), nil
	}
	return json.RawMessage(rawOut), nil
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
