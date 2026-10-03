// SPDX-License-Identifier: Apache-2.0

package decimal_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// TestFunctions covers Code-ADR-0020, point 6: the functions of
// template.md, B53, compared with values of mpmath at 120 digits, rounded
// to the limits as the package rounds.
func TestFunctions(t *testing.T) {
	t.Parallel()
	fns := map[string]func(decimal.Decimal) (decimal.Decimal, error){
		"Sin": decimal.Decimal.Sin, "Cos": decimal.Decimal.Cos, "Tan": decimal.Decimal.Tan,
		"Atan": decimal.Decimal.Atan, "Asin": decimal.Decimal.Asin, "Acos": decimal.Decimal.Acos,
		"Sqrt": decimal.Decimal.Sqrt, "Ln": decimal.Decimal.Ln, "Log10": decimal.Decimal.Log10,
		"Exp": decimal.Decimal.Exp,
	}
	for _, tc := range []struct{ fn, arg, want string }{
		{"Sin", "0", "0"},
		{"Sin", "1", "0.841470984807896506652502321630299"},
		{"Sin", "-1", "-0.841470984807896506652502321630299"},
		{"Sin", "0.5", "0.4794255386042030002732879352155714"},
		{"Sin", "3.14", "0.0015926529164869525405414363244433"},
		{"Sin", "100", "-0.5063656411097587936565576104597854"},
		{"Sin", "1e10", "-0.487506025087510691527794294348106"},
		{"Sin", "1234567890123456789012345678901234", "0.974976704238127224842494761527628"},
		{"Sin", "1e-20", "0.00000000000000000001"},
		{"Sin", "3.141592653589793238462643383279503", "-0.0000000000000000000000000000000001"},
		{"Cos", "0", "1"},
		{"Cos", "1", "0.5403023058681397174009366074429766"},
		{"Cos", "2", "-0.4161468365471423869975682295007622"},
		{"Cos", "-3", "-0.9899924966004454572715727947312613"},
		{"Cos", "1e10", "0.8731196226768560011761913453076952"},
		{"Cos", "9999999999999999999999999999999999", "-0.970414995495510001376546271797825"},
		{"Tan", "1", "1.55740772465490223050697480745836"},
		{"Tan", "-0.5", "-0.5463024898437905132551794657802854"},
		{"Tan", "1.5707963", "37320539.58671654132004064246540849"},
		{"Tan", "100", "-0.5872139151569290766778096356445879"},
		{"Atan", "0", "0"},
		{"Atan", "1", "0.7853981633974483096156608458198757"},
		{"Atan", "-1", "-0.7853981633974483096156608458198757"},
		{"Atan", "0.5", "0.4636476090008061162142562314612144"},
		{"Atan", "10", "1.471127674303734591852875571761731"},
		{"Atan", "1e20", "1.570796326794896619221321691639751"},
		{"Atan", "-0.1", "-0.0996686524911620273784461198780206"},
		{"Atan", "0.0001", "0.000099999999666666668666666652381"},
		{"Asin", "0.5", "0.5235987755982988730771072305465838"},
		{"Asin", "-1", "-1.570796326794896619231321691639751"},
		{"Asin", "1", "1.570796326794896619231321691639751"},
		{"Asin", "0.999", "1.5260712396261631879816254589682"},
		{"Asin", "1e-10", "0.0000000001000000000000000000001667"},
		{"Asin", "0", "0"},
		{"Acos", "0.5", "1.047197551196597746154214461093168"},
		{"Acos", "-1", "3.141592653589793238462643383279503"},
		{"Acos", "1", "0"},
		{"Acos", "0", "1.570796326794896619231321691639751"},
		{"Acos", "-0.3", "1.875488980810294127203324652867281"},
		{"Sqrt", "2", "1.414213562373095048801688724209698"},
		{"Sqrt", "0.01", "0.1"},
		{"Sqrt", "1e33", "31622776601683793.31998893544432719"},
		{"Sqrt", "0", "0"},
		{"Ln", "2", "0.6931471805599453094172321214581766"},
		{"Ln", "10", "2.302585092994045684017991454684364"},
		{"Ln", "0.5", "-0.6931471805599453094172321214581766"},
		{"Ln", "1", "0"},
		{"Log10", "1000", "3"},
		{"Log10", "2", "0.301029995663981195213738894724493"},
		{"Log10", "0.001", "-3"},
		{"Exp", "1", "2.718281828459045235360287471352662"},
		{"Exp", "-1", "0.3678794411714423215955237701614609"},
		{"Exp", "10", "22026.46579480671651695790064528424"},
		{"Exp", "0", "1"},
		{"Exp", "-100", "0"},
		{"Exp", "-70", "0.0000000000000000000000000000003975"},
	} {
		got, err := fns[tc.fn](num(t, tc.arg))
		require.NoError(t, err, "%s(%s)", tc.fn, tc.arg)
		assert.Equal(t, tc.want, got.String(), "%s(%s)", tc.fn, tc.arg)
	}
	assert.Equal(t, "3.141592653589793238462643383279503", decimal.Pi().String())
	assert.Equal(t, "2.718281828459045235360287471352662", decimal.E().String())
}

