// SPDX-License-Identifier: MIT

// Package timer runs the timer commands and the groups with their own
// timer interval (roadmap 5.4, spec commands.md, B21, B31). It is a
// runnable (Code-ADR-0004): the app runs it as long as the core runs.
//
// The global schedule (settings section "timers") fires the timer commands
// that have no group with its own interval, once per connected platform in
// the home channel, subject to "live only" and the minimum of chat messages.
// A group with a timer interval fires its timer commands on its own
// schedule in its target channel, independent of the global interval and
// minimum (B31); "live only" and the order still apply.
package timer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// DefaultPoll is how often the service checks what is due; the intervals of
// a timer and a group are minute-scale, so a second keeps the timing close
// and the idle work small.
const DefaultPoll = time.Second

// ErrInvalidOption is returned by New for a port or option without a value.
var ErrInvalidOption = errors.New("invalid option")

// Commands lists the commands and the groups of the profile.
// *command.Service implements it.
type Commands interface {
	// Commands returns all stored commands.
	Commands(ctx context.Context) ([]command.Command, error)
	// Groups returns all stored groups.
	Groups(ctx context.Context) ([]command.Group, error)
}

// Engine starts the commands the timer runs. *engine.Engine implements it.
type Engine interface {
	Start(ctx context.Context, cmd command.Command, p engine.Params) (id.ID, error)
}

// Platforms names the platforms that are connected now.
type Platforms interface {
	Connected() []platform.Name
}

// Live reports whether the stream of a platform is live now.
type Live interface {
	IsLive(p platform.Name) bool
}

// Messages counts the recent chat messages of a platform.
type Messages interface {
	MessagesSince(p platform.Name, since time.Time) int
}

// Ports are what the service needs. All are required.
type Ports struct {
	Commands  Commands
	Engine    Engine
	Platforms Platforms
	Live      Live
	Messages  Messages
	// Settings reads the section "timers" whenever a tick needs it, so a
	// change applies from the next tick.
	Settings func(ctx context.Context) (settings.Timers, error)
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

// WithPoll sets how often the service checks what is due; it must be
// positive. Without it, the service uses DefaultPoll.
func WithPoll(d time.Duration) Option {
	return func(s *Service) error {
		if d <= 0 {
			return fmt.Errorf("%w: poll %s is not positive", ErrInvalidOption, d)
		}
		s.poll = d
		return nil
	}
}

// Service runs the timer commands and the timer groups. It is safe for
// concurrent use; only Run touches the schedule.
type Service struct {
	ports   Ports
	logger  *slog.Logger
	poll    time.Duration
	now     func() time.Time
	started bool
}

// New returns a service with the ports p. A missing port fails with
// ErrInvalidOption.
func New(p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Commands == nil, p.Engine == nil, p.Platforms == nil, p.Live == nil, p.Messages == nil, p.Settings == nil:
		return nil, fmt.Errorf("new timer: %w: missing port", ErrInvalidOption)
	}
	s := &Service{ports: p, logger: slog.New(slog.DiscardHandler), poll: DefaultPoll, now: time.Now}
	var errs []error
	for _, opt := range opts {
		errs = append(errs, opt(s))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new timer: %w", err)
	}
	return s, nil
}

// Run keeps the service until ctx ends (Code-ADR-0004). The first global
// tick and the first tick of every group are due after their interval, not
// at once. A second call returns an error.
func (s *Service) Run(ctx context.Context) error {
	if s.started {
		return errors.New("run timer: already running")
	}
	s.started = true
	tick := time.NewTicker(s.poll)
	defer tick.Stop()
	lastGlobal := s.now()
	lastGroup := make(map[id.ID]time.Time)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.tick(ctx, &lastGlobal, lastGroup)
		}
	}
}

// tick fires what is due now (roadmap 5.4, B21, B31).
func (s *Service) tick(ctx context.Context, lastGlobal *time.Time, lastGroup map[id.ID]time.Time) {
	now := s.now()
	cfg, err := s.ports.Settings(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the timer settings failed", "error", err)
		return
	}
	groups, err := s.ports.Commands.Groups(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the groups failed", "error", err)
		return
	}
	all, err := s.ports.Commands.Commands(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the commands failed", "error", err)
		return
	}
	// The groups with their own timer (B31), by group ID.
	groupTimer := make(map[id.ID]command.Group, len(groups))
	for _, g := range groups {
		if g.TimerInterval > 0 {
			groupTimer[g.ID] = g
		}
	}
	platforms := s.ports.Platforms.Connected()

	// The global timer (B21): the interval and the minimum of the settings,
	// once per connected platform in the home channel. The window of the
	// minimum of chat messages is the interval that fired the tick.
	if iv := cfg.Interval.Std(); iv > 0 && now.Sub(*lastGlobal) >= iv {
		var cmds []command.Command
		for _, c := range all {
			if !c.Enabled || c.Kind != command.KindTimer {
				continue
			}
			if _, g := groupTimer[c.GroupID]; g {
				continue // its group runs on its own schedule (B31)
			}
			cmds = append(cmds, c)
		}
		if s.fire(ctx, cfg, now, platforms, "", cmds, iv) > 0 {
			*lastGlobal = now
		}
	}

	// The timer groups (B31): their own interval and target channel,
	// independent of the global interval and minimum.
	for _, g := range groups {
		if g.TimerInterval <= 0 {
			continue
		}
		first, ok := lastGroup[g.ID]
		if !ok {
			// The group appears now; its first tick is after its interval.
			lastGroup[g.ID] = now
			continue
		}
		if now.Sub(first) < g.TimerInterval {
			continue
		}
		var cmds []command.Command
		for _, c := range all {
			if c.Enabled && c.Kind == command.KindTimer && c.GroupID == g.ID {
				cmds = append(cmds, c)
			}
		}
		// window 0: a group timer does not check the minimum of messages
		// (B31).
		if s.fire(ctx, cfg, now, platforms, g.TimerChannel, cmds, 0) > 0 {
			lastGroup[g.ID] = now
		}
	}
}

// fire runs cmds once per platform that passes the checks, in the channel
// ch (empty: the home channel), and returns how many commands it started.
// window is the window the minimum of chat messages counts in (0 for no
// check). It logs a failure to start one command and keeps going.
func (s *Service) fire(ctx context.Context, cfg settings.Timers, now time.Time, platforms []platform.Name, ch string, cmds []command.Command, window time.Duration) int {
	if len(cmds) == 0 {
		return 0
	}
	ran := 0
	for _, p := range platforms {
		if cfg.LiveOnly && !s.ports.Live.IsLive(p) {
			continue
		}
		if window > 0 && cfg.MinChatMessages > 0 && s.ports.Messages.MessagesSince(p, now.Add(-window)) < cfg.MinChatMessages {
			continue
		}
		for _, c := range ordered(cmds, cfg.Order) {
			if _, err := s.ports.Engine.Start(ctx, c, engine.Params{Platform: p, Channel: ch}); err != nil {
				s.logger.ErrorContext(ctx, "starting the timer command failed", "command", c.Name, "platform", p, "error", err)
				continue
			}
			ran++
		}
	}
	return ran
}

// ordered returns the commands in the order o: fixed in their stored order
// (by ID), random in a new random order.
func ordered(cmds []command.Command, o settings.TimerOrder) []command.Command {
	out := make([]command.Command, len(cmds))
	copy(out, cmds)
	if o == settings.OrderRandom {
		//nolint:gosec // not a cryptographic source, but the random order of the timer
		rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		return out
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() < out[j].ID.String() })
	return out
}
