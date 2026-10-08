// SPDX-License-Identifier: MIT

package counter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/counter"
)

// num reads a decimal in a test.
func num(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.Parse(s)
	require.NoError(t, err)
	return d
}

// TestOperations covers B2 and B5: values are exact decimals.
func TestOperations(t *testing.T) {
	t.Parallel()
	c := counter.New("deaths")
	require.NoError(t, c.Add(decimal.New(3)))
	require.NoError(t, c.Add(decimal.New(-5)))
	assert.Equal(t, "-2", c.Value.String())
	c.Set(num(t, "0.1"))
	require.NoError(t, c.Add(num(t, "0.2")))
	assert.Equal(t, "0.3", c.Value.String(), "B5: exact")
	c.Set(decimal.New(40))
	assert.Equal(t, "40", c.Value.String())
	c.Reset()
	assert.True(t, c.Value.IsZero())
}

// TestNew covers B8: a new counter starts at 0 with the step 1.
func TestNew(t *testing.T) {
	t.Parallel()
	c := counter.New("deaths")
	assert.Equal(t, "deaths", c.Name)
	assert.True(t, c.Value.IsZero())
	assert.Equal(t, "1", c.Step.String())
	assert.True(t, counter.DefaultStep().Equal(decimal.New(1)))
	require.NoError(t, c.Validate())
}

// TestSteps covers B2 and B8: Increment and Decrement change the value by
// the step, also one with decimal places, and keep it on an overflow (B43).
func TestSteps(t *testing.T) {
	t.Parallel()
	c := counter.New("points")
	require.NoError(t, c.Increment())
	assert.Equal(t, "1", c.Value.String())
	c.Step = num(t, "2.5")
	require.NoError(t, c.Increment())
	require.NoError(t, c.Increment())
	require.NoError(t, c.Decrement())
	assert.Equal(t, "3.5", c.Value.String())

	c.Set(decimal.Max())
	require.ErrorIs(t, c.Increment(), counter.ErrOverflow)
	assert.Equal(t, decimal.Max().String(), c.Value.String())
	c.Set(decimal.Max().Neg())
	require.ErrorIs(t, c.Decrement(), counter.ErrOverflow)
	assert.Equal(t, decimal.Max().Neg().String(), c.Value.String())

	c.Step = decimal.Max()
	c.Reset()
	require.NoError(t, c.Decrement(), "the largest step")
	assert.Equal(t, decimal.Max().Neg().String(), c.Value.String())
}

// TestOverflow covers B43: Add reports an overflow, and the value stays as
// it was.
func TestOverflow(t *testing.T) {
	t.Parallel()
	c := counter.Counter{Name: "big", Value: decimal.Max(), Step: counter.DefaultStep()}
	require.ErrorIs(t, c.Add(decimal.New(1)), counter.ErrOverflow)
	assert.Equal(t, decimal.Max().String(), c.Value.String())
	require.NoError(t, c.Add(num(t, "-0.5")), "rounded to 34 digits, half to even")
	assert.Equal(t, "9999999999999999999999999999999998", c.Value.String())
	require.NoError(t, c.Add(decimal.New(1)))
	assert.Equal(t, decimal.Max().String(), c.Value.String())
}

// TestValidate covers B7 and B8.
func TestValidate(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"deaths", "Deaths2", "x", strings.Repeat("a", 64)} {
		require.NoError(t, counter.ValidateName(name), name)
		assert.NoError(t, counter.New(name).Validate(), name)
	}
	for _, name := range []string{"", "two words", "tode_zähler", "a-b", "$x", strings.Repeat("a", 65)} {
		require.ErrorIs(t, counter.ValidateName(name), counter.ErrInvalid, name)
		assert.ErrorIs(t, counter.New(name).Validate(), counter.ErrInvalid, name)
	}
	for _, step := range []string{"1", "10", "0.25", "1e-34", "9999999999999999999999999999999999"} {
		c := counter.New("deaths")
		c.Step = num(t, step)
		assert.NoError(t, c.Validate(), step)
	}
	for _, step := range []string{"0", "-1", "-0.5"} {
		c := counter.New("deaths")
		c.Step = num(t, step)
		assert.ErrorIs(t, c.Validate(), counter.ErrInvalid, step)
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
