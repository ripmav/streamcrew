// SPDX-License-Identifier: Apache-2.0

package textfunc_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
	"github.com/ripmav/streamcrew/internal/textfunc"
)

// berlin is the time zone of the profile in the tests.
func berlin(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)
	return loc
}

// engine returns a template engine with the argument identifiers.
func engine(t testing.TB) *template.Engine {
	t.Helper()
	reg, err := template.NewRegistry(template.ArgumentFamily())
	require.NoError(t, err)
	return template.New(reg)
}

// scope returns a scope with the arguments args.
func scope(loc *time.Location, args ...string) *template.Scope {
	return &template.Scope{Location: loc, ArgDelimiter: "|", Args: args, ArgsText: strings.Join(args, " ")}
}

// eval parses src, renders its templates with the arguments args in one
// render and evaluates it at now.
func eval(t testing.TB, e *template.Engine, src string, now time.Time, args ...string) (string, error) {
	t.Helper()
	x, err := textfunc.Parse(src)
	if err != nil {
		return "", err
	}
	loc := now.Location()
	rendered, err := e.RenderEach(t.Context(), x.Templates(), scope(loc, args...))
	require.NoError(t, err)
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	return x.EvalWithTexts(texts, now, loc)
}

// TestFunctions covers actions.md B52 to B54.
func TestFunctions(t *testing.T) {
	t.Parallel()
	e := engine(t)
	now := time.Date(2026, 10, 1, 0, 30, 0, 0, berlin(t))
	for _, tc := range []struct {
		src  string
		args []string
		want string
	}{
		{"tolower(ÄBC Def)", nil, "äbc def"},
		{"toupper(äbc def)", nil, "ÄBC DEF"},
		{"removespaces(a b\tc d\ne)", nil, "abcde"},
		{"removecommas($arg1text)", []string{"1,234,567"}, "1234567"},
		{`removecommas("1,234,567")`, nil, "1234567"},
		{"length(héllo 👋)", nil, "7"},
		{"length()", nil, "0"},
		{"count(Count the number of e letters in this sentence,e)", nil, "8"},
		{"count(a1b22c333,[0-9]+)", nil, "3"},
		{"replace(Hello World,World,Universe)", nil, "Hello Universe"},
		{"replace(abc,,x)", nil, "abc"},
		{"replace(aaa,a,)", nil, ""},
		{"urlencode(Hello World/ä+b)", nil, "Hello+World%2F%C3%A4%2Bb"},
		{"uriescape(Hello World/ä+b)", nil, "Hello%20World%2F%C3%A4%2Bb"},
		{"datefrom(2025-01-01)", nil, "1 Year, 9 Months"},
		{"dateto(2026-12-25)", nil, "2 Months, 24 Days"},
		{"datefrom(2026-10-01)", nil, "0 Days"},
		{"dateto(2026-10-01)", nil, "0 Days"},
		{"tolower()", nil, ""},

		{"names regardless of case: TOLOWER(ABC) ToUpper(def)", nil, "names regardless of case: abc DEF"},
		{"nested: toupper(replace($arg1text,b,length(removespaces(x y z))))", []string{"abc"}, "nested: A3C"},
		{"no functions: Hello $arg1text", []string{"x"}, "no functions: Hello x"},
		{"B210: toupper($arg1text)", []string{"a),removespaces(b"}, "B210: A),REMOVESPACES(B"},
		{"inserted commas: replace($arg1text,$arg2text,-)", []string{"a,b", ","}, "inserted commas: a-b"},
		{"quoted: replace($arg1text,\",\",\"(;)\")", []string{"a,b"}, "quoted: a(;)b"},
		{"quoted with an identifier: replace(\"$arg1text, (1)\",\"(1)\",x)", []string{"hi"}, "quoted with an identifier: hi, x"},
		{`quote not at the end: tolower("A"B)`, nil, `quote not at the end: "a"b`},
		{"parentheses without a name: tolower(A (B, C) D)", nil, "parentheses without a name: a (b, c) d"},
		{"no closing parenthesis: tolower(ABC", nil, "no closing parenthesis: tolower(ABC"},
		{"outer without closing: toupper(tolower(ABC)", nil, "outer without closing: toupper(abc"},
		{"after a digit: 5tolower(A)", nil, "after a digit: 5tolower(A)"},
		{"B53 other names: Score(s) unknown(x, y)", nil, "B53 other names: Score(s) unknown(x, y)"},
		{"in a word: xtolower(A)", nil, "in a word: xtolower(A)"},
		{"functions inside: Score(toupper(a))", nil, "functions inside: Score(A)"},
		{"inside a function: tolower(Score(A, B))", nil, "inside a function: score(a, b)"},
		{"after a token: $arg1texttolower(A)", []string{"x"}, "after a token: xtolower(A)"},
		{"a token is no function: $tolower(A)", nil, "a token is no function: $tolower(A)"},
		{"spaces belong to parameters: replace(a b, b, c)", nil, "spaces belong to parameters: a c"},
		{"stray ) and (: a) (b toupper(c)", nil, "stray ) and (: a) (b C"},
	} {
		got, err := eval(t, e, tc.src, now, tc.args...)
		require.NoError(t, err, tc.src)
		assert.Equal(t, tc.want, got, tc.src)
	}
}

