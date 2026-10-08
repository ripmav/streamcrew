// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// wait returns the options of a call that waits for the called command.
func wait() engine.CallOptions {
	return engine.CallOptions{Wait: true}
}

// call returns an action that calls the command commandID. It writes the
// error, if any, and outcomes other than completed and queued.
func (f *fixture) call(commandID id.ID, opts engine.CallOptions) action {
	var waits id.ID
	if opts.Wait {
		waits = commandID
	}
	return action{typ: "command", waits: waits, fn: func(ctx context.Context, run *engine.Run) error {
		res, err := run.Call(ctx, commandID, opts)
		switch {
		case err != nil:
			f.journal.add("call failed: " + err.Error())
		case res.Outcome != engine.OutcomeCompleted && res.Outcome != engine.OutcomeQueued:
			f.journal.add("call " + string(res.Outcome))
		}
		return err
	}}
}

// show returns an action that writes text, rendered with the identifiers of
// the run, its arguments and its users.
func (f *fixture) show(text string) action {
	f.t.Helper()
	registry, err := template.NewRegistry(template.RunFamily(), template.ArgumentFamily(), template.UserFamily(nil))
	require.NoError(f.t, err)
	render := template.New(registry)
	return action{typ: "show", fn: func(ctx context.Context, run *engine.Run) error {
		out, err := render.Render(ctx, template.Parse(text), run.Scope(), template.Text)
		f.journal.add(out)
		return err
	}}
}

// set returns an action that sets a value of the run.
func set(name, value string) action {
	return action{typ: "set", fn: func(_ context.Context, run *engine.Run) error {
		run.Scope().SetValue(name, template.TextValue(value))
		return nil
	}}
}

// TestCallWait covers B30, B31, B34, B35 and B105: a call runs at once as
// part of its caller, with its user and arguments, and shares the values of
// the run.
func TestCallWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		ada := &user.User{ID: id.New(), Identities: []user.Identity{{Platform: platform.Twitch, Login: "ada"}}}
		called := f.command("called", command.KindChat, f.show("$commandname: $arg1text by $username from $caller"), set("answer", "42"))
		withArgs := f.command("with args", command.KindActionGroup, f.show("$commandname: $arg1text | $allargs"))
		caller := f.command("caller", command.KindChat,
			set("caller", "caller"),
			f.call(called.ID, wait()),
			f.call(withArgs.ID, engine.CallOptions{Wait: true, OwnArgs: true, Args: []string{"given", "by the action"}}),
			f.show("$commandname: $answer"))
		callerID := f.start(caller, engine.Params{Platform: platform.Twitch, User: ada, Args: []string{"mine"}, ArgsText: "mine"})

		assert.Equal(t, []string{
			"called: mine by ada from caller",
			"with args: given | given by the action",
			"caller: 42",
		}, f.journal.get())
		assert.Equal(t, engine.StateCompleted, f.state(callerID))
		assert.Equal(t, []string{
			"queued caller", "started caller",
			"queued called", "started called", "completed called",
			"queued with args", "started with args", "completed with args",
			"completed caller",
		}, f.events())
		h := f.engine.History()
		require.Len(t, h, 3)
		assert.Equal(t, engine.SourceCall, h[1].Source)
		assert.Equal(t, callerID, h[1].Parent)
		assert.Equal(t, ada.ID, h[1].UserID, "B34")
		assert.Equal(t, []string{"given", "by the action"}, h[2].Args)
		assert.True(t, h[0].Parent.IsZero())
	})
}

// TestCallDuringPause covers B31, B32, B40 and B106: during a pause, a call
// with waiting runs; one without waiting is queued until the pause ends and
// gets a copy of the values (B35).
func TestCallDuringPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		go1 := make(chan struct{})
		waited := f.command("waited", command.KindChat, f.show("waited"))
		queued := f.command("queued", command.KindChat, f.show("queued sees $mood"), set("answer", "42"))
		caller := f.command("caller", command.KindChat,
			f.journal.hold("x", "caller", go1),
			set("mood", "happy"),
			f.call(waited.ID, wait()),
			f.call(queued.ID, engine.CallOptions{}),
			set("mood", "sad"),
			f.show("caller sees $answer"))
		f.start(caller, engine.Params{})
		require.NoError(t, f.engine.Pause(t.Context(), engine.PauseAll))
		close(go1)
		synctest.Wait()

		assert.Equal(t, []string{"caller", "waited", "caller sees $answer"}, f.journal.get())
		require.NoError(t, f.engine.Resume(t.Context(), engine.PauseAll))
		synctest.Wait()
		assert.Equal(t, "queued sees happy", f.journal.get()[3], "a copy at the time of the call")
		h := f.engine.History()
		assert.Equal(t, engine.SourceCall, h[2].Source)
		assert.Equal(t, h[0].ID, h[2].Parent)
	})
}

