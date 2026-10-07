// Package accounting is the only way the AI service talks to the accounting
// application: its public /api/v1 REST API, called *as the user* by forwarding
// the user's session cookie and CSRF token. It never uses a service account,
// database credentials or a proxy.
package accounting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/netx"
)

const (
	SessionCookie = "tp_session"
	CSRFCookie    = "tp_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// Credentials are the user's own session values, taken from the incoming request.
type Credentials struct {
	Session string
	CSRF    string
}

func (c Credentials) Valid() bool { return c.Session != "" }

// FromRequest extracts the user's credentials from an incoming /ai-api request.
func FromRequest(r *http.Request) Credentials {
	var c Credentials
	if ck, err := r.Cookie(SessionCookie); err == nil {
		c.Session = ck.Value
	}
	if ck, err := r.Cookie(CSRFCookie); err == nil {
		c.CSRF = ck.Value
	}
	return c
}

// APIError mirrors the accounting error envelope {"error":{code,message,fields,params}}.
type APIError struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	Params  map[string]any    `json:"params,omitempty"`
}

func (e *APIError) Error() string { return fmt.Sprintf("accounting %d %s: %s", e.Status, e.Code, e.Message) }

func AsAPIError(err error) (*APIError, bool) {
	var e *APIError
	ok := errors.As(err, &e)
	return e, ok
}

// Client calls the accounting API.
type Client struct {
	Base string // e.g. http://app:8085
	HTTP *http.Client
}

func New(base string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), HTTP: netx.Direct(60 * time.Second)}
}

// Call is one request. Extra headers are limited to an allowlist (budget control).
type Call struct {
	Method string
	Path   string // relative to /api/v1, e.g. "/parties"
	Query  url.Values
	Body   any
	Header map[string]string
}

var allowedExtraHeaders = map[string]bool{"X-Budget-Confirm": true, "X-Budget-Override": true}

// Do performs the call as the user and decodes a JSON response into out (if non-nil).
func (c *Client) Do(ctx context.Context, cred Credentials, call Call, out any) error {
	var body io.Reader
	if call.Body != nil {
		b, err := json.Marshal(call.Body)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, cred, call, body)
	if err != nil {
		return err
	}
	if call.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

func (c *Client) newRequest(ctx context.Context, cred Credentials, call Call, body io.Reader) (*http.Request, error) {
	if !strings.HasPrefix(call.Path, "/") || strings.Contains(call.Path, "..") {
		return nil, fmt.Errorf("invalid accounting path")
	}
	u := c.Base + "/api/v1" + call.Path
	if len(call.Query) > 0 {
		u += "?" + call.Query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: cred.Session})
	if cred.CSRF != "" {
		req.AddCookie(&http.Cookie{Name: CSRFCookie, Value: cred.CSRF})
		req.Header.Set(CSRFHeader, cred.CSRF)
	}
	req.Header.Set("X-Requested-By", "tafakor-ai")
	for k, v := range call.Header {
		if allowedExtraHeaders[k] {
			req.Header.Set(k, v)
		}
	}
	return req, nil
}

func (c *Client) send(req *http.Request, out any) error {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("accounting unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			env.Error.Status = resp.StatusCode
			return env.Error
		}
		return &APIError{Status: resp.StatusCode, Code: "http_" + fmt.Sprint(resp.StatusCode), Message: http.StatusText(resp.StatusCode)}
	}
	if out == nil || len(data) == 0 || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if raw, ok := out.(*[]byte); ok {
		*raw = data
		return nil
	}
	return json.Unmarshal(data, out)
}

// Upload sends a multipart attachment as the user.
func (c *Client) Upload(ctx context.Context, cred Credentials, entityType string, entityID int64, fileName string, content []byte, out any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("entity_type", entityType)
	_ = w.WriteField("entity_id", fmt.Sprint(entityID))
	fw, err := w.CreateFormFile("file", fileName)
	if err != nil {
		return err
	}
	if _, err := fw.Write(content); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := c.newRequest(ctx, cred, Call{Method: http.MethodPost, Path: "/attachments"}, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.send(req, out)
}

// Raw fetches a binary resource (e.g. a PDF) as the user.
func (c *Client) Raw(ctx context.Context, cred Credentials, call Call) ([]byte, string, error) {
	var body io.Reader
	if call.Body != nil {
		b, err := json.Marshal(call.Body)
		if err != nil {
			return nil, "", err
		}
		body = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, cred, call, body)
	if err != nil {
		return nil, "", err
	}
	if call.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("accounting unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode >= 400 {
		var env struct {
			Error *APIError `json:"error"`
		}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			env.Error.Status = resp.StatusCode
			return nil, "", env.Error
		}
		return nil, "", &APIError{Status: resp.StatusCode, Code: "http_error", Message: http.StatusText(resp.StatusCode)}
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// Me is the subset of GET /auth/me the assistant needs.
type Me struct {
	ID          int64    `json:"id"`
	Email       string   `json:"email"`
	FullName    string   `json:"full_name"`
	Role        string   `json:"role"`
	Language    string   `json:"language"`
	Theme       string   `json:"theme"`
	Permissions []string `json:"permissions"`
	Company     struct {
		ID             int64  `json:"id"`
		Name           string `json:"name"`
		BaseCurrency   string `json:"base_currency"`
		AmountDecimals int    `json:"amount_decimals"`
		Calendar       string `json:"calendar"`
	} `json:"company"`
	FiscalYear *struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Status    string `json:"status"`
	} `json:"fiscal_year"`
}

func (m *Me) Can(perm string) bool {
	for _, p := range m.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Me validates the session and returns the user.
func (c *Client) Me(ctx context.Context, cred Credentials) (*Me, error) {
	if !cred.Valid() {
		return nil, &APIError{Status: 401, Code: "unauthorized", Message: "not signed in"}
	}
	var m Me
	if err := c.Do(ctx, cred, Call{Method: http.MethodGet, Path: "/auth/me"}, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
