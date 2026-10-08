// SPDX-License-Identifier: MIT

package eventservice_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
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
}

func newMemStore() *memStore {
	return &memStore{
		sessions:      make(map[platform.Name]stream.Session),
		sessionEvents: make(map[string]bool),
		userEvents:    make(map[string]bool),
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

func (s *eventSettings) set(change func(*settings.Events)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.cfg)
}

// fixture is a service with fakes.
type fixture struct {
	store    *memStore
	engine   *fakeEngine
	pub      *publisher
	settings *eventSettings
	cmds     commands
	service  *eventservice.Service
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
		cmds:     commands{list: cmds},
	}
	f.service = f.restart(t)
	return f
}

// restart returns a new service on the same store, as after a restart of
// the core.
func (f *fixture) restart(t *testing.T) *eventservice.Service {
	t.Helper()
	svc, err := eventservice.New(t.Context(), eventservice.Ports{
		Store: f.store, Commands: f.cmds, Engine: f.engine, Publisher: f.pub, Settings: f.settings.get,
	})
	require.NoError(t, err)
	return svc
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
