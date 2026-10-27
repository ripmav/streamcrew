// SPDX-License-Identifier: MIT

package commandadmin_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commandadmin"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// fakeCommands is the commandadmin Commands port.
type fakeCommands struct {
	mu      sync.Mutex
	list    []command.Command
	listErr error
	saves   int
	nextID  id.ID
}

func (f *fakeCommands) Commands(context.Context) ([]command.Command, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]command.Command(nil), f.list...), nil
}

func (f *fakeCommands) Save(_ context.Context, cmd command.Command) (command.Saved, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if cmd.ID.IsZero() {
		f.nextID = id.New()
		cmd.ID = f.nextID
		f.list = append(f.list, cmd)
		return command.Saved{Command: cmd}, nil
	}
	for i, c := range f.list {
		if c.ID == cmd.ID {
			f.list[i] = cmd
			return command.Saved{Command: cmd}, nil
		}
	}
	f.list = append(f.list, cmd)
	return command.Saved{Command: cmd}, nil
}

func (f *fakeCommands) SwitchCommand(_ context.Context, commandID id.ID, sw command.Switch) (command.Command, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.list {
		if c.ID != commandID {
			continue
		}
		on, err := sw.Apply(c.Enabled)
		if err != nil {
			return command.Command{}, err
		}
		c.Enabled = on
		f.list[i] = c
		return c, nil
	}
	return command.Command{}, errors.New("no command with the ID")
}

type fixture struct {
	t       *testing.T
	reg     *action.Registry
	harness *actiontest.Harness
	cmds    *fakeCommands
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cmds := &fakeCommands{}
	ds, err := commandadmin.Descriptors(commandadmin.Ports{Commands: cmds, Logger: slog.New(slog.DiscardHandler)})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return &fixture{t: t, reg: reg, harness: actiontest.NewHarness(t, reg), cmds: cmds}
}

func (f *fixture) action(k commandadmin.Kind) commandadmin.CommandAdmin {
	f.t.Helper()
	d, ok := f.reg.Descriptor(commandadmin.TypeCommandAdmin)
	require.True(f.t, ok)
	a, ok := d.New().(commandadmin.CommandAdmin)
	require.True(f.t, ok)
	a.Kind = k
	require.NoError(f.t, a.Validate())
	return a
}

func TestAdd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		inst := f.harness.Start(f.harness.Command("addcmd", f.action(commandadmin.KindAdd)),
			engine.Params{Args: []string{"greet", "Hello", "there"}, ArgsText: "greet Hello there"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.cmds.list, 1)
		assert.Equal(t, "greet", f.cmds.list[0].Name)
		assert.Equal(t, []string{"greet"}, f.cmds.list[0].Triggers)
		assert.True(t, f.cmds.list[0].Enabled)
		require.Len(t, f.cmds.list[0].Actions, 1)
	})
}

func TestAddRefusesExisting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{{Name: "greet", Kind: command.KindChat, Triggers: []string{"greet"}}}
		inst := f.harness.Start(f.harness.Command("addcmd", f.action(commandadmin.KindAdd)),
			engine.Params{Args: []string{"greet", "again"}, ArgsText: "greet again"})
		assert.NotEmpty(t, inst.Errors)
		assert.Len(t, f.cmds.list, 1, "no second command is created")
	})
}

// fakeChatAction returns a chat message action with the message, for a
// command the update finds and changes.
func fakeChatAction(message string) chataction.Chat {
	return chataction.Chat{
		Kind:    chataction.KindMessage,
		Message: action.Template(message),
		Chat:    &chataction.MessageOptions{},
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		greet := command.Command{
			ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true, TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"},
			Actions: []command.Action{fakeChatAction("old")},
		}
		f.cmds.list = []command.Command{greet}
		inst := f.harness.Start(f.harness.Command("updatecmd", f.action(commandadmin.KindUpdate)),
			engine.Params{Args: []string{"greet", "new", "answer"}, ArgsText: "greet new answer"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.cmds.list, 1)
	})
}

func TestDisable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{{ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true, Triggers: []string{"greet"}}}
		inst := f.harness.Start(f.harness.Command("disablecmd", f.action(commandadmin.KindDisable)),
			engine.Params{Args: []string{"greet"}, ArgsText: "greet"})
		assert.Equal(t, engine.StateCompleted, inst.State)
		require.Len(t, f.cmds.list, 1)
		assert.False(t, f.cmds.list[0].Enabled, "the command is disabled")
	})
}

func TestNotfound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		inst := f.harness.Start(f.harness.Command("disablecmd", f.action(commandadmin.KindDisable)),
			engine.Params{Args: []string{"missing"}, ArgsText: "missing"})
		assert.NotEmpty(t, inst.Errors, "a missing command fails the action")
	})
}

