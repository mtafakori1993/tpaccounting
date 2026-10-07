package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/accounting"
	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

// User is the authenticated caller, validated against the accounting API on every request.
type User struct {
	Me    *accounting.Me
	Cred  accounting.Credentials
	Scope store.Scope
}

type userKey struct{}

func UserFrom(ctx context.Context) *User {
	u, _ := ctx.Value(userKey{}).(*User)
	return u
}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// csrfValid implements the same double-submit check as the accounting app.
func csrfValid(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	c, err := r.Cookie(accounting.CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.Header.Get(accounting.CSRFHeader))) == 1
}

// authenticate validates the session through GET /api/v1/auth/me (no local login,
// no cache) and enforces CSRF and per-user rate limits.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cred := accounting.FromRequest(r)
		if !cred.Valid() {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "Please sign in to the accounting application.")
			return
		}
		if !csrfValid(r) {
			writeErr(w, http.StatusForbidden, "csrf", "Missing or invalid CSRF token.")
			return
		}
		me, err := s.Acc.Me(r.Context(), cred)
		if err != nil {
			if e, ok := accounting.AsAPIError(err); ok && (e.Status == 401 || e.Status == 403) {
				writeErr(w, http.StatusUnauthorized, "unauthorized", "Your session has expired. Please sign in again.")
				return
			}
			writeErr(w, http.StatusBadGateway, "accounting_unavailable", "The accounting application is not reachable right now.")
			return
		}
		u := &User{Me: me, Cred: cred, Scope: store.Scope{CompanyID: me.Company.ID, UserID: me.ID}}
		if !s.limiter.allow(u.Scope, r.Method != http.MethodGet) {
			writeErr(w, http.StatusTooManyRequests, "rate_limited", "Too many requests to the assistant. Please wait a moment.")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// limiter is a per-user token bucket for state-changing requests and a looser one for reads.
type limiter struct {
	mu      sync.Mutex
	perMin  int
	buckets map[store.Scope]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMin int) *limiter {
	if perMin <= 0 {
		perMin = 30
	}
	return &limiter{perMin: perMin, buckets: map[store.Scope]*bucket{}}
}

func (l *limiter) allow(s store.Scope, write bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	capacity := float64(l.perMin)
	cost := 1.0
	if !write {
		cost = 0.2 // reads (polling, lists) are cheaper
	}
	b, ok := l.buckets[s]
	now := time.Now()
	if !ok {
		b = &bucket{tokens: capacity, last: now}
		l.buckets[s] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * capacity
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.last = now
	if b.tokens < cost {
		return false
	}
	b.tokens -= cost
	return true
}
