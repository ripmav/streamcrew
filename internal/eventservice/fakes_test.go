// SPDX-License-Identifier: MIT

package eventservice_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
	"github.com/ripmav/streamcrew/internal/moderation"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// The store implements the port.
var _ eventservice.Store = (*store.Store)(nil)

// memStore keeps users, sessions and the events that fire once in memory,
// so that it can live across services like a profile across restarts.
type memStore struct {
	mu            sync.Mutex
	users         []user.User
	sessions      map[platform.Name]stream.Session
	sessionEvents map[string]bool
	userEvents    map[string]bool
	feed          []feed.Event
	sessionStats  map[string]feed.SessionStats
}

func newMemStore() *memStore {
	return &memStore{
		sessions:      make(map[platform.Name]stream.Session),
		sessionEvents: make(map[string]bool),
		userEvents:    make(map[string]bool),
		sessionStats:  make(map[string]feed.SessionStats),
	}
}

func (m *memStore) UpsertIdentity(_ context.Context, ident user.Identity) (user.User, bool, error) {
	if err := ident.Validate(); err != nil {
		return user.User{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, u := range m.users {
		for j, have := range u.Identities {
			if have.Platform == ident.Platform && have.PlatformUserID == ident.PlatformUserID {
				have.Login, have.DisplayName = ident.Login, ident.DisplayName
				m.users[i].Identities[j] = have
				return clone(m.users[i]), false, nil
			}
		}
	}
	u := user.User{ID: id.New(), Identities: []user.Identity{ident}}
	m.users = append(m.users, u)
	return clone(u), true, nil
}

func (m *memStore) SetRoles(_ context.Context, p platform.Name, platformUserID string, roles role.Set) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, u := range m.users {
		for j, have := range u.Identities {
			if have.Platform == p && have.PlatformUserID == platformUserID {
				m.users[i].Identities[j].Roles = roles
				return nil
			}
		}
	}
	return store.ErrNotFound
}

func (m *memStore) StreamSessions(context.Context) ([]stream.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []stream.Session
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) PutStreamSession(_ context.Context, s stream.Session) error {
	if err := s.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.Platform] = s
	return nil
}

func (m *memStore) StartStreamSession(ctx context.Context, s stream.Session) error {
	m.mu.Lock()
	for k := range m.sessionEvents {
		if len(k) > len(s.Platform) && k[:len(s.Platform)+1] == string(s.Platform)+"/" {
			delete(m.sessionEvents, k)
		}
	}
	m.mu.Unlock()
	return m.PutStreamSession(ctx, s)
}

func (m *memStore) FirstInSession(_ context.Context, p platform.Name, t event.Type, userID id.ID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fmt.Sprintf("%s/%s/%s", p, t, userID)
	first := !m.sessionEvents[k]
	m.sessionEvents[k] = true
	return first, nil
}

func (m *memStore) FirstForUser(_ context.Context, userID id.ID, t event.Type) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fmt.Sprintf("%s/%s", userID, t)
	first := !m.userEvents[k]
	m.userEvents[k] = true
	return first, nil
}

// RecordFeedEvent appends ev to the in-memory feed and, if sessionStarted
// is set, adds it to that session's statistics (roadmap 5.3).
func (m *memStore) RecordFeedEvent(_ context.Context, ev feed.Event, sessionStarted time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.feed = append(m.feed, ev)
	if sessionStarted.IsZero() {
		return nil
	}
	k := fmt.Sprintf("%s/%d", ev.Platform, sessionStarted.UnixMilli())
	st := m.sessionStats[k]
	st.Platform = ev.Platform
	st.StartedAt = sessionStarted
	d := ev.StatsDelta()
	st.Followers += d.Followers
	st.Subscribers += d.Subscribers
	st.Resubscribers += d.Resubscribers
	st.Gifts += d.Gifts
	st.Raids += d.Raids
	st.Cheers += d.Cheers
	st.Donations += d.Donations
	st.BitsTotal += d.BitsTotal
	st.DonationCents += d.DonationCents
	m.sessionStats[k] = st
	return nil
}

// mockSession returns the stored session of the mock platform.
func (m *memStore) mockSession() stream.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[platform.Mock]
}

// setEntrance gives the user of the account the entrance command.
func (m *memStore) setEntrance(t *testing.T, ident user.Identity, commandID id.ID) {
	t.Helper()
	u, _, err := m.UpsertIdentity(t.Context(), ident)
	require.NoError(t, err)
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.users {
		if m.users[i].ID == u.ID {
			m.users[i].EntranceCommand = commandID
		}
	}
}

