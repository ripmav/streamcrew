// SPDX-License-Identifier: MIT

// Package apitoken manages the API access tokens of the core (roadmap
// 6.2.2): tokens with scopes, stored as a 0600 JSON file in the data
// directory. Only the SHA-256 hash of a token is stored; the value itself
// is shown once on creation. Every operation reads the file fresh, so a
// running core and the CLI see the same state without synchronization.
package apitoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
	"uuid"
)

// FileName is the token file in the data directory.
const FileName = "api-tokens.json"

// Scope is one right of a token.
type Scope string

// Scopes, strongest last: admin covers control, control covers read, and
// overlay is a read scope for overlay processes.
const (
	Read    Scope = "read"
	Control Scope = "control"
	Admin   Scope = "admin"
	Overlay Scope = "overlay"
)

// All lists the valid scopes.
//
//nolint:gochecknoglobals // the static list of scopes
var All = []Scope{Read, Overlay, Control, Admin}

// ErrInvalidScope is returned for a scope that is not in All.
var ErrInvalidScope = errors.New("invalid scope")

// Token is one stored token; SHA256 is the hex hash of the token value.
type Token struct {
	// ID names the token.
	ID string `json:"id"`
	// Name is the human-readable name chosen on creation.
	Name string `json:"name"`
	// Scopes are the rights of the token.
	Scopes []Scope `json:"scopes"`
	// CreatedAt is when the token was created.
	CreatedAt time.Time `json:"created"`
	// SHA256 is the hex hash of the token value.
	SHA256 string `json:"sha256"`
}

// rank is the hierarchy: control covers read, admin covers control, and
// overlay counts as read.
//
//nolint:gochecknoglobals // the static hierarchy
var rank = map[Scope]int{Read: 1, Overlay: 1, Control: 2, Admin: 3}

// Satisfies reports whether the token has scope through the hierarchy.
func (t Token) Satisfies(scope Scope) bool {
	best := 0
	for _, s := range t.Scopes {
		if r := rank[s]; r > best {
			best = r
		}
	}
	return best >= rank[scope]
}

// file is the on-disk shape.
type file struct {
	Version int     `json:"version"`
	Tokens  []Token `json:"tokens"`
}

// Store manages the token file of one data directory.
type Store struct {
	path string
	mu   sync.Mutex
}

// Open returns the store of the data directory; the file is created on the
// first write.
func Open(dir string) *Store {
	return &Store{path: filepath.Join(dir, FileName)}
}

// NewValue returns a new token value and its SHA-256 hash (hex).
func NewValue() (value, hash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	value = "sc_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(value))
	return value, hex.EncodeToString(sum[:]), nil
}

// Create adds a token with name and scopes and returns it with its value,
// which is shown once.
func (s *Store) Create(name string, scopes []Scope) (Token, string, error) {
	if name == "" {
		return Token{}, "", errors.New("name is required")
	}
	clean, err := normalizeScopes(scopes)
	if err != nil {
		return Token{}, "", err
	}
	value, hash, err := NewValue()
	if err != nil {
		return Token{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return Token{}, "", err
	}
	tok := Token{
		ID:        uuid.NewV7().String(),
		Name:      name,
		Scopes:    clean,
		CreatedAt: time.Now().UTC(),
		SHA256:    hash,
	}
	f.Tokens = append(f.Tokens, tok)
	if err := s.write(f); err != nil {
		return Token{}, "", err
	}
	return tok, value, nil
}

// List returns the stored tokens.
func (s *Store) List() ([]Token, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	return f.Tokens, nil
}

// Revoke removes the token with the ID or name; it reports whether the
// token existed.
func (s *Store) Revoke(idOrName string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return false, err
	}
	kept := f.Tokens[:0]
	found := false
	for _, t := range f.Tokens {
		if t.ID == idOrName || t.Name == idOrName {
			found = true
			continue
		}
		kept = append(kept, t)
	}
	if !found {
		return false, nil
	}
	f.Tokens = kept
	if err := s.write(f); err != nil {
		return false, err
	}
	return true, nil
}

// Validate checks a token value and returns the stored token.
func (s *Store) Validate(value string) (Token, bool) {
	tokens, err := s.List()
	if err != nil {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(value))
	hash := hex.EncodeToString(sum[:])
	for _, t := range tokens {
		if subtle.ConstantTimeCompare([]byte(t.SHA256), []byte(hash)) == 1 {
			return t, true
		}
	}
	return Token{}, false
}

// Enabled reports whether at least one token is stored.
func (s *Store) Enabled() bool {
	tokens, err := s.List()
	return err == nil && len(tokens) > 0
}

// normalizeScopes validates and de-duplicates the scopes in a stable order.
func normalizeScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return nil, errors.New("at least one scope is required")
	}
	seen := make(map[Scope]bool, len(scopes))
	for _, s := range scopes {
		if !slices.Contains(All, s) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidScope, s)
		}
		seen[s] = true
	}
	clean := make([]Scope, 0, len(seen))
	for _, s := range All {
		if seen[s] {
			clean = append(clean, s)
		}
	}
	return clean, nil
}

// read loads the token file; a missing file is an empty store.
func (s *Store) read() (file, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return file{Version: 1}, nil
	}
	if err != nil {
		return file{}, fmt.Errorf("read tokens: %w", err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return file{}, fmt.Errorf("read tokens: %w", err)
	}
	if f.Version != 1 {
		return file{}, fmt.Errorf("read tokens: unsupported version %d", f.Version)
	}
	return f, nil
}

// write stores the token file atomically with mode 0600.
func (s *Store) write(f file) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("write tokens: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tmp-tokens-*")
	if err != nil {
		return fmt.Errorf("write tokens: %w", err)
	}
	_, werr := tmp.Write(data)
	werr = errors.Join(werr, tmp.Chmod(0o600), tmp.Sync(), tmp.Close())
	if werr == nil {
		werr = os.Rename(tmp.Name(), s.path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write tokens: %w", werr)
	}
	return nil
}
