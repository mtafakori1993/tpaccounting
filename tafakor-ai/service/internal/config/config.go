// Package config loads the AI service configuration from environment variables.
// Secrets (API keys, proxy credentials) are only ever read here and never logged.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Provider struct {
	BaseURL  string
	APIKey   string
	Model    string
	ProxyURL string // http://host:8118 (may contain credentials); empty = direct
	Timeout  time.Duration
}

type Config struct {
	Port            int
	AccountingURL   string // internal base URL of the accounting app, e.g. http://app:8085
	DataDir         string // SQLite file and uploads live here (own Docker volume)
	WidgetDir       string // built widget bundle
	KnowledgeDir    string
	LLM             Provider
	VisionModel     string
	Speech          Provider
	SpeechEnabled   bool
	TTSEnabled      bool
	NoProxy         []string // hosts that must never use a provider proxy
	MaxUploadBytes  int64
	AuditRetention  time.Duration
	RateLimitPerMin int
	ApprovalTTL     time.Duration
	CompanyLockAuto bool // company-wide lock forbidding auto mode (default off)
	AppName         string
}

func Load() (Config, error) {
	c := Config{
		Port:          envInt("AI_PORT", 8090),
		AccountingURL: strings.TrimRight(env("AI_ACCOUNTING_URL", "http://app:8085"), "/"),
		DataDir:       env("AI_DATA_DIR", "/data"),
		WidgetDir:     env("AI_WIDGET_DIR", "/app/widget"),
		KnowledgeDir:  env("AI_KNOWLEDGE_DIR", "/app/knowledge"),
		LLM: Provider{
			BaseURL:  strings.TrimRight(env("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"), "/"),
			APIKey:   os.Getenv("OPENROUTER_API_KEY"),
			Model:    env("OPENROUTER_MODEL", "anthropic/claude-sonnet-4.6"),
			ProxyURL: os.Getenv("OPENROUTER_PROXY_URL"),
			Timeout:  time.Duration(envInt("OPENROUTER_TIMEOUT_SECONDS", 120)) * time.Second,
		},
		Speech: Provider{
			BaseURL:  strings.TrimRight(os.Getenv("SPEECH_BASE_URL"), "/"),
			APIKey:   os.Getenv("SPEECH_API_KEY"),
			Model:    env("SPEECH_MODEL", "whisper-1"),
			ProxyURL: os.Getenv("SPEECH_PROXY_URL"),
			Timeout:  time.Duration(envInt("SPEECH_TIMEOUT_SECONDS", 60)) * time.Second,
		},
		TTSEnabled:      envBool("AI_TTS_ENABLED", false),
		MaxUploadBytes:  int64(envInt("MAX_UPLOAD_MB", 20)) << 20,
		AuditRetention:  time.Duration(envInt("AI_AUDIT_RETENTION_DAYS", 365)) * 24 * time.Hour,
		RateLimitPerMin: envInt("AI_RATE_LIMIT_PER_MINUTE", 30),
		ApprovalTTL:     time.Duration(envInt("AI_APPROVAL_TTL_SECONDS", 600)) * time.Second,
		CompanyLockAuto: envBool("AI_COMPANY_LOCK_AUTO", false),
	}
	c.VisionModel = env("OPENROUTER_VISION_MODEL", c.LLM.Model)
	c.SpeechEnabled = c.Speech.BaseURL != ""
	for _, h := range strings.Split(env("AI_NO_PROXY", "localhost,127.0.0.1,app,db,tafakor-ai"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.NoProxy = append(c.NoProxy, strings.ToLower(h))
		}
	}
	for _, p := range []string{c.LLM.ProxyURL, c.Speech.ProxyURL} {
		if p == "" {
			continue
		}
		u, err := url.Parse(p)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return c, fmt.Errorf("invalid proxy URL (expected http://host:port)")
		}
	}
	if _, err := url.Parse(c.AccountingURL); err != nil {
		return c, fmt.Errorf("invalid AI_ACCOUNTING_URL")
	}
	return c, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(k))); err == nil {
		return v
	}
	return def
}

// RedactURL removes credentials from a URL for safe display.
func RedactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ""
	}
	u.User = nil
	return u.Scheme + "://" + u.Host
}
