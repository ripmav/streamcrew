// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// TestLifecycle covers B1, B2 and B61: an instance is queued, starts, runs
// its actions in order and completes; each change is an event.
func TestLifecycle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		ada := &user.User{ID: id.New(), Identities: []user.Identity{{Platform: platform.Twitch, Login: "ada", DisplayName: "Ada"}}}
		hug := f.command("hug", command.KindChat, f.journal.note("one"), f.journal.note("two"))
		queued := time.Now()
		instanceID := f.start(hug, engine.Params{Platform: platform.Twitch, User: ada, Args: []string{"@bob"}, ArgsText: "@bob"})

		assert.Equal(t, []string{"one", "two"}, f.journal.get())
		assert.Equal(t, []string{"queued hug", "started hug", "completed hug"}, f.events())
		in, ok := f.engine.Instance(instanceID)
		require.True(t, ok)
		assert.Equal(t, engine.Instance{
			ID:          instanceID,
			CommandID:   hug.ID,
			CommandName: "hug",
			Source:      engine.SourceManual,
			State:       engine.StateCompleted,
			Platform:    platform.Twitch,
			UserID:      ada.ID,
			UserName:    "Ada",
			Args:        []string{"@bob"},
			QueuedAt:    queued,
			StartedAt:   queued,
			EndedAt:     queued,
			Errors:      []engine.ActionError{},
		}, in)
		assert.True(t, in.State.Final())
		assert.False(t, engine.StateRunning.Final())
	})
}

// TestErrorPolicy covers B4 and B71: under "continue" the next action runs
// and the instance completes with the error; under "abort" it fails.
func TestErrorPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		policy command.ErrorPolicy
		state  engine.State
		lines  []string
		event  string
	}{
		{command.ErrorContinue, engine.StateCompleted, []string{"fail", "after"}, "completed x"},
		{command.ErrorAbort, engine.StateFailed, []string{"fail"}, "failed x"},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockPerCommandType)
				defer f.stop()

				cmd := f.command("x", command.KindChat, f.journal.fail("fail"), f.journal.note("after"))
				cmd.ErrorPolicy = tc.policy
				instanceID := f.start(cmd, engine.Params{})

				assert.Equal(t, tc.lines, f.journal.get())
				assert.Equal(t, []string{"queued x", "started x", tc.event}, f.events())
				in, _ := f.engine.Instance(instanceID)
				assert.Equal(t, tc.state, in.State)
				assert.Equal(t, []engine.ActionError{{Position: 1, Type: "fail", Message: "boom"}}, in.Errors)
			})
		})
	}
}

// TestStop covers B4: an action that ends its command completes the
// instance; later actions do not run.
func TestStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		stop := action{typ: "stop", fn: func(context.Context, *engine.Run) error {
			return fmt.Errorf("end: %w", engine.ErrStop)
		}}
		cmd := f.command("x", command.KindChat, f.journal.note("before"), stop, f.journal.note("after"))
		cmd.ErrorPolicy = command.ErrorAbort
		instanceID := f.start(cmd, engine.Params{})

		assert.Equal(t, []string{"before"}, f.journal.get())
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
	})
}

// TestUnknownActionSkipped covers B70.
func TestUnknownActionSkipped(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		cmd := f.command("x", command.KindChat, unknown{typ: "obs.scene"}, f.journal.note("after"))
		cmd.ErrorPolicy = command.ErrorAbort
		instanceID := f.start(cmd, engine.Params{})

		assert.Equal(t, []string{"after"}, f.journal.get())
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
	})
}

// TestPanickingAction: a panic is the error of the action, not of the core.
func TestPanickingAction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		bad := action{typ: "bad", fn: func(context.Context, *engine.Run) error { panic("oops") }}
		instanceID := f.start(f.command("x", command.KindChat, bad, f.journal.note("after")), engine.Params{})

		assert.Equal(t, []string{"after"}, f.journal.get())
		in, _ := f.engine.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Equal(t, []engine.ActionError{{Position: 1, Type: "bad", Message: "action panicked: oops"}}, in.Errors)
	})
}