func TestNoArgs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		inst := f.harness.Start(f.harness.Command("addcmd", f.action(commandadmin.KindAdd)),
			engine.Params{})
		assert.NotEmpty(t, inst.Errors, "no name fails the action")
	})
}

func TestAddStripsExclamation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		inst := f.harness.Start(f.harness.Command("addcmd", f.action(commandadmin.KindAdd)),
			engine.Params{Args: []string{"!greet", "Hello", "there"}, ArgsText: "!greet Hello there"})
		assert.Equal(t, engine.StateCompleted, inst.State, "a leading ! is the trigger marker, not the name")
		require.Len(t, f.cmds.list, 1)
		assert.Equal(t, "greet", f.cmds.list[0].Name)
		assert.Equal(t, []string{"greet"}, f.cmds.list[0].Triggers)
	})
}

func TestUpdateStripsExclamation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{{
			ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true,
			TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"},
			Actions: []command.Action{fakeChatAction("old")},
		}}
		inst := f.harness.Start(f.harness.Command("updatecmd", f.action(commandadmin.KindUpdate)),
			engine.Params{Args: []string{"!greet", "new"}, ArgsText: "!greet new"})
		assert.Equal(t, engine.StateCompleted, inst.State, "the command is found by the bare trigger")
	})
}

func TestUpdateNoChatAction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{{
			ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true,
			TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"},
		}}
		inst := f.harness.Start(f.harness.Command("updatecmd", f.action(commandadmin.KindUpdate)),
			engine.Params{Args: []string{"greet", "new"}, ArgsText: "greet new"})
		require.NotEmpty(t, inst.Errors)
		assert.Contains(t, inst.Errors[0].Message, "no chat action")
		assert.Zero(t, f.cmds.saves, "nothing is saved")
	})
}

func TestAddFailsOnListingError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.listErr = errors.New("the listing failed")
		inst := f.harness.Start(f.harness.Command("addcmd", f.action(commandadmin.KindAdd)),
			engine.Params{Args: []string{"greet", "hi"}, ArgsText: "greet hi"})
		require.NotEmpty(t, inst.Errors, "a listing error is not a free name")
		assert.Contains(t, inst.Errors[0].Message, "the listing failed")
		assert.Empty(t, f.cmds.list, "no command is created")
	})
}

func TestUpdateFailsOnListingError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.listErr = errors.New("the listing failed")
		inst := f.harness.Start(f.harness.Command("updatecmd", f.action(commandadmin.KindUpdate)),
			engine.Params{Args: []string{"greet", "new"}, ArgsText: "greet new"})
		require.NotEmpty(t, inst.Errors, "the listing error is reported, not swallowed as not found")
		assert.Contains(t, inst.Errors[0].Message, "the listing failed")
	})
}

func TestUpdateAmbiguous(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{
			{ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true,
				TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"},
				Actions: []command.Action{fakeChatAction("one")}},
			{ID: id.New(), Name: "greet-old", Kind: command.KindChat, Enabled: false,
				TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"}},
		}
		inst := f.harness.Start(f.harness.Command("updatecmd", f.action(commandadmin.KindUpdate)),
			engine.Params{Args: []string{"greet", "new"}, ArgsText: "greet new"})
		require.NotEmpty(t, inst.Errors, "several commands fail the update")
		assert.Contains(t, inst.Errors[0].Message, "several")
		assert.Zero(t, f.cmds.saves, "no command is changed")
	})
}

func TestDisableAmbiguous(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.cmds.list = []command.Command{
			{ID: id.New(), Name: "greet", Kind: command.KindChat, Enabled: true,
				TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"}},
			{ID: id.New(), Name: "greet-old", Kind: command.KindChat, Enabled: false,
				TriggerMode: command.TriggerExclamation, Triggers: []string{"greet"}},
		}
		inst := f.harness.Start(f.harness.Command("disablecmd", f.action(commandadmin.KindDisable)),
			engine.Params{Args: []string{"greet"}, ArgsText: "greet"})
		require.NotEmpty(t, inst.Errors, "several commands fail the disable")
		assert.Contains(t, inst.Errors[0].Message, "several")
		assert.True(t, f.cmds.list[0].Enabled, "nothing is disabled")
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		assert.NoError(t, f.action(commandadmin.KindAdd).Validate())
		assert.NoError(t, f.action(commandadmin.KindUpdate).Validate())
		assert.NoError(t, f.action(commandadmin.KindDisable).Validate())
		a := f.action(commandadmin.KindAdd)
		a.Kind = ""
		assert.Error(t, a.Validate())
	})
}
