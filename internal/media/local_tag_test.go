//go:build localaudio

// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

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

// fakePlayer records the oto player the local sink drives, so the
// playback runs without an audio device (Review 345-2).
type fakePlayer struct {
	played bool
	paused bool
	closed bool
}

func (f *fakePlayer) SetVolume(_ float64)  {}
func (f *fakePlayer) Play()                { f.played = true }
func (f *fakePlayer) PauseAndStopReading() { f.paused = true }
func (f *fakePlayer) Close() error         { f.closed = true; return nil }

// TestLocalPlaybackRunsUntilTheFileIsDone is the regression of the
// playback (Review 345-2, B46): the engine cancels the action context
// as soon as the action returns, so the playback must not stop with it
// — it runs until the file is done, and only then the player closes and
// the end channel.
func TestLocalPlaybackRunsUntilTheFileIsDone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &fakePlayer{}
		// One second of stereo float32: the playback runs a second.
		pcm := make([]byte, 8*targetRate)
		end, err := playUntilDone(t.Context(), slog.New(slog.DiscardHandler), "f.wav", p, pcm, 100)
		require.NoError(t, err)
		assert.True(t, p.played, "the playback starts")
		select {
		case <-end:
			t.Fatal("the playback is done before the file is")
		default:
		}
		assert.False(t, p.paused, "the action context must not pause the playback")
		time.Sleep(time.Second + time.Millisecond)
		select {
		case <-end:
		default:
			t.Fatal("the playback is not done when the file is")
		}
		assert.True(t, p.closed, "the player closes when the file is done")
		assert.False(t, p.paused, "the playback is never paused")
	})
}