// TestNoActions covers B29 and B107: a command without actions needs no lock
// and completes at once.
func TestNoActions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("busy", command.KindChat, f.journal.hold("x", "busy", release)), engine.Params{})
		empty := f.start(f.command("empty", command.KindChat), engine.Params{})

		assert.Equal(t, engine.StateCompleted, f.state(empty))
	})
}

// TestVersionAtQueue covers B3 and B101: an instance runs the command as it
// was when it was queued.
func TestVersionAtQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		release := make(chan struct{})
		f.start(f.command("busy", command.KindChat, f.journal.hold("x", "busy", release)), engine.Params{})
		cmd := f.command("x", command.KindChat, f.journal.note("old"))
		waiting := f.start(cmd, engine.Params{})

		cmd.Actions[0] = f.journal.note("changed")
		f.commands.delete(cmd.ID)
		close(release)
		synctest.Wait()

		assert.Equal(t, []string{"busy", "old"}, f.journal.get())
		assert.Equal(t, engine.StateCompleted, f.state(waiting))
	})
}

// TestLockModes covers B20 to B25 and B27: which instances run side by side
// and which wait.
func TestLockModes(t *testing.T) {
	t.Parallel()
	type start struct {
		name     string
		kind     command.Kind
		types    []string
		unlocked bool
		running  bool
	}
	for _, tc := range []struct {
		mode   settings.LockMode
		starts []start
	}{
		{settings.LockPerCommandType, []start{
			{name: "chat1", kind: command.KindChat, types: []string{"chat"}, running: true},
			{name: "chat2", kind: command.KindChat, types: []string{"chat"}},
			{name: "event", kind: command.KindEvent, types: []string{"chat"}, running: true},
			{name: "unlocked", kind: command.KindChat, types: []string{"chat"}, unlocked: true, running: true},
		}},
		{settings.LockPerActionType, []start{
			{name: "chat", kind: command.KindChat, types: []string{"chat"}, running: true},
			{name: "sound", kind: command.KindEvent, types: []string{"sound"}, running: true},
			{name: "chat again", kind: command.KindTimer, types: []string{"chat"}},
			{name: "overlay", kind: command.KindChat, types: []string{"overlay"}, running: true},
		}},
		{settings.LockVisualAudio, []start{
			{name: "sound", kind: command.KindChat, types: []string{"chat", "sound"}, running: true},
			{name: "overlay", kind: command.KindEvent, types: []string{"overlay"}},
			{name: "chat", kind: command.KindChat, types: []string{"chat"}, running: true},
			{name: "chat again", kind: command.KindChat, types: []string{"chat"}, running: true},
		}},
		{settings.LockSingular, []start{
			{name: "chat", kind: command.KindChat, types: []string{"chat"}, running: true},
			{name: "event", kind: command.KindEvent, types: []string{"sound"}},
			{name: "unlocked", kind: command.KindTimer, types: []string{"chat"}, unlocked: true, running: true},
		}},
		{settings.LockNone, []start{
			{name: "chat1", kind: command.KindChat, types: []string{"chat"}, running: true},
			{name: "chat2", kind: command.KindChat, types: []string{"chat"}, running: true},
		}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				visualAudio := engine.WithVisualAudio(func(typ string) bool { return typ == "sound" || typ == "overlay" })
				f := newFixture(t, tc.mode, visualAudio)
				defer f.stop()

				release := make(chan struct{})
				defer close(release)
				ids := make([]id.ID, len(tc.starts))
				for i, s := range tc.starts {
					actions := make([]command.Action, 0, len(s.types))
					for _, typ := range s.types {
						actions = append(actions, f.journal.hold(typ, s.name, release))
					}
					cmd := f.command(s.name, s.kind, actions...)
					cmd.Unlocked = s.unlocked
					ids[i] = f.start(cmd, engine.Params{})
				}
				for i, s := range tc.starts {
					want := engine.StatePending
					if s.running {
						want = engine.StateRunning
					}
					assert.Equal(t, want, f.state(ids[i]), s.name)
				}
			})
		})
	}
}

