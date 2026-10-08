// SPDX-License-Identifier: MIT

package decimal_test

import (
	json "encoding/json/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// num reads a decimal number in a test.
func num(t testing.TB, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.Parse(s)
	require.NoError(t, err, s)
	return d
}

// TestParse covers Code-ADR-0020, points 4, 5 and 7: the syntax of
// numbers, the canonical form, rounding to the limits and the range.
func TestParse(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"0":      "0",
		"-0":     "0",
		"+1":     "1",
		"2.50":   "2.5",
		".5":     "0.5",
		"-.5":    "-0.5",
		"5.":     "5",
		"1e3":    "1000",
		"1E-3":   "0.001",
		"1.5e+2": "150",
		"0.1234567890123456789012345678901234567":  "0.1234567890123456789012345678901235",
		"1234567890123456789012345678901234":       "1234567890123456789012345678901234",
		"-1234567890123456789.0123456789012345678": "-1234567890123456789.012345678901235",
		"0.00000000000000000000000000000000005":    "0",
		"0.00000000000000000000000000000000015":    "0.0000000000000000000000000000000002",
		"0.00000000000000000000000000000000025":    "0.0000000000000000000000000000000002",
		"1e-99999999999":                           "0",
		"-0.000":                                   "0",
	} {
		d, err := decimal.Parse(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, d.String(), in)
	}
	for _, in := range []string{"", "-", "+-1", "--1", "1e", "1e+", "e5", ".", ".e1", "1.2.3", "0x10", "1_000", " 1", "1 ", "inf", "NaN", "1,5", "1e1.5", "١"} {
		_, err := decimal.Parse(in)
		require.ErrorIs(t, err, decimal.ErrSyntax, "%q", in)
	}
	for _, in := range []string{
		"10000000000000000000000000000000000",
		"-1e34",
		"9999999999999999999999999999999999.5",
		"1e99999999999",
		"1e5000",
		"-123e4000",
		"1234567890123456789012345678901234567890e5000",
	} {
		_, err := decimal.Parse(in)
		require.ErrorIs(t, err, decimal.ErrRange, in)
	}
	assert.Equal(t, "9999999999999999999999999999999999", num(t, "9999999999999999999999999999999999.4").String())
	assert.Equal(t, "9999999999999999999999999999999999", decimal.Max().String())
	_, err := decimal.Max().Add(num(t, "1e-34"))
	require.NoError(t, err, "rounds back to the largest")
	_, err = decimal.Max().Add(decimal.New(1))
	require.ErrorIs(t, err, decimal.ErrRange)
}

// TestZeroValue: the zero value is 0.
func TestZeroValue(t *testing.T) {
	t.Parallel()
	var d decimal.Decimal
	assert.Equal(t, "0", d.String())
	assert.True(t, d.IsZero())
	assert.True(t, d.IsWhole())
	assert.Zero(t, d.Sign())
	assert.True(t, d.Equal(decimal.New(0)))
}

