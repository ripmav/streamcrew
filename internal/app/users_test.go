// SPDX-License-Identifier: MIT

package app_test

// The test covers roadmap 5.2.1 and 5.2.2 (users-and-roles.md, B9, B10)
// at the app level against the mock platform: the user service counts the
// watched stream, the messages, the mentions, the commands and the seen
// times of the users from the events of the chat pipeline, and it keeps
// the follow data of the identities fresh. The watch time is counted per
// whole minute and is covered by the unit tests of internal/userservice.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestUserStatsE2E covers the user statistics through the chat pipeline.
func TestUserStatsE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	mp, coll := startedCore(t, cfg, chatCommands, settings.DefaultChat())
	ctx := t.Context()

	st, err := store.Open(ctx, profile.NewManager(cfg.DataDir).Path(profile.DefaultID))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, st.Close()) })

	// The stream goes live; wait until the core knows it.
	require.NoError(t, mp.GoLive(ctx, "streaming", "games"))
	require.Eventually(t, func() bool {
		sessions, err := st.StreamSessions(ctx)
		if err != nil {
			return false
		}
		for _, s := range sessions {
			if s.Platform == "mock" && s.State == stream.StateLive {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the session is live")

	// Alice and Bob join the live stream and write messages; Bob mentions
	// nobody, Alice mentions Bob, and both run the command.
	require.NoError(t, mp.Join(ctx, "alice"))
	require.NoError(t, mp.Join(ctx, "bob"))
	_, err = mp.Say(ctx, "alice", "hi @bob")
	require.NoError(t, err)
	_, err = mp.Say(ctx, "bob", "!hello")
	require.NoError(t, err)
	_, err = mp.Say(ctx, "alice", "!hello")
	require.NoError(t, err)

	// Wait until both hello commands have completed.
	require.Eventually(t, func() bool {
		return coll.count(func(e event.Envelope) bool { return instanceNamed(e, "Hello") }) >= 2
	}, 10*time.Second, 10*time.Millisecond, "both hello commands complete")

	// The statistics follow the events; they are written after the
	// commands, so wait for them.
	readUsers := func() (alice, bob user.Stats, ok bool) {
		for _, name := range []string{"alice", "bob"} {
			u, err := st.UserByLogin(ctx, platform.Mock, name)
			if err != nil {
				return user.Stats{}, user.Stats{}, false
			}
			if name == "alice" {
				alice = u.Stats
			} else {
				bob = u.Stats
			}
		}
		return alice, bob, true
	}
	var alice, bob user.Stats
	require.Eventually(t, func() bool {
		var ok bool
		alice, bob, ok = readUsers()
		return ok &&
			alice.Messages == 2 && bob.Messages == 1 &&
			alice.CommandsRun == 1 && bob.CommandsRun == 1 &&
			alice.Mentions == 0 && bob.Mentions == 1
	}, 5*time.Second, 10*time.Millisecond, "the statistics are counted")

	assert.Equal(t, int64(1), alice.StreamsWatched)
	assert.Equal(t, int64(1), bob.StreamsWatched)
	assert.False(t, alice.FirstSeen.IsZero())
	assert.False(t, alice.LastSeen.IsZero())
	assert.True(t, alice.LastSeen.After(alice.FirstSeen))

	// The follow data of the identities is refreshed on contact and cached
	// (users-and-roles.md, B10).
	var aliceUser user.User
	require.Eventually(t, func() bool {
		var err error
		aliceUser, err = st.UserByLogin(ctx, platform.Mock, "alice")
		if err != nil || aliceUser.ID.IsZero() {
			return false
		}
		if i, ok := aliceUser.Identity(platform.Mock); ok {
			return !i.Data.UpdatedAt.IsZero() && !i.Data.FollowedAt.IsZero()
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the follow data is cached on contact")
}