// TestNoOvertaking covers B26 and B104: instances start in queue order, and
// a later one does not overtake an earlier one that waits for one of its
// locks, even if its own locks are free.
func TestNoOvertaking(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerActionType)
		defer f.stop()

		chat := make(chan struct{})
		sound := make(chan struct{})
		f.start(f.command("holder", command.KindChat, f.journal.hold("chat", "holder", chat)), engine.Params{})
		a := f.start(f.command("a", command.KindChat,
			f.journal.hold("chat", "a", sound), f.journal.hold("sound", "a sound", sound)), engine.Params{})
		b := f.start(f.command("b", command.KindChat, f.journal.hold("sound", "b", sound)), engine.Params{})
		c := f.start(f.command("c", command.KindChat, f.journal.hold("chat", "c", sound)), engine.Params{})
		overlay := f.start(f.command("overlay", command.KindChat, f.journal.note("overlay")), engine.Params{})

		assert.Equal(t, engine.StatePending, f.state(a))
		assert.Equal(t, engine.StatePending, f.state(b), "B waits behind A although sound is free")
		assert.Equal(t, engine.StatePending, f.state(c))
		assert.Equal(t, engine.StateCompleted, f.state(overlay), "no lock in common with the waiting ones")

		close(chat)
		synctest.Wait()
		assert.Equal(t, engine.StateRunning, f.state(a))
		assert.Equal(t, engine.StatePending, f.state(b))
		assert.Equal(t, engine.StatePending, f.state(c))

		close(sound)
		synctest.Wait()
		lines := f.journal.get()
		require.Len(t, lines, 6)
		assert.Equal(t, []string{"holder", "overlay", "a", "a sound"}, lines[:4])
		assert.ElementsMatch(t, []string{"b", "c"}, lines[4:], "B and C have no lock in common")
	})
}

// TestNestedActionLocks covers B22 and B23: the actions inside other
// actions count for the locks.
func TestNestedActionLocks(t *testing.T) {
	t.Parallel()
	for _, mode := range []settings.LockMode{settings.LockPerActionType, settings.LockVisualAudio} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, mode, engine.WithVisualAudio(func(typ string) bool { return typ == "sound" }))
				defer f.stop()

				release := make(chan struct{})
				defer close(release)
				f.start(f.command("holder", command.KindChat, f.journal.hold("sound", "holder", release)), engine.Params{})
				group := action{typ: "group", children: []command.Action{
					action{typ: "condition", children: []command.Action{action{typ: "sound"}}},
				}}
				nested := f.start(f.command("nested", command.KindEvent, group), engine.Params{})

				assert.Equal(t, engine.StatePending, f.state(nested))
			})
		})
	}
}

// TestLockModeChange covers B28: a new lock mode applies to instances queued
// afterwards.
func TestLockModeChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		before := f.start(f.command("before", command.KindEvent, f.journal.note("before")), engine.Params{})
		f.configs.lockMode(settings.LockNone)
		after := f.start(f.command("after", command.KindEvent, f.journal.note("after")), engine.Params{})

		assert.Equal(t, engine.StatePending, f.state(before))
		assert.Equal(t, engine.StateCompleted, f.state(after))
	})
}

