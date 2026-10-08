// SPDX-License-Identifier: MIT

package actiontest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

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
	commands *Commands
}

// NewHarness returns a harness whose engine knows the action types from
// types, e.g. an action.Registry, and reads commands from a store of its
// own.
func NewHarness(t *testing.T, types engine.ActionTypes) *Harness {
	t.Helper()
	return NewHarnessWith(t, types, NewCommands())
}

// NewHarnessWith returns a harness whose engine knows the action types from
// types, reads commands from commands and has the options opts, e.g.
// engine.WithRequirements. Action types that switch commands get the same
// store as a port.
func NewHarnessWith(t *testing.T, types engine.ActionTypes, commands *Commands, opts ...engine.Option) *Harness {
	t.Helper()
	e, err := engine.New(commands, types, opts...)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { assert.NoError(t, e.Run(ctx)) })
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	synctest.Wait()
	return &Harness{t: t, engine: e, commands: commands}
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
	h.commands.Put(cmd)
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

// Commands is a fake command store: the engine reads commands from it
// (engine.Commands), and the command action switches them as
// command.Service does (spec actions.md, B34).
type Commands struct {
	mu   sync.Mutex
	cmds map[id.ID]command.Command
	// groups are the known groups.
	groups map[id.ID]bool
}

// NewCommands returns an empty store.
func NewCommands() *Commands {
	return &Commands{cmds: make(map[id.ID]command.Command), groups: make(map[id.ID]bool)}
}

// ErrNotFound is returned for a command or group the store does not have.
var ErrNotFound = errors.New("not found")

// Command implements engine.Commands.
func (s *Commands) Command(_ context.Context, commandID id.ID) (command.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.cmds[commandID]
	if !ok {
		return command.Command{}, fmt.Errorf("command %s: %w", commandID, ErrNotFound)
	}
	return cmd, nil
}

// Put stores cmd, and its group as a known group.
func (s *Commands) Put(cmd command.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cmds[cmd.ID] = cmd
	if !cmd.GroupID.IsZero() {
		s.groups[cmd.GroupID] = true
	}
}

// AddGroup makes groupID a known group, also without commands.
func (s *Commands) AddGroup(groupID id.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups[groupID] = true
}

// SwitchCommand switches the command as command.Service.SwitchCommand does,
// without the triggers.
func (s *Commands) SwitchCommand(_ context.Context, commandID id.ID, sw command.Switch) (command.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.cmds[commandID]
	if !ok {
		return command.Command{}, fmt.Errorf("command %s: %w", commandID, ErrNotFound)
	}
	return s.switchLocked(cmd, sw)
}

// SwitchGroup switches the commands of the group as
// command.Service.SwitchGroup does.
func (s *Commands) SwitchGroup(_ context.Context, groupID id.ID, sw command.Switch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.groups[groupID] {
		return fmt.Errorf("command group %s: %w", groupID, ErrNotFound)
	}
	for _, cmd := range s.cmds {
		if cmd.GroupID != groupID {
			continue
		}
		if _, err := s.switchLocked(cmd, sw); err != nil {
			return err
		}
	}
	return nil
}

// switchLocked switches cmd; s.mu is held.
func (s *Commands) switchLocked(cmd command.Command, sw command.Switch) (command.Command, error) {
	enabled, err := sw.Apply(cmd.Enabled)
	if err != nil {
		return command.Command{}, err
	}
	if enabled != cmd.Enabled {
		cmd.Enabled, cmd.UpdatedAt = enabled, time.Now()
		s.cmds[cmd.ID] = cmd
	}
	return cmd, nil
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