// TestParseFails covers B55 for what saving can see.
func TestParseFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ src, want string }{
		{"tolower(a,b)", "tolower takes 1 parameters, not 2"},
		{"replace(a,b)", "replace takes 3 parameters, not 2"},
		{"count(abc,[)", "count: invalid pattern"},
		{"datefrom(2025-13-01)", `datefrom: invalid date "2025-13-01"`},
		{"dateto(\"1.1.2027\")", `dateto: invalid date "1.1.2027"`},
		{strings.Repeat("tolower(", textfunc.MaxDepth+1) + strings.Repeat(")", textfunc.MaxDepth+1), "nested deeper than 100"},
	} {
		_, err := textfunc.Parse(tc.src)
		require.ErrorIs(t, err, textfunc.ErrInvalid, tc.src)
		assert.ErrorContains(t, err, tc.want, tc.src)
	}
	_, err := textfunc.Parse(strings.Repeat("tolower(", textfunc.MaxDepth) + "A" + strings.Repeat(")", textfunc.MaxDepth))
	require.NoError(t, err, "as deep as allowed")
	_, err = textfunc.Parse("count($arg1text,$arg2text) datefrom($arg1text)")
	require.NoError(t, err, "parameters with identifiers are checked when they are evaluated")
}

// TestEvalFails covers B55 for what only the run shows, and the limit of
// results.
func TestEvalFails(t *testing.T) {
	t.Parallel()
	e := engine(t)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, berlin(t))
	for _, tc := range []struct {
		src  string
		args []string
		want string
	}{
		{"count(abc,$arg1text)", []string{"["}, "count: invalid pattern"},
		{"datefrom($arg1text)", []string{"tomorrow"}, `datefrom: invalid date "tomorrow"`},
		{"datefrom($arg1text)", []string{"2026-10-02"}, "datefrom: 2026-10-02 is in the future"},
		{"dateto($arg1text)", []string{"2026-09-30"}, "dateto: 2026-09-30 is in the past"},
		{"replace($arg1text,a," + strings.Repeat("b", 1100) + ")", []string{strings.Repeat("a", 1000)}, "replace: result too long"},
		{"toupper(replace($arg1text,a,aa))", []string{strings.Repeat("a", textfunc.MaxResult/2+1)}, "replace: result too long"},
		{"urlencode(urlencode($arg1text))", []string{strings.Repeat("ä", textfunc.MaxResult/8+1)}, "urlencode: result longer than 1048576 bytes"},
	} {
		_, err := eval(t, e, tc.src, now, tc.args...)
		require.ErrorIs(t, err, textfunc.ErrEvaluation, tc.src)
		assert.ErrorContains(t, err, tc.want, tc.src)
	}

	x, err := textfunc.Parse("tolower($arg1text)")
	require.NoError(t, err)
	_, err = x.EvalWithTexts(nil, now, now.Location())
	require.ErrorIs(t, err, textfunc.ErrEvaluation, "too few texts")
	_, err = x.EvalWithTexts([]string{"a", "b"}, now, now.Location())
	require.ErrorIs(t, err, textfunc.ErrEvaluation, "too many texts")
}

// TestDatesInTheProfileZone: today is the date in the time zone of the
// profile, not in UTC.
func TestDatesInTheProfileZone(t *testing.T) {
	t.Parallel()
	e := engine(t)
	// 1 October 00:30 in Berlin is still 30 September in UTC.
	now := time.Date(2026, 10, 1, 0, 30, 0, 0, berlin(t))
	got, err := eval(t, e, "datefrom(2026-09-30) dateto(2026-10-02)", now)
	require.NoError(t, err)
	assert.Equal(t, "1 Day 1 Day", got)
	_, err = eval(t, e, "dateto(2026-09-30)", now)
	require.ErrorIs(t, err, textfunc.ErrEvaluation)
}

// TestTemplatesInOrder: Templates lists the texts in the order of the
// value, also inside quoted parameters.
func TestTemplatesInOrder(t *testing.T) {
	t.Parallel()
	x, err := textfunc.Parse(`a $arg1text replace(b,"c,",tolower(d)) e`)
	require.NoError(t, err)
	var got []string
	for _, tmpl := range x.Templates() {
		got = append(got, tmpl.String())
	}
	assert.Equal(t, []string{"a $arg1text ", "b", "c,", "d", " e"}, got)
	assert.Equal(t, `a $arg1text replace(b,"c,",tolower(d)) e`, x.String())
}

// TestParseIsLinear: names without closing parentheses and long texts do
// not make Parse slow.
func TestParseIsLinear(t *testing.T) {
	t.Parallel()
	for _, src := range []string{
		strings.Repeat("tolower(a) ", 20_000),
		strings.Repeat("x (a, b) ", 20_000),
		strings.Repeat("a(b) c(", 50) + strings.Repeat(" text", 20_000),
		strings.Repeat("tolower(", 20_000),
		strings.Repeat("score(", 20_000),
	} {
		_, err := textfunc.Parse(src)
		if err != nil {
			require.ErrorIs(t, err, textfunc.ErrInvalid)
		}
	}
}