// TestPause covers B40: while paused, no queued instance starts, not even an
// unlocked one; running ones go on; after resuming, the queued ones start in
// their order.
func TestPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		release := make(chan struct{})
		running := f.start(f.command("running", command.KindChat,
			f.journal.hold("x", "running", release), f.journal.note("running goes on")), engine.Params{})
		require.NoError(t, f.engine.Pause(t.Context(), engine.PauseAll))
		require.NoError(t, f.engine.Pause(t.Context(), engine.PauseAll))
		assert.True(t, paused(t, f.engine, engine.PauseAll))

		unlocked := f.command("unlocked", command.KindEvent, f.journal.note("unlocked"))
		unlocked.Unlocked = true
		first := f.start(unlocked, engine.Params{})
		second := f.start(f.command("timer", command.KindTimer, f.journal.note("timer")), engine.Params{})
		close(release)
		synctest.Wait()

		assert.Equal(t, engine.StateCompleted, f.state(running))
		assert.Equal(t, engine.StatePending, f.state(first))
		assert.Equal(t, engine.StatePending, f.state(second))

		require.NoError(t, f.engine.Resume(t.Context(), engine.PauseAll))
		require.NoError(t, f.engine.Resume(t.Context(), engine.PauseAll))
		synctest.Wait()
		assert.False(t, paused(t, f.engine, engine.PauseAll))

		require.ErrorIs(t, f.engine.Pause(t.Context(), "timers"), engine.ErrUnknownPauseScope)
		require.ErrorIs(t, f.engine.Resume(t.Context(), "timers"), engine.ErrUnknownPauseScope)
		_, err := f.engine.Paused("timers")
		require.ErrorIs(t, err, engine.ErrUnknownPauseScope)
		lines := f.journal.get()
		require.Len(t, lines, 4)
		assert.Equal(t, []string{"running", "running goes on"}, lines[:2])
		assert.ElementsMatch(t, []string{"unlocked", "timer"}, lines[2:], "both start after resuming")
		events := f.events()
		require.Len(t, events, 11)
		assert.Equal(t, []string{
			"queued running", "started running", "paused all", "queued unlocked", "queued timer",
			"completed running", "resumed all", "started unlocked", "started timer",
		}, events[:9], "in queue order")
		assert.ElementsMatch(t, []string{"completed unlocked", "completed timer"}, events[9:])
	})
}

// TestCancel covers B2, B50 and B103.
func TestCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()
		ctx := t.Context()

		release := make(chan struct{})
		running := f.start(f.command("running", command.KindChat,
			f.journal.hold("x", "running", release), f.journal.note("not reached")), engine.Params{})
		pending := f.start(f.command("pending", command.KindChat, f.journal.note("pending")), engine.Params{})
		next := f.start(f.command("next", command.KindChat, f.journal.note("next")), engine.Params{})
		f.events()

		require.NoError(t, f.engine.Cancel(ctx, pending))
		assert.Equal(t, engine.StateCanceled, f.state(pending), "a queued instance ends at once")
		assert.Equal(t, []string{"canceled pending"}, f.events())

		require.NoError(t, f.engine.Cancel(ctx, running))
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, f.state(running))
		assert.Equal(t, engine.StateCompleted, f.state(next))
		assert.Equal(t, []string{"running", "next"}, f.journal.get())
		assert.Equal(t, []string{"canceled running", "started next", "completed next"}, f.events())

		// B103: canceling an ended instance does nothing.
		require.NoError(t, f.engine.Cancel(ctx, next))
		require.NoError(t, f.engine.Cancel(ctx, running))
		assert.Equal(t, engine.StateCompleted, f.state(next))
		assert.Empty(t, f.events())

		require.ErrorIs(t, f.engine.Cancel(ctx, id.New()), engine.ErrNotFound)
		close(release)
	})
}

// TestCancelWhileWaitingForAPI covers B109: an action that returns after the
// cancellation does not change the end state.
func TestCancelWhileWaitingForAPI(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		answer := make(chan struct{})
		call := action{typ: "api", fn: func(ctx context.Context, _ *engine.Run) error {
			<-ctx.Done()
			<-answer // the API answers late
			return nil
		}}
		instanceID := f.start(f.command("x", command.KindChat, call, f.journal.note("after")), engine.Params{})
		require.NoError(t, f.engine.Cancel(t.Context(), instanceID))
		synctest.Wait()
		assert.Equal(t, engine.StateRunning, f.state(instanceID), "until the action returns")

		close(answer)
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, f.state(instanceID))
		assert.Empty(t, f.journal.get())
	})
}

