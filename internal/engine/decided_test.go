// SPDX-License-Identifier: MIT

package engine_test

import (
	"errors"
	"log/slog"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/settings"
)

// TestDecided covers requirements.md, B61: once the decision about a
// triggered command is final, the engine tells the requirements: after the
// runs were queued, after the user was told about a rejection, and while a
// threshold waits; not without a decision, nor for a call.
func TestDecided(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs))
		defer f.stop()

		var queued []string
		reqs.onDecided = func(command.Command) {
			queued = nil
			for _, in := range f.engine.History() {
				queued = append(queued, in.CommandName)
			}
		}
		_, err := f.trigger(f.command("met", command.KindChat, f.journal.note("met")), engine.Params{})
		require.NoError(t, err)
		assert.Contains(t, queued, "met", "after the run was queued")

		reqs.decide("no", rejectedBy("role"), false)
		reqs.decide("later", engine.Waiting(), false)
		for _, name := range []string{"no", "later"} {
			_, err := f.trigger(f.command(name, command.KindChat), engine.Params{})
			require.NoError(t, err)
		}
		assert.Equal(t, []string{"decided met", "notified no", "decided no", "decided later"}, reqs.trailed(),
			"a rejection after telling the user")

		disabled := f.command("off", command.KindChat)
		disabled.Enabled = false
		_, err = f.trigger(disabled, engine.Params{})
		require.NoError(t, err)
		reqs.mu.Lock()
		reqs.err = errors.New("store gone")
		reqs.mu.Unlock()
		_, err = f.trigger(f.command("broken", command.KindChat), engine.Params{})
		require.Error(t, err)
		reqs.mu.Lock()
		reqs.err = nil
		reqs.mu.Unlock()
		called := f.command("called", command.KindActionGroup)
		f.start(f.command("caller", command.KindChat, f.call(called.ID, engine.CallOptions{CheckRequirements: true})), engine.Params{})
		assert.Contains(t, reqs.decidedFor(), "called", "the call was decided")
		assert.Len(t, reqs.trailed(), 4, "not without a decision, nor for a disabled command or a call")
	})
}

// TestDecidedWhenNotQueued: a met command that cannot be queued, because
// the core stops meanwhile, is decided all the same.
func TestDecidedWhenNotQueued(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs))
		defer f.stop()

		reqs.gate = make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := f.engine.Trigger(t.Context(), engine.Request{Command: f.command("late", command.KindChat), Source: engine.SourceChat})
			result <- err
		}()
		synctest.Wait()
		go f.stop()
		synctest.Wait()
		close(reqs.gate)
		require.ErrorIs(t, <-result, engine.ErrClosed)
		assert.Equal(t, []string{"decided late"}, reqs.trailed())
	})
}

// TestDecidedAfterTurn: what the requirements do after a decision, e.g.
// deleting a message, does not hold up the decisions after it (B16), and a
// failure is logged without changing the outcome.
func TestDecidedAfterTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := newRequirements()
		logs := &records{}
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs), engine.WithLogger(slog.New(logs)))
		defer f.stop()

		hold := make(chan struct{})
		reqs.onDecided = func(cmd command.Command) {
			if cmd.Name == "slow" {
				<-hold
			}
		}
		out := make(outcomes, 2)
		f.triggerAsync(f.command("slow", command.KindChat), out)
		f.triggerAsync(f.command("next", command.KindChat), out)
		assert.Equal(t, []string{"slow", "next"}, reqs.decidedFor())
		assert.Equal(t, []string{"next queued"}, out.drain(1))
		close(hold)
		assert.Equal(t, []string{"slow queued"}, out.drain(1))

		reqs.mu.Lock()
		reqs.onDecided, reqs.decidedErr = nil, errors.New("message gone")
		reqs.mu.Unlock()
		res, err := f.trigger(f.command("failing", command.KindChat), engine.Params{})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		assert.True(t, slices.Contains(logs.messages(), "finishing the requirements after the decision failed"))
	})
}
