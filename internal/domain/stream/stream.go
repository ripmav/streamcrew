// SPDX-License-Identifier: Apache-2.0

// Package stream is the model of the stream sessions (spec events.md, B8,
// B11, B21): each platform has one, from a stream start to the next, so
// that events that fire once per session (B3) and greetings know when it
// begins, and a short break does not end it.
package stream

import (
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// State says where the stream of a session is.
type State string

// States of a session.
const (
	// StateLive means the stream is live.
	StateLive State = "live"
	// StateGrace means the stream went offline and the grace period runs;
	// the session goes on if the stream is back before it ends (B8).
	StateGrace State = "grace"
	// StateOffline means the stream is offline and the session ended, or
	// the stream never started; going live starts a new session.
	StateOffline State = "offline"
)

// Valid reports whether s is a known state.
func (s State) Valid() bool {
	switch s {
	case StateLive, StateGrace, StateOffline:
		return true
	default:
		return false
	}
}

// Session is the stream session of a platform.
type Session struct {
	Platform platform.Name
	// StartedAt is when the session started; zero for the session before
	// the first stream start (B11).
	StartedAt time.Time
	State     State
	// Since is when the state began: the going live, the going offline
	// that started the grace period, or the end of the session; zero for
	// the session before the first stream start.
	Since time.Time
	// SeenLive is the last time the core knew the stream live, e.g. when
	// it stopped (B27); zero if it never did.
	SeenLive time.Time
}

// Initial returns the session of p before its first stream start.
func Initial(p platform.Name) Session {
	return Session{Platform: p, State: StateOffline}
}

// Validate checks a session before it is stored.
func (s Session) Validate() error {
	if err := s.Platform.Validate(); err != nil {
		return fmt.Errorf("stream session: %w", err)
	}
	if !s.State.Valid() {
		return fmt.Errorf("stream session of %s: unknown state %q", s.Platform, s.State)
	}
	if s.State != StateOffline && (s.StartedAt.IsZero() || s.Since.IsZero()) {
		return fmt.Errorf("stream session of %s: state %s without its start", s.Platform, s.State)
	}
	return nil
}
