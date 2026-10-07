// Package approval issues single-use, expiring approval tokens bound to the exact
// payload (tool + canonical arguments, which include amount, party, account, date
// and the attached document's hash). Any change to the payload voids the approval.
package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/tafakorpardazan/tafakor-ai/internal/store"
)

type Service struct {
	Store store.Repository
	TTL   time.Duration
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Issue creates a token for this user/task/payload. Only the raw token goes to the
// browser; only its hash is stored.
func (s *Service) Issue(ctx context.Context, sc store.Scope, taskID, payloadHash string) (string, time.Time, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	exp := s.now().Add(s.TTL)
	err := s.Store.SaveApproval(ctx, store.ApprovalToken{TokenHash: hash(tok), CompanyID: sc.CompanyID, UserID: sc.UserID,
		TaskID: taskID, PayloadHash: payloadHash, ExpiresAt: exp})
	return tok, exp, err
}

// Consume validates and burns the token. payloadHash must be recomputed by the
// caller from the payload it is about to execute (never taken from the client).
func (s *Service) Consume(ctx context.Context, sc store.Scope, taskID, token, payloadHash string) error {
	return s.Store.ConsumeApproval(ctx, store.ApprovalToken{TokenHash: hash(token), CompanyID: sc.CompanyID, UserID: sc.UserID,
		TaskID: taskID, PayloadHash: payloadHash}, s.now())
}