// TestCancelAll covers B51.
func TestCancelAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		ids := []id.ID{
			f.start(f.command("a", command.KindChat, f.journal.hold("x", "a", release)), engine.Params{}),
			f.start(f.command("b", command.KindChat, f.journal.note("b")), engine.Params{}),
			f.start(f.command("c", command.KindChat, f.journal.note("c")), engine.Params{}),
		}
		f.engine.CancelAll(t.Context())
		synctest.Wait()

		for _, instanceID := range ids {
			assert.Equal(t, engine.StateCanceled, f.state(instanceID))
		}
		assert.Equal(t, []string{"a"}, f.journal.get())
	})
}

// TestTimeLimit covers B72: an action that runs past its time limit fails.
func TestTimeLimit(t *testing.T) {
	t.Parallel()
	hang := action{typ: "hang", fn: func(ctx context.Context, _ *engine.Run) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	for _, tc := range []struct {
		name string
		hang command.Action
		want time.Duration
	}{
		{"default", hang, engine.DefaultTimeLimit},
		{"own", limited{action: hang, limit: 5 * time.Minute}, 5 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockPerCommandType)
				defer f.stop()

				began := time.Now()
				instanceID := f.start(f.command("x", command.KindChat, tc.hang, f.journal.note("after")), engine.Params{})
				time.Sleep(tc.want)
				synctest.Wait()

				in, _ := f.engine.Instance(instanceID)
				assert.Equal(t, engine.StateCompleted, in.State)
				assert.Equal(t, tc.want, in.EndedAt.Sub(began))
				assert.Equal(t, []string{"after"}, f.journal.get())
				require.Len(t, in.Errors, 1)
				assert.Equal(t, "action time limit exceeded after "+tc.want.String(), in.Errors[0].Message)
			})
		})
	}
}

// TestInvalidTimeLimit: an action whose own time limit is not positive
// fails without running.
func TestInvalidTimeLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		for _, limit := range []time.Duration{0, -time.Second} {
			instanceID := f.start(f.command("x", command.KindChat, limited{action: f.journal.note("ran"), limit: limit}), engine.Params{})
			in, _ := f.engine.Instance(instanceID)
			require.Len(t, in.Errors, 1)
			assert.Contains(t, in.Errors[0].Message, engine.ErrInvalidTimeLimit.Error())
		}
		assert.Empty(t, f.journal.get())
	})
}

// TestQueueFull covers B15.
func TestQueueFull(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockSingular)
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		waiting := f.command("waiting", command.KindChat, f.journal.note("waiting"))
		for range engine.MaxPending {
			_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
			require.NoError(t, err)
		}
		_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
		require.ErrorIs(t, err, engine.ErrQueueFull)
	})
}

// TestHistory covers B60: the history keeps the last HistorySize instances.
func TestHistory(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		cmd := f.command("x", command.KindChat)
		ids := make([]id.ID, engine.HistorySize+5)
		for i := range ids {
			ids[i] = f.start(cmd, engine.Params{})
		}
		h := f.engine.History()
		require.Len(t, h, engine.HistorySize)
		assert.Equal(t, ids[5], h[0].ID, "oldest first")
		assert.Equal(t, ids[len(ids)-1], h[len(h)-1].ID)
		_, ok := f.engine.Instance(ids[4])
		assert.False(t, ok, "dropped from the history")
		replayed := f.engine.Replay(t.Context(), ids[4])
		require.Len(t, replayed, 1)
		require.ErrorIs(t, replayed[0].Err, engine.ErrNotFound)
	})
}

