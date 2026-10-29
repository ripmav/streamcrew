//go:build !localaudio

// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
)

// TestLocalStubWithoutBuildTagCoversTheClearError covers the stub of
// the local sink without the build tag localaudio (ADR-0020 §1, B40):
// the service knows the sink, the playback is the clear error. Under
// the tag the real oto sink replaces the stub and this test does not
// compile (ErrNotBuilt is the stub's error).
func TestLocalStubWithoutBuildTagCoversTheClearError(t *testing.T) {
	sink := NewLocal(nil)
	require.Equal(t, SinkLocal, sink.Name())
	assert.Equal(t, capability.HostAudio, sink.Required())
	_, err := sink.Play(context.Background(), "f.wav", 100)
	assert.ErrorIs(t, err, ErrNotBuilt)
}
