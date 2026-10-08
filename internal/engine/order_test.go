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
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// outcomes collects the outcomes of triggers that run in goroutines.
type outcomes chan string

// trigger triggers c from the chat in a goroutine and waits until the
// engine has nothing more to do; the outcome goes to out.
func (f *fixture) triggerAsync(c command.Command, out outcomes) {
	f.t.Helper()
	go func() {
		res, err := f.engine.Trigger(f.t.Context(), engine.Request{Command: c, Source: engine.SourceChat})
		if err != nil {
			out <- c.Name + " " + err.Error()
			return
		}
		out <- c.Name + " " + string(res.Outcome)
	}()
	synctest.Wait()
}

// drain returns n outcomes.
func (o outcomes) drain(n int) []string {
	got := make([]string, 0, n)
	for range n {
		got = append(got, <-o)
	}
	return got
}

// TestTriggerOrder covers B16: triggers prepare their decisions at the same
// time, but are decided and queued in the order in which they came; a slow
// preparation holds up the decisions after it, not their preparations.
func TestTriggerOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockSingular, engine.WithRequirements(&failing{requirements: reqs, name: "broken"}))
		defer f.stop()

		release := make(chan struct{})
		f.start(f.command("holder", command.KindChat, f.journal.hold("x", "holder", release)), engine.Params{})
		slow := f.command("slow", command.KindChat, f.journal.note("slow"))
		fast := f.command("fast", command.KindChat, f.journal.note("fast"))
		rejected := f.command("rejected", command.KindChat, f.journal.note("rejected"))
		reqs.decide("rejected", rejectedBy("role"), false)
		unhold := reqs.hold("slow")

		out := make(outcomes, 4)
		f.triggerAsync(slow, out)
		f.triggerAsync(f.command("broken", command.KindChat), out)
		f.triggerAsync(fast, out)
		f.triggerAsync(rejected, out)
		assert.Equal(t, []string{"slow", "fast", "rejected"}, reqs.appliedTo(), "all prepared at the same time")
		assert.Empty(t, reqs.decidedFor(), "the slow one holds up the decisions after it, also past one that failed")

		unhold()
		synctest.Wait()
		assert.Equal(t, []string{"slow", "fast", "rejected"}, reqs.decidedFor())
		got := out.drain(4)
		assert.Contains(t, got, "slow queued")
		assert.Contains(t, got, "fast queued")
		assert.Contains(t, got, "rejected rejected")
		assert.Equal(t, []string{"rejected role"}, reqs.messages())
		close(release)
		synctest.Wait()
		assert.Equal(t, []string{"holder", "slow", "fast"}, f.journal.get(), "queued in the order of the triggers")
	})
}

// failing is a fake of engine.Requirements whose preparation for the
// command name fails.
type failing struct {
	*requirements
	name string
}

func (f *failing) Prepare(ctx context.Context, cmd command.Command, p engine.Params, users engine.Users) (engine.Decide, error) {
	if cmd.Name == f.name {
		return nil, errors.New("prepare failed")
	}
	return f.requirements.Prepare(ctx, cmd, p, users)
}

// TestQueueInTurn covers B16: a trigger lets the next one decide only
// after it was queued.
func TestQueueInTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		types := &blockingTypes{typ: "slow.action", block: make(chan struct{})}
		reqs := newRequirements()
		f := newFixtureWithTypes(t, settings.LockVisualAudio, types, engine.WithRequirements(reqs))
		defer f.stop()

		out := make(outcomes, 2)
		f.triggerAsync(f.command("slow", command.KindChat, unknown{typ: "slow.action"}), out)
		f.triggerAsync(f.command("fast", command.KindChat, f.journal.note("fast")), out)
		assert.Equal(t, []string{"slow"}, reqs.decidedFor(), "the next decides after the one before is queued")
		close(types.block)
		synctest.Wait()
		assert.Equal(t, []string{"slow", "fast"}, reqs.decidedFor())
		assert.ElementsMatch(t, []string{"slow queued", "fast queued"}, out.drain(2))
	})
}

// blockingTypes is a fake of engine.ActionTypes that waits until block is
// closed when asked about the action type typ.
type blockingTypes struct {
	actionTypes
	typ   string
	block chan struct{}
}

