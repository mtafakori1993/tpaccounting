package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// doH performs a request with extra headers.
func (c *client) doH(method, path string, body any, headers map[string]string) (int, map[string]any, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	}
	return res.StatusCode, out, res.Header
}

// balance returns the closing ledger balance (debit − credit) of an account.
func balance(c *client, accountID int64) string {
	c.t.Helper()
	return str(c.must("GET", fmt.Sprintf("/reports/ledger?account_id=%d", accountID), nil), "closing")
}

// partyBalance returns a party's balance.
func partyBalance(c *client, partyID int64) string {
	c.t.Helper()
	return str(c.must("GET", fmt.Sprintf("/parties/%d", partyID), nil), "balance")
}

// loginAs creates a user with the given role and returns a signed-in client.
func loginAs(t *testing.T, srv *httptest.Server, admin *client, roleCode, email string) *client {
	t.Helper()
	var roleID int64
	for _, r := range items(admin.must("GET", "/roles", nil)) {
		if r["code"] == roleCode {
			roleID = id(r)
		}
	}
	if roleID == 0 {
		t.Fatalf("role %s not found", roleCode)
	}
	admin.must("POST", "/users", map[string]any{"email": email, "full_name": roleCode, "password": "Password123", "role_id": roleID})
	c := newClient(t, srv)
	c.must("POST", "/auth/login", map[string]any{"email": email, "password": "Password123"})
	return c
}

// lastEntry returns the journal entry with the given id.
func entry(c *client, id any) map[string]any {
	c.t.Helper()
	return c.must("GET", fmt.Sprintf("/journal-entries/%v", id), nil)
}

func escape(s string) string { return url.QueryEscape(s) }

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		var n int64
		fmt.Sscan(x, &n)
		return n
	}
	return 0
}
