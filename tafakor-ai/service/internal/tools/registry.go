// Package tools is the strict allowlist of operations the assistant may perform.
// Every tool is a thin wrapper over the public accounting API, called as the user
// (so the user's permissions, period locks and budget control always apply).
//
// Read tools can be called freely. Write tools can only be called with an
// Authorization issued by the policy engine for exactly the same payload — the
// LLM can never call a write tool on its own.
package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/catalog"
)

type Kind string

const (
	Read  Kind = "read"
	Write Kind = "write"
)

// Env is the per-request execution environment: the user's credentials.
type Env struct {
	Acc  *accounting.Client
	Cred accounting.Credentials
	Me   *accounting.Me
	// Files resolves an upload id to its bytes (attachments, PDFs). Scoped to the user.
	Files FileStore
}

type FileStore interface {
	Read(ctx context.Context, companyID, userID int64, id string) (name string, content []byte, err error)
	Save(ctx context.Context, companyID, userID int64, name, contentType string, content []byte) (id string, err error)
}

// Authorization proves the policy engine (and, when required, the human) approved
// this exact payload. Only the agent package constructs it, after policy evaluation.
type Authorization struct {
	PayloadHash string
	Mode        string // "human" | "auto"
	ApprovedBy  int64
	TaskID      string
}

type Tool struct {
	Name        string
	Kind        Kind
	Permissions []string // accounting permissions the user must hold (checked before calling; the API re-checks)
	Run         func(ctx context.Context, env Env, args json.RawMessage) (any, error)
}

var (
	ErrUnknownTool  = errors.New("tool is not on the allowlist")
	ErrUnauthorized = errors.New("write tool called without a matching policy authorization")
	ErrNoPermission = errors.New("you do not have permission for this action")
	ErrInvalidInput = errors.New("invalid tool input")
)

type Registry struct{ tools map[string]Tool }

func NewRegistry() *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range readTools() {
		r.tools[t.Name] = t
	}
	for _, t := range writeTools() {
		r.tools[t.Name] = t
	}
	r.RegisterCatalog(catalog.Load())
	return r
}

func (r *Registry) Names(kind Kind) []string {
	var out []string
	for n, t := range r.tools {
		if t.Kind == kind {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Call runs an allowlisted tool. Write tools require auth with a matching payload hash.
func (r *Registry) Call(ctx context.Context, env Env, name string, args json.RawMessage, auth *Authorization) (any, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	if t.Kind == Write {
		if auth == nil || auth.PayloadHash == "" || auth.PayloadHash != PayloadHash(name, args) {
			return nil, ErrUnauthorized
		}
	}
	if env.Me != nil {
		for _, p := range t.Permissions {
			if !env.Me.Can(p) {
				return nil, fmt.Errorf("%w (%s)", ErrNoPermission, p)
			}
		}
	}
	return t.Run(ctx, env, args)
}

// PayloadHash is the canonical hash of a tool call: tool name + canonical JSON args.
func PayloadHash(name string, args json.RawMessage) string {
	h := sha256.New()
	h.Write([]byte(name))
	h.Write([]byte{0})
	h.Write(Canonical(args))
	return hex.EncodeToString(h.Sum(nil))
}

// Canonical re-encodes JSON with sorted keys and no insignificant whitespace.
func Canonical(raw json.RawMessage) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return raw
	}
	b, err := json.Marshal(v) // map keys are sorted by encoding/json
	if err != nil {
		return raw
	}
	return b
}

// decodeStrict decodes args into v, rejecting unknown fields and trailing data.
func decodeStrict(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data", ErrInvalidInput)
	}
	return nil
}

// decodeList accepts either a JSON array or a page {"items": [...]}.
func decodeList[T any](raw []byte) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	var out []T
	if len(raw) > 0 && raw[0] == '[' {
		err := json.Unmarshal(raw, &out)
		return out, err
	}
	var p Page[T]
	err := json.Unmarshal(raw, &p)
	return p.Items, err
}
