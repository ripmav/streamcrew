// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestCounterNamesUnique covers B1, B7 and B40.
func TestCounterNamesUnique(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	c, err := s.CreateCounter(ctx, counter.Counter{Name: "deaths", Value: 3})
	require.NoError(t, err)
	assert.False(t, c.ID.IsZero())
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "Deaths"})
	require.ErrorIs(t, err, store.ErrConflict)
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "two words"})
	require.ErrorIs(t, err, counter.ErrInvalid)

	got, err := s.Counter(ctx, "DEATHS")
	require.NoError(t, err, "names match regardless of case")
	assert.Equal(t, c, got)
	_, err = s.Counter(ctx, "wins")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestCounterOperations covers B2 and B6: every change is stored.
func TestCounterOperations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	_, err := s.CreateCounter(ctx, counter.Counter{Name: "deaths"})
	require.NoError(t, err)

	value := func() int64 {
		c, err := s.Counter(ctx, "deaths")
		require.NoError(t, err)
		return c.Value
	}
	_, err = s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { return c.Add(5) })
	require.NoError(t, err)
	assert.Equal(t, int64(5), value())
	_, err = s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { return c.Add(-2) })
	require.NoError(t, err)
	assert.Equal(t, int64(3), value())
	_, err = s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { c.Set(40); return nil })
	require.NoError(t, err)
	assert.Equal(t, int64(40), value())
	c, err := s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { c.Reset(); return nil })
	require.NoError(t, err)
	assert.Zero(t, c.Value)
	assert.Zero(t, value())

	_, err = s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error {
		c.Set(99)
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	assert.Zero(t, value(), "an error discards the change")

	_, err = s.UpdateCounter(ctx, "wins", func(*counter.Counter) error { return nil })
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestCounterOverflowStopsAtLimit covers B43: the caller stores the clamped
// value and reports the overflow.
func TestCounterOverflowStopsAtLimit(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	_, err := s.CreateCounter(ctx, counter.Counter{Name: "big", Value: math.MaxInt64 - 1})
	require.NoError(t, err)

	var overflow error
	c, err := s.UpdateCounter(ctx, "big", func(c *counter.Counter) error {
		overflow = c.Add(10)
		return nil
	})
	require.NoError(t, err)
	require.ErrorIs(t, overflow, counter.ErrOverflow)
	assert.Equal(t, int64(math.MaxInt64), c.Value)
}

// TestResetCountersOnStart covers B3.
func TestResetCountersOnStart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	_, err := s.CreateCounter(ctx, counter.Counter{Name: "session", Value: 7, ResetOnStart: true})
	require.NoError(t, err)
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "total", Value: 7})
	require.NoError(t, err)

	n, err := s.ResetCountersOnStart(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	all, err := s.Counters(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "session", all[0].Name)
	assert.Zero(t, all[0].Value)
	assert.Equal(t, int64(7), all[1].Value)
}

func TestRenameAndDeleteCounter(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	_, err := s.CreateCounter(ctx, counter.Counter{Name: "deaths"})
	require.NoError(t, err)
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "wins"})
	require.NoError(t, err)

	_, err = s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { c.Name = "Wins"; return nil })
	require.ErrorIs(t, err, store.ErrConflict)
	renamed, err := s.UpdateCounter(ctx, "deaths", func(c *counter.Counter) error { c.Name = "fails"; return nil })
	require.NoError(t, err)
	assert.Equal(t, "fails", renamed.Name)

	require.NoError(t, s.DeleteCounter(ctx, "fails"))
	require.ErrorIs(t, s.DeleteCounter(ctx, "fails"), store.ErrNotFound)
}
