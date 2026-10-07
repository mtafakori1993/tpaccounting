// Package storage abstracts binary file storage (attachments, logos) so the
// local-disk backend can later be replaced, e.g. by S3, without touching callers.
package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type Storage interface {
	Put(ctx context.Context, key string, r io.Reader, maxBytes int64) (int64, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

var ErrTooLarge = errors.New("file too large")

var validKey = regexp.MustCompile(`^[a-z0-9_-]+(/[a-z0-9_.-]+)*$`)

// NewKey returns a unique, unguessable storage key under prefix.
func NewKey(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s/%s/%s", prefix, time.Now().UTC().Format("2006/01"), hex.EncodeToString(b))
}

type Local struct{ Dir string }

func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Local{Dir: dir}, nil
}

func (l *Local) path(key string) (string, error) {
	if !validKey.MatchString(key) {
		return "", fmt.Errorf("invalid storage key")
	}
	return filepath.Join(l.Dir, filepath.FromSlash(key)), nil
}

func (l *Local) Put(_ context.Context, key string, r io.Reader, maxBytes int64) (int64, error) {
	p, err := l.path(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return 0, err
	}
	tmp := p + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, io.LimitReader(r, maxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxBytes {
		err = ErrTooLarge
	}
	if err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, p)
}

func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
