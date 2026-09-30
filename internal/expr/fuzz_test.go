// SPDX-License-Identifier: Apache-2.0

package expr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/expr"
	"github.com/ripmav/streamcrew/internal/template"
)

// FuzzExpression compiles and evaluates arbitrary expressions and argument
// values: nothing panics, and every error is one of the documented kinds
// (B52).
func FuzzExpression(f *testing.F) {
	for _, seed := range []struct{ text, arg string }{
		{"$arg1text * 2", "21"},
		{"$arg1text + 1", "1)+(2"},
		{`"$arg1text" == 'x' || $argcount > 1`, "x"},
		{"round($arg1text / 3) % 2 ^ 3", "1e5"},
		{"[1, 2] | map(# * 2)", ""},
		{`"a\"$arg1text`, `"`},
		{"$", "$"},
	} {
		f.Add(seed.text, seed.arg)
	}
	e := engine(f)
	f.Fuzz(func(t *testing.T, text, arg string) {
		x, err := expr.Compile(text)
		if err != nil {
			assert.ErrorIs(t, err, expr.ErrInvalid)
			return
		}
		r, err := x.Eval(t.Context(), e, &template.Scope{Args: []string{arg, arg}})
		if err != nil {
			assert.ErrorIs(t, err, expr.ErrEvaluation)
			return
		}
		assert.True(t, r.Kind == expr.Number || r.Kind == expr.Bool || r.Kind == expr.Text)
	})
}