func clone(u user.User) user.User {
	u.Identities = slices.Clone(u.Identities)
	return u
}

// commands finds commands in a fixed list.
type commands struct {
	list []command.Command
}

func (c commands) Recognize(_ context.Context, message string) (command.Recognition, error) {
	return command.NewTriggerIndex(c.list).Recognize(message), nil
}

func (c commands) EventCommand(_ context.Context, t event.Type) (command.Command, bool, error) {
	for _, cmd := range c.list {
		if cmd.Kind == command.KindEvent && cmd.Event == t {
			return cmd, true, nil
		}
	}
	return command.Command{}, false, nil
}

func (c commands) CommandByName(_ context.Context, name string) (command.Command, bool, error) {
	for _, cmd := range c.list {
		if strings.EqualFold(cmd.Name, name) {
			return cmd, true, nil
		}
	}
	return command.Command{}, false, nil
}

func (c commands) Command(_ context.Context, commandID id.ID) (command.Command, error) {
	for _, cmd := range c.list {
		if cmd.ID == commandID {
			return cmd, nil
		}
	}
	return command.Command{}, store.ErrNotFound
}

// fakeEngine records what it is asked to run.
type fakeEngine struct {
	mu       sync.Mutex
	requests []engine.Request
	canceled int
}

func (e *fakeEngine) Submit(_ context.Context, req engine.Request, done func(engine.Result, error)) error {
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.mu.Unlock()
	done(engine.Result{Outcome: engine.OutcomeQueued}, nil)
	return nil
}

func (e *fakeEngine) CancelEntrance(context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.canceled++
}

// runs returns the names of the commands asked to run, with "+entrance" for
// a user's entrance command, and forgets them.
func (e *fakeEngine) runs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.requests))
	for i, r := range e.requests {
		out[i] = r.Command.Name
		if r.Entrance {
			out[i] += "+entrance"
		}
	}
	e.requests = nil
	return out
}

// take returns the requests and forgets them.
func (e *fakeEngine) take() []engine.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := e.requests
	e.requests = nil
	return out
}

func (e *fakeEngine) cancels() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.canceled
}

// publisher records the published events; the catalog checks them.
type publisher struct {
	mu      sync.Mutex
	catalog *event.Catalog
	events  []event.Envelope
}

func (p *publisher) Publish(_ context.Context, e event.Envelope) error {
	if err := p.catalog.Check(e); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, e)
	return nil
}

// types returns the types of the published events and forgets them.
func (p *publisher) types() []event.Type {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]event.Type, len(p.events))
	for i, e := range p.events {
		out[i] = e.Type
	}
	p.events = nil
	return out
}

// eventSettings are changeable settings of the section "events".
type eventSettings struct {
	mu  sync.Mutex
	cfg settings.Events
	err error
}

func (s *eventSettings) get(context.Context) (settings.Events, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, s.err
}

// channelPointsSettings is the fake of the channel points section.
type channelPointsSettings struct {
	mu  sync.Mutex
	cfg settings.ChannelPoints
}

func (s *channelPointsSettings) get(context.Context) (settings.ChannelPoints, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, nil
}

// fakeChat is the chat service of the fixture (spec chat-service.md, B14,
// B21 to B23): it records the entries it takes, the deletes it is asked
// for, and can be muted.
type fakeChat struct {
	mu      sync.Mutex
	entries []chatsvc.Entry
	deleted []string
	muted   bool
	fail    error
}

func (c *fakeChat) Record(_ context.Context, e chatsvc.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
	return nil
}

func (c *fakeChat) Muted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.muted
}

func (c *fakeChat) Delete(_ context.Context, p platform.Name, ch connector.Channel, messageID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleted = append(c.deleted, fmt.Sprintf("%s/%s/%s", p, ch, messageID))
	return c.fail
}

func (c *fakeChat) setMuted(b bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.muted = b
}

func (c *fakeChat) setDeleteError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = err
}

// taken returns the recorded entries and forgets them.
func (c *fakeChat) taken() []chatsvc.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.entries
	c.entries = nil
	return out
}

// deletes returns the deletes asked for and forgets them.
func (c *fakeChat) deletes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.deleted
	c.deleted = nil
	return out
}

func (s *eventSettings) set(change func(*settings.Events)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.cfg)
}

// fixture is a service with fakes.
// fakeModeration applies the real moderation rules with a configurable
// section, so the pipeline tests can set the filters. The default is the
// empty section (no filters), which lets every message through.
type fakeModeration struct {
	mu      sync.Mutex
	cfg     settings.Moderation
	cmds    commands
	engine  *fakeEngine
	strikes *memStrikes
}

