// SPDX-License-Identifier: Apache-2.0

package engine_test

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
)

// mediaTypes are the action types of these tests that show a picture or
// play a sound (B23).
func mediaTypes() *actionTypes {
	return visual("sound", "image", "scene")
}

// mediaGap sets the gap after the pictures and sounds of greetings (B43).
func (c *configs) mediaGap(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg.Commands.EntranceMediaGap = polydoc.Duration(d)
}

// at returns an action of type typ that writes line with the time since
// began, e.g. "ada hi 13s". With a positive playback, it says that its
// playback lasts that long (B43).
func (f *fixture) at(typ string, began time.Time, line string, playback time.Duration) action {
	return action{typ: typ, fn: func(_ context.Context, run *engine.Run) error {
		f.journal.add(fmt.Sprintf("%s %s", line, time.Since(began)))
		if playback > 0 {
			run.PlaybackEnds(time.Now().Add(playback))
		}
		return nil
	}}
}

// greet triggers cmd as a greeting and returns its instance.
func (f *fixture) greet(cmd command.Command) id.ID {
	f.t.Helper()
	res, err := f.engine.Trigger(f.t.Context(), engine.Request{Command: cmd, Source: engine.SourceChat, Entrance: true})
	require.NoError(f.t, err)
	require.Equal(f.t, engine.OutcomeQueued, res.Outcome)
	return res.Instances[0]
}

// TestGreetingMediaGap covers B43 and B111: pictures and sounds of
// greetings play one after the other, each after the end of the playback
// before plus the gap, also within one greeting; chat messages and the
// pictures and sounds of other commands do not wait. A change of the gap
// applies to greetings queued afterwards.
func TestGreetingMediaGap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureWithTypes(t, settings.LockNone, mediaTypes())
		defer f.stop()
		began := time.Now()

		f.greet(f.command("ada", command.KindChat,
			f.at("note", began, "ada hi", 0), f.at("sound", began, "ada sound", 8*time.Second)))
		synctest.Wait()
		f.greet(f.command("bob", command.KindChat,
			f.at("note", began, "bob hi", 0), f.at("image", began, "bob image", 0), f.at("sound", began, "bob sound", 3*time.Second)))
		f.greet(f.command("cem", command.KindChat, f.at("note", began, "cem hi", 0)))
		f.start(f.command("jingle", command.KindChat, f.at("sound", began, "jingle", 10*time.Second)), engine.Params{})
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.ElementsMatch(t, []string{
			"ada hi 0s", "ada sound 0s", "bob hi 0s", "cem hi 0s", "jingle 0s",
			"bob image 13s", // 8 s of playback, 5 s gap
			"bob sound 18s", // the image says no playback: 5 s after the action
		}, f.journal.get())

		f.configs.mediaGap(time.Second)
		f.journal = &journal{}
		began = time.Now()
		f.greet(f.command("dan", command.KindChat, f.at("sound", began, "dan sound", 2*time.Second)))
		synctest.Wait()
		f.greet(f.command("eve", command.KindChat, f.at("sound", began, "eve sound", 0)))
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"dan sound 0s", "eve sound 3s"}, f.journal.get())

		// Only the playback of a picture or sound counts: a chat message
		// that names an end is no playback.
		f.journal = &journal{}
		began = time.Now()
		f.greet(f.command("fay", command.KindChat,
			f.at("note", began, "fay hi", 30*time.Second), f.at("sound", began, "fay sound", 0)))
		synctest.Wait()
		f.greet(f.command("gus", command.KindChat, f.at("sound", began, "gus sound", 0)))
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"fay hi 0s", "fay sound 0s", "gus sound 1s"}, f.journal.get())
	})
}

// TestGreetingMediaOrder covers B43 and B112: waiting pictures and sounds
// get their turn in the order they began to wait, and the wait does not
// count for their time limit.
func TestGreetingMediaOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureWithTypes(t, settings.LockNone, mediaTypes())
		defer f.stop()
		began := time.Now()

		var greetings []id.ID
		for i := range 6 {
			name := fmt.Sprintf("user%d", i+1)
			greetings = append(greetings, f.greet(f.command(name, command.KindChat, f.at("sound", began, name, 8*time.Second))))
			synctest.Wait()
		}
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"user1 0s", "user2 13s", "user3 26s", "user4 39s", "user5 52s", "user6 1m5s"}, f.journal.get())
		for _, g := range greetings {
			in, _ := f.engine.Instance(g)
			assert.Equal(t, engine.StateCompleted, in.State)
			assert.Empty(t, in.Errors, "B72: waiting is no part of the time limit")
		}
	})
}

