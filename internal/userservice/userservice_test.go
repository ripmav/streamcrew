// SPDX-License-Identifier: MIT

package userservice_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/userservice"
)

// memStore keeps the statistics and the stream sessions in memory.
type memStore struct {
	stats    map[id.ID]user.Stats
	sessions map[platform.Name]stream.Session
}

func newMemStore() *memStore {
	return &memStore{stats: make(map[id.ID]user.Stats), sessions: make(map[platform.Name]stream.Session)}
}

func (m *memStore) AddStats(_ context.Context, deltas []user.StatsDelta) error {
	for _, d := range deltas {
		st := m.stats[d.UserID]
		st.WatchMinutes += d.WatchMinutes
		st.Messages += d.Messages
		st.CommandsRun += d.CommandsRun
		st.Mentions += d.Mentions
		st.StreamsWatched += d.StreamsWatched
		if !d.Seen.IsZero() {
			if st.FirstSeen.IsZero() {
				st.FirstSeen = d.Seen
			}
			if d.Seen.After(st.LastSeen) {
				st.LastSeen = d.Seen
			}
		}
		m.stats[d.UserID] = st
	}
	return nil
}

func (m *memStore) StreamSessions(context.Context) ([]stream.Session, error) {
	var out []stream.Session
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) setSession(s stream.Session) {
	m.sessions[s.Platform] = s
}

// fixture is the service against the bus and the fake store.
type fixture struct {
	store *memStore
	bus   *event.Bus
	svc   *userservice.Service
}

// newFixture builds the service; run starts its Run in the test's bubble
// and returns the stop, which the test defers so that the goroutine ends
// with the bubble.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: newMemStore(), bus: event.NewBus(nil)}
	var err error
	f.svc, err = userservice.New(userservice.Ports{Store: f.store, Bus: f.bus})
	require.NoError(t, err)
	return f
}

func (f *fixture) run(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.svc.Run(ctx) }()
	return func() {
		cancel()
		require.NoError(t, <-done)
	}
}

// mockUser returns a user with the login on the mock platform.
func mockUser(login string) user.User {
	return user.User{
		ID:         id.New(),
		Identities: []user.Identity{{Platform: platform.Mock, PlatformUserID: login, Login: login, DisplayName: login}},
	}
}

// publish sends an event of type typ with the platform-neutral shape.
func (f *fixture) publish(t *testing.T, typ event.Type, u *user.User, details eventtype.Details, at time.Time) {
	t.Helper()
	payload := eventtype.Payload{Platform: platform.Mock, Details: details}
	if u != nil {
		payload.User = u
	}
	env := event.New(event.Source{Kind: event.SourcePlatform, Name: string(platform.Mock)}, typ, payload)
	env.Time = at
	require.NoError(t, f.bus.Publish(t.Context(), env))
}

func (f *fixture) activeLogins(t *testing.T) []string {
	t.Helper()
	logins := []string{}
	for _, a := range f.svc.Active(platform.Mock) {
		logins = append(logins, a.User.Identities[0].Login)
	}
	return logins
}

// settle waits until the service has handled the published events (and the
// watch time ticks until then).
func settle(t *testing.T) {
	t.Helper()
	synctest.Wait()
}