// TestReplay covers B54 and B102: a replay is a new instance with the same
// parameters and the current version of the command.
func TestReplay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()
		ctx := t.Context()

		echo := func(label string) action {
			return action{typ: "echo", fn: func(_ context.Context, run *engine.Run) error {
				f.journal.add(label + " " + run.Params().Args[0])
				return nil
			}}
		}
		cmd := f.command("x", command.KindChat, echo("old"))
		first := f.start(cmd, engine.Params{Args: []string{"a"}, ArgsText: "a"})
		gone := f.command("gone", command.KindChat)
		deleted := f.start(gone, engine.Params{})

		cmd.Actions = []command.Action{echo("new")}
		cmd.Enabled = false
		f.commands.put(cmd)
		f.commands.delete(gone.ID)
		f.events()

		replayed := f.engine.Replay(ctx, first, deleted, first)
		synctest.Wait()
		require.Len(t, replayed, 3)
		assert.Equal(t, []engine.Replayed{{From: deleted, Err: replayed[1].Err}}, replayed[1:2])
		require.ErrorIs(t, replayed[1].Err, errNoCommand, "B102")

		assert.Equal(t, []string{"old a", "new a", "new a"}, f.journal.get())
		for _, r := range []engine.Replayed{replayed[0], replayed[2]} {
			require.NoError(t, r.Err)
			assert.Equal(t, first, r.From)
			in, _ := f.engine.Instance(r.Instance)
			assert.Equal(t, engine.SourceReplay, in.Source)
			assert.Equal(t, []string{"a"}, in.Args)
			assert.Equal(t, engine.StateCompleted, in.State)
		}
		assert.Len(t, f.events(), 6, "queued, started and completed per replay")
	})
}

// TestScope covers B80: the actions see the parameters in the scope of the
// templates and share the values of the run.
func TestScope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		loc := time.FixedZone("test", 3600)
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()
		f.configs.mu.Lock()
		f.configs.cfg.Commands.ArgDelimiter = ";"
		f.configs.cfg.Location = loc
		f.configs.mu.Unlock()

		render := template.New(nil)
		set := action{typ: "set", fn: func(_ context.Context, run *engine.Run) error {
			run.Scope().SetValue("mood", template.TextValue("happy"))
			return nil
		}}
		read := action{typ: "read", fn: func(ctx context.Context, run *engine.Run) error {
			s := run.Scope()
			assert.Equal(t, "greet", s.CommandName)
			assert.Equal(t, ";", s.ArgDelimiter)
			assert.Equal(t, loc, s.Location)
			assert.Equal(t, "greet", run.Command().Name)
			assert.False(t, run.InstanceID().IsZero())
			assert.Same(t, run.Params().User, s.Target, "without a target from the caller, the triggering user (B81)")
			text, err := render.Render(ctx, template.Parse("$raid $mood"), s, template.Text)
			f.journal.add(text)
			f.journal.add(s.Message + " / " + run.Params().Emotes[0])
			return err
		}}
		f.start(f.command("greet", command.KindEvent, set, read), engine.Params{
			User:    &user.User{ID: id.New()},
			Message: "hello Kappa",
			Emotes:  []string{"Kappa"},
			Values:  map[string]template.Value{"raid": template.IntValue(5)},
		})

		assert.Equal(t, []string{"5 happy", "hello Kappa / Kappa"}, f.journal.get())
	})
}

// TestShutdown covers B55: queued instances end as canceled, running ones
// get the shutdown timeout, and no new instance is taken.
func TestShutdown(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		finishIn time.Duration
		want     engine.State
	}{
		{"within the timeout", 5 * time.Second, engine.StateCompleted},
		{"after the timeout", time.Minute, engine.StateCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockSingular)
				defer f.stop()

				release := make(chan struct{})
				running := f.start(f.command("running", command.KindChat, f.journal.hold("x", "running", release)), engine.Params{})
				pending := f.start(f.command("pending", command.KindChat, f.journal.note("pending")), engine.Params{})
				stopped := make(chan struct{})
				go func() {
					f.stop()
					close(stopped)
				}()
				synctest.Wait()

				assert.Equal(t, engine.StateCanceled, f.state(pending))
				assert.Equal(t, engine.StateRunning, f.state(running))
				_, err := f.engine.Start(t.Context(), f.command("late", command.KindChat), engine.Params{})
				require.ErrorIs(t, err, engine.ErrClosed)

				time.Sleep(tc.finishIn)
				close(release)
				<-stopped
				assert.Equal(t, tc.want, f.state(running))
			})
		})
	}
}

