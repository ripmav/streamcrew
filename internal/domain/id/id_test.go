// SPDX-License-Identifier: MIT

package id_test

import (
	"database/sql/driver"
	json "encoding/json/v2"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// TestNewIsIncreasingAndCarriesTime must not run in parallel with other
// tests that create IDs: the UUIDv7 generator is process-wide, and an ID with
// real time created between two IDs in the bubble looks to it like the clock
// jumping back, which breaks the order (Code-ADR-0009).
func TestNewIsIncreasingAndCarriesTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := id.New()
		b := id.New()
		time.Sleep(time.Second)
		c := id.New()

		assert.Negative(t, a.Compare(b))
		assert.Negative(t, b.Compare(c))
		assert.Equal(t, time.Now().Add(-time.Second).UTC(), a.Time())
		assert.Equal(t, time.Now().UTC(), c.Time())
		assert.False(t, a.IsZero())
	})
}

func TestTextRoundTrip(t *testing.T) {
	t.Parallel()
	v := id.New()
	s := v.String()
	assert.Len(t, s, 36)

	parsed, err := id.Parse(s)
	require.NoError(t, err)
	assert.Equal(t, v, parsed)

	out, err := json.Marshal(struct{ ID id.ID }{v})
	require.NoError(t, err)
	assert.JSONEq(t, `{"ID":"`+s+`"}`, string(out))

	var back struct{ ID id.ID }
	require.NoError(t, json.Unmarshal(out, &back))
	assert.Equal(t, v, back.ID)
}

func TestParseErrors(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "not-an-id", "0192f0c4-8f7e-7c3a-9b1d"} {
		_, err := id.Parse(s)
		assert.Error(t, err, s)
	}
	assert.Panics(t, func() { id.MustParse("nope") })
	var v id.ID
	assert.Error(t, json.Unmarshal([]byte(`"nope"`), &v))
}

func TestDatabaseValue(t *testing.T) {
	t.Parallel()
	v := id.New()

	val, err := v.Value()
	require.NoError(t, err)
	assert.Equal(t, driver.Value(v.String()), val)

	var fromString, fromBytes id.ID
	require.NoError(t, fromString.Scan(v.String()))
	require.NoError(t, fromBytes.Scan([]byte(v.String())))
	assert.Equal(t, v, fromString)
	assert.Equal(t, v, fromBytes)

	var bad id.ID
	assert.Error(t, bad.Scan(int64(1)))
}

func TestZeroAndOtherVersions(t *testing.T) {
	t.Parallel()
	var zero id.ID
	assert.True(t, zero.IsZero())
	assert.True(t, zero.Time().IsZero())
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", zero.String())

	v4 := id.MustParse("f47ac10b-58cc-4372-a567-0e02b2c3d479")
	assert.True(t, v4.Time().IsZero(), "only UUIDv7 carry a time")
}

func FuzzParse(f *testing.F) {
	f.Add("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d")
	f.Add("")
	f.Add("{0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d}")
	f.Fuzz(func(t *testing.T, s string) {
		v, err := id.Parse(s)
		if err != nil {
			return
		}
		again, err := id.Parse(v.String())
		require.NoError(t, err)
		assert.Equal(t, v, again)
	})
}
