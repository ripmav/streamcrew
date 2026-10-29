// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOverlayWidgetState covers the state document of the overlay widget
// (spec overlays.md, B20, ADR-0019 §5): one JSON document per widget,
// replaced on write.
func TestOverlayWidgetState(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	_, found, err := s.OverlayWidgetState(ctx, "g1")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, s.SetOverlayWidgetState(ctx, "g1", []byte(`{"current":1,"segment":0}`)))
	doc, found, err := s.OverlayWidgetState(ctx, "g1")
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"current":1,"segment":0}`, string(doc))

	// A new document replaces the old one.
	require.NoError(t, s.SetOverlayWidgetState(ctx, "g1", []byte(`{"current":2,"segment":1}`)))
	doc, found, err = s.OverlayWidgetState(ctx, "g1")
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"current":2,"segment":1}`, string(doc))

	// The other widgets are untouched.
	require.NoError(t, s.SetOverlayWidgetState(ctx, "t1", []byte(`{"paused":true}`)))
	docT, found, err := s.OverlayWidgetState(ctx, "t1")
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"paused":true}`, string(docT))
	doc, found, err = s.OverlayWidgetState(ctx, "g1")
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"current":2,"segment":1}`, string(doc))
}