// TestNotRunning: instances are only taken while Run runs, and Run runs
// once.
func TestNotRunning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, err := engine.New(&commandStore{})
		require.NoError(t, err)
		cmd := command.Command{Name: "x", Kind: command.KindChat, ErrorPolicy: command.ErrorContinue}
		_, err = e.Start(t.Context(), cmd, engine.Params{})
		require.ErrorIs(t, err, engine.ErrNotRunning)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		require.NoError(t, e.Run(ctx))
		require.ErrorIs(t, e.Run(ctx), engine.ErrAlreadyRunning)
		_, err = e.Start(t.Context(), cmd, engine.Params{})
		require.ErrorIs(t, err, engine.ErrClosed)
	})
}

// TestSettingsError: an instance is not queued without valid settings.
func TestSettingsError(t *testing.T) {
	t.Parallel()
	broken := errors.New("disk on fire")
	withLockMode := func(m settings.LockMode) engine.Config {
		cfg := engine.DefaultConfig()
		cfg.Commands.LockMode = m
		return cfg
	}
	for _, tc := range []struct {
		name string
		cfg  engine.Config
		err  error
		want error
	}{
		{"unreadable", engine.DefaultConfig(), broken, broken},
		{"no time zone", engine.Config{Commands: settings.DefaultCommands()}, nil, engine.ErrInvalidConfig},
		{"no lock mode", withLockMode(""), nil, engine.ErrInvalidConfig},
		{"unknown lock mode", withLockMode("per_user"), nil, engine.ErrInvalidConfig},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newFixture(t, settings.LockPerCommandType,
					engine.WithConfig(func(context.Context) (engine.Config, error) { return tc.cfg, tc.err }))
				defer f.stop()

				_, err := f.engine.Start(t.Context(), f.command("x", command.KindChat), engine.Params{})
				require.ErrorIs(t, err, tc.want)
				assert.Empty(t, f.engine.History())
			})
		})
	}
}

// TestInvalidRun: the engine does not queue a command it cannot run or
// parameters that contradict each other.
func TestInvalidRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		for _, tc := range []struct {
			name string
			edit func(cmd *command.Command, p *engine.Params)
			want error
		}{
			{"unknown kind", func(cmd *command.Command, _ *engine.Params) { cmd.Kind = "webhook" }, engine.ErrInvalidCommand},
			{"no error policy", func(cmd *command.Command, _ *engine.Params) { cmd.ErrorPolicy = "" }, engine.ErrInvalidCommand},
			{"empty action", func(cmd *command.Command, _ *engine.Params) { cmd.Actions = []command.Action{nil} }, engine.ErrInvalidCommand},
			{"arguments without text", func(_ *command.Command, p *engine.Params) { p.Args = []string{"a"} }, engine.ErrInvalidParams},
			{"emotes without message", func(_ *command.Command, p *engine.Params) { p.Emotes = []string{"Kappa"} }, engine.ErrInvalidParams},
		} {
			cmd := f.command("x", command.KindChat)
			var p engine.Params
			tc.edit(&cmd, &p)
			_, err := f.engine.Start(t.Context(), cmd, p)
			require.ErrorIs(t, err, tc.want, tc.name)
		}
		assert.Empty(t, f.engine.History())
	})
}

// TestOptions: an option without a value is an error, not a default.
func TestOptions(t *testing.T) {
	t.Parallel()
	for name, opt := range map[string]engine.Option{
		"logger":           engine.WithLogger(nil),
		"publisher":        engine.WithPublisher(nil),
		"settings":         engine.WithConfig(nil),
		"visual and audio": engine.WithVisualAudio(nil),
		"no timeout":       engine.WithShutdownTimeout(0),
	} {
		_, err := engine.New(&commandStore{}, opt)
		require.ErrorIs(t, err, engine.ErrInvalidOption, name)
	}
	_, err := engine.New(nil)
	require.ErrorIs(t, err, engine.ErrInvalidOption, "commands")
}

