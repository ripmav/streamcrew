// SPDX-License-Identifier: Apache-2.0

package counter_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/counter"
)

// TestOperations covers B2.
func TestOperations(t *testing.T) {
	t.Parallel()
	c := counter.Counter{Name: "deaths"}
	require.NoError(t, c.Add(3))
	require.NoError(t, c.Add(-5))
	assert.Equal(t, int64(-2), c.Value)
	c.Set(40)
	assert.Equal(t, int64(40), c.Value)
	c.Reset()
	assert.Zero(t, c.Value)
}

// TestOverflow covers B43: Add reports an overflow, and the value stays as
// it was.
func TestOverflow(t *testing.T) {
	t.Parallel()
	c := counter.Counter{Name: "big", Value: math.MaxInt64 - 1}
	require.ErrorIs(t, c.Add(10), counter.ErrOverflow)
	assert.Equal(t, int64(math.MaxInt64-1), c.Value)
	require.NoError(t, c.Add(1), "up to the limit")
	assert.Equal(t, int64(math.MaxInt64), c.Value)
	require.ErrorIs(t, c.Add(1), counter.ErrOverflow)
	assert.Equal(t, int64(math.MaxInt64), c.Value)

	c.Set(math.MinInt64 + 1)
	require.ErrorIs(t, c.Add(-2), counter.ErrOverflow)
	assert.Equal(t, int64(math.MinInt64+1), c.Value)
	require.NoError(t, c.Add(-1))
	assert.Equal(t, int64(math.MinInt64), c.Value)
	require.ErrorIs(t, c.Add(math.MinInt64), counter.ErrOverflow)
	require.NoError(t, c.Add(math.MaxInt64))
	assert.Equal(t, int64(-1), c.Value)
}

// TestValidate covers B7.
func TestValidate(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"deaths", "Deaths2", "x", strings.Repeat("a", 64)} {
		assert.NoError(t, counter.Counter{Name: name}.Validate(), name)
	}
	for _, name := range []string{"", "two words", "tode_zähler", "a-b", "$x", strings.Repeat("a", 65)} {
		assert.ErrorIs(t, counter.Counter{Name: name}.Validate(), counter.ErrInvalid, name)
	}
}

// reserver is a fake of the template registry: names that start with one of
// its built-in names are reserved.
type reserver []string

func (r reserver) Reserved(name string) (string, bool) {
	for _, builtIn := range r {
		if strings.HasPrefix(strings.ToLower(name), builtIn) {
			return builtIn, true
		}
	}
	return "", false
}

// TestCheckReserved covers B7: neither $<name> nor $<name>display may
// collide with a built-in identifier.
func TestCheckReserved(t *testing.T) {
	t.Parallel()
	r := reserver{"username", "deathsdisplayed"}
	require.NoError(t, counter.Counter{Name: "deaths"}.CheckReserved(r))
	require.ErrorIs(t, counter.Counter{Name: "UserNames"}.CheckReserved(r), counter.ErrReserved)
	err := counter.Counter{Name: "deaths"}.CheckReserved(reserver{"deathsdisplay"})
	require.ErrorIs(t, err, counter.ErrReserved)
	assert.Contains(t, err.Error(), "$deathsdisplay")
}
