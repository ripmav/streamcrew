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
	var s template.Scope
	s.SetValue("text", template.TextValue("1)+(2"))

	var ts []template.Template
	for _, text := range []string{"$score", "$SCORE", "$score0", "$text", "$arg3text", "score: $score <b>", ""} {
		ts = append(ts, template.Parse(text))
	}
	texts, err := e.RenderEach(t.Context(), ts, &s)
	require.NoError(t, err)
	assert.Equal(t, []string{"21", "21", "210", "1)+(2", "$arg3text", "score: 21 <b>", ""}, texts)
	assert.Equal(t, int64(1), calls.Load(), "all templates share one render")

	texts, err = e.RenderEach(t.Context(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, texts)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = e.RenderEach(ctx, ts, nil)
	require.ErrorIs(t, err, context.Canceled)
}
