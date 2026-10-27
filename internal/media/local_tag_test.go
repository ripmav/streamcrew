//go:build localaudio

// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
)

// TestLocalSinkWithBuildTagCoversTheNoDevicePath covers the local sink
// with the build tag localaudio without an audio device: the sink is
// known by name with the capability, and a missing file is a clear
// error before the audio context opens. The playback on the device
// itself is a real hardware path, checked on the streaming PC, not in
// the CI (ADR-0020 §1).
func TestLocalSinkWithBuildTagCoversTheNoDevicePath(t *testing.T) {
	sink := NewLocal(nil)
	require.Equal(t, SinkLocal, sink.Name())
	assert.Equal(t, capability.HostAudio, sink.Required())
	_, err := sink.Play(context.Background(), "/nonexistent/f.wav", 100)
	require.Error(t, err)
}