func (f *fakeModeration) get(context.Context) (settings.Moderation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg, nil
}

// set replaces the section; it applies from the next check.
func (f *fakeModeration) set(cfg settings.Moderation) {
	f.mu.Lock()
	f.cfg = cfg
	f.mu.Unlock()
}

func (f *fakeModeration) Check(ctx context.Context, m moderation.Message) (moderation.Decision, error) {
	svc, err := moderation.New(moderation.Ports{
		Settings: f.get, Strikes: f.strikes, Commands: f.cmds, Engine: f.engine,
	})
	if err != nil {
		return moderation.Decision{}, err
	}
	return svc.Check(ctx, m)
}

// memStrikes implements the moderation Strikes port in memory.
type memStrikes struct {
	mu      sync.Mutex
	strikes map[id.ID]int64
}

func newMemStrikes() *memStrikes { return &memStrikes{strikes: map[id.ID]int64{}} }

func (m *memStrikes) UpdateUser(_ context.Context, u id.ID, fn func(*user.User) error) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uu := user.User{ID: u, Stats: user.Stats{Strikes: m.strikes[u]}}
	if err := fn(&uu); err != nil {
		return user.User{}, err
	}
	m.strikes[u] = uu.Stats.Strikes
	return uu, nil
}

func (m *memStrikes) count(u id.ID) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.strikes[u]
}

type fixture struct {
	store    *memStore
	engine   *fakeEngine
	pub      *publisher
	settings *eventSettings
	points   *channelPointsSettings
	chat     *fakeChat
	mod      *fakeModeration
	cmds     commands
	service  *eventservice.Service
	// logs captures the service log when set; gate opens the log of
	// unknown chat commands when set (WithUnknownCommandGate); msgLogs
	// captures the separate log of the user messages when set
	// (WithMessagesLogger).
	logs    *records
	gate    func() bool
	msgLogs *records
}

func newFixture(t *testing.T, cmds ...command.Command) *fixture {
	t.Helper()
	catalog := event.NewCatalog()
	require.NoError(t, eventservice.RegisterEvents(catalog))
	require.NoError(t, errors.Join(
		event.Register[string](catalog, "app.started"),
		event.Register[string](catalog, "app.stopping"),
	))
	f := &fixture{
		store:    newMemStore(),
		engine:   &fakeEngine{},
		pub:      &publisher{catalog: catalog},
		settings: &eventSettings{cfg: settings.DefaultEvents()},
		points:   &channelPointsSettings{cfg: settings.DefaultChannelPoints()},
		chat:     &fakeChat{},
		cmds:     commands{list: cmds},
	}
	f.mod = &fakeModeration{
		cfg:     settings.DefaultModeration(),
		cmds:    f.cmds,
		engine:  f.engine,
		strikes: newMemStrikes(),
	}
	f.service = f.restart(t)
	return f
}

// restart returns a new service on the same store, as after a restart of
// the core.
func (f *fixture) restart(t *testing.T) *eventservice.Service {
	t.Helper()
	var opts []eventservice.Option
	if f.logs != nil {
		opts = append(opts, eventservice.WithLogger(slog.New(f.logs)))
	}
	if f.gate != nil {
		opts = append(opts, eventservice.WithUnknownCommandGate(f.gate))
	}
	if f.msgLogs != nil {
		opts = append(opts, eventservice.WithMessagesLogger(slog.New(f.msgLogs)))
	}
	svc, err := eventservice.New(t.Context(), eventservice.Ports{
		Store: f.store, Commands: f.cmds, Engine: f.engine, Publisher: f.pub,
		Chat: f.chat, Moderation: f.mod, Settings: f.settings.get, ChannelPoints: f.points.get,
	}, opts...)
	require.NoError(t, err)
	return svc
}

// records collects the log messages of the service (WithLogger).
type records struct {
	mu   sync.Mutex
	msgs []string
}

func (*records) Enabled(context.Context, slog.Level) bool { return true }

func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, rec.Message)
	return nil
}

func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

func (r *records) find(msg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.msgs, msg)
}

func (r *records) count(msg string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

// run runs svc until the returned function stops it.
func run(t *testing.T, svc *eventservice.Service) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx) }()
	return func() {
		cancel()
		require.NoError(t, <-done)
	}
}

// grace returns settings with the grace period d.
func grace(d polydoc.Duration) func(*settings.Events) {
	return func(e *settings.Events) { e.StreamGracePeriod = d }
}
