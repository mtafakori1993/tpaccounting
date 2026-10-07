package server

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAIProxyDisabledIsHarmless(t *testing.T) {
	h := aiProxy("")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/ai-api/loader.js", nil))
	if w.Code != 200 || w.Body.Len() != 0 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("disabled loader must be an empty script, got %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/ai-api/chat", nil))
	if w.Code != 404 {
		t.Fatalf("disabled route must 404, got %d", w.Code)
	}
}

func TestAIProxyForwardsAndStreams(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ai-api/chat" {
			w.WriteHeader(404)
			return
		}
		if c, err := r.Cookie("tp_session"); err != nil || c.Value != "s" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "data: two\n\n")
	}))
	defer upstream.Close()
	front := httptest.NewServer(aiProxy(upstream.URL))
	defer front.Close()
	req, _ := http.NewRequest("POST", front.URL+"/ai-api/chat", nil)
	req.AddCookie(&http.Cookie{Name: "tp_session", Value: "s"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	line, _ := br.ReadString('\n')
	if line != "data: one\n" {
		t.Fatalf("first event not streamed: %q", line)
	}
	close(release)
	_, _ = br.ReadString('\n')
	line, _ = br.ReadString('\n')
	if line != "data: two\n" {
		t.Fatalf("second event %q", line)
	}
}

func TestMicrophoneOnlyWhenAIEnabled(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	for _, ai := range []bool{false, true} {
		w := httptest.NewRecorder()
		securityHeaders(false, ai)(ok).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		has := strings.Contains(w.Header().Get("Permissions-Policy"), "microphone=(self)")
		if has != ai {
			t.Fatalf("ai=%v permissions=%q", ai, w.Header().Get("Permissions-Policy"))
		}
	}
}
