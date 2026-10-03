// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
)

// action is an action for tests.
type action struct {
	typ      string
	fn       func(ctx context.Context, run *engine.Run) error
	children []command.Action
	disabled bool
}

func (a action) DocType() string            { return a.typ }
func (a action) Children() []command.Action { return a.children }
func (a action) Enabled() bool              { return !a.disabled }
func (a action) Validate() error            { return nil }
func (a action) Perform(ctx context.Context, run *engine.Run) error {
	if a.fn == nil {
		return nil
	}
	return a.fn(ctx, run)
}

// actionTypes is a fake of engine.ActionTypes.
type actionTypes struct {
	visual  []string
	missing map[string][]capability.Capability
}

// visual returns action types in which the given types are visual or audio.
func visual(types ...string) *actionTypes {
	return &actionTypes{visual: types}
}

func (t *actionTypes) VisualAudio(actionType string) bool {
	return slices.Contains(t.visual, actionType)
}

func (t *actionTypes) Missing(actionType string) []capability.Capability {
	return append([]capability.Capability{}, t.missing[actionType]...)
}

// unknown is an action the engine cannot run.
type unknown struct{ typ string }

func (u unknown) DocType() string { return u.typ }
func (u unknown) Validate() error { return nil }

// journal records what the actions do, in order.
type journal struct {
	mu    sync.Mutex
	lines []string
}

func (j *journal) add(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, line)
}

func (j *journal) get() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.lines)
}

// note returns an action that writes line.
func (j *journal) note(line string) action {
	return action{typ: "note", fn: func(context.Context, *engine.Run) error {
		j.add(line)
		return nil
	}}
}

// fail returns an action that writes line and fails.
func (j *journal) fail(line string) action {
	return action{typ: "fail", fn: func(context.Context, *engine.Run) error {
		j.add(line)
		return errors.New("boom")
	}}
}

// hold returns an action of type typ that writes line and waits until
// release is closed or its context ends.
func (j *journal) hold(typ, line string, release <-chan struct{}) action {
	return action{typ: typ, fn: func(ctx context.Context, _ *engine.Run) error {
		j.add(line)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
}

// commandStore is a fake of engine.Commands.
type commandStore struct {
	mu   sync.Mutex
	cmds map[id.ID]command.Command
}

var errNoCommand = errors.New("no such command")

func (s *commandStore) Command(_ context.Context, commandID id.ID) (command.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.cmds[commandID]
	if !ok {
		return command.Command{}, fmt.Errorf("command %s: %w", commandID, errNoCommand)
	}
	return cmd, nil
}

func (s *commandStore) put(cmd command.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds[cmd.ID] = cmd
}

func (s *commandStore) delete(commandID id.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cmds, commandID)
}

// configs holds the settings the engine reads; tests change them.
type configs struct {
	mu  sync.Mutex
	cfg engine.Config
}

func (c *configs) get(context.Context) (engine.Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg, nil
}

func (c *configs) lockMode(m settings.LockMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Commands.LockMode = m
}

func (c *configs) queueSize(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Commands.QueueSize = n
}

// fixture is a running engine with a subscription to its events. Create it
// inside a synctest bubble and defer stop.
type fixture struct {
	t        *testing.T
	engine   *engine.Engine
	commands *commandStore
	configs  *configs
	sub      *event.Subscription
	journal  *journal
	stop     func()
}

func newFixture(t *testing.T, mode settings.LockMode, opts ...engine.Option) *fixture {
	t.Helper()
	return newFixtureWithTypes(t, mode, &actionTypes{}, opts...)
}

// newFixtureWithTypes returns a fixture whose engine knows the action types
// from types.
func newFixtureWithTypes(t *testing.T, mode settings.LockMode, types engine.ActionTypes, opts ...engine.Option) *fixture {
	t.Helper()
	catalog := event.NewCatalog()
	require.NoError(t, engine.RegisterEvents(catalog))
	bus := event.NewBus(nil, event.WithCatalog(catalog))
	cfg := engine.DefaultConfig()
	cfg.Commands.LockMode = mode
	f := &fixture{
		t:        t,
		commands: &commandStore{cmds: make(map[id.ID]command.Command)},
		configs:  &configs{cfg: cfg},
		sub:      bus.Subscribe(t.Context(), event.WithPrefixes("command."), event.WithBuffer(8192)),
		journal:  &journal{},
	}
	opts = append([]engine.Option{engine.WithPublisher(bus), engine.WithConfig(f.configs.get)}, opts...)
	var err error
	f.engine, err = engine.New(f.commands, types, opts...)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { assert.NoError(t, f.engine.Run(ctx)) })
	f.stop = sync.OnceFunc(func() {
		cancel()
		wg.Wait()
		f.sub.Close()
	})
	synctest.Wait()
	return f
}

// command returns an enabled command of kind that f's store knows.
func (f *fixture) command(name string, kind command.Kind, actions ...command.Action) command.Command {
	cmd := command.Command{
		ID: id.New(), Name: name, Kind: kind, Enabled: true, ErrorPolicy: command.ErrorContinue, Actions: actions,
	}
	f.commands.put(cmd)
	return cmd
}

// start starts cmd by hand and waits until the engine has nothing more to
// do.
func (f *fixture) start(cmd command.Command, p engine.Params) id.ID {
	f.t.Helper()
	instanceID, err := f.engine.Start(f.t.Context(), cmd, p)
	require.NoError(f.t, err)
	synctest.Wait()
	return instanceID
}

// state returns the state of an instance.
func (f *fixture) state(instanceID id.ID) engine.State {
	f.t.Helper()
	in, ok := f.engine.Instance(instanceID)
	require.True(f.t, ok, "instance %s", instanceID)
	return in.State
}

// events returns the events published since the last call, as "<change>
// <command or scope>", e.g. "started hug".
func (f *fixture) events() []string {
	var out []string
	for {
		select {
		case env := <-f.sub.C():
			change := string(env.Type)
			for _, prefix := range []string{"command.instance.", "command.queue."} {
				change = strings.TrimPrefix(change, prefix)
			}
			switch p := env.Payload.(type) {
			case engine.Instance:
				out = append(out, change+" "+p.CommandName)
			case engine.QueuePause:
				out = append(out, change+" "+string(p.Scope))
			default:
				f.t.Errorf("unexpected payload %T of %s", p, env.Type)
			}
		default:
			return out
		}
	}
}

// publisherFunc adapts a function to engine.Publisher.
type publisherFunc func(ctx context.Context, e event.Envelope) error

func (p publisherFunc) Publish(ctx context.Context, e event.Envelope) error { return p(ctx, e) }

// records is a slog handler that keeps the messages.
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

func (r *records) messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs)
}

// paused reports whether scope is paused.
func paused(t *testing.T, e *engine.Engine, scope engine.PauseScope) bool {
	t.Helper()
	p, err := e.Paused(scope)
	require.NoError(t, err)
	return p
}
