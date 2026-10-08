// SPDX-License-Identifier: MIT

package expr_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/expr"
)

// TestEval_Functions_B53 covers every function and constant of B53 with an
// example; the transcendental functions go through float64, so the expected
// results are the shortest text of their float64.
func TestEval_Functions_B53(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want string
	}{
		// the constants
		{"pi", "3.141592653589793"},
		{"e", "2.718281828459045"},
		{"pi / 2", "1.5707963267948965"},
		{"2 * pi", "6.283185307179586"},
		// the trigonometric functions
		{"sin(0)", "0"},
		{"sin(1)", "0.8414709848078965"},
		{"sin(pi / 2)", "1"},
		{"cos(0)", "1"},
		{"cos(1)", "0.5403023058681398"},
		{"tan(0)", "0"},
		{"tan(1)", "1.557407724654902"},
		{"csc(pi / 2)", "1"},
		{"csc(1)", "1.1883951057781212"},
		{"sec(0)", "1"},
		{"sec(1)", "1.8508157176809255"},
		{"cot(1)", "0.6420926159343308"},
		{"asin(0)", "0"},
		{"asin(0.5)", "0.5235987755982989"},
		{"asin(1)", "1.5707963267948966"},
		{"acos(0)", "1.5707963267948966"},
		{"acos(0.5)", "1.0471975511965976"},
		{"acos(1)", "0"},
		{"atan(1)", "0.7853981633974483"},
		{"acot(0)", "1.5707963267948966"},
		{"acot(1)", "0.7853981633974483"},
		// the logarithms and the root
		{"loge(e)", "1"},
		{"loge(1)", "0"},
		{"log10(100)", "2"},
		{"log10(1000)", "3"},
		{"logn(2, 8)", "3"},
		{"logn(2, 16)", "4"},
		{"logn(10, 1000)", "2.9999999999999996"},
		{"sqrt(0)", "0"},
		{"sqrt(0.25)", "0.5"},
		{"sqrt(4)", "2"},
		{"sqrt(2)", "1.4142135623730951"},
		// rounding and whole numbers
		{"abs(-2.5)", "2.5"},
		{"ceiling(1.2)", "2"},
		{"ceiling(-1.2)", "-1"},
		{"ceil(1.2)", "2"},
		{"floor(1.8)", "1"},
		{"floor(-1.2)", "-2"},
		{"truncate(1.9)", "1"},
		{"truncate(-1.9)", "-1"},
		{"round(2.5)", "3"},
		// the averages
		{"avg(1, 2, 3)", "2"},
		{"avg(1, 2)", "1.5"},
		{"avg(-1, 1)", "0"},
		{"median(5)", "5"},
		{"median(3, 1, 2)", "2"},
		{"median(4, 1, 3, 2)", "2.5"},
		{"min(3, 1, 2)", "1"},
		{"max(1, 3, 2)", "3"},
		// the conditions
		{"if(true, 1, 2)", "1"},
		{"if(false, 1, 2)", "2"},
		{`if(1 > 2, "a", "b")`, "b"},
		{"if(0, 1, 2)", "2"},
		{"if(2, 1, 2)", "1"},
		{`if(true, "yes", "no")`, "yes"},
		{"if(1 == 1, 10, 20)", "10"},
		{"ifless(1, 2, 3, 4)", "3"},
		{"ifless(2, 1, 3, 4)", "4"},
		{"ifless(1, 1, 3, 4)", "4"},
		{`ifless(1, 2, "less", "no")`, "less"},
		{"ifmore(2, 1, 3, 4)", "3"},
		{"ifmore(1, 2, 3, 4)", "4"},
		{"ifmore(1, 1, 3, 4)", "4"},
		{"ifequal(1, 1, 3, 4)", "3"},
		{"ifequal(1, 1.0, 3, 4)", "3"},
		{"ifequal(1, 2, 3, 4)", "4"},
		// functions in larger expressions
		{"2 * sin(0) + cos(0)", "1"},
		{"if(sqrt(4) == 2, 1, 0) + round(2.5)", "4"},
		{"median(1, 2, 3, 4) * avg(1, 3)", "5"},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			r, err := eval(t, tc.text)
			require.NoError(t, err)
			assert.Equal(t, tc.want, r.String())
		})
	}
}

