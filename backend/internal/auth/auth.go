// Package auth implements login, sessions, CSRF protection and the
// authentication middleware.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/config"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/db"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/identity"
)

const (
	SessionCookie = "tp_session"
	CSRFCookie    = "tp_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

type Service struct {
	Pool    *pgxpool.Pool
	Cfg     config.Config
	limiter *limiter
}

func NewService(pool *pgxpool.Pool, cfg config.Config) *Service {
	return &Service{Pool: pool, Cfg: cfg, limiter: newLimiter(cfg.LoginRateLimit)}
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Service) hashToken(token string) []byte {
	m := hmac.New(sha256.New, []byte(s.Cfg.SessionSecret))
	m.Write([]byte(token))
	return m.Sum(nil)
}

// ClientIP returns the caller's address, honouring X-Forwarded-For only behind a trusted proxy.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return xr
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Meta middleware stores IP and user agent for auditing.
func (s *Service) Meta(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := identity.WithMeta(r.Context(), identity.Meta{IP: ClientIP(r, s.Cfg.TrustProxy), UserAgent: r.UserAgent()})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StartSession creates a session for the user and sets the auth and CSRF cookies.
func (s *Service) StartSession(ctx context.Context, q db.DBTX, w http.ResponseWriter, userID, companyID int64) error {
	token := randomToken()
	m := identity.MetaFrom(ctx)
	if _, err := q.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, company_id, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		s.hashToken(token), userID, companyID, m.IP, truncate(m.UserAgent, 300), time.Now().Add(s.Cfg.SessionTTL)); err != nil {
		return err
	}
	if _, err := q.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, userID); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.Cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(s.Cfg.SessionTTL.Seconds()),
	})
	s.setCSRF(w)
	return nil
}

func (s *Service) setCSRF(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookie, Value: randomToken(), Path: "/", HttpOnly: false, Secure: s.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(s.Cfg.SessionTTL.Seconds()),
	})
}

func (s *Service) clearCookies(w http.ResponseWriter) {
	for _, n := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{Name: n, Value: "", Path: "/", MaxAge: -1, HttpOnly: n == SessionCookie, Secure: s.Cfg.CookieSecure})
	}
}

// LoadPrincipal resolves a session token into a principal.
func (s *Service) LoadPrincipal(ctx context.Context, token string) (*identity.Principal, error) {
	p := &identity.Principal{Perms: map[string]bool{}}
	var roleID int64
	var lastSeen time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT s.id, u.id, s.company_id, u.email, u.full_name, r.code, r.id, s.last_seen_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		JOIN roles r ON r.id = u.role_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now() AND u.is_active`,
		s.hashToken(token)).Scan(&p.SessionID, &p.UserID, &p.CompanyID, &p.Email, &p.FullName, &p.RoleCode, &roleID, &lastSeen)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT permission_code FROM role_permissions WHERE role_id = $1`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		p.Perms[c] = true
	}
	// Sliding expiration, written at most every five minutes.
	if time.Since(lastSeen) > 5*time.Minute {
		_, _ = s.Pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now(), expires_at = $2 WHERE id = $1`,
			p.SessionID, time.Now().Add(s.Cfg.SessionTTL))
	}
	return p, rows.Err()
}

// Authenticate requires a valid session and, for state-changing requests, a matching CSRF token.
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			httpx.Error(w, r, apperr.Unauthorized())
			return
		}
		p, err := s.LoadPrincipal(r.Context(), c.Value)
		if err != nil {
			if err != pgx.ErrNoRows {
				httpx.Error(w, r, err)
				return
			}
			s.clearCookies(w)
			httpx.Error(w, r, apperr.Unauthorized())
			return
		}
		if !CSRFValid(r) {
			httpx.Error(w, r, apperr.New(http.StatusForbidden, "csrf", "missing or invalid CSRF token"))
			return
		}
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(r.Context(), p)))
	})
}

// CSRFValid implements the double-submit cookie check for unsafe methods.
func CSRFValid(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	h := r.Header.Get(CSRFHeader)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(h)) == 1
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
