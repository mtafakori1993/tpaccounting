// Package files stores uploads (receipts, invoices, voice notes) and generated
// PDFs on the AI service's own volume. Only references go into the database.
// Files are scoped per company and user; ids are random and unguessable.
package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	ErrTooLarge   = errors.New("the file is too large")
	ErrBadType    = errors.New("this file type is not supported (use JPG, PNG, WEBP or PDF)")
	ErrNotFound   = errors.New("file not found")
	idRe          = regexp.MustCompile(`^[a-f0-9]{32}$`)
	DocumentTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "application/pdf": true}
	AudioTypes    = map[string]bool{"audio/webm": true, "audio/ogg": true, "audio/mpeg": true, "audio/wav": true, "audio/mp4": true, "video/webm": true, "application/ogg": true}
)

type Meta struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	CompanyID   int64     `json:"company_id"`
	UserID      int64     `json:"user_id"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	Dir      string
	MaxBytes int64
}

func (s *Store) dir(companyID, userID int64) string {
	return filepath.Join(s.Dir, "files", fmt.Sprint(companyID), fmt.Sprint(userID))
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Sniff detects the real content type from the bytes (never trusts the name or header).
func Sniff(content []byte) string {
	ct := http.DetectContentType(content)
	if i := strings.Index(ct, ";"); i > 0 {
		ct = ct[:i]
	}
	if bytes.HasPrefix(content, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		return "audio/webm"
	}
	if len(content) > 12 && string(content[4:8]) == "ftyp" {
		return "audio/mp4"
	}
	if ct == "application/ogg" {
		return "audio/ogg"
	}
	return ct
}

// Validate checks size and sniffed type against an allowlist.
func (s *Store) Validate(content []byte, allowed map[string]bool) (string, error) {
	if int64(len(content)) > s.MaxBytes {
		return "", ErrTooLarge
	}
	if len(content) == 0 {
		return "", ErrBadType
	}
	ct := Sniff(content)
	if !allowed[ct] {
		return "", ErrBadType
	}
	return ct, nil
}

func cleanName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	if len(name) > 120 {
		name = name[len(name)-120:]
	}
	if name == "" || name == "." {
		name = "file"
	}
	return name
}

func (s *Store) Save(ctx context.Context, companyID, userID int64, name, contentType string, content []byte) (string, error) {
	if int64(len(content)) > s.MaxBytes*2 { // generated PDFs may be a bit larger than uploads
		return "", ErrTooLarge
	}
	d := s.dir(companyID, userID)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	id := newID()
	m := Meta{ID: id, Name: cleanName(name), ContentType: contentType, Size: int64(len(content)), CompanyID: companyID, UserID: userID, CreatedAt: time.Now().UTC()}
	if err := os.WriteFile(filepath.Join(d, id+".bin"), content, 0o600); err != nil {
		return "", err
	}
	mb, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(d, id+".json"), mb, 0o600); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) Meta(companyID, userID int64, id string) (Meta, error) {
	var m Meta
	if !idRe.MatchString(id) {
		return m, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(s.dir(companyID, userID), id+".json"))
	if err != nil {
		return m, ErrNotFound
	}
	err = json.Unmarshal(b, &m)
	if m.CompanyID != companyID || m.UserID != userID {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Store) Read(ctx context.Context, companyID, userID int64, id string) (string, []byte, error) {
	m, err := s.Meta(companyID, userID, id)
	if err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.dir(companyID, userID), id+".bin"))
	if err != nil {
		return "", nil, ErrNotFound
	}
	return m.Name, b, nil
}

// Purge deletes files older than the retention period.
func (s *Store) Purge(before time.Time) int {
	n := 0
	_ = filepath.Walk(filepath.Join(s.Dir, "files"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.ModTime().Before(before) {
			if os.Remove(p) == nil && strings.HasSuffix(p, ".bin") {
				n++
			}
		}
		return nil
	})
	return n
}
