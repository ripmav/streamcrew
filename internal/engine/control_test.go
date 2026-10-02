// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// do returns an action that runs fn with the run.
func do(fn func(ctx context.Context, run *engine.Run) error) action {
	return action{typ: "command", fn: fn}
}

// TestRunCancelAll covers B51 from an action (actions.md B35): all
// instances end as canceled, the calling one too.
func TestRunCancelAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		running := f.start(f.command("running", command.KindEvent, f.journal.hold("x", "running", nil)), engine.Params{})
		blocked := f.start(f.command("queued", command.KindEvent, f.journal.note("not run")), engine.Params{})
		require.Equal(t, engine.StatePending, f.state(blocked))

		canceling := f.start(f.command("cancel all", command.KindChat,
			do(func(ctx context.Context, run *engine.Run) error {
				run.CancelAll(ctx)
				return nil
			}),
			f.journal.note("after")), engine.Params{})

		assert.Equal(t, engine.StateCanceled, f.state(running))
		assert.Equal(t, engine.StateCanceled, f.state(blocked))
		assert.Equal(t, engine.StateCanceled, f.state(canceling), "B35: the calling instance too")
		assert.Equal(t, []string{"running"}, f.journal.get())
	})
}

// TestRunPause covers B40 and B41 from an action (actions.md B36).
func TestRunPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()
		control := func(name string, fn func(ctx context.Context, run *engine.Run) error) command.Command {
			return f.command(name, command.KindActionGroup, do(fn), f.journal.note(name+" goes on"))
		}

		release := make(chan struct{})
		f.start(f.command("break", command.KindActionGroup,
			do(func(ctx context.Context, run *engine.Run) error { return run.Pause(ctx, engine.PauseAll) }),
			f.journal.hold("x", "break", release),
			do(func(ctx context.Context, run *engine.Run) error { return run.Resume(ctx, engine.PauseAll) }),
		), engine.Params{})
		paused, err := f.engine.Paused(engine.PauseAll)
		require.NoError(t, err)
		assert.True(t, paused)
		assert.Equal(t, []string{"break"}, f.journal.get(), "the pausing instance runs on")

		held := f.start(f.command("held", command.KindChat, f.journal.note("held back")), engine.Params{})
		assert.Equal(t, engine.StatePending, f.state(held))

		close(release)
		synctest.Wait()
		paused, err = f.engine.Paused(engine.PauseAll)
		require.NoError(t, err)
		assert.False(t, paused)
		assert.Equal(t, engine.StateCompleted, f.state(held), "the queue goes on after the resume")

		f.start(control("pause entrance", func(ctx context.Context, run *engine.Run) error {
			return run.Pause(ctx, engine.PauseEntrance)
		}), engine.Params{})
		paused, err = f.engine.Paused(engine.PauseEntrance)
		require.NoError(t, err)
		assert.True(t, paused)
		f.start(control("resume entrance", func(ctx context.Context, run *engine.Run) error {
			return run.Resume(ctx, engine.PauseEntrance)
		}), engine.Params{})
		paused, err = f.engine.Paused(engine.PauseEntrance)
		require.NoError(t, err)
		assert.False(t, paused)

		f.start(control("unknown scope", func(ctx context.Context, run *engine.Run) error {
			return run.Pause(ctx, "nap")
		}), engine.Params{})
		h := f.engine.History()
		last := h[len(h)-1]
		require.Len(t, last.Errors, 1)
		assert.Contains(t, last.Errors[0].Message, `unknown pause scope "nap"`)
	})
}

// TestRunStartCooldown covers actions.md B37 on the side of the engine.
func TestRunStartCooldown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockPerCommandType, engine.WithRequirements(reqs))
		defer f.stop()
		target := f.command("target", command.KindChat)
		var errs []error
		starter := func(commandID id.ID) command.Command {
			return f.command("starter", command.KindChat, do(func(ctx context.Context, run *engine.Run) error {
				err := run.StartCooldown(ctx, commandID)
				errs = append(errs, err)
				return err
			}))
		}

		viewer := &user.User{ID: id.New()}
		f.start(starter(target.ID), engine.Params{User: viewer})
		f.start(starter(target.ID), engine.Params{})
		assert.Equal(t, []string{"target for " + viewer.ID.String(), "target without a user"}, reqs.started())

		f.start(starter(id.New()), engine.Params{})
		require.Len(t, errs, 3)
		require.ErrorIs(t, errs[2], errNoCommand, "a command that does not exist")

		reqs.mu.Lock()
		reqs.cooldownErr = errors.New("a cooldown per user needs a user")
		reqs.mu.Unlock()
		f.start(starter(target.ID), engine.Params{})
		require.Len(t, errs, 4)
		require.ErrorContains(t, errs[3], `start cooldown of command "target": a cooldown per user needs a user`)
	})
}

// TestRunStartCooldownWithoutRequirements: without a requirement service
// there are no cooldowns to start.
func TestRunStartCooldownWithoutRequirements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()
		target := f.command("target", command.KindChat)
		in := f.start(f.command("starter", command.KindChat, do(func(ctx context.Context, run *engine.Run) error {
			return run.StartCooldown(ctx, target.ID)
		})), engine.Params{})
		got, ok := f.engine.Instance(in)
		require.True(t, ok)
		assert.Empty(t, got.Errors)
	})
}
