// SPDX-License-Identifier: MIT

package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAskAndAnswer covers the round-trip of the prompt mechanism: a prompt is
// published once, an answer reaches it, and an answered prompt is not
// pending anymore.
func TestAskAndAnswer(t *testing.T) {
	var published []Prompt
	s := New(func(_ context.Context, p Prompt) { published = append(published, p) })

	p, ch := s.Ask(context.Background(), KindConfirmation, "sure?", []string{"yes", "no"})
	require.Len(t, published, 1)
	require.NotEmpty(t, p.ID)
	assert.Equal(t, KindConfirmation, p.Kind)
	assert.Equal(t, []string{"yes", "no"}, p.Options)

	assert.False(t, s.Answer("unknown", "", false))
	require.True(t, s.Answer(p.ID, "yes", true))

	select {
	case ans := <-ch:
		assert.Equal(t, "yes", ans.Value)
		assert.True(t, ans.Confirmed)
	default:
		t.Fatal("the answer did not reach the prompt")
	}
	assert.False(t, s.Answer(p.ID, "again", false), "an answered prompt is not pending")
}
