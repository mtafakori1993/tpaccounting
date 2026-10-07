// Package speech turns push-to-talk audio into an editable transcript through a
// provider-agnostic SpeechProvider. The default implementation calls any
// OpenAI-compatible /audio/transcriptions endpoint through the configured proxy.
// The transcript is always shown to the user before anything happens.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/netx"
	"github.com/tafakorpardazan/tafakor-ai/internal/normalize"
	"github.com/tafakorpardazan/tafakor-ai/internal/provider"
)

type Transcript struct {
	Text       string `json:"text"`       // raw provider text
	Normalized string `json:"normalized"` // number words → digits, ASR corrections applied
}

type SpeechProvider interface {
	Transcribe(ctx context.Context, audio []byte, contentType, lang string) (string, error)
	Enabled() bool
}

var ErrDisabled = errors.New("voice input is not configured")

// OpenAICompatible implements SpeechProvider for /audio/transcriptions endpoints.
type OpenAICompatible struct {
	BaseURL, APIKey, Model string
	HTTP                   *http.Client
	Timeout                time.Duration
}

func NewOpenAICompatible(baseURL, apiKey, model, proxyURL string, noProxy []string, timeout time.Duration) (*OpenAICompatible, error) {
	c, err := netx.Provider(proxyURL, noProxy, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return &OpenAICompatible{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Model: model, HTTP: c, Timeout: timeout}, nil
}

func (o *OpenAICompatible) Enabled() bool { return o != nil && o.BaseURL != "" }

func ext(ct string) string {
	switch {
	case strings.Contains(ct, "ogg"):
		return "ogg"
	case strings.Contains(ct, "mp4"):
		return "m4a"
	case strings.Contains(ct, "mpeg"):
		return "mp3"
	case strings.Contains(ct, "wav"):
		return "wav"
	}
	return "webm"
}

func (o *OpenAICompatible) Transcribe(ctx context.Context, audio []byte, contentType, lang string) (string, error) {
	if !o.Enabled() {
		return "", ErrDisabled
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "voice."+ext(contentType))
	_, _ = fw.Write(audio)
	_ = w.WriteField("model", o.Model)
	if lang == "fa" || lang == "en" {
		_ = w.WriteField("language", lang)
	}
	_ = w.WriteField("response_format", "json")
	_ = w.Close()
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/audio/transcriptions", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	resp, err := o.HTTP.Do(req)
	if err != nil {
		if netx.IsProxyError(err) {
			return "", provider.ErrProxy
		}
		return "", fmt.Errorf("%w: speech service unreachable", provider.ErrUnavailable)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return "", provider.ErrAuth
	case resp.StatusCode == 429:
		return "", provider.ErrRateLimited
	case resp.StatusCode >= 300:
		return "", fmt.Errorf("%w (HTTP %d)", provider.ErrUnavailable, resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: unreadable transcript", provider.ErrUnavailable)
	}
	return strings.TrimSpace(out.Text), nil
}

// Correction maps a mis-recognised phrase to the right one (learned ASR aliases).
type Correction struct{ From, To string }

// Normalize applies ASR corrections and converts spoken numbers to digits.
func Normalize(text string, corrections []Correction) string {
	t := text
	for _, c := range corrections {
		if c.From != "" && strings.Contains(normalize.Text(t), normalize.Text(c.From)) {
			t = replaceFold(t, c.From, c.To)
		}
	}
	return normalize.ReplaceNumberWords(t)
}

func replaceFold(s, from, to string) string {
	if i := strings.Index(s, from); i >= 0 {
		return s[:i] + to + s[i+len(from):]
	}
	// fall back to normalised replacement
	ns, nf := normalize.Text(s), normalize.Text(from)
	return strings.Replace(ns, nf, to, 1)
}
