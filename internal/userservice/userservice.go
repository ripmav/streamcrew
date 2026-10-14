// SPDX-License-Identifier: MIT

// Package userservice is the user service of roadmap 5.2: it keeps the
// statistics of the users (users-and-roles.md, B9) up to date from the
// events of the core, and it tracks who is active in the chat of each
// platform, for the watch time.
//
// The watch time counts whole minutes: every minute, each user active in
// the chat of a platform whose stream is live gets one minute. A user is
// active from its join or its first message until it leaves (B9).
package userservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
)

// ErrInvalidOption is returned by New for a missing port.
var ErrInvalidOption = errors.New("invalid option")

// ErrAlreadyRunning is returned by a second call of Run.
var ErrAlreadyRunning = errors.New("the user service runs already")

// watchTick is the interval of the watch time (B9, "Watchtime in Minuten").
const watchTick = time.Minute

// Store keeps the statistics and the stream sessions; *store.Store
// implements it.
type Store interface {
	// AddStats adds the deltas to the stored statistics.
	AddStats(ctx context.Context, deltas []user.StatsDelta) error
	// StreamSessions returns the stored sessions.
	StreamSessions(ctx context.Context) ([]stream.Session, error)
}

// Ports are what the service needs. All are required.
type Ports struct {
	Store Store
	Bus   *event.Bus
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

// Active is a user active in the chat of the session of its platform.
type Active struct {
	// User is the user as it came with the last event that made it
	// active, e.g. to name it without a lookup.
	User user.User
	// Last is when the user was last active.
	Last time.Time
}

// Service counts the user statistics and the watch time. It is safe for
// concurrent use; the events arrive in the order the bus delivers them.
type Service struct {
	ports  Ports
	logger *slog.Logger

	// mu guards active and the started state.
	mu      sync.Mutex
	started bool
	// active are the users of the current session by platform, by user ID.
	active map[platform.Name]map[id.ID]*Active
}

// New returns a service with the ports p.
func New(p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Store == nil, p.Bus == nil:
		return nil, fmt.Errorf("new user service: %w: missing port", ErrInvalidOption)
	}
	s := &Service{ports: p, logger: slog.New(slog.DiscardHandler), active: make(map[platform.Name]map[id.ID]*Active)}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("new user service: %w", err)
		}
	}
	return s, nil
}