// TestCallRequirements covers B33: a call checks the requirements of the
// called command only if asked to, with the user of the caller. Rejected
// and waiting are outcomes of the call, not failures of the action.
func TestCallRequirements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()

		check := engine.CallOptions{Wait: true, CheckRequirements: true}
		free := f.command("free", command.KindActionGroup, f.show("free"))
		guarded := f.command("guarded", command.KindActionGroup, f.show("guarded"))
		gathering := f.command("gathering", command.KindActionGroup, f.show("gathering"))
		reqs.decide("guarded", rejected("role"), false)
		reqs.decide("gathering", engine.Waiting(), false)
		caller := f.command("caller", command.KindChat,
			f.call(free.ID, check),
			f.call(guarded.ID, wait()),
			f.call(guarded.ID, check),
			f.call(gathering.ID, check),
			f.show("after"))
		callerID := f.start(caller, engine.Params{})

		assert.Equal(t, []string{"free", "guarded", "call rejected", "call waiting", "after"}, f.journal.get())
		assert.Equal(t, []string{"free", "guarded", "gathering"}, reqs.appliedTo())
		assert.Equal(t, []string{"guarded role"}, reqs.messages())
		in, _ := f.engine.Instance(callerID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
	})
}

// TestInvalidCall: arguments without OwnArgs are an error, not ignored.
func TestInvalidCall(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		called := f.command("called", command.KindActionGroup, f.show("[$allargs]"))
		f.start(f.command("caller", command.KindChat,
			f.call(called.ID, engine.CallOptions{Wait: true, Args: []string{"lost"}}),
			f.call(called.ID, engine.CallOptions{Wait: true, OwnArgs: true})), engine.Params{Args: []string{"mine"}, ArgsText: "mine"})

		lines := f.journal.get()
		require.Len(t, lines, 2)
		assert.Contains(t, lines[0], engine.ErrInvalidCall.Error())
		assert.Equal(t, "[]", lines[1], "own arguments can be none")
	})
}

// TestCallFails covers B36: a failed or canceled call fails the calling
// action, and the error policy of the caller decides. A disabled command is
// an outcome of the call.
func TestCallFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		failing := f.command("failing", command.KindActionGroup, f.journal.fail("failing"))
		failing.ErrorPolicy = command.ErrorAbort
		f.commands.put(failing)
		disabled := f.command("disabled", command.KindActionGroup)
		disabled.Enabled = false
		f.commands.put(disabled)

		lenient := f.command("lenient", command.KindChat,
			f.call(failing.ID, wait()),
			f.call(disabled.ID, wait()),
			f.call(id.New(), wait()),
			f.show("lenient goes on"))
		lenientID := f.start(lenient, engine.Params{})
		in, _ := f.engine.Instance(lenientID)
		assert.Equal(t, engine.StateCompleted, in.State)
		require.Len(t, in.Errors, 2)
		assert.Equal(t, []int{1}, in.Errors[0].Path)
		assert.Contains(t, in.Errors[0].Message, engine.ErrCallFailed.Error())
		assert.Equal(t, []int{3}, in.Errors[1].Path)
		assert.Contains(t, in.Errors[1].Message, errNoCommand.Error())
		assert.Contains(t, f.journal.get(), "call disabled", "B14: an outcome, not an error")

		strict := f.command("strict", command.KindChat, f.call(failing.ID, wait()), f.show("not reached"))
		strict.ErrorPolicy = command.ErrorAbort
		assert.Equal(t, engine.StateFailed, f.state(f.start(strict, engine.Params{})))
		assert.NotContains(t, f.journal.get(), "not reached")
	})
}

// TestCallCycle covers B73: a call of a command that is already in the chain
// of calls fails, with and without waiting.
func TestCallCycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()

		a := f.command("a", command.KindChat)
		b := f.command("b", command.KindActionGroup, f.show("b"), f.call(a.ID, engine.CallOptions{}))
		a.Actions = []command.Action{f.show("a"), f.call(b.ID, engine.CallOptions{}), f.call(a.ID, wait())}
		f.commands.put(a)
		f.start(a, engine.Params{})

		cycle := "call failed: call command " + a.ID.String() + ": " + engine.ErrCallCycle.Error()
		assert.Equal(t, []string{
			"a",
			cycle, // a calls itself
			"b",   // queued by a, runs after it
			cycle, // b calls a
		}, f.journal.get())
	})
}