func (b *blockingTypes) VisualAudio(actionType string) bool {
	if actionType == b.typ {
		<-b.block
	}
	return b.actionTypes.VisualAudio(actionType)
}

// TestTellAfterTurn covers B16: telling the user about a rejection does not
// hold up the decisions after it.
func TestTellAfterTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		reqs.notifyHold = make(chan struct{})
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs))
		defer f.stop()

		reqs.decide("no", rejectedBy("role"), false)
		out := make(outcomes, 2)
		f.triggerAsync(f.command("no", command.KindChat), out)
		f.triggerAsync(f.command("next", command.KindChat), out)
		assert.Equal(t, []string{"no", "next"}, reqs.decidedFor())
		assert.Equal(t, []string{"next queued"}, out.drain(1))
		close(reqs.notifyHold)
		assert.Equal(t, []string{"no rejected"}, out.drain(1))
	})
}

// rejectedBy returns a rejection of requirement that tells the user.
func rejectedBy(requirement string) engine.Decision {
	return engine.Rejected(engine.Rejection{Requirement: requirement, Reason: reason("no"), Tell: true})
}

// TestSubmit covers B16: Submit returns once the trigger has its place and
// reports the result later; submitted triggers keep their order. Errors
// before the requirements come from Submit, without a result.
func TestSubmit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockSingular, engine.WithRequirements(reqs))
		defer f.stop()

		out := make(outcomes, 3)
		report := func(name string) func(engine.Result, error) {
			return func(res engine.Result, err error) {
				if err != nil {
					out <- name + " " + err.Error()
					return
				}
				out <- name + " " + string(res.Outcome)
			}
		}
		submit := func(c command.Command) error {
			return f.engine.Submit(t.Context(), engine.Request{Command: c, Source: engine.SourceChat}, report(c.Name))
		}
		unhold := reqs.hold("slow")
		require.NoError(t, submit(f.command("slow", command.KindChat, f.journal.note("slow"))))
		require.NoError(t, submit(f.command("fast", command.KindChat, f.journal.note("fast"))))
		synctest.Wait()
		assert.Empty(t, f.journal.get(), "nothing decided while the first is prepared")
		unhold()
		synctest.Wait()
		assert.ElementsMatch(t, []string{"slow queued", "fast queued"}, out.drain(2))
		assert.Equal(t, []string{"slow", "fast"}, f.journal.get())

		disabled := f.command("off", command.KindChat)
		disabled.Enabled = false
		require.NoError(t, submit(disabled))
		assert.Equal(t, []string{"off disabled"}, out.drain(1), "reported before Submit returns")

		err := f.engine.Submit(t.Context(), engine.Request{Command: f.command("x", command.KindChat), Source: engine.SourceManual}, report("x"))
		require.ErrorIs(t, err, engine.ErrInvalidSource)
		err = f.engine.Submit(t.Context(), engine.Request{Command: f.command("x", command.KindChat), Source: engine.SourceChat}, nil)
		require.ErrorIs(t, err, engine.ErrInvalidOption)
		f.stop()
		require.ErrorIs(t, submit(f.command("late", command.KindChat)), engine.ErrClosed)
		synctest.Wait()
		assert.Empty(t, out, "no result after an error")
	})
}

// TestCallInTurn covers B16 for calls: a call that checks requirements is
// decided in its turn after the triggers before it; a call without checks
// has no turn.
func TestCallInTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs))
		defer f.stop()

		called := f.command("called", command.KindActionGroup, f.show("called"))
		unhold := reqs.hold("slow")
		out := make(outcomes, 1)
		f.triggerAsync(f.command("slow", command.KindChat, f.journal.note("slow")), out)
		f.start(f.command("unchecked", command.KindChat, f.call(called.ID, wait())), engine.Params{})
		assert.Equal(t, []string{"called"}, f.journal.get(), "no turn without checks")

		f.start(f.command("checked", command.KindChat,
			f.call(called.ID, engine.CallOptions{Wait: true, CheckRequirements: true})), engine.Params{})
		assert.Equal(t, []string{"called"}, f.journal.get(), "the call waits for the trigger before it")
		unhold()
		synctest.Wait()
		assert.Equal(t, []string{"slow", "called"}, reqs.decidedFor())
		assert.Equal(t, []string{"slow queued"}, out.drain(1))
		assert.ElementsMatch(t, []string{"called", "slow", "called"}, f.journal.get())
	})
}
