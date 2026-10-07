// Package netx builds HTTP clients with explicit proxy rules:
//   - external AI/speech providers go through their configured proxy (port 8118),
//   - the accounting API and every host in the no-proxy list never use a proxy,
//   - environment proxy variables (HTTP_PROXY, …) are ignored on purpose so
//     traffic never takes an unexpected route.
package netx

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrProxyUnreachable marks failures to connect to the configured proxy.
var ErrProxyUnreachable = errors.New("proxy unreachable")

// Direct returns a client for internal calls (accounting API): never proxied.
func Direct(timeout time.Duration) *http.Client {
	t := baseTransport()
	t.Proxy = nil
	return &http.Client{Transport: t, Timeout: timeout,
		// Never follow redirects to other hosts with the user's cookie.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// ProxyFunc returns a proxy selector: proxyURL for every host except those in noProxy.
func ProxyFunc(proxyURL string, noProxy []string) (func(*http.Request) (*url.URL, error), error) {
	if proxyURL == "" {
		return nil, nil
	}
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) (*url.URL, error) {
		if Bypass(r.URL.Hostname(), noProxy) {
			return nil, nil
		}
		return pu, nil
	}, nil
}

// Bypass reports whether host matches the no-proxy list (exact host or ".suffix").
func Bypass(host string, noProxy []string) bool {
	host = strings.ToLower(host)
	for _, n := range noProxy {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if host == n || (strings.HasPrefix(n, ".") && strings.HasSuffix(host, n)) || strings.HasSuffix(host, "."+n) {
			return true
		}
	}
	return false
}

// Provider returns a client for an external provider. Streaming responses are not
// buffered (no response compression negotiation, no body timeout: callers use contexts).
func Provider(proxyURL string, noProxy []string, connectTimeout time.Duration) (*http.Client, error) {
	t := baseTransport()
	pf, err := ProxyFunc(proxyURL, noProxy)
	if err != nil {
		return nil, err
	}
	t.Proxy = pf
	t.DialContext = (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext
	t.DisableCompression = true // keeps SSE chunks flowing as they arrive
	return &http.Client{Transport: t}, nil
}

func baseTransport() *http.Transport {
	return &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// IsProxyError reports whether err came from connecting to the proxy itself.
func IsProxyError(err error) bool {
	if err == nil {
		return false
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "proxyconnect" {
		return true
	}
	return strings.Contains(err.Error(), "proxyconnect")
}