// TestArithmetic covers Code-ADR-0020, point 5: exact where the result
// fits, rounded half to even otherwise, errors beyond the range and for a
// division by zero.
func TestArithmetic(t *testing.T) {
	t.Parallel()
	type op func(a, b decimal.Decimal) (decimal.Decimal, error)
	add, sub, mul, quo, rem := decimal.Decimal.Add, decimal.Decimal.Sub, decimal.Decimal.Mul, decimal.Decimal.Quo, decimal.Decimal.Rem
	for _, tc := range []struct {
		name string
		op   op
		a, b string
		want string
	}{
		{"exact sum", add, "0.1", "0.2", "0.3"},
		{"exact difference", sub, "1", "0.9", "0.1"},
		{"negative zero", sub, "0.5", "0.5", "0"},
		{"exact product", mul, "1.1", "1.1", "1.21"},
		{"exact quotient", quo, "1", "8", "0.125"},
		{"rounded quotient", quo, "10", "3", "3.333333333333333333333333333333333"},
		{"half to even up", quo, "2", "3", "0.6666666666666666666666666666666667"},
		{"places limit", quo, "1", "30000", "0.0000333333333333333333333333333333"},
		{"negative quotient", quo, "-1", "7", "-0.1428571428571428571428571428571429"},
		{"sum beyond the digits", add, "1e33", "1e-34", "1000000000000000000000000000000000"},
		{"sum beyond the places", add, "0.1", "1e-35", "0.1"},
		{"remainder", rem, "7", "3", "1"},
		{"remainder with the sign of the dividend", rem, "-7.5", "2", "-1.5"},
		{"remainder of a large number", rem, "1e33", "7", "6"},
		{"remainder of a huge quotient", rem, "1e33", "3e-34", "0.0000000000000000000000000000000001"},
		{"negative remainder of a huge quotient", rem, "-1e33", "7e-34", "-0.0000000000000000000000000000000003"},
		{"product to zero", mul, "1e-20", "1e-20", "0"},
	} {
		got, err := tc.op(num(t, tc.a), num(t, tc.b))
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got.String(), tc.name)
	}

	maxValue := num(t, "9999999999999999999999999999999999")
	_, err := maxValue.Add(decimal.New(1))
	require.ErrorIs(t, err, decimal.ErrRange)
	_, err = maxValue.Neg().Sub(decimal.New(1))
	require.ErrorIs(t, err, decimal.ErrRange)
	_, err = num(t, "1e17").Mul(num(t, "1e17"))
	require.ErrorIs(t, err, decimal.ErrRange)
	_, err = maxValue.Quo(num(t, "0.1"))
	require.ErrorIs(t, err, decimal.ErrRange)
	_, err = decimal.New(1).Quo(decimal.Decimal{})
	require.ErrorIs(t, err, decimal.ErrDivisionByZero)
	_, err = decimal.New(1).Rem(decimal.Decimal{})
	require.ErrorIs(t, err, decimal.ErrDivisionByZero)
}

// TestCopies: a calculation leaves its operands and copies of them as they
// were.
func TestCopies(t *testing.T) {
	t.Parallel()
	a := num(t, "123456789012345678901234567890.1234")
	b := a
	sum, err := a.Add(decimal.New(1))
	require.NoError(t, err)
	assert.Equal(t, "123456789012345678901234567891.1234", sum.String())
	assert.Equal(t, "123456789012345678901234567890.1234", a.String())
	assert.Equal(t, a.String(), b.String())
	assert.Equal(t, "-123456789012345678901234567890.1234", a.Neg().String())
	assert.Equal(t, "123456789012345678901234567890.1234", a.Neg().Abs().String())
	assert.Equal(t, "123456789012345678901234567890.1234", a.String())
}

// TestCompare covers Cmp, Equal, Sign, IsZero and IsWhole.
func TestCompare(t *testing.T) {
	t.Parallel()
	assert.Equal(t, -1, num(t, "1.5").Cmp(num(t, "2")))
	assert.Equal(t, 0, num(t, "2.50").Cmp(num(t, "2.5")))
	assert.Equal(t, 1, num(t, "-1").Cmp(num(t, "-2")))
	assert.True(t, num(t, "1e2").Equal(decimal.New(100)))
	assert.Equal(t, -1, num(t, "-0.1").Sign())
	assert.Equal(t, 1, num(t, "0.1").Sign())
	assert.True(t, num(t, "100").IsWhole())
	assert.True(t, num(t, "-3").IsWhole())
	assert.False(t, num(t, "2.5").IsWhole())
	assert.False(t, num(t, "0.001").IsZero())
	assert.Equal(t, "-7", decimal.New(-7).String())
	assert.Equal(t, "1000", decimal.New(1000).String())
}

// TestInt64: whole numbers convert, others fail.
func TestInt64(t *testing.T) {
	t.Parallel()
	n, err := num(t, "-42").Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(-42), n)
	n, err = num(t, "9223372036854775807").Int64()
	require.NoError(t, err)
	assert.Equal(t, int64(9223372036854775807), n)
	_, err = num(t, "2.5").Int64()
	require.ErrorIs(t, err, decimal.ErrNotWhole)
	_, err = num(t, "9223372036854775808").Int64()
	require.ErrorIs(t, err, decimal.ErrRange)
	_, err = num(t, "1e30").Int64()
	require.ErrorIs(t, err, decimal.ErrRange)
}

