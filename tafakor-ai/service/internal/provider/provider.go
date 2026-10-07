// Package provider abstracts the language/vision model behind AIProvider.
// OpenRouterProvider is the only implementation; all calls go through the
// configured HTTP proxy. A small classifier model can later be plugged in as
// another AIProvider (see Router in router.go) without touching callers.
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/netx"
)

// Part is one piece of message content: text or an image (data: URL).
type Part struct {
	Type     string    `json:"type"` // text | image_url | file
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	File     *File     `json:"file,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type File struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"` // data:application/pdf;base64,...
}

type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content []Part `json:"content"`
}

func Text(role, s string) Message {
	return Message{Role: role, Content: []Part{{Type: "text", Text: s}}}
}

type Request struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	JSON        bool // ask for a JSON object response
}

type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	Cost             float64 `json:"cost"`
}

type Result struct {
	Text  string
	Model string
	Usage Usage
}

// AIProvider is implemented by every model backend.
type AIProvider interface {
	// Stream sends the request and calls onDelta for every text chunk as it arrives.
	Stream(ctx context.Context, req Request, onDelta func(string)) (Result, error)
	// Complete is Stream without incremental callbacks.
	Complete(ctx context.Context, req Request) (Result, error)
	// Health checks connectivity (through the proxy) without spending tokens.
	Health(ctx context.Context) error
	DefaultModel() string
}

// Error kinds surfaced to users in plain language.
var (
	ErrNotConfigured = errors.New("the AI provider is not configured")
	ErrProxy         = errors.New("the network proxy for the AI provider is unreachable")
	ErrUnavailable   = errors.New("the AI provider is unavailable")
	ErrRateLimited   = errors.New("the AI provider is rate limiting requests")
	ErrAuth          = errors.New("the AI provider rejected the API key")
)

type OpenRouter struct {
	BaseURL    string
	APIKey     string
	Model      string
	HTTP       *http.Client
	Timeout    time.Duration
	MaxRetries int
	AppTitle   string
}

func NewOpenRouter(baseURL, apiKey, model, proxyURL string, noProxy []string, timeout time.Duration) (*OpenRouter, error) {
	c, err := netx.Provider(proxyURL, noProxy, 15*time.Second)
	if err != nil {
		return nil, err
	}
	return &OpenRouter{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Model: model, HTTP: c, Timeout: timeout, MaxRetries: 2, AppTitle: "Tafakor AI"}, nil
}

func (o *OpenRouter) DefaultModel() string { return o.Model }

func (o *OpenRouter) Complete(ctx context.Context, req Request) (Result, error) {
	return o.Stream(ctx, req, nil)
}

func (o *OpenRouter) Health(ctx context.Context) error {
	if o.APIKey == "" {
		return ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/key", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return classifyNetErr(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return statusErr(resp.StatusCode)
}

func classifyNetErr(err error) error {
	if netx.IsProxyError(err) {
		return fmt.Errorf("%w", ErrProxy)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, safeErr(err))
}

// safeErr strips URLs (which may embed proxy credentials) from network errors.
func safeErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "://"); i >= 0 {
		return "network error"
	}
	return s
}

func statusErr(code int) error {
	switch {
	case code < 300:
		return nil
	case code == 401 || code == 403:
		return ErrAuth
	case code == 429:
		return ErrRateLimited
	case code == 407:
		return ErrProxy
	default:
		return fmt.Errorf("%w (HTTP %d)", ErrUnavailable, code)
	}
}

func retryable(err error) bool {
	return errors.Is(err, ErrRateLimited) || (errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrAuth))
}

type chatBody struct {
	Model          string         `json:"model"`
	Messages       []Message      `json:"messages"`
	Stream         bool           `json:"stream"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	Usage          map[string]any `json:"usage,omitempty"`
}

func (o *OpenRouter) Stream(ctx context.Context, req Request, onDelta func(string)) (Result, error) {
	if o.APIKey == "" {
		return Result{}, ErrNotConfigured
	}
	if req.Model == "" {
		req.Model = o.Model
	}
	body := chatBody{Model: req.Model, Messages: req.Messages, Stream: true, MaxTokens: req.MaxTokens, Temperature: req.Temperature,
		Usage: map[string]any{"include": true}}
	if req.JSON {
		body.ResponseFormat = map[string]any{"type": "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	var lastErr error
	for attempt := 0; attempt <= o.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return Result{}, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 700 * time.Millisecond):
			}
		}
		res, started, err := o.once(ctx, payload, onDelta)
		if err == nil {
			return res, nil
		}
		lastErr = err
		// Never retry once text has been streamed to the user, on proxy failure
		// (stop safely, no alternative route) or on auth errors.
		if started || errors.Is(err, ErrProxy) || !retryable(err) {
			break
		}
	}
	return Result{}, lastErr
}

func (o *OpenRouter) once(ctx context.Context, payload []byte, onDelta func(string)) (Result, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Result{}, false, err
	}
	hr.Header.Set("Authorization", "Bearer "+o.APIKey)
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	hr.Header.Set("X-Title", o.AppTitle)
	resp, err := o.HTTP.Do(hr)
	if err != nil {
		return Result{}, false, classifyNetErr(err)
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return Result{}, false, err
	}
	var res Result
	var sb strings.Builder
	started := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // SSE comments (": OPENROUTER PROCESSING") and blank lines
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
				Code    any    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return res, started, fmt.Errorf("%w: %s", ErrUnavailable, chunk.Error.Message)
		}
		if chunk.Model != "" {
			res.Model = chunk.Model
		}
		if chunk.Usage != nil {
			res.Usage = *chunk.Usage
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				started = true
				sb.WriteString(c.Delta.Content)
				if onDelta != nil {
					onDelta(c.Delta.Content)
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return res, started, classifyNetErr(err)
	}
	res.Text = sb.String()
	return res, started, nil
}

// PlainMessage converts provider errors into a short user-facing explanation.
func PlainMessage(err error) string {
	switch {
	case errors.Is(err, ErrNotConfigured):
		return "The AI assistant is not configured yet (missing API key). Ask your administrator."
	case errors.Is(err, ErrProxy):
		return "I can't reach the AI service because the network proxy is not responding. Nothing was registered. Please try again later."
	case errors.Is(err, ErrAuth):
		return "The AI service rejected our credentials. Nothing was registered. Ask your administrator to check the API key."
	case errors.Is(err, ErrRateLimited):
		return "The AI service is busy right now. Nothing was registered. Please try again in a minute."
	case errors.Is(err, context.DeadlineExceeded):
		return "The AI service took too long to answer. Nothing was registered."
	case errors.Is(err, ErrUnavailable):
		return "The AI service is unavailable right now. Nothing was registered."
	}
	return "Something went wrong while talking to the AI service. Nothing was registered."
}