// TestGreetingMediaLocks covers B43: a greeting that waits for its turn
// keeps its locks.
func TestGreetingMediaLocks(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureWithTypes(t, settings.LockPerCommandType, mediaTypes())
		defer f.stop()
		began := time.Now()

		f.greet(f.command("ada", command.KindChat, f.at("sound", began, "ada sound", 8*time.Second)))
		f.greet(f.command("bob", command.KindChat, f.at("sound", began, "bob sound", 0)))
		_, err := f.trigger(f.command("other", command.KindChat, f.at("note", began, "other", 0)), engine.Params{})
		require.NoError(t, err)
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"ada sound 0s", "bob sound 13s", "other 13s"}, f.journal.get())
	})
}

// TestGreetingCancel covers B41, B43 and B113: a greeting canceled while
// its sound waits gives up its turn; CancelEntrance cancels all greetings
// that have not ended and nothing else.
func TestGreetingCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureWithTypes(t, settings.LockNone, mediaTypes())
		defer f.stop()
		ctx := t.Context()
		began := time.Now()

		f.greet(f.command("ada", command.KindChat, f.at("sound", began, "ada sound", 8*time.Second)))
		synctest.Wait()
		bob := f.greet(f.command("bob", command.KindChat, f.at("sound", began, "bob sound", 0)))
		f.greet(f.command("cem", command.KindChat, f.at("sound", began, "cem sound", 0)))
		time.Sleep(3 * time.Second)
		require.NoError(t, f.engine.Cancel(ctx, bob))
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"ada sound 0s", "cem sound 13s"}, f.journal.get())
		assert.Equal(t, engine.StateCanceled, f.state(bob))

		// The stream ends: a sound that waits, a greeting held by the
		// pause and a running one are canceled; other commands go on.
		f.journal = &journal{}
		began = time.Now()
		release := make(chan struct{})
		defer close(release)
		f.greet(f.command("dan", command.KindChat, f.at("sound", began, "dan sound", 8*time.Second)))
		synctest.Wait()
		waiting := f.greet(f.command("eve", command.KindChat, f.at("sound", began, "eve sound", 0)))
		running := f.greet(f.command("fay", command.KindChat, f.journal.hold("note", "fay holds", release)))
		require.NoError(t, f.engine.Pause(ctx, engine.PauseEntrance))
		held := f.greet(f.command("gus", command.KindChat, f.at("note", began, "gus hi", 0)))
		other := f.start(f.command("other", command.KindChat, f.journal.hold("note", "other holds", release)), engine.Params{})
		f.engine.CancelEntrance(ctx)
		synctest.Wait()
		for _, g := range []id.ID{waiting, running, held} {
			assert.Equal(t, engine.StateCanceled, f.state(g))
		}
		assert.Equal(t, engine.StateRunning, f.state(other))
		require.NoError(t, f.engine.Resume(ctx, engine.PauseEntrance))

		hal := f.greet(f.command("hal", command.KindChat, f.at("sound", began, "hal sound", 0)))
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, engine.StateCompleted, f.state(hal))
		assert.ElementsMatch(t, []string{"dan sound 0s", "fay holds", "other holds", "hal sound 13s"}, f.journal.get(),
			"the gap after the last playback still holds")
	})
}

// TestGreetingCalls covers B43: a command that a greeting calls and waits
// for belongs to the greeting; one called without waiting, a start by hand
// and a replay do not. A picture that runs its sound as a child action
// keeps its turn for it; of the ends of playback they name, the latest
// counts.
func TestGreetingCalls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureWithTypes(t, settings.LockNone, mediaTypes())
		defer f.stop()
		ctx := t.Context()
		began := time.Now()

		first := f.greet(f.command("ada", command.KindChat, f.at("sound", began, "ada sound", 8*time.Second)))
		synctest.Wait()
		waited := f.command("waited", command.KindActionGroup, f.at("sound", began, "waited sound", 0))
		queued := f.command("queued", command.KindActionGroup, f.at("sound", began, "queued sound", 0))
		f.greet(f.command("bob", command.KindChat, f.call(waited.ID, wait()), f.call(queued.ID, engine.CallOptions{})))
		f.start(f.command("manual", command.KindChat, f.at("sound", began, "manual sound", 0)), engine.Params{})
		replays := f.engine.Replay(ctx, first)
		require.NoError(t, replays[0].Err)
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.ElementsMatch(t, []string{
			"ada sound 0s", "manual sound 0s", "ada sound 0s", // the replay
			"waited sound 13s", "queued sound 13s",
		}, f.journal.get())

		f.journal = &journal{}
		began = time.Now()
		scene := action{typ: "scene", children: []command.Action{f.at("sound", began, "scene sound", 2*time.Second)}}
		scene.fn = func(ctx context.Context, run *engine.Run) error {
			run.PlaybackEnds(time.Now().Add(8 * time.Second))
			_, err := run.PerformChild(ctx, 0)
			return err
		}
		f.greet(f.command("cem", command.KindChat, scene))
		synctest.Wait()
		f.greet(f.command("dan", command.KindChat, f.at("sound", began, "dan sound", 0)))
		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"scene sound 0s", "dan sound 13s"}, f.journal.get(), "the latest end of the playback counts")
	})
}