// TestEval_Functions_Errors_B53 covers values outside the domain, kinds of
// values the functions do not take and the wrong number of values.
func TestEval_Functions_Errors_B53(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text  string
		cause error
	}{
		// a value outside the domain of the function
		{"asin(2)", expr.ErrEvaluation},
		{"acos(-2)", expr.ErrEvaluation},
		{"loge(0)", expr.ErrEvaluation},
		{"loge(-1)", expr.ErrEvaluation},
		{"log10(0)", expr.ErrEvaluation},
		{"logn(1, 2)", expr.ErrEvaluation},
		{"logn(0, 2)", expr.ErrEvaluation},
		{"logn(2, 0)", expr.ErrEvaluation},
		{"logn(2, -1)", expr.ErrEvaluation},
		{"sqrt(-1)", expr.ErrEvaluation},
		{"csc(0)", expr.ErrEvaluation},
		{"cot(0)", expr.ErrEvaluation},
		// a kind of value the function does not take
		{`sin("a")`, expr.ErrEvaluation},
		{"sqrt(true)", expr.ErrEvaluation},
		{`if("a", 1, 2)`, expr.ErrEvaluation},
		{`if(true, 1, "a")`, expr.ErrEvaluation},
		{`ifless("a", 1, 2, 3)`, expr.ErrEvaluation},
		{`ifmore(1, "a", 2, 3)`, expr.ErrEvaluation},
		{`ifequal(true, 1, 2, 3)`, expr.ErrEvaluation},
		// the random functions with values they cannot draw from
		{"random(0)", expr.ErrEvaluation},
		{"random(-2)", expr.ErrEvaluation},
		{"random(1.5)", expr.ErrEvaluation},
		{"randomrange(5, 2)", expr.ErrEvaluation},
		{"randomrange(1.5, 3)", expr.ErrEvaluation},
		{"randomrange(3, 2.5)", expr.ErrEvaluation},
	}
	for _, tc := range tests {
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			_, err := eval(t, tc.text)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.cause)
		})
	}
}

// TestCompile_Functions_Arguments_B53 covers the wrong number of values as
// an error at the compile.
func TestCompile_Functions_Arguments_B53(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"sin()",
		"sin(1, 2)",
		"if(1, 2)",
		"if(1, 2, 3, 4)",
		"ifless(1, 2, 3)",
		"ifmore(1, 2, 3, 4, 5)",
		"ifequal(1)",
		"logn(8)",
		"logn(2, 8, 3)",
		"randomrange(1)",
		"min()",
		"avg()",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			_, err := expr.Compile(text)
			require.Error(t, err)
			assert.ErrorIs(t, err, expr.ErrInvalid)
		})
	}
}

// TestEval_Random_B53 covers random(n) and randomrange(a, b) with injected
// sources: both include their upper bound, random(6) gives 1 to 6.
func TestEval_Random_B53(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  func(n int) int
		text string
		want string
	}{
		{"highest", func(n int) int { return n }, "random(6)", "6"},
		{"lowest", func(int) int { return 1 }, "random(6)", "1"},
		{"fixed", func(int) int { return 4 }, "random(10)", "4"},
		{"highest range", func(n int) int { return n }, "randomrange(1, 10)", "10"},
		{"lowest range", func(int) int { return 1 }, "randomrange(1, 10)", "1"},
		{"fixed range", func(int) int { return 4 }, "randomrange(5, 9)", "8"},
		{"same bound", func(int) int { return 1 }, "randomrange(3, 3)", "3"},
		{"in an expression", func(n int) int { return n }, "random(6) + randomrange(1, 10)", "16"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			x, err := expr.Compile(tc.text, expr.WithRandom(tc.src))
			require.NoError(t, err)
			r, err := x.EvalWithTexts(nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, r.String())
		})
	}
}

// TestEval_Random_Default_B53 covers that the default source draws within
// the bounds, including the upper bound.
func TestEval_Random_Default_B53(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		text   string
		lo, hi int
	}{
		{"random(6)", 1, 6},
		{"randomrange(3, 7)", 3, 7},
	} {
		t.Run(tc.text, func(t *testing.T) {
			t.Parallel()
			x, err := expr.Compile(tc.text)
			require.NoError(t, err)
			for range 100 {
				r, err := x.EvalWithTexts(nil)
				require.NoError(t, err)
				k, err := strconv.Atoi(r.String())
				require.NoError(t, err)
				assert.GreaterOrEqual(t, k, tc.lo)
				assert.LessOrEqual(t, k, tc.hi)
			}
		})
	}
}
