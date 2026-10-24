// SPDX-License-Identifier: MIT

// Package userservice is the user service of roadmap 5.2: it keeps the
// statistics of the users (users-and-roles.md, B9) up to date from the
// events of the core, it tracks who is active in the chat of each
// platform, for the watch time, it evaluates the regular role (B26) from
// the watch time, and it keeps the follow and subscription data of the
// identities fresh (B10), fetched from the platform on contact when the
// cache is missing or stale.
//
// The watch time counts whole minutes: every minute, each user active in
// the chat of a platform whose stream is live gets one minute. A user is
// active from its join or its first message until it leaves (B9). The
// regular role is set from the whole hours of watch time and the
// threshold of the settings, on the tick (B26).
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

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/dryrun"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
)

// ErrInvalidOption is returned by New for a missing port.
var ErrInvalidOption = errors.New("invalid option")

// ErrAlreadyRunning is returned by a second call of Run.
var ErrAlreadyRunning = errors.New("the user service runs already")

// watchTickDefault is the interval of the watch time (B9, "Watchtime in
// Minuten").
const watchTickDefault = time.Minute

// activeWindow is how long a silent user still counts as watching: its
// last activity (join or chat message) must be within it (code review
// 2026-10-24, U1). The Twitch platform names no leave, so without the
// window a one-message chatter would count every minute of the stream.
const activeWindow = 5 * time.Minute

// dataTTL is how long the cached platform data of an identity stays fresh
// (users-and-roles.md, B10); older data is refetched on the next contact.
const dataTTL = time.Hour

// Store keeps the statistics and the stream sessions and the users the
// core knows; *store.Store implements it.
type Store interface {
	// AddStats adds the deltas to the stored statistics.
	AddStats(ctx context.Context, deltas []user.StatsDelta) error
	// StreamSessions returns the stored sessions.
	StreamSessions(ctx context.Context) ([]stream.Session, error)
	// ListUsersRegular returns the users and their watch minutes for the
	// sweep of the regular role (users-and-roles.md, B26).
	ListUsersRegular(ctx context.Context) ([]user.RegularSweep, error)
	// TopUsers returns the users ranked by stat, from the highest value
	// down (roadmap 5.2.5).
	TopUsers(ctx context.Context, stat user.Stat, n int) ([]user.Ranked, error)
	// UpdateUser changes the user, e.g. the regular role (B26) or the
	// strikes (actions.md, B84).
	UpdateUser(ctx context.Context, userID id.ID, fn func(*user.User) error) (user.User, error)
	// SetPlatformData replaces the cached platform data of an identity
	// (users-and-roles.md, B10).
	SetPlatformData(ctx context.Context, p platform.Name, platformUserID string, data user.PlatformData) error
	// UserByLogin finds the user with the login name on p, regardless of
	// case for ASCII letters; ok is false if there is none.
	UserByLogin(ctx context.Context, p platform.Name, login string) (u user.User, ok bool, err error)
	// UserByPlatformID finds the user of the account on p; ok is false if
	// there is none.
	UserByPlatformID(ctx context.Context, p platform.Name, platformUserID string) (u user.User, ok bool, err error)
	// UpsertIdentity stores an account like a newly seen one, or updates
	// its names if the core knows it (users-and-roles.md, B4); created
	// reports a new user.
	UpsertIdentity(ctx context.Context, ident user.Identity) (u user.User, created bool, err error)
	// ResetStrikes sets the strikes of every user to 0 (actions.md, B84).
	ResetStrikes(ctx context.Context) error
}

// Ports are what the service needs. All are required.
type Ports struct {
	Store Store
	Bus   *event.Bus
	// Settings reads the settings section "users" on the watch tick, so a
	// changed regular threshold applies from the next tick (B26).
	Settings func(ctx context.Context) (settings.Users, error)
	// Platforms returns the platform adapters of the profile, for the
	// refresh of the follow and subscription data (B10), the lookup of the
	// channel's accounts and of users over the platform (B91), and the
	// exclusion of the rankings (5.2.5); nil skips them.
	Platforms Platforms
}

