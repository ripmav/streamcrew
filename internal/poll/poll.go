// SPDX-License-Identifier: MIT

// Package poll is the active poll or prediction of the profile
// (roadmap 7.5, spec overlays.md, B34): the twitch action tracks the
// polls and predictions it creates, and the poll widget shows the
// active one live. The core event model has no poll events
// (twitch-events.md, B17, B22); the state comes from the Helix status
// checks the action already runs.
package poll

import (
	"context"
	"sync"
)

// Kind says whether the active vote is a poll or a prediction.
type Kind string

// The kinds of the active vote.
const (
	// KindPoll is a poll of Twitch.
	KindPoll Kind = "poll"
	// KindPrediction is a prediction of Twitch.
	KindPrediction Kind = "prediction"
)

// Outcome is one option of the active vote (B34).
type Outcome struct {
	// Name is the name of the option or the outcome.
	Name string `json:"name"`
	// Votes is the number of votes of the option of a poll.
	Votes int64 `json:"votes,omitempty"`
	// Points is the points wagered on the outcome of a prediction.
	Points int64 `json:"points,omitempty"`
	// Color is the color of the outcome of a prediction; empty if
	// Twitch names none.
	Color string `json:"color,omitempty"`
}

// State is the state of the active vote (B34).
type State struct {
	// Kind says whether the vote is a poll or a prediction.
	Kind Kind `json:"kind"`
	// Name is the title of the poll; empty for a prediction, whose
	// outcomes carry the content.
	Name string `json:"name,omitempty"`
	// Outcomes are the options or the outcomes, in order.
	Outcomes []Outcome `json:"outcomes"`
	// Ended reports whether the vote is over (completed, canceled or
	// resolved).
	Ended bool `json:"ended"`
	// Winner is the winning option or outcome; empty while the vote
	// runs.
	Winner string `json:"winner,omitempty"`
}

// Service holds the state of the active vote and tells the
// subscribers (the poll widget) about its changes.
type Service struct {
	mu   sync.Mutex
	st   State
	has  bool
	subs []func(context.Context, State)
}

// New returns the service; without a poll or prediction it has no
// active state.
func New() *Service {
	return &Service{}
}

// Update sets the state of the active vote and tells the subscribers
// when it changed; the subscribers run in order, synchronously with
// the update.
func (s *Service) Update(ctx context.Context, st State) {
	s.mu.Lock()
	changed := !s.has || !s.st.equal(st)
	s.st, s.has = st, true
	subs := make([]func(context.Context, State), len(s.subs))
	copy(subs, s.subs)
	s.mu.Unlock()
	if changed {
		for _, fn := range subs {
			fn(ctx, st)
		}
	}
}

// Active returns the state of the active vote; ok is false without a
// poll or prediction.
func (s *Service) Active() (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st, s.has
}

// OnChange registers a subscriber of the state changes; it is called
// from the goroutine that updates the state.
func (s *Service) OnChange(fn func(context.Context, State)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subs = append(s.subs, fn)
}

// equal reports whether a and b carry the same state.
func (a State) equal(b State) bool {
	if a.Kind != b.Kind || a.Name != b.Name || a.Ended != b.Ended ||
		a.Winner != b.Winner || len(a.Outcomes) != len(b.Outcomes) {
		return false
	}
	for i := range a.Outcomes {
		if a.Outcomes[i] != b.Outcomes[i] {
			return false
		}
	}
	return true
}
