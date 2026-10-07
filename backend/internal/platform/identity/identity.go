// Package identity carries the authenticated principal and request metadata
// through the request context.
package identity

import (
	"context"
	"net/http"

	"github.com/tafakorpardazan/tpaccounting/internal/apperr"
	"github.com/tafakorpardazan/tpaccounting/internal/platform/httpx"
)

type Principal struct {
	UserID    int64
	CompanyID int64
	SessionID int64
	Email     string
	FullName  string
	RoleCode  string
	Perms     map[string]bool
}

func (p *Principal) Can(perm string) bool { return p != nil && p.Perms[perm] }

type Meta struct {
	IP        string
	UserAgent string
}

type principalKey struct{}
type metaKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

func WithMeta(ctx context.Context, m Meta) context.Context {
	return context.WithValue(ctx, metaKey{}, m)
}

func MetaFrom(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

// Require returns middleware rejecting requests whose principal lacks perm.
func Require(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !From(r.Context()).Can(perm) {
				httpx.Error(w, r, apperr.Forbidden().WithParams("permission", perm))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Check returns a Forbidden error unless the principal has perm.
func Check(ctx context.Context, perm string) error {
	if !From(ctx).Can(perm) {
		return apperr.Forbidden().WithParams("permission", perm)
	}
	return nil
}