// Platforms is the platform set of the profile; the app's platformSet
// implements it.
type Platforms interface {
	// Platform returns the adapter by name; ok is false if the profile
	// has no platform by the name.
	Platform(name platform.Name) (connector.Platform, bool)
	// All returns the adapters of the profile, in the order of the set.
	All() []connector.Platform
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

// WithWatchTick sets the interval of the watch time; it must be positive.
// The default is a minute (B9); a shorter interval is for tests.
func WithWatchTick(d time.Duration) Option {
	return func(s *Service) error {
		if d <= 0 {
			return fmt.Errorf("%w: watch tick %s", ErrInvalidOption, d)
		}
		s.watchTickDur = d
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
	// watchTickDur is the interval of the watch time; zero for the default
	// (watchTickDefault), set by WithWatchTick (tests, shorter ticks).
	watchTickDur time.Duration

	// mu guards active, the started state, lastData and lookup.
	mu      sync.Mutex
	started bool
	// active are the users of the current session by platform, by user ID.
	active map[platform.Name]map[id.ID]*Active
	// lastData is when the service last fetched the platform data (B10),
	// by platform, by platform user ID.
	lastData map[platform.Name]map[string]time.Time
	// lookup limits the lookups over a platform (actions.md, B92).
	lookup lookupLimiter
}

// New returns a service with the ports p.
func New(p Ports, opts ...Option) (*Service, error) {
	switch {
	case p.Store == nil, p.Bus == nil, p.Settings == nil:
		return nil, fmt.Errorf("new user service: %w: missing port", ErrInvalidOption)
	}
	s := &Service{ports: p, logger: slog.New(slog.DiscardHandler), active: make(map[platform.Name]map[id.ID]*Active), lastData: make(map[platform.Name]map[string]time.Time)}
	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("new user service: %w", err)
		}
	}
	return s, nil
}

// watchTickInterval returns the configured watch tick, or the default.
func (s *Service) watchTickInterval() time.Duration {
	if s.watchTickDur > 0 {
		return s.watchTickDur
	}
	return watchTickDefault
}

// Run counts until ctx ends (Code-ADR-0004). It subscribes to the events
// that change the statistics and ticks the watch time on the watch interval.
// A second call returns ErrAlreadyRunning.
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
	tick := time.NewTicker(s.watchTickInterval())
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

