// SPDX-License-Identifier: MIT

package engine_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
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
	"github.com/ripmav/streamcrew/internal/settings"
)

// countingUsers is a fake of engine.Users that counts the lookups per
// name. The first stall attempts of each name wait until their context
// ends, the next fail ones fail.
type countingUsers struct {
	mu     sync.Mutex
	known  map[string]user.User
	stall  int
	fail   int
	asked  map[string]int
	failed error
}

func newCountingUsers(logins ...string) *countingUsers {
	u := &countingUsers{known: make(map[string]user.User), asked: make(map[string]int), failed: errors.New("platform busy")}
	for _, login := range logins {
		u.known[login] = user.User{ID: id.New(), Identities: []user.Identity{{Platform: platform.Twitch, Login: login}}}
	}
	return u
}

func (u *countingUsers) UserByName(ctx context.Context, _ platform.Name, name string) (user.User, bool, error) {
	u.mu.Lock()
	name = strings.ToLower(name)
	u.asked[name]++
	n := u.asked[name]
	found, ok := u.known[name]
	u.mu.Unlock()
	switch {
	case n <= u.stall:
		<-ctx.Done()
		return user.User{}, false, ctx.Err()
	case n <= u.stall+u.fail:
		return user.User{}, false, u.failed
	}
	return found, ok, nil
}

// times returns how often name was asked for.
func (u *countingUsers) times(name string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.asked[name]
}

// lookingUp is a fake of engine.Requirements that looks up the user of the
// first argument through the lookup of the run, as the requirement service
// does for arguments.
type lookingUp struct {
	*requirements
	found chan string
}

func (l *lookingUp) Prepare(ctx context.Context, cmd command.Command, p engine.Params, users engine.Users) (engine.Decide, error) {
	if len(p.Args) > 0 {
		u, ok, err := users.UserByName(ctx, p.Platform, strings.TrimPrefix(p.Args[0], "@"))
		if err != nil {
			return nil, err
		}
		if ok {
			l.found <- u.ID.String()
		} else {
			l.found <- "nobody"
		}
	}
	return l.requirements.Prepare(ctx, cmd, p, users)
}

// lookupAct returns an action that looks up bob through the run, as
// actions do, and sends his ID or "nobody" to found.
func lookupAct(found chan<- string) action {
	return action{typ: "test.lookup", fn: func(ctx context.Context, run *engine.Run) error {
		u, ok, err := run.UserByName(ctx, platform.Twitch, "bob")
		if err != nil || !ok {
			found <- "nobody"
			return err
		}
		found <- u.ID.String()
		return nil
	}}
}

// targetAct returns an action that sends the ID of the target of the run,
// or "none", to found.
func targetAct(found chan<- string) action {
	return action{typ: "test.target", fn: func(_ context.Context, run *engine.Run) error {
		if tu := run.Scope().Target; tu != nil {
			found <- tu.ID.String()
			return nil
		}
		found <- "none"
		return nil
	}}
}

// drainN returns n values of found.
func drainN(found <-chan string, n int) []string {
	got := make([]string, 0, n)
	for range n {
		got = append(got, <-found)
	}
	return got
}

// TestLookupShared covers B17: a user found for the target is not looked
// up again for the requirements, the actions or the commands the run
// calls; a name that was not found is looked up again.
func TestLookupShared(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		users := newCountingUsers("bob")
		found := make(chan string, 8)
		reqs := &lookingUp{requirements: newRequirements(), found: found}
		f := newFixture(t, settings.LockNone, engine.WithRequirements(reqs), engine.WithUsers(users))
		defer f.stop()

		called := f.command("called", command.KindActionGroup, lookupAct(found))
		cmd := f.command("hug", command.KindChat, targetAct(found), lookupAct(found), f.call(called.ID, wait()), f.show("$arg1username"))
		p := engine.Params{Platform: platform.Twitch, Args: []string{"@BOB"}, ArgsText: "@BOB", Message: "!hug @BOB"}
		_, err := f.trigger(cmd, p)
		require.NoError(t, err)
		bob := users.known["bob"].ID.String()
		assert.Equal(t, []string{bob, bob, bob, bob}, drainN(found, 4), "requirements, target, action and called command")
		assert.Equal(t, 1, users.times("bob"), "looked up once")
		assert.Equal(t, []string{"bob"}, f.journal.get(), "the templates find bob through the run as well")

		_, err = f.trigger(cmd, p)
		require.NoError(t, err)
		drainN(found, 4)
		assert.Equal(t, 2, users.times("bob"), "each trigger looks up for itself")

		stranger := engine.Params{Platform: platform.Twitch, Args: []string{"nobody"}, ArgsText: "nobody", Message: "!hug nobody"}
		_, err = f.trigger(f.command("plain", command.KindChat), stranger)
		require.NoError(t, err)
		assert.Equal(t, "nobody", <-found)
		assert.Equal(t, 2, users.times("nobody"), "not found is looked up again")
	})
}

// TestLookupAttempts covers B17 and B90: an attempt that takes longer than
// the time limit or fails is tried again, as often as the settings say;
// after the last one the lookup fails, and the run goes on without a
// target.
func TestLookupAttempts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		users := newCountingUsers("bob")
		users.stall, users.fail = 1, 1
		logs := &records{}
		f := newFixture(t, settings.LockNone, engine.WithUsers(users), engine.WithLogger(slog.New(logs)))
		defer f.stop()

		found := make(chan string, 4)
		hug := f.command("hug", command.KindChat, targetAct(found))
		p := engine.Params{Platform: platform.Twitch, Args: []string{"bob"}, ArgsText: "bob"}
		start := time.Now()
		_, err := f.trigger(hug, p)
		require.NoError(t, err)
		assert.Equal(t, settings.DefaultUserLookupTimeout, time.Since(start), "one attempt ran into the time limit")
		assert.Equal(t, users.known["bob"].ID.String(), <-found, "the third attempt found bob")
		assert.Equal(t, 3, users.times("bob"))
		assert.Contains(t, logs.messages(), "looking up a user failed, trying again")

		users.mu.Lock()
		users.asked = map[string]int{}
		users.stall, users.fail = 0, 3
		users.mu.Unlock()
		_, err = f.trigger(hug, p)
		require.NoError(t, err)
		assert.Equal(t, "none", <-found, "no target after the last attempt")
		assert.Equal(t, 3, users.times("bob"))
		assert.Contains(t, logs.messages(), "looking up the target user failed")

		f.configs.mu.Lock()
		f.configs.cfg.Commands.UserLookupAttempts = 1
		f.configs.mu.Unlock()
		users.mu.Lock()
		users.asked = map[string]int{}
		users.mu.Unlock()
		_, err = f.trigger(hug, p)
		require.NoError(t, err)
		<-found
		assert.Equal(t, 1, users.times("bob"), "the attempts come from the settings")
	})
}

// TestLookupWithoutUsers: without users of the engine, the run finds
// nobody.
func TestLookupWithoutUsers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		found := make(chan string, 1)
		f := newFixture(t, settings.LockNone)
		defer f.stop()
		f.start(f.command("look", command.KindChat, lookupAct(found)), engine.Params{})
		assert.Equal(t, "nobody", <-found)
	})
}
