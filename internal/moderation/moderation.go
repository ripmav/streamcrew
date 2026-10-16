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
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
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

// Decision is what the pipeline does with the message and what the
// moderation did: Allow is false for a message that is discarded (no
// events, no trigger, no history, spec moderation.md, B1, B11); Filter is
// the violated filter, Strikes the author's strikes after the raise and
// Command the follow-up command that ran (the zero values if there is
// none).
type Decision struct {
	Allow   bool
	Filter  settings.Filter
	Strikes int64
	Command string
}

// Ports are what the service needs. All are required.
type Ports struct {
	// Settings reads the section "moderation" whenever a message is
	// checked, so a change applies from the next message (B13).
	Settings func(ctx context.Context) (settings.Moderation, error)
	// Strikes changes a user in one transaction (B9); the user service
	// implements it.
	Strikes Strikes
	// Commands finds the follow-up commands by name (B10).
	Commands Commands
	// Engine runs the follow-up commands without waiting for the decision
	// (B10, B11).
	Engine Engine
	// Now is the clock for the participation rules; it defaults to
	// time.Now. The tests set a fixed time.
	Now func() time.Time
}

// Strikes changes the strikes of a user in one transaction; the user
// service implements it (B9, users-and-roles.md, B9).
type Strikes interface {
	// UpdateUser changes the user with the ID in one transaction: fn gets
	// the stored user and changes it; an error from fn discards the
	// changes.
	UpdateUser(ctx context.Context, userID id.ID, fn func(*user.User) error) (user.User, error)
}

// Commands finds the follow-up commands (B10).
type Commands interface {
	// CommandByName returns the command with the name, case-insensitively;
	// ok is false if there is none.
	CommandByName(ctx context.Context, name string) (cmd command.Command, ok bool, err error)
}