// TestCallDepth covers B73: calls nest at most MaxCallDepth deep.
func TestCallDepth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		cmds := make([]command.Command, engine.MaxCallDepth+2)
		for i := len(cmds) - 1; i >= 0; i-- {
			actions := []command.Action{f.show(fmt.Sprintf("level %d", i))}
			if i+1 < len(cmds) {
				actions = append(actions, f.call(cmds[i+1].ID, wait()))
			}
			cmds[i] = f.command(fmt.Sprintf("c%d", i), command.KindActionGroup, actions...)
		}
		first := f.start(cmds[0], engine.Params{})

		lines := f.journal.get()
		require.Len(t, lines, engine.MaxCallDepth+2, "levels 0 to MaxCallDepth and one failed call")
		assert.Equal(t, fmt.Sprintf("level %d", engine.MaxCallDepth), lines[engine.MaxCallDepth])
		assert.Contains(t, lines[engine.MaxCallDepth+1], engine.ErrCallDepth.Error())
		assert.NotContains(t, strings.Join(lines, "\n"), fmt.Sprintf("level %d", engine.MaxCallDepth+1))
		assert.Equal(t, engine.StateCompleted, f.state(first), "the error policy continue at every level")
	})
}

// TestCancelWithCall covers B52: a call ends with its caller; canceling only
// the call fails the calling action.
func TestCancelWithCall(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		cancelCaller bool
		caller       engine.State
	}{
		{"caller", true, engine.StateCanceled},
		{"call", false, engine.StateCompleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockPerCommandType)
				defer f.stop()

				called := f.command("called", command.KindActionGroup, f.journal.hold("x", "called", nil))
				callerID := f.start(f.command("caller", command.KindChat,
					f.call(called.ID, wait()), f.show("caller goes on")), engine.Params{})
				h := f.engine.History()
				require.Len(t, h, 2)
				calledID := h[1].ID
				assert.Equal(t, engine.StateRunning, f.state(calledID))

				target := calledID
				if tc.cancelCaller {
					target = callerID
				}
				require.NoError(t, f.engine.Cancel(t.Context(), target))
				synctest.Wait()

				assert.Equal(t, engine.StateCanceled, f.state(calledID))
				assert.Equal(t, tc.caller, f.state(callerID))
				assert.Equal(t, !tc.cancelCaller, strings.Contains(strings.Join(f.journal.get(), "\n"), "caller goes on"))
			})
		})
	}
}

// TestCallAtShutdown covers B31 and B55: while the core stops, a running
// instance may still call a command and wait for it, but not queue one.
func TestCallAtShutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		release := make(chan struct{})
		called := f.command("called", command.KindActionGroup, f.show("called"))
		f.start(f.command("caller", command.KindChat,
			f.journal.hold("x", "caller", release),
			f.call(called.ID, wait()),
			f.call(called.ID, engine.CallOptions{})), engine.Params{})
		go f.stop()
		synctest.Wait()
		close(release)
		synctest.Wait()

		lines := f.journal.get()
		require.Len(t, lines, 3)
		assert.Equal(t, []string{"caller", "called"}, lines[:2])
		assert.Contains(t, lines[2], engine.ErrClosed.Error())
	})
}

// TestCallSettingsError: a call needs the settings.
func TestCallSettingsError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfg := &configs{cfg: engine.DefaultConfig()}
		var broken atomic.Bool
		f := newFixture(t, settings.LockPerCommandType, engine.WithConfig(func(ctx context.Context) (engine.Config, error) {
			if broken.Load() {
				return engine.Config{}, errors.New("disk on fire")
			}
			return cfg.get(ctx)
		}))
		defer f.stop()

		called := f.command("called", command.KindActionGroup, f.show("called"))
		breaker := action{typ: "break", fn: func(context.Context, *engine.Run) error {
			broken.Store(true)
			return nil
		}}
		f.start(f.command("caller", command.KindChat, breaker, f.call(called.ID, wait())), engine.Params{})
		assert.Equal(t, []string{`call failed: call command "called": read settings: disk on fire`}, f.journal.get())
	})
}
