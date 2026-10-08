// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// group returns a container that runs its child actions in order, like the
// action "group" (actions.md B14).
func group(children ...command.Action) action {
	return action{typ: "group", children: children, fn: func(ctx context.Context, run *engine.Run) error {
		for i := range children {
			out, err := run.PerformChild(ctx, i)
			if err != nil {
				return err
			}
			if out == engine.ChildEnd {
				return nil
			}
		}
		return nil
	}}
}

// sleep returns an action that takes d.
func sleep(d time.Duration) action {
	return action{typ: "sleep", fn: func(ctx context.Context, _ *engine.Run) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
}

// off returns a in an inactive state.
func off(a action) action {
	a.disabled = true
	return a
}

// TestInactiveActions covers actions.md B1: inactive actions do not run,
// with their child actions, and need no lock.
func TestInactiveActions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerActionType)
		defer f.stop()

		instanceID := f.start(f.command("x", command.KindChat,
			f.journal.note("a"),
			off(f.journal.note("b")),
			off(group(f.journal.note("c"))),
			group(off(f.journal.note("d")), f.journal.note("e")),
		), engine.Params{})
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"a", "e"}, f.journal.get())

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("holder", command.KindChat, f.journal.hold("sound", "holder", release)), engine.Params{})
		free := f.start(f.command("free", command.KindChat,
			off(action{typ: "sound"}), group(off(action{typ: "sound"})), f.journal.note("free")), engine.Params{})
		assert.Equal(t, engine.StateCompleted, f.state(free), "inactive actions need no lock")
	})
}

// TestChildActions covers actions.md B9: the error policy applies to child
// actions, which are recorded under their path.
func TestChildActions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		policy    command.ErrorPolicy
		wantState engine.State
		wantLines []string
	}{
		{command.ErrorContinue, engine.StateCompleted, []string{"1", "2", "3", "after"}},
		{command.ErrorAbort, engine.StateFailed, []string{"1", "2"}},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockPerCommandType)
				defer f.stop()

				cmd := f.command("x", command.KindChat,
					group(f.journal.note("1"), group(f.journal.fail("2")), f.journal.note("3")),
					f.journal.note("after"))
				cmd.ErrorPolicy = tc.policy
				f.commands.put(cmd)
				in, _ := f.engine.Instance(f.start(cmd, engine.Params{}))
				assert.Equal(t, tc.wantState, in.State)
				assert.Equal(t, tc.wantLines, f.journal.get())
				assert.Equal(t, []engine.ActionError{{Path: []int{1, 2, 1}, Type: "fail", Message: "boom"}}, in.Errors)
			})
		})
	}
}

// TestChildStop: an action that asks to stop ends the instance as
// completed, also as a child action (B4, actions.md B38).
func TestChildStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		exit := action{typ: "exit", fn: func(context.Context, *engine.Run) error { return engine.ErrStop }}
		instanceID := f.start(f.command("x", command.KindChat,
			group(f.journal.note("1"), group(exit), f.journal.note("not reached")),
			f.journal.note("not reached either")), engine.Params{})
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"1"}, f.journal.get())
	})
}

// TestContainerAfterEnd: once a child action ended the instance, what the
// container returns does not change how it ends.
func TestContainerAfterEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		stubborn := action{typ: "stubborn", children: []command.Action{f.journal.fail("child")}}
		stubborn.fn = func(ctx context.Context, run *engine.Run) error {
			out, err := run.PerformChild(ctx, 0)
			require.NoError(t, err)
			assert.Equal(t, engine.ChildEnd, out)
			return errors.New("ignored")
		}
		cmd := f.command("x", command.KindChat, stubborn)
		cmd.ErrorPolicy = command.ErrorAbort
		f.commands.put(cmd)
		in, _ := f.engine.Instance(f.start(cmd, engine.Params{}))
		assert.Equal(t, engine.StateFailed, in.State)
		assert.Equal(t, []engine.ActionError{{Path: []int{1, 1}, Type: "fail", Message: "boom"}}, in.Errors)
	})
}

// TestChildCancel: canceling the instance during a child action ends the
// container with ChildEnd.
func TestChildCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		var got engine.ChildOutcome
		container := action{typ: "group", children: []command.Action{f.journal.hold("hold", "held", release)}}
		container.fn = func(ctx context.Context, run *engine.Run) error {
			got, _ = run.PerformChild(ctx, 0)
			return nil
		}
		instanceID := f.start(f.command("x", command.KindChat, container, f.journal.note("not reached")), engine.Params{})
		require.NoError(t, f.engine.Cancel(t.Context(), instanceID))
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, f.state(instanceID))
		assert.Equal(t, engine.ChildEnd, got)
		assert.Equal(t, []string{"held"}, f.journal.get())
	})
}

