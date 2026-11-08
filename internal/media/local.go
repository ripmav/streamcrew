//go:build localaudio

// SPDX-License-Identifier: MIT

package media

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"

	"github.com/ripmav/streamcrew/internal/capability"
)

// LocalSink plays the files on the audio device of the host
// (ADR-0020 §1). The backend oto/v3 uses the system default device; the
// device cannot be selected (ADR-0020, open items).
type LocalSink struct {
	mu     sync.Mutex
	ctx    *oto.Context
	logger *slog.Logger
}

// NewLocal returns the local sink; the audio context is opened on the
// first playback, so that a core without audio hardware still starts.
func NewLocal(logger *slog.Logger) *LocalSink {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &LocalSink{logger: logger}
}

// Name implements Sink.
func (s *LocalSink) Name() string { return SinkLocal }

// Required implements Sink (B40, ADR-0020 §2).
func (s *LocalSink) Required() capability.Capability { return capability.HostAudio }

// context opens the audio context on the first call.
func (s *LocalSink) context() (*oto.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx != nil {
		return s.ctx, nil
	}
	c, _, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      targetRate,
		ChannelCount:    targetChannels,
		Format:          oto.FormatFloat32LE,
		ApplicationName: "streamcrew",
	})
	if err != nil {
		return nil, fmt.Errorf("media: open the audio device: %w", err)
	}
	s.ctx = c
	return c, nil
}

// otoPlayer is the oto player the local sink drives (ADR-0020 §1); the
// production plays with *oto.Player, the tests without an audio device
// fake it (Review 345-2).
type otoPlayer interface {
	SetVolume(volume float64)
	Play()
	PauseAndStopReading()
	Close() error
}

// Play implements Sink. The file must be a 16-bit PCM WAV (decodeWAV);
// it is normalized to the parameters of the context before it plays.
func (s *LocalSink) Play(ctx context.Context, file string, volume float64) (<-chan struct{}, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("media: the local sink: %w", err)
	}
	samples, rate, channels, err := decodeWAV(data)
	if err != nil {
		return nil, err
	}
	pcm := normalize(samples, rate, channels)
	c, err := s.context()
	if err != nil {
		return nil, err
	}
	return playUntilDone(ctx, s.logger, file, c.NewPlayer(bytes.NewReader(pcm)), pcm, volume)
}

// playUntilDone plays pcm on the player and returns a channel that is
// closed when the file is done (B46, ADR-0020 §1). The playback runs
// until the file is done, not until the action: the action context ends
// with the action, and the engine cancels it right after, so the player
// is not stopped on it (Review 345-2). The player must stay reachable
// until the timer closes it, or oto closes it mid-playback.
func playUntilDone(ctx context.Context, logger *slog.Logger, file string, p otoPlayer, pcm []byte, volume float64) (<-chan struct{}, error) {
	p.SetVolume(volume / 100)
	p.Play()
	frames := len(pcm) / 8 // two channels of 32 bits
	dur := time.Duration(float64(frames) / float64(targetRate) * float64(time.Second))
	end := make(chan struct{})
	var once sync.Once
	time.AfterFunc(dur, func() {
		once.Do(func() {
			_ = p.Close()
			close(end)
		})
	})
	logger.DebugContext(ctx, "media: local playback started", "file", file, "duration", dur)
	return end, nil
}
