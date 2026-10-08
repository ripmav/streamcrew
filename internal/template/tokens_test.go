// SPDX-License-Identifier: MIT

package template_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

func TestTemplate_Segments(t *testing.T) {
	t.Parallel()
	type segment struct {
		text  string
		token bool
	}
	var got []segment
	for text, token := range template.Parse("a $$User $5 b$").Segments() {
		got = append(got, segment{text, token})
	}
	assert.Equal(t, []segment{{"a $", false}, {"$User", true}, {" ", false}, {"$5", true}, {" b$", false}}, got)

	var first []string
	for text := range template.Parse("$a $b $c").Segments() {
		first = append(first, text)
		break
	}
	assert.Equal(t, []string{"$a"}, first, "the iterator stops when asked")
}

// TestEngine_RenderEach_B21 renders several templates in one render.
func TestEngine_RenderEach_B21(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	family := template.Family{Name: "count", Identifiers: []template.Identifier{{Name: "score", Resolve: counting("21", &calls)}}}
	e := template.New(newRegistry(t, family))
	s := scope()
	s.SetValue("text", template.TextValue("1)+(2"))

	var ts []template.Template
	for _, text := range []string{"$score", "$SCORE", "$score0", "$text", "$arg3text", "score: $score <b>", ""} {
		ts = append(ts, template.Parse(text))
	}
	got, err := e.RenderEach(t.Context(), ts, &s)
	require.NoError(t, err)
	assert.Equal(t, []template.Rendered{
		{Text: "21", Replaced: true},
		{Text: "21", Replaced: true},
		{Text: "210", Replaced: true},
		{Text: "1)+(2", Replaced: true},
		{Text: "$arg3text", Replaced: false},
		{Text: "score: 21 <b>", Replaced: true},
		{Text: "", Replaced: true},
	}, got)
	assert.Equal(t, int64(1), calls.Load(), "all templates share one render")

	got, err = e.RenderEach(t.Context(), nil, new(scope()))
	require.NoError(t, err)
	assert.Empty(t, got)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = e.RenderEach(ctx, ts, new(scope()))
	require.ErrorIs(t, err, context.Canceled)
}

// TestEngine_RenderEach_Replaced: a template counts as replaced if every
// token got a value (B3, B4, B23; actions.md B24).
func TestEngine_RenderEach_Replaced(t *testing.T) {
	t.Parallel()
	e := template.New(newRegistry(t))
	for text, want := range map[string]bool{
		"":                              true,
		"no tokens":                     true,
		"a lone $ is text":              true,
		"$username":                     true,
		"$USERNAMEsuffix":               true,
		"$username and $":               true,
		"$targetusername":               false,
		"$username $targetusername":     false,
		"$unknown":                      false,
		"$followage, the source failed": false,
	} {
		got, err := e.RenderEach(t.Context(), []template.Template{template.Parse(text)}, new(scope()))
		require.NoError(t, err)
		assert.Equal(t, want, got[0].Replaced, text)
	}
}
