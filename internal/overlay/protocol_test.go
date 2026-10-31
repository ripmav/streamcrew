// SPDX-License-Identifier: MIT

package overlay_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/overlay"
	"github.com/ripmav/streamcrew/internal/settings"
)

func jsonV2MarshalT(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonV2Marshal(v)
	require.NoError(t, err)
	return b
}

// TestProtocolOmitsZeroNumericFields checks (Review 342-9) that the
// numeric protocol and position fields use omitzero, so that a zero
// value is absent from the wire format: the json/v2 omitempty tag
// would have kept "field":0 in the output.
func TestProtocolOmitsZeroNumericFields(t *testing.T) {
	assertNotContains := func(t *testing.T, b, key string) {
		t.Helper()
		assert.False(t, strings.Contains(b, key), "%s must be omitted: %s", key, b)
	}

	id := string(jsonV2MarshalT(t, overlay.ItemData{ID: "i1", Kind: "tts"}))
	assertNotContains(t, id, `"animationDuration"`)
	assertNotContains(t, id, `"duration"`)

	tts := string(jsonV2MarshalT(t, overlay.TTSSpeakData{Text: "hallo"}))
	for _, key := range []string{`"seq"`, `"volume"`, `"rate"`, `"pitch"`} {
		assertNotContains(t, tts, key)
	}

	pos := string(jsonV2MarshalT(t, settings.Position{Model: "percent"}))
	assertNotContains(t, pos, `"x"`)
	assertNotContains(t, pos, `"y"`)

	// The non-zero values stay on the wire.
	id = string(jsonV2MarshalT(t, overlay.ItemData{ID: "i1", Kind: "tts", AnimationDuration: 150, Duration: 5000}))
	assert.Contains(t, id, `"animationDuration":150`)
	assert.Contains(t, id, `"duration":5000`)

	tts = string(jsonV2MarshalT(t, overlay.TTSSpeakData{Text: "hallo", Seq: 7, Volume: 0.5, Rate: 1.2, Pitch: 0.8}))
	for _, key := range []string{`"seq":7`, `"volume":0.5`, `"rate":1.2`, `"pitch":0.8`} {
		assert.Contains(t, tts, key)
	}

	pos = string(jsonV2MarshalT(t, settings.Position{Model: "percent", X: 10, Y: 20}))
	assert.Contains(t, pos, `"x":10`)
	assert.Contains(t, pos, `"y":20`)
}