// Run counts until ctx ends (Code-ADR-0004). It subscribes to the events
// that change the statistics and ticks the watch time every minute. A
// second call returns ErrAlreadyRunning.
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("run user service: %w", ErrAlreadyRunning)
	}
	s.started = true
	s.mu.Unlock()

	sub := s.ports.Bus.Subscribe(ctx,
		event.WithName("users"),
		event.WithTypes(
			eventtype.ChatUserJoin,
			eventtype.ChatUserLeave,
			eventtype.ChatMessage,
			engine.TypeInstanceCompleted,
		),
	)
	tick := time.NewTicker(watchTick)
	defer tick.Stop()
	for {
		select {
		case env, ok := <-sub.C():
			if !ok { // the subscription ended with ctx
				return nil
			}
			s.handle(ctx, env)
		case <-tick.C:
			s.watchTick(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

// Active returns the users active in the chat of the session of p, in the
// order of their IDs.
func (s *Service) Active(p platform.Name) []Active {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Active, 0, len(s.active[p]))
	for _, a := range s.active[p] {
		out = append(out, *a)
	}
	slices.SortFunc(out, func(a, b Active) int {
		return strings.Compare(a.User.ID.String(), b.User.ID.String())
	})
	return out
}

// handle applies one event to the statistics.
func (s *Service) handle(ctx context.Context, env event.Envelope) {
	switch env.Type {
	case event.TypeLag:
		lag, _ := event.Payload[event.Lag](env)
		s.logger.WarnContext(ctx, "the user statistics lagged behind the events", "dropped", lag.Dropped)
	case eventtype.ChatUserJoin:
		s.join(ctx, env)
	case eventtype.ChatUserLeave:
		s.leave(env)
	case eventtype.ChatMessage:
		s.message(ctx, env)
	case engine.TypeInstanceCompleted:
		s.instance(ctx, env)
	default:
		// The subscription filters; the default keeps a new type from
		// being silently counted wrong.
	}
}

// join marks the user active and, while the stream is live, counts the
// session as a watched stream (B9).
func (s *Service) join(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.User == nil {
		return
	}
	s.markActive(payload.Platform, payload.User, env.Time)
	if !s.live(ctx, payload.Platform) {
		return
	}
	if err := s.ports.Store.AddStats(ctx, []user.StatsDelta{{
		UserID:         payload.User.ID,
		StreamsWatched: 1,
		Seen:           env.Time,
	}}); err != nil {
		s.logger.ErrorContext(ctx, "adding the stream to the user statistics failed", "error", err)
	}
}

// live reports whether the stream of p is live now (B9). A store error
// leaves the session uncounted; the next contact tries again.
func (s *Service) live(ctx context.Context, p platform.Name) bool {
	sessions, err := s.ports.Store.StreamSessions(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the stream sessions failed", "error", err)
		return false
	}
	for _, se := range sessions {
		if se.Platform == p {
			return se.State == stream.StateLive
		}
	}
	return false
}

// leave removes the user from the active set.
func (s *Service) leave(env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.User == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.active[payload.Platform]; m != nil {
		delete(m, payload.User.ID)
	}
}

// message marks the user active, counts the message, and counts the other
// active users it mentions with "@login" (B9, "wie oft der Nutzer
// erwähnt wurde").
func (s *Service) message(ctx context.Context, env event.Envelope) {
	payload, ok := event.Payload[eventtype.Payload](env)
	if !ok || payload.User == nil {
		return
	}
	s.markActive(payload.Platform, payload.User, env.Time)

	deltas := []user.StatsDelta{{UserID: payload.User.ID, Messages: 1, Seen: env.Time}}
	if m := payload.Details.Message; m != nil {
		for _, a := range s.Active(payload.Platform) {
			if a.User.ID == payload.User.ID {
				continue
			}
			if mentions(m.Text, a.User, payload.Platform) {
				deltas = append(deltas, user.StatsDelta{UserID: a.User.ID, Mentions: 1})
			}
		}
	}
	if err := s.ports.Store.AddStats(ctx, deltas); err != nil {
		s.logger.ErrorContext(ctx, "adding the message to the user statistics failed", "error", err)
	}
}

// instance counts a completed instance for its triggering user (B9, "Zahl
// ausgeführter Commands").
func (s *Service) instance(ctx context.Context, env event.Envelope) {
	ins, ok := event.Payload[engine.Instance](env)
	if !ok || ins.UserID.IsZero() {
		return
	}
	if err := s.ports.Store.AddStats(ctx, []user.StatsDelta{{UserID: ins.UserID, CommandsRun: 1}}); err != nil {
		s.logger.ErrorContext(ctx, "adding the command to the user statistics failed", "error", err)
	}
}

// watchTick adds one minute of watch time to every active user of a live
// platform (B9, "Watchtime (nur während live)").
func (s *Service) watchTick(ctx context.Context) {
	sessions, err := s.ports.Store.StreamSessions(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the stream sessions failed", "error", err)
		return
	}
	var deltas []user.StatsDelta
	s.mu.Lock()
	for _, se := range sessions {
		if se.State != stream.StateLive {
			continue
		}
		for _, a := range s.active[se.Platform] {
			deltas = append(deltas, user.StatsDelta{UserID: a.User.ID, WatchMinutes: 1})
		}
	}
	s.mu.Unlock()
	if len(deltas) == 0 {
		return
	}
	if err := s.ports.Store.AddStats(ctx, deltas); err != nil {
		s.logger.ErrorContext(ctx, "adding the watch time failed", "error", err)
	}
}

// markActive records u as active on p at t; the event data of the user
// replace the stored one (B4: the names follow the platform).
func (s *Service) markActive(p platform.Name, u *user.User, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.active[p]
	if m == nil {
		m = make(map[id.ID]*Active)
		s.active[p] = m
	}
	if a, ok := m[u.ID]; ok {
		a.User, a.Last = *u, t
		return
	}
	m[u.ID] = &Active{User: *u, Last: t}
}

// mentions reports whether text names u on p: "@login" case-insensitively
// for ASCII letters, not followed by a letter, digit or underscore, so
// "@ada" does not count for the login "ada_l".
func mentions(text string, u user.User, p platform.Name) bool {
	ident, ok := u.Identity(p)
	if !ok {
		return false
	}
	return mentionsLogin(text, ident.Login)
}

// mentionsLogin reports whether text names the login: "@login",
// case-insensitively for ASCII letters, not followed by a letter, digit or
// underscore, so "@ada" does not count for the login "ada_l".
func mentionsLogin(text, login string) bool {
	folded := foldASCII(text)
	needle := foldASCII("@" + login)
	for i := 0; ; {
		j := strings.Index(folded[i:], needle)
		if j < 0 {
			return false
		}
		end := i + j + len(needle)
		if end < len(text) {
			// The boundary check uses the original text; folding is
			// ASCII-only, so the byte offsets match.
			if r, _ := utf8.DecodeRuneInString(text[end:]); isWordPart(r) {
				i = end
				continue
			}
		}
		return true
	}
}

// foldASCII lowercases the ASCII letters of s; other bytes stay as they are.
func foldASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// isWordPart reports whether r continues a login name.
func isWordPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
