//go:build !localaudio

// SPDX-License-Identifier: MIT

package media

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ripmav/streamcrew/internal/capability"
)

// ErrNotBuilt is the error of the local sink without the build tag
// localaudio (ADR-0020 §1): the desktop build has the tag, the server
// image and the core build do not, and the error is a clear one (B40).
var ErrNotBuilt = errors.New("media: the sink local is not built; the build needs the tag localaudio (ADR-0020)")

// LocalSink is the local sink without the build tag localaudio: the
// service knows it by name, so the configuration and the actions work
// unchanged, but a playback is a clear error.
type LocalSink struct{}

// NewLocal returns the stub of the local sink.
func NewLocal(_ *slog.Logger) *LocalSink { return &LocalSink{} }

// Name implements Sink.
func (s *LocalSink) Name() string { return SinkLocal }

// Required implements Sink (B40, ADR-0020 §2); the capability check
// comes before the build check, so the error names the capability
// first.
func (s *LocalSink) Required() capability.Capability { return capability.HostAudio }

// Play implements Sink.
func (s *LocalSink) Play(_ context.Context, _ string, _ float64) (<-chan struct{}, error) {
	return nil, ErrNotBuilt
}
