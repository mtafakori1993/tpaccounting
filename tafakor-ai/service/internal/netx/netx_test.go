package netx

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestBypass(t *testing.T) {
	np := []string{"app", "localhost", ".internal"}
	for host, want := range map[string]bool{"app": true, "APP": true, "localhost": true, "x.internal": true, "openrouter.ai": false, "app.evil.com": false} {
		if got := Bypass(host, np); got != want {
			t.Errorf("%s: got %v", host, got)
		}
	}
}

func TestProviderProxySkipsNoProxyHosts(t *testing.T) {
	pf, err := ProxyFunc("http://proxy:8118", []string{"app"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("GET", "http://app:8085/api/v1/auth/me", nil)
	if u, _ := pf(r); u != nil {
		t.Fatal("internal host must bypass the proxy")
	}
	r2, _ := http.NewRequest("GET", "https://openrouter.ai/api/v1/chat/completions", nil)
	if u, _ := pf(r2); u == nil || u.Host != "proxy:8118" {
		t.Fatal("external provider must use the proxy")
	}
}

// The accounting client must never use a proxy, even if HTTP_PROXY is set in the environment.
func TestDirectIgnoresEnvironmentProxy(t *testing.T) {
	var proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer target.Close()
	os.Setenv("HTTP_PROXY", proxy.URL)
	os.Setenv("http_proxy", proxy.URL)
	defer os.Unsetenv("HTTP_PROXY")
	defer os.Unsetenv("http_proxy")
	resp, err := Direct(5 * time.Second).Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || proxyHits.Load() != 0 {
		t.Fatalf("direct client used a proxy (status %d, hits %d)", resp.StatusCode, proxyHits.Load())
	}
}