// TestChildTimeLimits covers actions.md B8: the time limit of an action
// counts its own time; each child action has its own.
func TestChildTimeLimits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		began := time.Now()
		long := f.start(f.command("long", command.KindChat, group(sleep(50*time.Second), sleep(50*time.Second))), engine.Params{})
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		in, _ := f.engine.Instance(long)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors, "the group's time stands while its child actions run")
		assert.Equal(t, 100*time.Second, in.EndedAt.Sub(began))

		over := f.start(f.command("over", command.KindChat, group(sleep(70*time.Second), f.journal.note("next child"))), engine.Params{})
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		in, _ = f.engine.Instance(over)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []engine.ActionError{{Path: []int{1, 1}, Type: "sleep", Message: "action time limit exceeded after 1m0s"}}, in.Errors)
		assert.Equal(t, []string{"next child"}, f.journal.get())

		// The container's own time adds up across its child actions:
		// 40 s, a child of 10 s, then 20 s more reach its limit of 60 s.
		slow := action{typ: "slow", children: []command.Action{sleep(10 * time.Second)}}
		slow.fn = func(ctx context.Context, run *engine.Run) error {
			time.Sleep(40 * time.Second)
			if _, err := run.PerformChild(ctx, 0); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		began = time.Now()
		slowID := f.start(f.command("slow", command.KindChat, slow), engine.Params{})
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		in, _ = f.engine.Instance(slowID)
		assert.Equal(t, []engine.ActionError{{Path: []int{1}, Type: "slow", Message: "action time limit exceeded after 1m0s"}}, in.Errors)
		assert.Equal(t, 70*time.Second, in.EndedAt.Sub(began))
	})
}

// TestLimitTo covers actions.md B8: an action sets its own time limit when
// it knows it, counted from then.
func TestLimitTo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		waitFor := func(d time.Duration) action {
			return action{typ: "wait", fn: func(ctx context.Context, run *engine.Run) error {
				time.Sleep(30 * time.Second) // e.g. rendering the duration
				if err := run.LimitTo(d + 5*time.Second); err != nil {
					return err
				}
				return sleep(d).fn(ctx, run)
			}}
		}
		began := time.Now()
		instanceID := f.start(f.command("x", command.KindChat, waitFor(90*time.Second)), engine.Params{})
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		in, _ := f.engine.Instance(instanceID)
		assert.Empty(t, in.Errors)
		assert.Equal(t, 120*time.Second, in.EndedAt.Sub(began))

		late := action{typ: "late", fn: func(ctx context.Context, run *engine.Run) error {
			<-ctx.Done()
			err := run.LimitTo(time.Hour)
			assert.ErrorIs(t, err, engine.ErrTimeLimit, "the limit has run out already")
			return err
		}}
		in, _ = f.engine.Instance(f.start(f.command("late", command.KindChat, late), engine.Params{}))
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		in, _ = f.engine.Instance(in.ID)
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, engine.ErrTimeLimit.Error())
	})
}

// TestCallTimeStands covers actions.md B8: the time a command action waits
// for the called command does not count for its own time limit.
func TestCallTimeStands(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		called := f.command("called", command.KindActionGroup, sleep(50*time.Second), sleep(50*time.Second))
		instanceID := f.start(f.command("caller", command.KindChat, f.call(called.ID, wait())), engine.Params{})
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
	})
}

// TestPath covers actions.md B9: Run.Path names the running action.
func TestPath(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		where := action{typ: "where", fn: func(_ context.Context, run *engine.Run) error {
			f.journal.add(fmt.Sprint(run.Path()))
			return nil
		}}
		f.start(f.command("x", command.KindChat, where, group(f.journal.note("n"), group(where))), engine.Params{})
		assert.Equal(t, []string{"[1]", "n", "[2 2 1]"}, f.journal.get())
	})
}

// TestPerformChildErrors: an index that is not a child action is an error.
func TestPerformChildErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		var errs []error
		probe := action{typ: "probe", children: []command.Action{f.journal.note("child")}}
		probe.fn = func(ctx context.Context, run *engine.Run) error {
			for _, i := range []int{-1, 1} {
				_, err := run.PerformChild(ctx, i)
				errs = append(errs, err)
			}
			return nil
		}
		f.start(f.command("x", command.KindChat, probe), engine.Params{})
		require.Len(t, errs, 2)
		for _, err := range errs {
			assert.ErrorIs(t, err, engine.ErrInvalidChild)
		}
		assert.Empty(t, f.journal.get())
	})
}

// TestMissingCapability covers actions.md B7: an action whose type needs a
// capability the core does not have fails without running.
func TestMissingCapability(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		types := &actionTypes{missing: map[string][]capability.Capability{
			"file": {capability.HostFS},
		}}
		f := newFixtureWithTypes(t, settings.LockPerCommandType, types)
		defer f.stop()

		file := action{typ: "file", fn: func(context.Context, *engine.Run) error {
			f.journal.add("file ran")
			return nil
		}}
		instanceID := f.start(f.command("x", command.KindChat,
			file, off(file), group(file), f.journal.note("after")), engine.Params{})
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []string{"after"}, f.journal.get())
		assert.Equal(t, []engine.ActionError{
			{Path: []int{1}, Type: "file", Message: "missing capability: host:fs"},
			{Path: []int{3, 1}, Type: "file", Message: "missing capability: host:fs"},
		}, in.Errors, "an inactive action needs no capability")
	})
}
