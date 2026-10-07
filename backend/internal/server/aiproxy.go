package server

// Tafakor AI touchpoint (b): same-origin reverse proxy /ai-api/* → AI service.
// Enabled only when AI_SERVICE_URL is set; otherwise the loader is an empty
// script and every other /ai-api path is 404, so the app behaves as before.
// Remove this file and its single use in server.go to remove the integration.

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

func aiProxy(target string) http.Handler {
	if target == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ai-api/loader.js" {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache")
				return
			}
			http.NotFound(w, r)
		})
	}
	u, err := url.Parse(strings.TrimRight(target, "/"))
	if err != nil || u.Host == "" {
		panic("invalid AI_SERVICE_URL")
	}
	p := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(u)
			pr.Out.Host = u.Host
			pr.SetXForwarded()
		},
		FlushInterval: -1, // stream server-sent events immediately
		Transport:     &http.Transport{Proxy: nil, ResponseHeaderTimeout: 3 * time.Minute, MaxIdleConns: 20, IdleConnTimeout: 90 * time.Second},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"code":"ai_unavailable","message":"The AI assistant is not available right now."}}`))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assistant answers stream longer than the server's global write timeout.
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(15 * time.Minute))
		_ = rc.SetReadDeadline(time.Now().Add(5 * time.Minute))
		p.ServeHTTP(w, r)
	})
}