// TestMessageStats covers the counting of messages, mentions, commands,
// watched streams and the seen times (B9).
func TestMessageStats(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		stop := f.run(t)
		defer stop()
		// Wait until the service has subscribed, so that the published
		// events below are not lost.
		synctest.Wait()
		at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
		f.store.setSession(stream.Session{Platform: platform.Mock, State: stream.StateLive, StartedAt: at})

		ada, bob := mockUser("ada"), mockUser("bob")

		// Ada joins while live: active, one watched stream, seen.
		f.publish(t, eventtype.ChatUserJoin, &ada, eventtype.Details{}, at)
		settle(t)
		assert.Contains(t, f.activeLogins(t), "ada")
		assert.Equal(t, int64(1), f.store.stats[ada.ID].StreamsWatched)
		assert.Equal(t, at, f.store.stats[ada.ID].FirstSeen)
		assert.Equal(t, at, f.store.stats[ada.ID].LastSeen)

		// Bob arrives by message and mentions Ada.
		f.publish(t, eventtype.ChatMessage, &bob,
			eventtype.Details{Message: &eventtype.Message{ID: "m1", Text: "hello @ada"}}, at.Add(time.Second))
		settle(t)
		assert.Contains(t, f.activeLogins(t), "bob")
		assert.Equal(t, int64(1), f.store.stats[bob.ID].Messages)
		assert.Equal(t, int64(1), f.store.stats[ada.ID].Mentions, "Bob mentioned Ada")

		// Ada writes back: her own "@ada" does not count.
		f.publish(t, eventtype.ChatMessage, &ada,
			eventtype.Details{Message: &eventtype.Message{ID: "m2", Text: "hi @bob @ada"}}, at.Add(2*time.Second))
		// A longer login is not a mention of the shorter one.
		adaL := mockUser("ada_l")
		f.publish(t, eventtype.ChatMessage, &adaL,
			eventtype.Details{Message: &eventtype.Message{ID: "m3", Text: "yo @ada_l"}}, at.Add(3*time.Second))
		settle(t)
		assert.Equal(t, int64(1), f.store.stats[ada.ID].Messages)
		assert.Equal(t, int64(1), f.store.stats[bob.ID].Mentions, "Ada mentioned Bob")
		assert.Equal(t, int64(1), f.store.stats[ada.ID].Mentions, "her own mention stays at one, @ada_l does not count")

		// A completed instance counts the command of its user.
		env := event.New(event.Source{Kind: event.SourceSystem, Name: "engine"}, engine.TypeInstanceCompleted,
			engine.Instance{ID: id.New(), UserID: ada.ID, State: engine.StateCompleted})
		require.NoError(t, f.bus.Publish(t.Context(), env))
		settle(t)
		assert.Equal(t, int64(1), f.store.stats[ada.ID].CommandsRun)

		// A leave removes the user from the active set.
		f.publish(t, eventtype.ChatUserLeave, &bob, eventtype.Details{}, at.Add(4*time.Second))
		settle(t)
		assert.NotContains(t, f.activeLogins(t), "bob")
		assert.Contains(t, f.activeLogins(t), "ada")
	})
}

// TestWatchTime covers the watch time: whole minutes, only while live,
// only for the active users (B9, "Watchtime (nur während live)"). The
// counts are relative to when the test starts, so the minutes of the
// session before that do not matter.
func TestWatchTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		stop := f.run(t)
		defer stop()
		// Wait until the service has subscribed, so that the published
		// events below are not lost.
		synctest.Wait()
		at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
		f.store.setSession(stream.Session{Platform: platform.Mock, State: stream.StateLive, StartedAt: at})

		ada, bob := mockUser("ada"), mockUser("bob")
		f.publish(t, eventtype.ChatUserJoin, &ada, eventtype.Details{}, at)
		f.publish(t, eventtype.ChatUserJoin, &bob, eventtype.Details{}, at.Add(time.Second))

		// After the first minute, both have one minute.
		time.Sleep(time.Minute)
		settle(t)
		assert.Equal(t, int64(1), f.store.stats[ada.ID].WatchMinutes)
		assert.Equal(t, int64(1), f.store.stats[bob.ID].WatchMinutes)

		// Bob leaves; the next minute counts Ada only.
		f.publish(t, eventtype.ChatUserLeave, &bob, eventtype.Details{}, at.Add(65*time.Second))
		time.Sleep(time.Minute)
		settle(t)
		assert.Equal(t, int64(2), f.store.stats[ada.ID].WatchMinutes)
		assert.Equal(t, int64(1), f.store.stats[bob.ID].WatchMinutes, "the leaver stops counting")

		// The stream goes to the grace period: the minute counts nothing.
		f.store.setSession(stream.Session{Platform: platform.Mock, State: stream.StateGrace, Since: at.Add(2 * time.Minute)})
		time.Sleep(time.Minute)
		settle(t)
		assert.Equal(t, int64(2), f.store.stats[ada.ID].WatchMinutes, "grace does not count")
	})
}

// TestJoinOffline covers that a join while the stream is not live makes
// the user active but does not count a watched stream (B9).
func TestJoinOffline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		stop := f.run(t)
		defer stop()
		// Wait until the service has subscribed, so that the published
		// events below are not lost.
		synctest.Wait()
		at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
		f.store.setSession(stream.Session{Platform: platform.Mock, State: stream.StateOffline})

		ada := mockUser("ada")
		f.publish(t, eventtype.ChatUserJoin, &ada, eventtype.Details{}, at)
		settle(t)
		assert.Contains(t, f.activeLogins(t), "ada")
		assert.Zero(t, f.store.stats[ada.ID].StreamsWatched)
	})
}
