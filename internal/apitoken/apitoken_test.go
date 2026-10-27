// SPDX-License-Identifier: MIT

package apitoken

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRepo is an in-memory Repository; it enforces the unique name like the
// database does.
type fakeRepo struct {
	mu     sync.Mutex
	tokens []Token
	err    error
}

func (f *fakeRepo) fail() error { return f.err }

func (f *fakeRepo) APIToken(_ context.Context, id string) (Token, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.ID == id {
			return t, true, nil
		}
	}
	return Token{}, false, f.fail()
}

func (f *fakeRepo) APITokenByHash(_ context.Context, hash string) (Token, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.SHA256 == hash {
			return t, true, nil
		}
	}
	return Token{}, false, f.fail()
}

func (f *fakeRepo) APITokenByName(_ context.Context, name string) (Token, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.Name == name {
			return t, true, nil
		}
	}
	return Token{}, false, f.fail()
}

func (f *fakeRepo) CountAPITokensByName(_ context.Context, name string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, t := range f.tokens {
		if t.Name == name {
			n++
		}
	}
	return n, f.fail()
}

func (f *fakeRepo) ListAPITokens(_ context.Context) ([]Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return append([]Token(nil), f.tokens...), nil
}

func (f *fakeRepo) CreateAPIToken(_ context.Context, tok Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.Name == tok.Name {
			return ErrNameTaken
		}
	}
	f.tokens = append(f.tokens, tok)
	return nil
}

func (f *fakeRepo) DeleteAPIToken(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, t := range f.tokens {
		if t.ID == id {
			f.tokens = append(f.tokens[:i], f.tokens[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func newStore(t *testing.T) (*Store, *fakeRepo) {
	t.Helper()
	repo := &fakeRepo{}
	return New(repo), repo
}

// TestStoreRoundtrip covers creation, validation, listing and revocation of
// tokens.
func TestStoreRoundtrip(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := t.Context()

	enabled, err := s.Enabled(ctx)
	require.NoError(t, err)
	assert.False(t, enabled, "a new store has no tokens")

	tok, value, err := s.Create(ctx, "tui", []Scope{Admin})
	require.NoError(t, err)
	require.NotEmpty(t, tok.ID)
	assert.Equal(t, "tui", tok.Name)
	assert.Equal(t, []Scope{Admin}, tok.Scopes)
	assert.NotEmpty(t, tok.SHA256)
	assert.NotEqual(t, "", value)
	assert.NotEqual(t, value, tok.SHA256, "the stored hash is not the value")

	_, ok := s.Validate(ctx, value)
	require.True(t, ok, "the created token validates")
	// the flipped last character must differ from the original one, or
	// the "wrong" token is the right one (hex digits are 0-9a-f).
	flip := '0'
	if value[len(value)-1] == '0' {
		flip = '1'
	}
	_, ok = s.Validate(ctx, value[:len(value)-1]+string(flip))
	assert.False(t, ok, "a wrong value does not validate")

	other, _, err := s.Create(ctx, "overlay", []Scope{Overlay, Read})
	require.NoError(t, err)
	assert.Equal(t, "overlay", other.Name)

	tokens, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 2)
	assert.Equal(t, []Scope{Read, Overlay}, tokens[1].Scopes, "scopes are de-duplicated in a stable order")

	enabled, err = s.Enabled(ctx)
	require.NoError(t, err)
	assert.True(t, enabled)

	ok, err = s.Revoke(ctx, "overlay")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.Revoke(ctx, "overlay")
	require.NoError(t, err)
	require.False(t, ok, "a revoked token is gone")
}

// TestEnabledFailsClosed covers the fail-closed contract: a store error is
// an error, not an empty store (code review 2026-10-24, S1/V1).
func TestEnabledFailsClosed(t *testing.T) {
	t.Parallel()
	s, repo := newStore(t)

	enabled, err := s.Enabled(t.Context())
	require.NoError(t, err)
	assert.False(t, enabled)

	repo.err = errors.New("disk full")
	enabled, err = s.Enabled(t.Context())
	require.Error(t, err)
	assert.False(t, enabled)
}

// TestCreateWith covers the bootstrap token from a known value
// (STREAMCREW_TOKEN, code review 2026-10-24, S2).
func TestCreateWith(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := t.Context()

	_, err := s.CreateWith(ctx, "bootstrap", []Scope{Admin}, "")
	require.Error(t, err)

	tok, err := s.CreateWith(ctx, "bootstrap", []Scope{Admin}, "sc_fixed-value")
	require.NoError(t, err)
	assert.Equal(t, "bootstrap", tok.Name)

	_, ok := s.Validate(ctx, "sc_fixed-value")
	require.True(t, ok, "the fixed value validates")

	enabled, err := s.Enabled(ctx)
	require.NoError(t, err)
	assert.True(t, enabled)
}

// TestCreateValidatesChecks the input validation of Create.
func TestCreateValidates(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := t.Context()

	_, _, err := s.Create(ctx, "", []Scope{Read})
	require.Error(t, err)
	_, _, err = s.Create(ctx, "x", nil)
	require.Error(t, err)
	_, _, err = s.Create(ctx, "x", []Scope{"bogus"})
	require.ErrorIs(t, err, ErrInvalidScope)
}

// TestCreateRefusesATakenName covers the unique token name
// (code review 2026-10-24, S12).
func TestCreateRefusesATakenName(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := t.Context()

	_, _, err := s.Create(ctx, "cli", []Scope{Read})
	require.NoError(t, err)
	_, _, err = s.Create(ctx, "cli", []Scope{Read})
	require.ErrorIs(t, err, ErrNameTaken)
}

// TestImportStoresATokenAsIs covers the one-time move of the legacy file
// (code review 2026-10-24, S5).
func TestImportStoresATokenAsIs(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	ctx := t.Context()

	tok := Token{
		ID:        "33333333-3333-7000-8000-333333333333",
		Name:      "legacy",
		Scopes:    []Scope{Control},
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		SHA256:    "c",
	}
	require.NoError(t, s.Import(ctx, tok))

	got, found, err := s.repo.APIToken(ctx, tok.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, tok, got)
}

// TestSatisfies covers the scope hierarchy.
func TestSatisfies(t *testing.T) {
	t.Parallel()
	admin := Token{Scopes: []Scope{Admin}}
	control := Token{Scopes: []Scope{Control}}
	read := Token{Scopes: []Scope{Read}}
	overlay := Token{Scopes: []Scope{Overlay}}
	none := Token{Scopes: []Scope{}}

	assert.True(t, admin.Satisfies(Read))
	assert.True(t, admin.Satisfies(Control))
	assert.True(t, admin.Satisfies(Admin))
	assert.True(t, control.Satisfies(Read))
	assert.False(t, control.Satisfies(Admin))
	assert.True(t, read.Satisfies(Read))
	assert.False(t, read.Satisfies(Control))
	assert.True(t, overlay.Satisfies(Read), "overlay counts as read")
	assert.False(t, overlay.Satisfies(Control))
	assert.False(t, none.Satisfies(Read))
}
