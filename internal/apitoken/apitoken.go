// SPDX-License-Identifier: MIT

// Package apitoken manages the API access tokens of a profile (ADR-0013
// §5, roadmap 6.2.2): tokens with scopes, stored only as their SHA-256
// hash in the profile database. The value itself is shown once on creation.
package apitoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"time"
	"uuid"
)

// LegacyFileName is the token file in the data directory, used before the
// tokens moved to the profile database; it is imported once on start.
const LegacyFileName = "api-tokens.json"

// Scope is one right of a token.
type Scope string

// Scopes: read, control and admin form the hierarchy, strongest last
// (admin covers control, control covers read). Overlay is a standalone
// scope: it opens only the overlay server, never the API (B5, S8 of
// open-findings-2026-10-26.md).
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

// ErrNameTaken is returned when a token with the name already exists.
var ErrNameTaken = errors.New("token name is taken")

// Token is one stored token; SHA256 is the hex hash of the token value.
type Token struct {
	// ID names the token.
	ID string
	// Name is the human-readable name chosen on creation, unique.
	Name string
	// Scopes are the rights of the token.
	Scopes []Scope
	// CreatedAt is when the token was created.
	CreatedAt time.Time
	// SHA256 is the hex hash of the token value.
	SHA256 string
}

// legacyToken is the on-disk shape of a token in the legacy file.
type legacyToken struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Scopes    []Scope   `json:"scopes"`
	CreatedAt time.Time `json:"created"`
	SHA256    string    `json:"sha256"`
}

// rank is the hierarchy: control covers read, admin covers control. The
// overlay scope is not in the hierarchy: it opens only the overlay
// server, never the API (B5, S8), and must be carried explicitly.
//
//nolint:gochecknoglobals // the static hierarchy
var rank = map[Scope]int{Read: 1, Control: 2, Admin: 3}

// Satisfies reports whether the token has scope through the hierarchy;
// the overlay scope is outside of it and must be carried explicitly
// (B5, S8).
func (t Token) Satisfies(scope Scope) bool {
	if scope == Overlay {
		return slices.Contains(t.Scopes, Overlay)
	}
	best := 0
	for _, s := range t.Scopes {
		if r := rank[s]; r > best {
			best = r
		}
	}
	return best >= rank[scope]
}

// Repository stores the tokens of a profile; *store.Store implements it.
type Repository interface {
	// APIToken returns the token with the ID; found is false if there is
	// none.
	APIToken(ctx context.Context, id string) (Token, bool, error)
	// APITokenByHash returns the token with the SHA-256 hash of its value;
	// found is false if there is none.
	APITokenByHash(ctx context.Context, hash string) (Token, bool, error)
	// APITokenByName returns the token with the name; found is false if
	// there is none.
	APITokenByName(ctx context.Context, name string) (Token, bool, error)
	// CountAPITokensByName counts the tokens with the name.
	CountAPITokensByName(ctx context.Context, name string) (int64, error)
	// ListAPITokens returns the tokens, oldest first.
	ListAPITokens(ctx context.Context) ([]Token, error)
	// CreateAPIToken inserts a token; a taken name fails with ErrNameTaken.
	CreateAPIToken(ctx context.Context, tok Token) error
	// DeleteAPIToken removes the token with the ID; found is false if
	// there is none.
	DeleteAPIToken(ctx context.Context, id string) (bool, error)
}

// Store manages the tokens of one profile through its database.
type Store struct {
	repo Repository
}

// New returns the token store of the profile database.
func New(repo Repository) *Store {
	return &Store{repo: repo}
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
func (s *Store) Create(ctx context.Context, name string, scopes []Scope) (Token, string, error) {
	value, hash, err := NewValue()
	if err != nil {
		return Token{}, "", err
	}
	tok, err := s.create(ctx, name, scopes, hash)
	return tok, value, err
}

// CreateWith adds a token with name, scopes and the given value; the store
// hashes the value and never stores it. It backs the bootstrap token the
// core creates from the value of STREAMCREW_TOKEN.
func (s *Store) CreateWith(ctx context.Context, name string, scopes []Scope, value string) (Token, error) {
	if value == "" {
		return Token{}, errors.New("the token value is required")
	}
	sum := sha256.Sum256([]byte(value))
	return s.create(ctx, name, scopes, hex.EncodeToString(sum[:]))
}

// Import stores a token from the legacy file as is, with its ID and hash
// (the one-time move of the data directory file into the profile database,
// S5 of the code review 2026-10-24).
func (s *Store) Import(ctx context.Context, tok Token) error {
	return s.repo.CreateAPIToken(ctx, tok)
}

// create stores the token under the given hash.
func (s *Store) create(ctx context.Context, name string, scopes []Scope, hash string) (Token, error) {
	if name == "" {
		return Token{}, errors.New("name is required")
	}
	clean, err := normalizeScopes(scopes)
	if err != nil {
		return Token{}, err
	}
	tok := Token{
		ID:        uuid.NewV7().String(),
		Name:      name,
		Scopes:    clean,
		CreatedAt: time.Now().UTC(),
		SHA256:    hash,
	}
	if err := s.repo.CreateAPIToken(ctx, tok); err != nil {
		return Token{}, err
	}
	return tok, nil
}

// List returns the stored tokens.
func (s *Store) List(ctx context.Context) ([]Token, error) {
	return s.repo.ListAPITokens(ctx)
}

// Revoke removes the token with the ID, or with the name, which the
// database keeps unique; it reports whether a token was removed.
func (s *Store) Revoke(ctx context.Context, idOrName string) (bool, error) {
	if tok, found, err := s.repo.APIToken(ctx, idOrName); err != nil {
		return false, err
	} else if found {
		return s.repo.DeleteAPIToken(ctx, tok.ID)
	}
	tok, found, err := s.repo.APITokenByName(ctx, idOrName)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	return s.repo.DeleteAPIToken(ctx, tok.ID)
}

// Validate checks a token value and returns the stored token. A store
// error fails closed: the value does not validate.
func (s *Store) Validate(ctx context.Context, value string) (Token, bool) {
	if value == "" {
		return Token{}, false
	}
	sum := sha256.Sum256([]byte(value))
	tok, found, err := s.repo.APITokenByHash(ctx, hex.EncodeToString(sum[:]))
	return tok, found && err == nil
}

// Enabled reports whether at least one token is stored. The error is
// non-nil when the store cannot be read: the caller must fail closed, not
// treat the store as empty.
func (s *Store) Enabled(ctx context.Context) (bool, error) {
	tokens, err := s.repo.ListAPITokens(ctx)
	return len(tokens) > 0, err
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

// LegacyLoad parses the legacy token file and returns its tokens; the
// version must be 1.
func LegacyLoad(data []byte) ([]Token, error) {
	var f struct {
		Version int           `json:"version"`
		Tokens  []legacyToken `json:"tokens"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("read tokens: %w", err)
	}
	if f.Version != 1 {
		return nil, fmt.Errorf("read tokens: unsupported version %d", f.Version)
	}
	out := make([]Token, len(f.Tokens))
	for i, t := range f.Tokens {
		out[i] = Token(t)
	}
	return out, nil
}
