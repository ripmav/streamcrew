// SPDX-License-Identifier: MIT

package commandfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReaderKeepsFirst checks that a reader keeps the first problem of a
// document (B32).
func TestReaderKeepsFirst(t *testing.T) {
	t.Parallel()
	r := &reader{doc: Document{File: "a.yaml"}}
	r.fail(position{1, 2}, "a", "first")
	r.fail(position{3, 4}, "b", "second")
	require.NotNil(t, r.err)
	assert.Equal(t, "a.yaml:1:2: a: first", r.err.String())
}
