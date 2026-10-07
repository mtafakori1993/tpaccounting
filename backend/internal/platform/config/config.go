// Package config loads runtime configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv         string
	Port           int
	DatabaseURL    string
	SessionSecret  string
	SessionTTL     time.Duration
	CookieSecure   bool
	AllowedOrigins []string
	StorageDir     string
	StaticDir      string
	MaxUploadBytes int64
	LoginRateLimit int // attempts per minute per IP
	TrustProxy     bool
	ChromePath     string
}

func Load() (Config, error) {
	c := Config{
		AppEnv:         env("APP_ENV", "production"),
		Port:           envInt("APP_PORT", 8085),
		SessionSecret:  os.Getenv("SESSION_SECRET"),
		SessionTTL:     time.Duration(envInt("SESSION_TTL_HOURS", 12)) * time.Hour,
		CookieSecure:   envBool("COOKIE_SECURE", false),
		StorageDir:     env("STORAGE_DIR", "/data/attachments"),
		StaticDir:      env("STATIC_DIR", "/app/web"),
		MaxUploadBytes: int64(envInt("MAX_UPLOAD_MB", 20)) << 20,
		LoginRateLimit: envInt("LOGIN_RATE_LIMIT_PER_MINUTE", 10),
		TrustProxy:     envBool("TRUST_PROXY", false),
		ChromePath:     os.Getenv("CHROME_PATH"),
	}
	if o := strings.TrimSpace(os.Getenv("CORS_ALLOWED_ORIGINS")); o != "" {
		for _, s := range strings.Split(o, ",") {
			if s = strings.TrimSpace(s); s != "" {
				c.AllowedOrigins = append(c.AllowedOrigins, s)
			}
		}
	}
	c.DatabaseURL = os.Getenv("DATABASE_URL")
	if c.DatabaseURL == "" {
		u := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(env("DB_USER", "tpaccounting"), os.Getenv("DB_PASSWORD")),
			Host:   fmt.Sprintf("%s:%s", env("DB_HOST", "localhost"), env("DB_PORT", "5432")),
			Path:   env("DB_NAME", "tpaccounting"),
		}
		q := u.Query()
		q.Set("sslmode", env("DB_SSLMODE", "disable"))
		u.RawQuery = q.Encode()
		c.DatabaseURL = u.String()
	}
	if len(c.SessionSecret) < 32 {
		return c, fmt.Errorf("SESSION_SECRET must be set to at least 32 characters")
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
