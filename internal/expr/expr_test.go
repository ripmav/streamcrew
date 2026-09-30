// SPDX-License-Identifier: Apache-2.0

package expr_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/template"
)

// engine returns an engine with the argument and character families.
func engine(t testing.TB) *template.Engine {
	t.Helper()
	r, err := template.NewRegistry(template.ArgumentFamily(), template.CharacterFamily())
	require.NoError(t, err)
	return template.New(r)
}

// eval compiles and evaluates text with the arguments args.
func eval(t *testing.T, text string, args ...string) (expr.Result, error) {
	t.Helper()
	x, err := expr.Compile(text)
	require.NoError(t, err)
	return x.Eval(t.Context(), engine(t), &template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: args, ArgsText: strings.Join(args, " ")})
}

// TestEval_B50 covers the language of B50.
func TestEval_B50(t *testing.T) {
	t.Parallel()
	tests := []struct {
		text string
		want string
	}{
		{"1 + 2 * 3", "7"},
		{"(1 + 2) * 3", "9"},
		{"7 / 2", "3.5"},
		{"7 % 3", "1"},
		{"7.5 % 2", "1.5"},
		{"-7 % 3", "-1"},
		{"2 ^ 10", "1024"},
		{"2 ** 0.5 > 1.41", "true"},
		{"-3 + +2", "-1"},
		{"0.1 + 0.2 == 0.3", "false"},
		{"1 < 2 and 3 > 4", "false"},
		{"1 < 2 || 3 > 4", "true"},
		{"not (1 == 1)", "false"},
		{"!(1 != 1) && 2 >= 2 && 2 <= 3", "true"},
		{`"a" == "a"`, "true"},
		{`"b" > "a"`, "true"},
		{"round(2.5) + floor(1.9) + ceil(0.1) + abs(-2)", "7"},
		{"min(3, 1, 2) * max(3, 1, 2)", "3"},
		{"9007199254740993", "9007199254740992"},
		{`"text"`, "text"},
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

// TestEval_Identifiers_B51 passes identifier values as variables.
func TestEval_Identifiers_B51(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		args []string
		want expr.Result
	}{
		{"number", "$arg1text * 2", []string{"21"}, expr.Result{Kind: expr.Number, Number: 42}},
		{"decimal and exponent", "$arg1text + $arg2text", []string{"-1.5", "2e1"}, expr.Result{Kind: expr.Number, Number: 18.5}},
		{"text", `$arg1text == "yes"`, []string{"yes"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"code stays text", `$arg1text == "1)+(2"`, []string{"1)+(2"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"hex is text", `$arg1text == "0x10"`, []string{"0x10"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"infinity is text", `$arg1text == "Inf"`, []string{"Inf"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"digits but no number", `$arg1text + $arg2text`, []string{"1.2.3", "1e999"}, expr.Result{Kind: expr.Text, Text: "1.2.31e999"}},
		{"rest of the token", "$arg1text2 + 1", []string{"4"}, expr.Result{Kind: expr.Number, Number: 43}},
		{"same identifier twice", "$arg1text - $ARG1TEXT", []string{"5"}, expr.Result{Kind: expr.Number, Number: 0}},
		{"range of arguments", "$arg1:2text", []string{"a", "b"}, expr.Result{Kind: expr.Text, Text: "a b"}},
		{"token without value", `$arg3text == "$arg3text"`, nil, expr.Result{Kind: expr.Bool, Bool: true}},
		{"identifier next to a number", "$argcount > 1", []string{"a", "b"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"character", `$unicode65 == "A"`, nil, expr.Result{Kind: expr.Bool, Bool: true}},
		{"pipe to a function", "$arg1text | abs()", []string{"-3"}, expr.Result{Kind: expr.Number, Number: 3}},
		{"quoted identifier is text", `"$arg1text" == "5" && $arg1text == 5`, []string{"5"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"text with identifiers in quotes", `"Hi $arg1text!" == 'Hi Bob!'`, []string{"Bob"}, expr.Result{Kind: expr.Bool, Bool: true}},
		{"single and back quotes", "'$arg1text' + `$arg2text`", []string{"a", "b"}, expr.Result{Kind: expr.Text, Text: "ab"}},
		{"code in quotes stays text", `"$arg1text"`, []string{`" + 1 + "`}, expr.Result{Kind: expr.Text, Text: `" + 1 + "`}},
		{"quotes without identifiers", `"5$ \"x\"" == '5$ "x"'`, nil, expr.Result{Kind: expr.Bool, Bool: true}},
		{"v in the text", `$arg1text == "v0 v_0" && $arg2text == 'v__1'`, []string{"v0 v_0", "v__1"}, expr.Result{Kind: expr.Bool, Bool: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := eval(t, tc.text, tc.args...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, r)
		})
	}
}

// TestEval_Errors_B52_B76 covers expressions that cannot be evaluated.
func TestEval_Errors_B52_B76(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		args []string
	}{
		{"B76: text times a number", "$arg1text * 2", []string{"zwei"}},
		{"code in an argument", "$arg1text + 1", []string{"1)+(2"}},
		{"token without value", "$arg3text * 2", nil},
		{"text in a comparison with a number", "$arg1text < 2", []string{"a"}},
		{"text in logic", "not $arg1text", []string{"true"}},
		{"division by zero", "1 / 0", nil},
		{"zero by zero", "0 / 0", nil},
		{"remainder by zero", "$arg1text % 0", []string{"5"}},
		{"remainder of text", "$arg1text % 2", []string{"a"}},
		{"overflow", "10 ^ 400", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := eval(t, tc.text, tc.args...)
			require.ErrorIs(t, err, expr.ErrEvaluation)
		})
	}
}

// TestCompile_Invalid covers syntax errors, parts of expr outside B50 and
// the size limit (B52).
func TestCompile_Invalid(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"",
		"1 +",
		"(1",
		"x + 1",
		"$ + 1",
		`$ == "$arg1text"`,
		"5$",
		"v1 + $arg1text",
		// Names like those of the variables are unknown names as well
		// (review of PR #46).
		"v0 == $arg1text",
		"v0 + $arg1text",
		"v_0 + $arg1text + $arg2text",
		"v__1 == $arg1text + $arg2text",
		"[1, 2]",
		"{a: 1}",
		"1 ? 2 : 3",
		"1..3",
		"1 in [1]",
		`"abc" contains "b"`,
		`"abc" matches "b"`,
		`"a" ?? "b"`,
		"len(\"x\")",
		"mod(5, 3)",
		"now()",
		"nil",
		"let x = 1; x",
		"$arg1text.x",
		"-$arg1text[0]",
		`"$arg1text\n" == "a"`,
		`"$arg1text`,
		`"a" + 'b`,
		strings.Repeat("1 + ", 600) + "1",
	} {
		t.Run(text[:min(len(text), 30)], func(t *testing.T) {
			t.Parallel()
			_, err := expr.Compile(text)
			require.ErrorIs(t, err, expr.ErrInvalid)
		})
	}
}

func TestEval_Canceled(t *testing.T) {
	t.Parallel()
	x, err := expr.Compile("$arg1text + 1")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = x.Eval(ctx, engine(t), &template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: []string{"1"}, ArgsText: "1"})
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, expr.ErrEvaluation)
}

// TestEval_Concurrent evaluates one expression from several goroutines.
func TestEval_Concurrent(t *testing.T) {
	t.Parallel()
	x, err := expr.Compile("$arg1text * 2 + 1")
	require.NoError(t, err)
	e := engine(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			r, err := x.Eval(t.Context(), e, &template.Scope{Location: time.UTC, ArgDelimiter: "|", Args: []string{strconv.Itoa(i)}, ArgsText: strconv.Itoa(i)})
			assert.NoError(t, err)
			assert.InDelta(t, float64(i*2+1), r.Number, 0)
		})
	}
	wg.Wait()
}

func TestResult(t *testing.T) {
	t.Parallel()
	x, err := expr.Compile("$arg1text")
	require.NoError(t, err)
	assert.Equal(t, "$arg1text", x.String())

	assert.Equal(t, template.FloatValue(1.5), expr.Result{Kind: expr.Number, Number: 1.5}.Value())
	assert.Equal(t, template.TextValue("true"), expr.Result{Kind: expr.Bool, Bool: true}.Value())
	assert.Equal(t, template.TextValue("a"), expr.Result{Kind: expr.Text, Text: "a"}.Value())
	assert.Equal(t, "1000000000000000000000", expr.Result{Kind: expr.Number, Number: 1e21}.String())
	assert.Equal(t, "0.1", expr.Result{Kind: expr.Number, Number: 0.1}.String())
}
