// SPDX-License-Identifier: MIT

package connector_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
)

// TestDedup covers B22 of events.md: a repeated ID within the time to live
// is no longer first, and it is forgotten after it.
func TestDedup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		d, err := connector.NewDedup(time.Minute)
		require.NoError(t, err)

		assert.True(t, d.First("a"))
		assert.False(t, d.First("a"), "repeat")
		assert.True(t, d.First("b"))

		time.Sleep(30 * time.Second)
		assert.False(t, d.First("a"), "within the time to live")
		assert.True(t, d.First("c"))
		assert.Equal(t, 3, d.Len())

		time.Sleep(30 * time.Second)
		assert.Equal(t, 1, d.Len(), "a and b are forgotten, c is not")
		assert.True(t, d.First("a"), "after the time to live")
		assert.False(t, d.First("c"))
	})
}

func TestDedupEmptyID(t *testing.T) {
	t.Parallel()
	d, err := connector.NewDedup(connector.DefaultDedupTTL)
	require.NoError(t, err)
	assert.True(t, d.First(""))
	assert.True(t, d.First(""), "an empty ID is never remembered")
	assert.Zero(t, d.Len())
}

func TestNewDedupInvalid(t *testing.T) {
	t.Parallel()
	for _, ttl := range []time.Duration{0, -time.Second} {
		_, err := connector.NewDedup(ttl)
		assert.ErrorIs(t, err, connector.ErrInvalidTTL)
	}
}