// TestRound covers the roundings, also to tens and beyond the range.
func TestRound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value  string
		places int
		mode   decimal.Rounding
		want   string
	}{
		{"2.5", 0, decimal.RoundHalfEven, "2"},
		{"3.5", 0, decimal.RoundHalfEven, "4"},
		{"2.5", 0, decimal.RoundHalfAway, "3"},
		{"-2.5", 0, decimal.RoundHalfAway, "-3"},
		{"-2.5", 0, decimal.RoundHalfEven, "-2"},
		{"2.45", 1, decimal.RoundHalfEven, "2.4"},
		{"2.45", 1, decimal.RoundHalfAway, "2.5"},
		{"2.99", 1, decimal.RoundDown, "2.9"},
		{"-2.91", 1, decimal.RoundDown, "-2.9"},
		{"-2.91", 1, decimal.RoundFloor, "-3"},
		{"2.91", 1, decimal.RoundCeiling, "3"},
		{"1250", -2, decimal.RoundHalfEven, "1200"},
		{"1250", -2, decimal.RoundHalfAway, "1300"},
		{"-0.4", 0, decimal.RoundHalfAway, "0"},
		{"7", 3, decimal.RoundHalfEven, "7"},
	} {
		got, err := num(t, tc.value).Round(tc.places, tc.mode)
		require.NoError(t, err, tc)
		assert.Equal(t, tc.want, got.String(), "%s to %d places, %s", tc.value, tc.places, tc.mode)
	}
	_, err := num(t, "9999999999999999999999999999999999").Round(-1, decimal.RoundHalfAway)
	require.ErrorIs(t, err, decimal.ErrRange, "the carry leaves the range")
	_, err = decimal.New(1).Round(0, "nearest")
	require.ErrorContains(t, err, "unknown rounding")
	_, err = decimal.New(1).Round(decimal.Places+1, decimal.RoundHalfEven)
	require.Error(t, err)
	_, err = decimal.New(1).Round(-decimal.Magnitude-1, decimal.RoundHalfEven)
	require.Error(t, err)
}

// TestFixed covers Code-ADR-0020, point 10: exactly the places, rounded
// half away from zero, a rounded zero without a sign.
func TestFixed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		value  string
		places int
		want   string
	}{
		{"1234.5", 2, "1234.50"},
		{"2", 2, "2.00"},
		{"0.005", 2, "0.01"},
		{"-0.005", 2, "-0.01"},
		{"-0.004", 2, "0.00"},
		{"2.5", 0, "3"},
		{"9999999999999999999999999999999999", 2, "9999999999999999999999999999999999.00"},
	} {
		got, err := num(t, tc.value).Fixed(tc.places)
		require.NoError(t, err, tc.value)
		assert.Equal(t, tc.want, got, tc.value)
	}
	_, err := decimal.New(1).Fixed(-1)
	require.Error(t, err)
}

// TestText covers Code-ADR-0020, point 9: JSON holds a Decimal as text in
// the canonical form.
func TestText(t *testing.T) {
	t.Parallel()
	type doc struct {
		Value decimal.Decimal `json:"value"`
	}
	data, err := json.Marshal(doc{Value: num(t, "2.50")})
	require.NoError(t, err)
	assert.JSONEq(t, `{"value":"2.5"}`, string(data))
	var back doc
	require.NoError(t, json.Unmarshal([]byte(`{"value":"-1e2"}`), &back))
	assert.Equal(t, "-100", back.Value.String())
	require.ErrorIs(t, json.Unmarshal([]byte(`{"value":"zwei"}`), &back), decimal.ErrSyntax)
	require.Error(t, json.Unmarshal([]byte(`{"value":2.5}`), &back), "a JSON number is no Decimal")
}

// FuzzParse: the canonical form of a number reads back as the same number
// and the same text, and Parse does not panic.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{"0", "-2.50", "1e3", ".5", "0.1234567890123456789012345678901234567", "9999999999999999999999999999999999.5", "1e-40", "abc"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := decimal.Parse(s)
		if err != nil {
			return
		}
		text := d.String()
		require.False(t, strings.ContainsAny(text, "eE"), text)
		back, err := decimal.Parse(text)
		require.NoError(t, err, text)
		require.True(t, d.Equal(back), "%s and %s", d, back)
		require.Equal(t, text, back.String())
	})
}