// TestFunctionErrors: calculations without a result and beyond the range
// are errors.
func TestFunctionErrors(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func() (decimal.Decimal, error){
		"square root of a negative number": func() (decimal.Decimal, error) { return num(t, "-1").Sqrt() },
		"logarithm of 0":                   func() (decimal.Decimal, error) { return decimal.Decimal{}.Ln() },
		"logarithm of a negative number":   func() (decimal.Decimal, error) { return num(t, "-2").Log10() },
		"logarithm to the base 1":          func() (decimal.Decimal, error) { return decimal.New(8).Log(decimal.New(1)) },
		"logarithm to a negative base":     func() (decimal.Decimal, error) { return decimal.New(8).Log(decimal.New(-2)) },
		"logarithm of 0 to a base":         func() (decimal.Decimal, error) { return decimal.Decimal{}.Log(decimal.New(2)) },
		"arcsine beyond 1":                 func() (decimal.Decimal, error) { return num(t, "1.0001").Asin() },
		"arccosine below -1":               func() (decimal.Decimal, error) { return num(t, "-1.5").Acos() },
		"root of a negative number":        func() (decimal.Decimal, error) { return num(t, "-8").Pow(num(t, "0.5")) },
	} {
		_, err := f()
		require.ErrorIs(t, err, decimal.ErrUndefined, name)
	}
	_, err := decimal.Decimal{}.Pow(decimal.New(-1))
	require.ErrorIs(t, err, decimal.ErrDivisionByZero)
	for name, f := range map[string]func() (decimal.Decimal, error){
		"large power":    func() (decimal.Decimal, error) { return decimal.New(10).Pow(decimal.New(34)) },
		"huge power":     func() (decimal.Decimal, error) { return decimal.New(2).Pow(num(t, "1e33")) },
		"large exponent": func() (decimal.Decimal, error) { return decimal.New(100).Exp() },
		"huge exponent":  func() (decimal.Decimal, error) { return num(t, "1e33").Exp() },
	} {
		_, err := f()
		require.ErrorIs(t, err, decimal.ErrRange, name)
	}
}

// TestPowAndLog covers powers and logarithms to a base.
func TestPowAndLog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ a, b, want string }{
		{"2", "0.5", "1.414213562373095048801688724209698"},
		{"10", "-2", "0.01"},
		{"1.1", "2", "1.21"},
		{"-2", "3", "-8"},
		{"2", "10", "1024"},
		{"2", "100", "1267650600228229401496703205376"},
		{"7", "0.5", "2.64575131106459059050161575363926"},
		{"1.0001", "10000", "2.718145926825224864037664674913147"},
		{"0", "0", "1"},
		{"5", "0", "1"},
		{"0", "3", "0"},
		{"-8", "-1", "-0.125"},
	} {
		got, err := num(t, tc.a).Pow(num(t, tc.b))
		require.NoError(t, err, "%s ^ %s", tc.a, tc.b)
		assert.Equal(t, tc.want, got.String(), "%s ^ %s", tc.a, tc.b)
	}
	for _, tc := range []struct{ a, base, want string }{
		{"2", "3", "0.6309297535714574370995271143427609"},
		{"8", "2", "3"},
		{"100", "10", "2"},
		{"2", "10", "0.301029995663981195213738894724493"},
	} {
		got, err := num(t, tc.a).Log(num(t, tc.base))
		require.NoError(t, err, "log %s to %s", tc.a, tc.base)
		assert.Equal(t, tc.want, got.String(), "log %s to %s", tc.a, tc.base)
	}
}