// Rankings returns up to n users ranked by stat, from the highest value
// down (roadmap 5.2.5). Excluded users are never ranked (users-and-roles.md,
// B7, in the store); the channel's accounts (streamer, bot) are excluded
// here, so the result has at most n entries and is refilled with the next
// users.
func (s *Service) Rankings(ctx context.Context, stat user.Stat, n int) ([]user.Ranked, error) {
	if !stat.Valid() {
		return nil, fmt.Errorf("rankings: %w: %s", user.ErrInvalid, stat)
	}
	if n < 1 {
		return nil, fmt.Errorf("rankings: %w: n = %d", user.ErrInvalid, n)
	}
	var exclude map[id.ID]bool
	if s.ports.Platforms != nil {
		ids, err := s.accountUserIDs(ctx)
		if err != nil {
			return nil, fmt.Errorf("rankings: %w", err)
		}
		exclude = make(map[id.ID]bool, len(ids))
		for _, account := range ids {
			exclude[account] = true
		}
	}
	rows, err := s.ports.Store.TopUsers(ctx, stat, n+len(exclude))
	if err != nil {
		return nil, fmt.Errorf("rankings: %w", err)
	}
	var out []user.Ranked
	for _, r := range rows {
		if exclude[r.User.ID] {
			continue
		}
		out = append(out, r)
		if len(out) == n {
			break
		}
	}
	return out, nil
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
	s.refreshData(ctx, payload.Platform, payload.User)
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
	s.refreshData(ctx, payload.Platform, payload.User)

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
// platform (B9, "Watchtime (nur während live)"), and evaluates the
// regular role of all users (users-and-roles.md, B26). An active user is
// silent for at most activeWindow, and the active set ends with the
// stream session (code review 2026-10-24, U1).
func (s *Service) watchTick(ctx context.Context) {
	sessions, err := s.ports.Store.StreamSessions(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the stream sessions failed", "error", err)
		return
	}
	now := time.Now().UTC()
	var deltas []user.StatsDelta
	s.mu.Lock()
	for _, se := range sessions {
		switch {
		case se.State == stream.StateOffline:
			// The session ended: the active users of it are gone.
			delete(s.active, se.Platform)
			continue
		case se.State != stream.StateLive:
			// Grace: the session goes on, the minute does not count.
			continue
		}
		for uID, a := range s.active[se.Platform] {
			if now.Sub(a.Last) > activeWindow {
				delete(s.active[se.Platform], uID)
				continue
			}
			deltas = append(deltas, user.StatsDelta{UserID: uID, WatchMinutes: 1})
		}
	}
	s.mu.Unlock()
	if len(deltas) > 0 {
		if err := s.ports.Store.AddStats(ctx, deltas); err != nil {
			s.logger.ErrorContext(ctx, "adding the watch time failed", "error", err)
			return
		}
	}
	s.evaluateRegular(ctx)
}

// evaluateRegular sets the regular role of every user from its whole
// hours of watch time and the threshold of the settings
// (users-and-roles.md, B26). The watch time only changes in the tick and
// a changed threshold applies from the next one, so the role follows the
// state within at most one minute.
func (s *Service) evaluateRegular(ctx context.Context) {
	cfg, err := s.ports.Settings(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the users settings failed", "error", err)
		return
	}
	users, err := s.ports.Store.ListUsersRegular(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "reading the users for the regular role failed", "error", err)
		return
	}
	for _, u := range users {
		regular := cfg.RegularHours > 0 && u.WatchMinutes >= int64(cfg.RegularHours)*60
		if regular == u.Regular {
			continue
		}
		if _, err := s.ports.Store.UpdateUser(ctx, u.ID, func(u *user.User) error {
			u.Regular = regular
			return nil
		}); err != nil {
			s.logger.ErrorContext(ctx, "setting the regular role failed", "error", err)
		}
	}
}

// refreshData fetches the follow and subscription data of the user's
// identity on p when it is not fetched within dataTTL (users-and-roles.md,
// B10), and stores it. It runs in the service goroutine, so the event
// path never blocks on the platform; a platform without the capability or
// an error leave the cache as it is. The event payload of the user does
// not carry the cached data, so the memo lastData decides the TTL.
func (s *Service) refreshData(ctx context.Context, p platform.Name, u *user.User) {
	if s.ports.Platforms == nil {
		return
	}
	adapter, ok := s.ports.Platforms.Platform(p)
	if !ok {
		return
	}
	f, ok := adapter.(connector.PlatformData)
	if !ok {
		return
	}
	if inner, ok := dryrun.AsInner(adapter); ok {
		adapter = inner
		f, ok = adapter.(connector.PlatformData)
		if !ok {
			return
		}
	}
	ident, ok := u.Identity(p)
	if !ok {
		return
	}
	s.mu.Lock()
	last, fresh := s.lastData[p][ident.PlatformUserID]
	s.mu.Unlock()
	if fresh && time.Since(last) < dataTTL {
		return
	}
	data, err := f.UserPlatformData(ctx, ident.PlatformUserID)
	if err != nil {
		s.logger.DebugContext(ctx, "the platform data is not available", "platform", p, "error", err)
		return
	}
	data.UpdatedAt = time.Now()
	if err := s.ports.Store.SetPlatformData(ctx, p, ident.PlatformUserID, data); err != nil {
		s.logger.ErrorContext(ctx, "storing the platform data failed", "error", err)
		return
	}
	s.mu.Lock()
	if m := s.lastData[p]; m != nil {
		m[ident.PlatformUserID] = data.UpdatedAt
	} else {
		s.lastData[p] = map[string]time.Time{ident.PlatformUserID: data.UpdatedAt}
	}
	s.mu.Unlock()
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
