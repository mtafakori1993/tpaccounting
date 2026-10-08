package provider

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// forwardProxy is a minimal HTTP forward proxy (absolute-URI requests) that streams
// the upstream body chunk by chunk, like a non-buffering real proxy.
func forwardProxy(t *testing.T, hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !r.URL.IsAbs() {
			http.Error(w, "not a proxy request", 400)
			return
		}
		out, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
		out.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.(*http.Transport).Clone().RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		buf := make([]byte, 512)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				w.(http.Flusher).Flush()
			}
			if err != nil {
				return
			}
		}
	}))
}

func TestSSEStreamsThroughProxyWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
		fmt.Fprint(w, `data: {"model":"m","choices":[{"delta":{"content":"Hel"}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		<-release // the second chunk is only sent after the client saw the first
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"lo"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"cost":0.001}}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	var hits atomic.Int32
	proxy := forwardProxy(t, &hits)
	defer proxy.Close()

	o, err := NewOpenRouter(upstream.URL, "k", "m", proxy.URL, nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	res, err := o.Stream(context.Background(), Request{Messages: []Message{Text("user", "hi")}}, func(d string) {
		deltas = append(deltas, d)
		if d == "Hel" {
			close(release) // proves the first chunk arrived before the stream ended
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "Hello" || len(deltas) != 2 {
		t.Fatalf("got %q %v", res.Text, deltas)
	}
	if res.Usage.CompletionTokens != 2 || res.Usage.Cost != 0.001 {
		t.Fatalf("usage %+v", res.Usage)
	}
	if hits.Load() == 0 {
		t.Fatal("request did not go through the proxy")
	}
}

func TestProxyUnreachableStopsSafelyWithoutRetry(t *testing.T) {
	var upstreamHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamHits.Add(1) }))
	defer upstream.Close()
	// a closed port: nothing listens there
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()
	o, _ := NewOpenRouter(upstream.URL, "k", "m", dead, nil, 5*time.Second)
	start := time.Now()
	_, err := o.Stream(context.Background(), Request{Messages: []Message{Text("user", "hi")}}, nil)
	if !errors.Is(err, ErrProxy) {
		t.Fatalf("want ErrProxy, got %v", err)
	}
	if upstreamHits.Load() != 0 {
		t.Fatal("must never fall back to a direct connection")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("proxy failure must not be retried with backoff")
	}
	if !strings.Contains(PlainMessage(err), "proxy") {
		t.Fatal("plain-language message expected")
	}
	if strings.Contains(err.Error(), dead) {
		t.Fatal("error must not leak the proxy URL")
	}
}

func TestProxyCredentialsNotLeaked(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	o, _ := NewOpenRouter("http://example.invalid", "secret-key", "m", "http://user:pass@"+addr, nil, 2*time.Second)
	_, err := o.Stream(context.Background(), Request{Messages: []Message{Text("user", "x")}}, nil)
	if err == nil || strings.Contains(err.Error(), "pass") || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("leaky error: %v", err)
	}
}

func TestRetryOn503ThenSuccess(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	o, _ := NewOpenRouter(up.URL, "k", "m", "", nil, 5*time.Second)
	res, err := o.Complete(context.Background(), Request{})
	if err != nil || res.Text != "ok" || n.Load() != 2 {
		t.Fatalf("%v %q %d", err, res.Text, n.Load())
	}
}

func TestAuthErrorNotRetried(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); w.WriteHeader(401) }))
	defer up.Close()
	o, _ := NewOpenRouter(up.URL, "k", "m", "", nil, 5*time.Second)
	_, err := o.Complete(context.Background(), Request{})
	if !errors.Is(err, ErrAuth) || n.Load() != 1 {
		t.Fatalf("%v %d", err, n.Load())
	}
}

// keep imports used on all platforms
var _ = bufio.NewReader
var _ = io.EOF
var _ = url.Parse

func TestCreditInFlight402IsRetriedThenSucceeds(t *testing.T) {
	var n atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(402)
			fmt.Fprint(w, `{"error":{"message":"This request would exceed your available credits given your current in-flight requests."}}`)
			return
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	o, _ := NewOpenRouter(up.URL, "k", "m", "", nil, 30*time.Second)
	res, err := o.Complete(context.Background(), Request{})
	if err != nil || res.Text != "ok" || n.Load() != 2 {
		t.Fatalf("%v %q %d", err, res.Text, n.Load())
	}
	// a plain 402 (no credit at all) is not retried
	var m atomic.Int32
	up2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Add(1)
		w.WriteHeader(402)
		fmt.Fprint(w, `{"error":{"message":"Insufficient credits"}}`)
	}))
	defer up2.Close()
	o2, _ := NewOpenRouter(up2.URL, "k", "m", "", nil, 5*time.Second)
	if _, err := o2.Complete(context.Background(), Request{}); !errors.Is(err, ErrRejected) || m.Load() != 1 {
		t.Fatalf("plain 402 must not be retried: %v %d", err, m.Load())
	}
}

func TestFallbackModelsAreSentToOpenRouter(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer up.Close()
	o, _ := NewOpenRouter(up.URL, "k", "google/gemini-2.5-flash-lite", "", nil, 5*time.Second)
	o.Fallbacks = []string{"google/gemini-2.5-flash-lite", "google/gemini-2.0-flash-001"}
	if _, err := o.Complete(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"models":["google/gemini-2.5-flash-lite","google/gemini-2.0-flash-001"]`) {
		t.Fatalf("fallbacks missing or duplicated: %s", got)
	}
}
