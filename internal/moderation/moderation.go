// SPDX-License-Identifier: MIT

// Package moderation checks incoming chat messages at the moderation stage
// of the chat pipeline (spec moderation.md, B1 to B13): the participation
// rules, the word filters and the strikes with their follow-up commands.
// The muted chat (spec chat-service.md, B21) runs before it.
package moderation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
)

// ErrInvalidOption is returned by New for a port or option without a value.
var ErrInvalidOption = errors.New("invalid option")

// Message is the moderation-relevant view of an incoming chat message.
type Message struct {
	// Platform and Channel are where the message came from; Channel is
	// empty for the home channel.
	Platform platform.Name
	Channel  string
	// Text is the text as written; Emotes the emote codes in it.
	Text   string
	Emotes []string
	// MessageID is the platform's ID of the message; empty if it has none.
	MessageID string
	// User is the resolved author.
	User user.User
}

// Roles returns the author's roles on the platform of the message.
func (m Message) Roles() role.Set {
	ident, ok := m.User.Identity(m.Platform)
	if !ok {
		return role.Set(0)
	}
	return ident.Roles
}

// Decision is what the pipeline does with the message: Allow is false for a
// message that is discarded (no events, no trigger, no history, spec
// moderation.md, B1, B11).
type Decision struct {
	Allow bool
}

// Ports are what the service needs. All are required.
type Ports struct {
	// Settings reads the section "moderation" whenever a message is
	// checked, so a change applies from the next message (B13).
	Settings func(ctx context.Context) (settings.Moderation, error)
}

// Option configures a Service. An option without a valid value makes New
// fail with ErrInvalidOption; to keep a default, leave the option out.
type Option func(*Service) error

// WithLogger sets the logger; without it, the service logs nothing.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) error {
		if l == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		s.logger = l
		return nil
	}
}

// Service checks the messages. It is safe for concurrent use.
type Service struct {
	ports  Ports
	logger *slog.Logger
}

// New returns a service with the ports p. A missing port fails with
// ErrInvalidOption.
func New(p Ports, opts ...Option) (*Service, error) {
	if p.Settings == nil {
		return nil, fmt.Errorf("new moderation: %w: missing settings", ErrInvalidOption)
	}
	s := &Service{ports: p, logger: slog.New(slog.DiscardHandler)}
	var errs []error
	for _, opt := range opts {
		errs = append(errs, opt(s))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new moderation: %w", err)
	}
	return s, nil
}

// Check applies the moderation rules to m (spec moderation.md, B1 to B11).
// The word filters (B4 to B8) run for every message that the muted chat
// did not already discard; the streamer runs through (B2). A message that
// violates a filter is discarded.
func (s *Service) Check(ctx context.Context, m Message) (Decision, error) {
	cfg, err := s.ports.Settings(ctx)
	if err != nil {
		return Decision{}, err
	}
	roles := m.Roles()
	// The streamer runs through the filters (B2).
	if !roles.Has(role.Streamer) {
		for _, f := range cfg.Filters {
			if len(f.Exempt) > 0 && roles.HasAny(f.Exempt...) {
				continue // the author is exempt from the filter (B22)
			}
			if violates(f, m.Text, m.Emotes) {
				s.logger.DebugContext(ctx, "message violates the filter", "filter", f.Kind)
				return Decision{Allow: false}, nil
			}
		}
	}
	return Decision{Allow: true}, nil
}
