// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/overlay"
)

// makeWAV builds a 16-bit PCM WAV file with the interleaved samples,
// the rate in Hz and the channels.
func makeWAV(t *testing.T, samples []int16, rate, channels int) []byte {
	t.Helper()
	data := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(data[2*i:], uint16(v)) //nolint:gosec // the two's-complement bit pattern of the sample
	}
	var b bytes.Buffer
	// put appends v little-endian; the buffer does not fail, but the
	// write is checked.
	put := func(v any) {
		require.NoError(t, binary.Write(&b, binary.LittleEndian, v))
	}
	//nolint:gosec // the test values fit the header fields
	u16 := func(v int) uint16 { return uint16(v) }
	//nolint:gosec // the test values fit the header fields
	u32 := func(v int) uint32 { return uint32(v) }
	b.WriteString("RIFF")
	put(u32(36 + len(data)))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	put(u32(16))
	put(u16(1)) // PCM
	put(u16(channels))
	put(u32(rate))
	put(u32(rate * 2 * channels))
	put(u16(2 * channels))
	put(u16(16))
	b.WriteString("data")
	put(u32(len(data)))
	b.Write(data)
	return b.Bytes()
}

// floatAt reads the little endian float32 at offset i of data.
func floatAt(data []byte, i int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[i : i+4]))
}

// TestWAVDecodeCoversFormatsAndErrors covers decodeWAV: the 16-bit PCM
// mono and stereo decode, and the non-WAV, the non-PCM, the 24-bit, the
// three-channel and the truncated files are clear errors (the shortcut
// of ADR-0020 §1).
func TestWAVDecodeCoversFormatsAndErrors(t *testing.T) {
	samples := []int16{-32768, 0, 1234, 32767}
	got, rate, channels, err := decodeWAV(makeWAV(t, samples, 22050, 1))
	require.NoError(t, err)
	assert.Equal(t, samples, got)
	assert.Equal(t, 22050, rate)
	assert.Equal(t, 1, channels)

	got, rate, channels, err = decodeWAV(makeWAV(t, samples, 48000, 2))
	require.NoError(t, err)
	assert.Equal(t, samples, got)
	assert.Equal(t, 48000, rate)
	assert.Equal(t, 2, channels)

	for _, tc := range []struct {
		name string
		wav  []byte
	}{
		{"not wav", []byte("not a riff file at all")},
		{"truncated", makeWAV(t, samples, 44100, 1)[:20]},
		{"24 bit", func() []byte {
			w := makeWAV(t, samples, 44100, 1)
			w[34] = 24 // bits per sample
			return w
		}()},
		{"float", func() []byte {
			w := makeWAV(t, samples, 44100, 1)
			w[20] = 3 // audio format
			return w
		}()},
		{"three channels", makeWAV(t, []int16{1, 2, 3}, 44100, 3)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := decodeWAV(tc.wav)
			assert.ErrorIs(t, err, errNotWAV)
		})
	}
}

// TestNormalizeCoversMonoAndResample covers normalize: mono becomes
// stereo, the rate becomes the target rate, and a silent input stays
// silent.
func TestNormalizeCoversMonoAndResample(t *testing.T) {
	out := normalize([]int16{1000, -1000}, 44100, 1)
	require.Len(t, out, 16) // two frames, two channels, 32 bits
	assert.Equal(t, floatAt(out, 0), floatAt(out, 4), "mono duplicates to both channels")
	assert.InDelta(t, 1000/32768.0, floatAt(out, 0), 1e-6)
	assert.InDelta(t, -1000/32768.0, floatAt(out, 8), 1e-6)

	in := make([]int16, 22050)
	for i := range in {
		in[i] = 8000
	}
	out = normalize(in, 22050, 1)
	// 22050 mono frames become 44100 stereo frames.
	assert.Len(t, out, 44100*2*4)

	out = normalize([]int16{0, 0, 0, 0}, 22050, 2)
	for i := 0; i < len(out); i += 4 {
		assert.Zero(t, floatAt(out, i))
	}
}

// TestNormalizeResamplesPerChannel is the regression of the stereo
// resample (Review 345-3): the input carries left = +1 and right = -1
// at 48 kHz, and the old resample interpolated over the interleaved
// array, so it mixed the channels. Per frame and per channel the left
// stays positive, the right negative, and the length is the even
// number of channels of the output frames.
func TestNormalizeResamplesPerChannel(t *testing.T) {
	samples := make([]int16, 2*4800)
	for i := range samples {
		if i%2 == 0 {
			samples[i] = 32767
		} else {
			samples[i] = -32767
		}
	}
	out := normalize(samples, 48000, 2)
	// 4800 frames at 48 kHz become 4410 frames at 44100 Hz.
	require.Len(t, out, 4410*2*4)
	for i := 0; i < len(out); i += 8 {
		assert.Greater(t, float64(floatAt(out, i)), 0.0, "the left channel stays positive")
		assert.Less(t, float64(floatAt(out, i+4)), 0.0, "the right channel stays negative")
	}
}

