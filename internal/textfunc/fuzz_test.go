// SPDX-License-Identifier: Apache-2.0

package textfunc_test

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
	"github.com/ripmav/streamcrew/internal/textfunc"
)

// FuzzParse parses arbitrary values and evaluates them with an arbitrary
// argument: nothing panics, every error is one of the documented kinds
// (B55), and a value without "(" has no functions and renders as a
// template does (B53).
func FuzzParse(f *testing.F) {
	for _, seed := range []struct{ src, arg string }{
		{"toupper($arg1text)", "a),removespaces(b"},
		{`replace($arg1text,",",";")`, "a,b"},
		{"tolower(A (B, C) D) toupper(tolower(x)", "x"},
		{"count($arg1text,[0-9]+) datefrom(2025-01-01)", "1a22"},
		{`replace("a"b,"," c)`, `"`},
		{"unknown(x) tolower(a,b)", ""},
		{strings.Repeat("a(", 200), "("},
		{"$arg1texttolower(x) 5tolower(y)", "$arg1text"},
	} {
		f.Add(seed.src, seed.arg)
	}
	e := engine(f)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, src, arg string) {
		s := fuzzScope(arg)
		x, err := textfunc.Parse(src)
		if err != nil {
			assert.ErrorIs(t, err, textfunc.ErrInvalid)
			return
		}
		assert.Equal(t, src, x.String())
		rendered, err := e.RenderEach(t.Context(), x.Templates(), s)
		require.NoError(t, err)
		texts := make([]string, len(rendered))
		for i, r := range rendered {
			texts[i] = r.Text
		}
		got, err := x.EvalWithTexts(texts, now, time.UTC)
		if err != nil {
			assert.ErrorIs(t, err, textfunc.ErrEvaluation)
			return
		}
		if !strings.Contains(src, "(") {
			want, err := e.Render(t.Context(), template.Parse(src), s, template.Text)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	})
}

// FuzzInjected covers B53 and B210: an inserted value never becomes a
// function or a separator, whatever parentheses, commas or quotes it
// contains.
func FuzzInjected(f *testing.F) {
	for _, seed := range []string{"a),removespaces(b", `",tolower(")`, "x,y", "((", "$arg1text", "ä,\"(,)\""} {
		f.Add(seed)
	}
	e := engine(f)
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, arg string) {
		got, err := eval(t, e, `toupper($arg1text)|replace($arg1text,",",-)|length($arg1text)|"$arg1text"`, now, nonEmpty(arg))
		require.NoError(t, err)
		arg = nonEmpty(arg)
		want := strings.Join([]string{
			strings.ToUpper(arg),
			strings.ReplaceAll(arg, ",", "-"),
			strconv.Itoa(utf8.RuneCountInString(arg)),
			`"` + arg + `"`,
		}, "|")
		assert.Equal(t, want, got)
	})
}

// fuzzScope returns a scope whose first argument is arg.
func fuzzScope(arg string) *template.Scope {
	return scope(time.UTC, nonEmpty(arg))
}

// nonEmpty returns arg, or "-" for the empty text, because a scope with an
// argument needs the text after the trigger.
func nonEmpty(arg string) string {
	if arg == "" {
		return "-"
	}
	return arg
}
