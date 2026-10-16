// SPDX-License-Identifier: MIT

// Package prompt is the prompt mechanism (Plan §6.14): prompts replace the
// dialogs of the original. A prompt asks the user for input (information,
// confirmation or a value); it is delivered through the live stream and
// answered with the AnswerPrompt RPC.
package prompt

import (
	"context"
	"sync"
	"uuid"

	"github.com/ripmav/streamcrew/internal/event"
)

// Kinds of prompts.
const (
	KindInformation  = "information"
	KindConfirmation = "confirmation"
	KindInput        = "input"
)

// Type is the event type of a prompt on the bus.
const Type event.Type = "prompt.ask"

// RegisterEvents registers the prompt event types in the catalog.
func RegisterEvents(c *event.Catalog) error {
	return event.Register[Prompt](c, Type)
}

// Prompt is a request for user input.
type Prompt struct {
	// ID names the prompt, used to answer it.
	ID string
	// Kind is "information", "confirmation" or "input".
	Kind string
	// Text is the prompt text.
	Text string
	// Options are the selectable options; empty for information/input.
	Options []string
}

// Answer is the result of answering a prompt.
type Answer struct {
	// Value is the entered or selected value; empty for a confirmation.
	Value string
	// Confirmed is the confirmation result.
	Confirmed bool
}

// Service tracks the pending prompts and delivers them to the bus.
type Service struct {
	mu      sync.Mutex
	pending map[string]chan Answer
	publish func(ctx context.Context, p Prompt)
}

// New returns a prompt service that publishes every prompt with publish.
func New(publish func(ctx context.Context, p Prompt)) *Service {
	return &Service{pending: make(map[string]chan Answer), publish: publish}
}

// Ask creates a prompt, publishes it and returns it with the channel that
// receives its answer.
func (s *Service) Ask(ctx context.Context, kind, text string, options []string) (Prompt, <-chan Answer) {
	p := Prompt{ID: uuid.NewV7().String(), Kind: kind, Text: text, Options: options}
	ch := make(chan Answer, 1)
	s.mu.Lock()
	s.pending[p.ID] = ch
	s.mu.Unlock()
	s.publish(ctx, p)
	return p, ch
}

// Answer delivers the answer to the prompt with the ID; it reports whether
// the prompt was pending.
func (s *Service) Answer(id, value string, confirmed bool) bool {
	s.mu.Lock()
	ch, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	ch <- Answer{Value: value, Confirmed: confirmed}
	return true
}