// fakeOverlay records the shown items and serves an end channel per
// item.
type fakeOverlay struct {
	items []overlay.Item
	ends  []chan struct{}
}

// ShowItem implements media.Overlay.
func (f *fakeOverlay) ShowItem(_ context.Context, _ string, item overlay.Item) (string, <-chan struct{}, error) {
	end := make(chan struct{})
	f.items = append(f.items, item)
	f.ends = append(f.ends, end)
	return "item", end, nil
}

// TestOverlaySinkShowsAudioItemOnDefaultEndpoint covers the overlay
// sink (B40, ADR-0020 §1): the file plays as an audio item on the
// default endpoint with the media URL and the volume, and the end of
// the item is the end of the playback (B14).
func TestOverlaySinkShowsAudioItemOnDefaultEndpoint(t *testing.T) {
	fake := &fakeOverlay{}
	sink := NewOverlay(fake, "/media")
	require.Equal(t, SinkOverlay, sink.Name())
	assert.Empty(t, sink.Required())

	end, err := sink.Play(context.Background(), "/media/sfx/hi.wav", 42)
	require.NoError(t, err)
	require.Len(t, fake.items, 1)
	item := fake.items[0]
	assert.Equal(t, "audio", item.Kind)
	content := item.Content.(map[string]any)
	assert.Equal(t, "/media/media/sfx/hi.wav", content["src"])
	assert.Equal(t, 42.0, content["volume"])
	close(fake.ends[0])
	select {
	case <-end:
	default:
		t.Fatal("the end of the item is the end of the playback")
	}

	// A file outside the media directory is a clear error.
	_, err = sink.Play(context.Background(), "/other/hi.wav", 100)
	require.Error(t, err)
}

// fakeSink is a sink that records its calls.
type fakeSink struct {
	name     string
	required capability.Capability
	played   int
	err      error
}

func (f *fakeSink) Name() string                    { return f.name }
func (f *fakeSink) Required() capability.Capability { return f.required }
func (f *fakeSink) Play(_ context.Context, _ string, _ float64) (<-chan struct{}, error) {
	f.played++
	return nil, f.err
}

// TestServicePlayCoversDispatchAndCapability covers the service (B40,
// ADR-0020 §2): the play dispatches to the sink of the name, the
// capability the sink requires must be granted with the rights of now,
// and the unknown sink and the unavailable agent are clear errors.
func TestServicePlayCoversDispatchAndCapability(t *testing.T) {
	local := &fakeSink{name: SinkLocal, required: capability.HostAudio}
	agent := &fakeSink{name: SinkAgent, err: ErrAgentUnavailable}
	svc := New(&fakeSink{name: SinkOverlay}, local, agent)
	assert.Equal(t, []string{SinkOverlay, SinkLocal, SinkAgent}, svc.Names())

	granted, err := capability.NewSet(capability.HostAudio)
	require.NoError(t, err)
	none, err := capability.NewSet()
	require.NoError(t, err)

	// The dispatch goes to the sink of the name.
	_, err = svc.Play(context.Background(), SinkLocal, "f.wav", 100, granted)
	require.NoError(t, err)
	assert.Equal(t, 1, local.played)

	// Without the capability the local sink is a clear error, the
	// overlay sink plays on.
	_, err = svc.Play(context.Background(), SinkLocal, "f.wav", 100, none)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host:audio")
	overlaySink, ok := svc.Sink(SinkOverlay)
	require.True(t, ok)
	_, err = overlaySink.Play(context.Background(), "f.wav", 100)
	assert.NoError(t, err)

	// The agent sink is a port for Phase 9.6.
	_, err = svc.Play(context.Background(), SinkAgent, "f.wav", 100, granted)
	assert.ErrorIs(t, err, ErrAgentUnavailable)

	// The unknown sink names itself and the sinks.
	_, err = svc.Play(context.Background(), "nope", "f.wav", 100, granted)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

// TestAgentSinkCoversThePort covers the agent sink (ADR-0020 §1): it is
// known by name, needs no capability, and the playback is the clear
// error until Phase 9.6.
func TestAgentSinkCoversThePort(t *testing.T) {
	sink := NewAgent()
	require.Equal(t, SinkAgent, sink.Name())
	assert.Empty(t, sink.Required())
	_, err := sink.Play(context.Background(), "f.wav", 100)
	assert.ErrorIs(t, err, ErrAgentUnavailable)
}
