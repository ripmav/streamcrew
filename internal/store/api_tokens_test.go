// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// apiTok makes a token with a name-derived ID and hash, for the tests.
func apiTok(name string, scopes ...apitoken.Scope) apitoken.Token {
	return apitoken.Token{
		ID:        "11111111-1111-7000-8000-11111111111" + name[0:1],
		Name:      name,
		Scopes:    scopes,
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		SHA256:    "hash-" + name,
	}
}

// TestAPITokenRoundtrip covers the profile-database storage of the API
// tokens (ADR-0013 §5, code review 2026-10-24, S5): the hash round-trips,
// the name is unique, and the delete removes exactly one token.
func TestAPITokenRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	tok := apiTok("tui", apitoken.Admin, apitoken.Read)
	require.NoError(t, s.CreateAPIToken(ctx, tok))

	got, found, err := s.APIToken(ctx, tok.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, tok, got, "the token round-trips with its scopes")

	got2, found, err := s.APITokenByHash(ctx, tok.SHA256)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, tok.ID, got2.ID)

	_, found, err = s.APITokenByName(ctx, "tui")
	require.NoError(t, err)
	require.True(t, found)

	other := apiTok("cli")
	require.NoError(t, s.CreateAPIToken(ctx, other))
	tokens, err := s.ListAPITokens(ctx)
	require.NoError(t, err)
	require.Len(t, tokens, 2)

	// A taken name is refused (S12).
	dup := apiTok("TUI")
	dup.ID = "22222222-2222-7000-8000-222222222222"
	dup.SHA256 = "hash-tui-2"
	// The name is compared exactly: "TUI" is a different name.
	require.NoError(t, s.CreateAPIToken(ctx, dup))

	exact := apiTok("tui")
	exact.ID = "33333333-3333-7000-8000-333333333333"
	exact.SHA256 = "hash-tui-3"
	err = s.CreateAPIToken(ctx, exact)
	require.ErrorIs(t, err, apitoken.ErrNameTaken)

	ok, err := s.DeleteAPIToken(ctx, other.ID)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = s.DeleteAPIToken(ctx, other.ID)
	require.NoError(t, err)
	require.False(t, ok, "a deleted token is gone")
}

// TestAPITokensConcurrentCreates covers the transactional name check: two
// concurrent creates of the same name cannot both pass (S12).
func TestAPITokensConcurrentCreates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	const n = 8
	errs := make(chan error, n)
	for range n {
		go func() {
			errs <- s.CreateAPIToken(ctx, apiTok("racy", apitoken.Read))
		}()
	}
	var taken int
	for range n {
		if err := <-errs; err != nil {
			require.ErrorIs(t, err, apitoken.ErrNameTaken)
		} else {
			taken++
		}
	}
	require.Equal(t, 1, taken, "exactly one create of the name passes")

	count, err := s.CountAPITokensByName(ctx, "racy")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