// TestLogs: skipped actions, dropped commands and failed events are logged.
func TestLogs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		logs := &records{}
		broken := publisherFunc(func(context.Context, event.Envelope) error { return errors.New("bus gone") })
		f := newFixture(t, settings.LockSingular, engine.WithLogger(slog.New(logs)), engine.WithPublisher(broken))
		defer f.stop()

		release := make(chan struct{})
		defer close(release)
		f.start(f.command("unknown", command.KindChat, unknown{typ: "obs.scene"}), engine.Params{})
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		waiting := f.command("waiting", command.KindChat, f.journal.note("waiting"))
		for range engine.MaxPending {
			_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
			require.NoError(t, err)
		}
		_, err := f.engine.Start(t.Context(), waiting, engine.Params{})
		require.ErrorIs(t, err, engine.ErrQueueFull)

		msgs := logs.messages()
		assert.Contains(t, msgs, "unknown action type skipped")
		assert.Contains(t, msgs, "command queue full, command dropped")
		assert.Contains(t, msgs, "publishing an event failed")
	})
}

// TestUserName: the history names the user as on the platform of the run,
// or as on its first platform.
func TestUserName(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		cmd := f.command("x", command.KindChat)
		for _, tc := range []struct {
			user *user.User
			want string
		}{
			{&user.User{Identities: []user.Identity{{Platform: platform.Twitch, Login: "ada"}}}, "ada"},
			{&user.User{Identities: []user.Identity{{Platform: platform.Twitch, Login: "ada", DisplayName: "Ada"}}}, "Ada"},
			{&user.User{}, ""},
		} {
			in, _ := f.engine.Instance(f.start(cmd, engine.Params{Platform: platform.YouTube, User: tc.user}))
			assert.Equal(t, tc.want, in.UserName)
		}
	})
}

// TestShutdownTimeout: WithShutdownTimeout sets how long running instances
// may go on (B55).
func TestShutdownTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType, engine.WithShutdownTimeout(time.Second))
		defer f.stop()

		running := f.start(f.command("x", command.KindChat, f.journal.hold("x", "x", nil)), engine.Params{})
		began := time.Now()
		f.stop()
		assert.Equal(t, time.Second, time.Since(began))
		assert.Equal(t, engine.StateCanceled, f.state(running))
	})
}

// TestCancelRightAfterStart: an instance canceled while it gets its locks
// ends as canceled.
func TestCancelRightAfterStart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, settings.LockPerCommandType)
		defer f.stop()

		instanceID, err := f.engine.Start(t.Context(), f.command("x", command.KindChat, f.journal.hold("x", "x", nil)), engine.Params{})
		require.NoError(t, err)
		require.NoError(t, f.engine.Cancel(t.Context(), instanceID))
		synctest.Wait()
		assert.Equal(t, engine.StateCanceled, f.state(instanceID))
	})
}

// TestDefaults: without options, the engine runs with DefaultConfig and
// publishes nothing.
func TestDefaults(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, err := engine.New(&commandStore{})
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			assert.NoError(t, e.Run(ctx))
			close(done)
		}()
		synctest.Wait()

		ran := false
		cmd := command.Command{Name: "x", Kind: command.KindChat, ErrorPolicy: command.ErrorContinue, Actions: []command.Action{
			action{typ: "x", fn: func(_ context.Context, run *engine.Run) error {
				ran = run.Scope().Location == time.UTC && run.Scope().ArgDelimiter == "|"
				return nil
			}},
		}}
		instanceID, err := e.Start(t.Context(), cmd, engine.Params{})
		require.NoError(t, err)
		synctest.Wait()
		in, _ := e.Instance(instanceID)
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.True(t, ran)
		cancel()
		<-done
	})
}
