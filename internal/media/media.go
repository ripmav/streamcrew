// SPDX-License-Identifier: MIT

// Package media plays audio files on the sinks of the core (roadmap 7.3,
// ADR-0020): the overlay sink shows an audio item on the default endpoint
// (the browser pane plays it, Q21), the local sink plays the file on the
// audio device of the host (build tag localaudio, capability host:audio),
// and the agent sink is the port for the passthrough of Phase 9.6.
package media

import (
	"context"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/capability"
)

// Sinks are the names of the sinks, as the actions and the configuration
// name them (B40, ADR-0020 §2).
const (
	// SinkOverlay plays the file as an audio item on the default endpoint
	// of the overlay (default, no capability needed).
	SinkOverlay = "overlay"
	// SinkLocal plays the file on the audio device of the host; the build
	// needs the tag localaudio, the core the capability host:audio (B40).
	SinkLocal = "local"
	// SinkAgent plays the file on the remote host of the agent; the
	// passthrough follows with Phase 9.6 (ADR-0020 §1).
	SinkAgent = "agent"
)

// Sinks returns the sink names, in the order editors show them.
func Sinks() []string { return []string{SinkOverlay, SinkLocal, SinkAgent} }

// ValidSink reports whether name is a sink name.
func ValidSink(name string) bool {
	return slices.Contains(Sinks(), name)
}

// Sink plays audio files on one device (ADR-0020 §1).
type Sink interface {
	// Name is the sink name, one of Sinks.
	Name() string
	// Required is the capability the core needs to play on the sink
	// (B40, ADR-0020 §2); empty for none. The check is at the action,
	// not at the configuration.
	Required() capability.Capability
	// Play plays the file, a path under the media directory of the
	// profile, at the volume from 0 to 100 and returns a channel that is
	// closed when the playback is done.
	Play(ctx context.Context, file string, volume float64) (<-chan struct{}, error)
}

// Service resolves the sinks by name and plays with the capability check
// of the sink (B40).
type Service struct {
	sinks map[string]Sink
}

// New returns a service of the sinks; a sink with a duplicate name is
// replaced by the later one.
func New(sinks ...Sink) *Service {
	m := make(map[string]Sink, len(sinks))
	for _, s := range sinks {
		m[s.Name()] = s
	}
	return &Service{sinks: m}
}

// Names returns the sink names of the service in a stable order: the
// known sinks in the order of Sinks, then the unknown ones sorted, for
// the error messages and the tests.
func (s *Service) Names() []string {
	var out, extra []string
	for _, n := range Sinks() {
		if _, ok := s.sinks[n]; ok {
			out = append(out, n)
		}
	}
	for n := range s.sinks {
		if !ValidSink(n) {
			extra = append(extra, n)
		}
	}
	slices.Sort(extra)
	return append(out, extra...)
}

// Sink returns the sink of the name; ok is false if the service does not
// know it.
func (s *Service) Sink(name string) (Sink, bool) {
	sink, ok := s.sinks[name]
	return sink, ok
}

// Play plays file, a path under the media directory, on the sink of the
// name with the rights the core has now (B40): the capability the sink
// requires must be granted, so the server mode without host:audio is a
// clear error for the sink local, not a failure of the configuration
// (ADR-0020 §2).
func (s *Service) Play(ctx context.Context, name, file string, volume float64, rights capability.Source) (<-chan struct{}, error) {
	sink, ok := s.sinks[name]
	if !ok {
		return nil, fmt.Errorf("media: the sink %q is unknown, the sinks are %v", name, s.Names())
	}
	if c := sink.Required(); c != "" && !rights.Current().Has(c) {
		return nil, fmt.Errorf("media: the sink %q needs the capability %s, which the core does not have now", name, c)
	}
	return sink.Play(ctx, file, volume)
}
