// Package integration exercises complete accounting workflows through the HTTP
// API against a real PostgreSQL database. Set TEST_DATABASE_URL to a database
// the tests may use to create (and drop) temporary databases, e.g.
//
//	TEST_DATABASE_URL=postgres://user:pass@localhost:5432/postgres?sslmode=disable
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/tafakorpardazan/tpaccounting/internal/platform/config"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/migrate"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/storage"
	"github.com/tafakorpardazan/tpaccounting/internal/server"
	"github.com/tafakorpardazan/tpaccounting/migrations"
)

const today = "2026-10-06"

// newApp creates an isolated database, migrates it and serves the API.
func newApp(t *testing.T) *httptest.Server {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "tptest_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("connect test db: %v", err)
	}
	if err := migrate.Up(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SessionSecret: "integration-test-secret-integration-test", SessionTTL: 3600e9, LoginRateLimit: 1000,
		MaxUploadBytes: 1 << 20, StaticDir: t.TempDir()}
	srv := httptest.NewServer(server.New(cfg, pool, store))
	t.Cleanup(func() {
		srv.Close()
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(context.Background())
	})
	return srv
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, srv *httptest.Server) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: srv.URL + "/api/v1", http: &http.Client{Jar: jar}}
}

func (c *client) csrf() string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "tp_csrf" {
			return ck.Value
		}
	}
	return ""
}

// do performs a request and decodes the JSON response into a generic map.
func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &out)
	} else if len(raw) > 0 && raw[0] == '[' {
		var arr []any
		_ = json.Unmarshal(raw, &arr)
		out = map[string]any{"items": arr}
	}
	return res.StatusCode, out
}

// must performs a request expecting a 2xx status.
func (c *client) must(method, path string, body any) map[string]any {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code >= 300 {
		c.t.Fatalf("%s %s: status %d: %v", method, path, code, out)
	}
	return out
}

// expect performs a request expecting a specific status and error code.
func (c *client) expect(status int, errCode, method, path string, body any) {
	c.t.Helper()
	code, out := c.do(method, path, body)
	if code != status {
		c.t.Fatalf("%s %s: want status %d got %d: %v", method, path, status, code, out)
	}
	if errCode != "" {
		e, _ := out["error"].(map[string]any)
		if e == nil || e["code"] != errCode {
			c.t.Fatalf("%s %s: want error %q got %v", method, path, errCode, out)
		}
	}
}

func id(m map[string]any) int64 { return int64(m["id"].(float64)) }

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func items(m map[string]any) []map[string]any {
	raw, _ := m["items"].([]any)
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

func sub(m map[string]any, key string) map[string]any { v, _ := m[key].(map[string]any); return v }

// setupCompany runs the setup wizard and returns a logged-in admin client.
func setupCompany(t *testing.T, srv *httptest.Server) *client {
	c := newClient(t, srv)
	c.must("POST", "/setup", map[string]any{
		"company": map[string]any{"name": "تفکر پردازان", "name_en": "Tafakor Pardazan", "calendar": "jalali"},
		"admin":   map[string]any{"full_name": "Admin", "email": "admin@example.com", "password": "Admin12345"},
	})
	return c
}

// postableAccount returns the id of the account with the given code.
func accountByCode(c *client, code string) int64 {
	c.t.Helper()
	for _, a := range items(c.must("GET", "/accounts?q="+code, nil)) {
		if a["code"] == code {
			return id(a)
		}
	}
	c.t.Fatalf("account %s not found", code)
	return 0
}
