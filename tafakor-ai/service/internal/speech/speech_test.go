package speech

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
)

func TestTranscribeMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		b, _ := io.ReadAll(f)
		if string(b) != "AUDIO" || r.FormValue("model") != "whisper-1" || r.FormValue("language") != "fa" {
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, `{"text":"دو میلیون و پانصد هزار تومان از محمد تفکوری قرض گرفتیم"}`)
	}))
	defer srv.Close()
	o, _ := NewOpenAICompatible(srv.URL+"/v1", "k", "whisper-1", "", nil, 5*time.Second)
	txt, err := o.Transcribe(context.Background(), []byte("AUDIO"), "audio/webm", "fa")
	if err != nil {
		t.Fatal(err)
	}
	n := Normalize(txt, []Correction{{From: "محمد تفکوری", To: "محمد تفکری"}})
	if !strings.Contains(n, "2500000 تومان") || !strings.Contains(n, "محمد تفکری") {
		t.Fatalf("normalized %q", n)
	}
}

func TestSpeechProxyUnreachable(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := "http://" + l.Addr().String()
	l.Close()
	o, _ := NewOpenAICompatible("http://example.invalid/v1", "k", "m", dead, nil, 3*time.Second)
	if _, err := o.Transcribe(context.Background(), []byte("x"), "audio/webm", "fa"); !errors.Is(err, provider.ErrProxy) {
		t.Fatalf("want proxy error, got %v", err)
	}
}

func TestDisabled(t *testing.T) {
	var o *OpenAICompatible
	if o.Enabled() {
		t.Fatal()
	}
	o2 := &OpenAICompatible{}
	if _, err := o2.Transcribe(context.Background(), nil, "", ""); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
}
