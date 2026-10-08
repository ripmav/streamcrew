// SPDX-License-Identifier: MIT

package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/auth"
)

// TestScopes is the required scope list of ADR-0014.
func TestScopes(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for _, s := range auth.Scopes {
		require.NotEmpty(t, s)
		require.False(t, seen[s], "duplicate scope %q", s)
		seen[s] = true
	}
	// The table of ADR-0014 has 41 scopes (2026-10-07); a change there
	// must update this list.
	assert.Len(t, auth.Scopes, 41)
}

func TestMissing(t *testing.T) {
	t.Parallel()
	want := []string{"a", "b", "c"}
	assert.Equal(t, want, auth.Missing("", want), "nothing granted")
	assert.Empty(t, auth.Missing("a b c", want), "everything granted")
	assert.Equal(t, []string{"c"}, auth.Missing("a b", want))
	assert.Equal(t, []string{"b", "c"}, auth.Missing("a", want), "in the order of want")
	assert.Empty(t, auth.Missing("c a b  c", want), "duplicates and spaces do not matter")
	assert.Empty(t, auth.Missing("A B C", want), "the casing does not matter")
	assert.Empty(t, auth.Missing("a b c d e", want), "extra scopes are fine")
}
