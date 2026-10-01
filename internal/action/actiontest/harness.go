// SPDX-License-Identifier: Apache-2.0

package actiontest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// Harness runs commands in a running engine, for the behavior tests of
// action types. Create it inside synctest.Test; it stops when the test
// ends.
type Harness struct {
	t        *testing.T
	engine   *engine.Engine
	commands *commandStore
}

// NewHarness returns a harness whose engine knows the action types from
// types, e.g. an action.Registry.
func NewHarness(t *testing.T, types engine.ActionTypes) *Harness {
	t.Helper()
	store := &commandStore{cmds: make(map[id.ID]command.Command)}
	e, err := engine.New(store, types)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { assert.NoError(t, e.Run(ctx)) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	synctest.Wait()
	return &Harness{t: t, engine: e, commands: store}
}

// Engine returns the engine.
func (h *Harness) Engine() *engine.Engine {
	return h.engine
}

// Command returns an enabled chat command with the actions, under the error
// policy "continue", and stores it so that calls find it.
func (h *Harness) Command(name string, actions ...command.Action) command.Command {
	cmd := command.Command{
		ID: id.New(), Name: name, Kind: command.KindChat, Enabled: true, ErrorPolicy: command.ErrorContinue, Actions: actions}
	h.Put(cmd)
	return cmd
}

// Put stores cmd.
func (h *Harness) Put(cmd command.Command) {
	h.commands.put(cmd)
}

// Start starts cmd by hand, waits until the engine has nothing more to do
// and returns the instance.
func (h *Harness) Start(cmd command.Command, p engine.Params) engine.Instance {
	h.t.Helper()
	instanceID, err := h.engine.Start(h.t.Context(), cmd, p)
	require.NoError(h.t, err)
	synctest.Wait()
	return h.Instance(instanceID)
}

// Instance returns the instance as it is now.
func (h *Harness) Instance(instanceID id.ID) engine.Instance {
	h.t.Helper()
	in, ok := h.engine.Instance(instanceID)
	require.True(h.t, ok, "instance %s", instanceID)
	return in
}

// commandStore is a fake of engine.Commands.
type commandStore struct {
	mu   sync.Mutex
	cmds map[id.ID]command.Command
}

// errNoCommand is returned for a command the store does not have.
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

// Journal records what note actions do, in order.
type Journal struct {
	mu    sync.Mutex
	lines []string
}

// Lines returns the lines so far.
func (j *Journal) Lines() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.lines)
}

func (j *Journal) add(line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, line)
}

// Note returns an active action of type "note" that writes text.
func (j *Journal) Note(text string) command.Action {
	return note{journal: j, text: text, active: true}
}

// Inactive returns an inactive action of type "note" that would write
// text.
func (j *Journal) Inactive(text string) command.Action {
	return note{journal: j, text: text}
}

// Fail returns an active action of type "fail" that writes text and fails.
func (j *Journal) Fail(text string) command.Action {
	return note{journal: j, text: text, active: true, fail: true}
}

// note is the action of Journal.
type note struct {
	journal *Journal
	text    string
	active  bool
	fail    bool
}

// errNote is the error of a failing note.
var errNote = errors.New("the note failed")

func (n note) DocType() string {
	if n.fail {
		return "fail"
	}
	return "note"
}

func (note) Validate() error { return nil }
func (n note) Enabled() bool { return n.active }
func (n note) Perform(context.Context, *engine.Run) error {
	n.journal.add(n.text)
	if n.fail {
		return errNote
	}
	return nil
}
