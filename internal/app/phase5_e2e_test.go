// SPDX-License-Identifier: MIT

package app_test

// The tests cover the Phase 5 exit criteria (roadmap 5.6): end-to-end tests
// against the mock platform for the timer (5.4), the chat moderation stage
// (5.5) and the watch time of a simulated live session (5.2.1). The chat
// pipeline itself is covered by the tests in chat_test.go.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// timerCommands is a timer command that runs on its group's one-second
// interval (roadmap 5.4, B21, B31).
const timerCommands = `apiVersion: streamcrew/v1alpha1
kind: TimerCommand
metadata: {name: Ping, group: PingGroup}
spec:
  actions:
    - {type: chat, kind: message, message: "ping!"}
---
apiVersion: streamcrew/v1alpha1
kind: CommandGroup
metadata: {name: PingGroup}
spec: {timerInterval: 1s}
`

// TestTimerE2E covers roadmap 5.4 end to end: the timer service runs the
// timer command of its group on the group's interval, and the command's chat
// action reaches the mock platform.
func TestTimerE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	_, coll := startedCore(t, cfg, timerCommands, settings.DefaultChat())

	coll.waitFor(t, "the timer command to fire", func(e event.Envelope) bool {
		return instanceNamed(e, "Ping")
	})
	coll.waitFor(t, "the timer message on the platform", func(e event.Envelope) bool {
		return mockSent(e, "ping!")
	})
}

// moderationCommands are a chat command and the setup for the moderation
// filter end-to-end test.
const moderationCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Play}
spec:
  triggers: [play]
  actions:
    - {type: chat, kind: message, message: "Playing $allargs"}
`

// TestModerationFilterE2E covers roadmap 5.5 end to end: a chat message that
// violates a word filter is discarded at the moderation stage — it runs no
// command and is not answered — while a clean message with the same trigger
// runs its command.
func TestModerationFilterE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	mods := settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badsong"}}}
	mp, coll := startedCoreModeration(t, cfg, moderationCommands, settings.DefaultChat(), mods)
	ctx := t.Context()

	// The banned word in the arguments discards the message: no command.
	sent, err := mp.Say(ctx, "alice", "!play badsong")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	time.Sleep(300 * time.Millisecond)
	assert.Zero(t, coll.count(func(e event.Envelope) bool { return instanceNamed(e, "Play") }),
		"the violating message runs nothing")
	assert.Zero(t, coll.count(func(e event.Envelope) bool { return mockSent(e, "Playing badsong") }),
		"the violating message is not answered")

	// A clean message with the same trigger runs its command.
	sent, err = mp.Say(ctx, "bob", "!play good")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	coll.waitFor(t, "the clean command to complete", func(e event.Envelope) bool {
		return instanceNamed(e, "Play")
	})
	coll.waitFor(t, "the answer for the clean message", func(e event.Envelope) bool {
		return mockSent(e, "Playing good")
	})
}

// TestWatchTimeE2E covers the watch time of a simulated live session
// (roadmap 5.2.1, users-and-roles.md B9): while the mock stream is live, an
// active user accumulates watch time on the watch tick. The tick is
// shortened for the test.
func TestWatchTimeE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	mp, _ := startedCoreWatch(t, cfg, chatCommands, settings.DefaultChat(), 50*time.Millisecond)
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

	// Alice joins and writes, so she is active on the live platform.
	require.NoError(t, mp.Join(ctx, "alice"))
	_, err = mp.Say(ctx, "alice", "hello there")
	require.NoError(t, err)

	// The watch tick adds a whole minute to every active user of the live
	// platform; wait until Alice's watch time is counted.
	require.Eventually(t, func() bool {
		u, err := st.UserByLogin(ctx, platform.Mock, "alice")
		return err == nil && u.Stats.WatchMinutes >= 1
	}, 5*time.Second, 10*time.Millisecond, "the watch time of the live session is counted")
}