// Engine runs the follow-up commands (B10, B11).
type Engine interface {
	// Submit triggers a command without waiting for the decision
	// (command-engine.md, B16).
	Submit(ctx context.Context, req engine.Request, done func(engine.Result, error)) error
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
	switch {
	case p.Settings == nil:
		return nil, fmt.Errorf("new moderation: %w: missing settings", ErrInvalidOption)
	case p.Strikes == nil:
		return nil, fmt.Errorf("new moderation: %w: missing strikes", ErrInvalidOption)
	case p.Commands == nil:
		return nil, fmt.Errorf("new moderation: %w: missing commands", ErrInvalidOption)
	case p.Engine == nil:
		return nil, fmt.Errorf("new moderation: %w: missing engine", ErrInvalidOption)
	}
	if p.Now == nil {
		p.Now = time.Now
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
// violates a filter is a violation: it raises the author's strikes (B9) and
// runs the follow-up command of the reached level (B10), and it is
// discarded.
func (s *Service) Check(ctx context.Context, m Message) (Decision, error) {
	cfg, err := s.ports.Settings(ctx)
	if err != nil {
		return Decision{}, err
	}
	roles := m.Roles()
	// The streamer runs through the filters (B2).
	if !roles.Has(role.Streamer) {
		// The participation rules run before the filters (B1, B3): a
		// failure discards the message without a strike or a follow-up
		// command.
		if !s.participation(m, cfg) {
			s.logger.DebugContext(ctx, "message discarded by the participation rules", "platform", m.Platform)
			return Decision{Allow: false}, nil
		}
		for _, f := range cfg.Filters {
			if len(f.Exempt) > 0 && roles.HasAny(f.Exempt...) {
				continue // the author is exempt from the filter (B22)
			}
			if violates(f, m.Text, m.Emotes) {
				return s.violate(ctx, cfg, m, f), nil
			}
		}
	}
	return Decision{Allow: true}, nil
}

// participation reports whether the author meets all the set participation
// rules (B3): the minimum role, the minimum watch time and the minimum
// follow time. A rule that is not set does not apply; without any rule,
// everyone may chat (B24).
func (s *Service) participation(m Message, cfg settings.Moderation) bool {
	ident, ok := m.User.Identity(m.Platform)
	if !ok {
		return false
	}
	// The minimum role (B3).
	if cfg.MinRole != "" && !ident.Roles.Meets(cfg.MinRole) {
		return false
	}
	// The minimum watch time, in whole minutes (B3).
	if cfg.MinWatchMinutes > 0 && m.User.Stats.WatchMinutes < int64(cfg.MinWatchMinutes) {
		return false
	}
	// The minimum follow time, in whole days since the follow (B3);
	// unknown is not met.
	if cfg.MinFollowDays > 0 {
		followed := ident.Data.FollowedAt
		if followed.IsZero() || wholeDays(followed, s.ports.Now()) < int64(cfg.MinFollowDays) {
			return false
		}
	}
	return true
}

// wholeDays is the whole days between t and now; 0 if now is before t.
func wholeDays(t, now time.Time) int64 {
	if now.Before(t) {
		return 0
	}
	return int64(now.Sub(t).Hours() / 24)
}

// violate handles a violated filter f (B9 to B11, B23, B25): it raises the
// author's strikes by one and, for the reached level, runs the follow-up
// command with the parameters of the message.
func (s *Service) violate(ctx context.Context, cfg settings.Moderation, m Message, f settings.Filter) Decision {
	d := Decision{Allow: false, Filter: f}
	u, err := s.ports.Strikes.UpdateUser(ctx, m.User.ID, func(u *user.User) error {
		u.Stats.Strikes++
		return nil
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "raising the strikes failed", "user", m.User.ID, "error", err)
		// The violation still counts: the message is discarded (B11).
		return d
	}
	d.Strikes = u.Stats.Strikes
	lvl, ok := strikeLevel(cfg.StrikeLevels, u.Stats.Strikes)
	if !ok || lvl.Command == "" {
		return d // only the counter (B25)
	}
	cmd, ok, err := s.ports.Commands.CommandByName(ctx, lvl.Command)
	if err != nil {
		s.logger.ErrorContext(ctx, "finding the follow-up command failed", "command", lvl.Command, "error", err)
		return d
	}
	if !ok {
		s.logger.WarnContext(ctx, "the follow-up command is not found", "command", lvl.Command)
		return d
	}
	d.Command = lvl.Command
	s.submit(ctx, cmd, m)
	return d
}

// strikeLevel resolves the follow-up level for the strike count n (B10,
// B23): the exact level, if set; above the highest set level, the highest;
// otherwise none (B25).
func strikeLevel(levels []settings.StrikeLevel, n int64) (settings.StrikeLevel, bool) {
	var exact, highest settings.StrikeLevel
	hasExact, hasHighest := false, false
	for _, l := range levels {
		if int64(l.Level) == n {
			exact, hasExact = l, true
		}
		if !hasHighest || l.Level > highest.Level {
			highest, hasHighest = l, true
		}
	}
	if hasExact {
		return exact, true
	}
	if hasHighest && n > int64(highest.Level) {
		return highest, true
	}
	return settings.StrikeLevel{}, false
}

// submit runs the follow-up command c for the message m without waiting for
// the decision; the outcome is logged (B10, B11, B26).
func (s *Service) submit(ctx context.Context, c command.Command, m Message) {
	req := engine.Request{
		Command: c,
		Source:  engine.SourceChat,
		Params:  engine.Params{Platform: m.Platform, Channel: m.Channel, User: &m.User, Message: m.Text, MessageID: m.MessageID, Emotes: m.Emotes},
	}
	err := s.ports.Engine.Submit(ctx, req, func(r engine.Result, err error) {
		if err != nil {
			s.logger.WarnContext(ctx, "the follow-up command failed", "command", c.Name, "error", err)
			return
		}
		s.logger.DebugContext(ctx, "the follow-up command ran", "command", c.Name, "outcome", r.Outcome)
	})
	if err != nil {
		s.logger.WarnContext(ctx, "submitting the follow-up command failed", "command", c.Name, "error", err)
	}
}
