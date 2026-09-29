// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/quote"
	"github.com/ripmav/streamcrew/internal/store"
)

func addQuotes(t *testing.T, s *store.Store, texts ...string) []quote.Quote {
	t.Helper()
	out := make([]quote.Quote, 0, len(texts))
	for _, text := range texts {
		q, err := s.AddQuote(t.Context(), quote.Quote{Text: text, Game: "Celeste"})
		require.NoError(t, err)
		out = append(out, q)
	}
	return out
}

// TestQuoteNumbers covers B21 and B41: numbers are given out in order and
// never again after a delete.
func TestQuoteNumbers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	qs := addQuotes(t, s, "one", "two", "three")
	for i, q := range qs {
		assert.Equal(t, int64(i+1), q.Number)
		assert.False(t, q.ID.IsZero())
		assert.False(t, q.QuotedAt.IsZero())
	}
	require.NoError(t, s.DeleteQuote(ctx, 3))
	require.NoError(t, s.DeleteQuote(ctx, 2))
	four := addQuotes(t, s, "four")[0]
	assert.Equal(t, int64(4), four.Number, "3 is not given out again")

	all, err := s.Quotes(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, []int64{1, 4}, []int64{all[0].Number, all[1].Number}, "the others keep their numbers")
	assert.Equal(t, qs[0], all[0])
	require.ErrorIs(t, s.DeleteQuote(ctx, 2), store.ErrNotFound)
}

// TestQuoteImportNumbers covers B42: a given number must be free, and the
// next number follows the highest one.
func TestQuoteImportNumbers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	said := time.Date(2025, 12, 24, 20, 15, 0, 0, time.UTC)
	imported, err := s.AddQuote(ctx, quote.Quote{Number: 10, Text: "imported", Game: "Just Chatting", QuotedAt: said})
	require.NoError(t, err)
	assert.Equal(t, int64(10), imported.Number)
	assert.Equal(t, said, imported.QuotedAt)

	_, err = s.AddQuote(ctx, quote.Quote{Number: 10, Text: "duplicate"})
	require.ErrorIs(t, err, store.ErrConflict)
	got, err := s.Quote(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, "imported", got.Text, "the existing quote stays")

	assert.Equal(t, int64(11), addQuotes(t, s, "next")[0].Number)
}

// TestQuoteQueries covers B23 and B26.
func TestQuoteQueries(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	_, err := s.RandomQuote(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.LatestQuote(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)
	n, err := s.QuoteCount(ctx)
	require.NoError(t, err)
	assert.Zero(t, n)

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	_, err = s.AddQuote(ctx, quote.Quote{Text: "by ada", AddedBy: ada.ID})
	require.NoError(t, err)
	addQuotes(t, s, "two", "three")

	n, err = s.QuoteCount(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	latest, err := s.LatestQuote(ctx)
	require.NoError(t, err)
	assert.Equal(t, "three", latest.Text)
	random, err := s.RandomQuote(ctx)
	require.NoError(t, err)
	assert.Contains(t, []string{"by ada", "two", "three"}, random.Text)
	first, err := s.Quote(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, ada.ID, first.AddedBy)

	require.NoError(t, s.DeleteUser(ctx, ada.ID))
	first, err = s.Quote(ctx, 1)
	require.NoError(t, err, "the quote stays when its author is deleted")
	assert.True(t, first.AddedBy.IsZero())

	_, err = s.AddQuote(ctx, quote.Quote{Text: "ghost", AddedBy: id.New()})
	require.ErrorIs(t, err, store.ErrConflict, "the author must exist")
	_, err = s.AddQuote(ctx, quote.Quote{Text: " "})
	require.ErrorIs(t, err, quote.ErrInvalid)
}

func TestUpdateQuote(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	q := addQuotes(t, s, "typo")[0]

	q.Text, q.Game = "fixed", "Hades"
	updated, err := s.UpdateQuote(ctx, q)
	require.NoError(t, err)
	assert.Equal(t, "fixed", updated.Text)
	assert.Equal(t, "Hades", updated.Game)
	assert.Equal(t, q.ID, updated.ID)
	assert.Equal(t, q.CreatedAt, updated.CreatedAt)

	q.Number = 99
	_, err = s.UpdateQuote(ctx, q)
	require.ErrorIs(t, err, store.ErrNotFound)
}
