// SPDX-License-Identifier: MIT

package userservice_test

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/userservice"
)

// memStore keeps the statistics and the stream sessions in memory; the
// service reads them from its goroutine, the test writes them from its
// own, so mu guards both maps.
type memStore struct {
	mu       sync.Mutex
	stats    map[id.ID]user.Stats
	regular  map[id.ID]bool
	excluded map[id.ID]bool
	sessions map[platform.Name]stream.Session
	data     map[string]user.PlatformData
}

func newMemStore() *memStore {
	return &memStore{stats: make(map[id.ID]user.Stats), regular: make(map[id.ID]bool), excluded: make(map[id.ID]bool), sessions: make(map[platform.Name]stream.Session), data: make(map[string]user.PlatformData)}
}

func (m *memStore) AddStats(_ context.Context, deltas []user.StatsDelta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []stream.Session
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (m *memStore) setSession(s stream.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.Platform] = s
}

func (m *memStore) stat(u id.ID) user.Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats[u]
}

func (m *memStore) regularOf(u id.ID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.regular[u]
}

// seed sets the stored watch minutes of a user, for the regular role
// tests.
func (m *memStore) seed(u id.ID, watchMinutes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats[u] = user.Stats{WatchMinutes: watchMinutes}
}

func (m *memStore) ListUsersRegular(context.Context) ([]user.RegularSweep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []user.RegularSweep
	for u, st := range m.stats {
		out = append(out, user.RegularSweep{ID: u, Regular: m.regular[u], WatchMinutes: st.WatchMinutes})
	}
	return out, nil
}

func (m *memStore) UpdateUser(_ context.Context, u id.ID, fn func(*user.User) error) (user.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	loaded := user.User{ID: u, Regular: m.regular[u], Stats: m.stats[u]}
	if err := fn(&loaded); err != nil {
		return user.User{}, err
	}
	m.regular[loaded.ID] = loaded.Regular
	return loaded, nil
}

func (m *memStore) SetPlatformData(_ context.Context, p platform.Name, platformUserID string, data user.PlatformData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[string(p)+"/"+platformUserID] = data
	return nil
}

func (m *memStore) dataOf(p platform.Name, platformUserID string) user.PlatformData {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.data[string(p)+"/"+platformUserID]
}

// TopUsers implements the Store port of the tests: the users with a value
// for stat, not excluded, from the highest value down, ties by ID, at most
// n (roadmap 5.2.5).
func (m *memStore) TopUsers(_ context.Context, stat user.Stat, n int) ([]user.Ranked, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []user.Ranked
	for u, st := range m.stats {
		if m.excluded[u] {
			continue
		}
		v := stValue(stat, st)
		if v == 0 {
			continue
		}
		out = append(out, user.Ranked{User: user.User{ID: u, Stats: st}, Value: v})
	}
	slices.SortFunc(out, func(a, b user.Ranked) int {
		if a.Value != b.Value {
			return -cmp.Compare(a.Value, b.Value)
		}
		return strings.Compare(a.User.ID.String(), b.User.ID.String())
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

func stValue(stat user.Stat, st user.Stats) int64 {
	switch stat {
	case user.StatWatchMinutes:
		return st.WatchMinutes
	case user.StatMessages:
		return st.Messages
	case user.StatCommandsRun:
		return st.CommandsRun
	case user.StatMentions:
		return st.Mentions
	case user.StatStreamsWatched:
		return st.StreamsWatched
	}
	return 0
}

// fakePlatform is the platform adapter of the tests: the PlatformData
// capability works, the rest stays unused. mu guards calls and answer,
// which the service writes from its goroutine and the test reads from its
// own.
type fakePlatform struct {
	mu     sync.Mutex
	calls  int
	answer user.PlatformData
}

func newFakePlatform() *fakePlatform { return &fakePlatform{} }

func (f *fakePlatform) setAnswer(data user.PlatformData) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = data
}

func (f *fakePlatform) callsCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// UserPlatformData implements connector.PlatformData.
func (f *fakePlatform) UserPlatformData(_ context.Context, _ string) (user.PlatformData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.answer, nil
}

// The rest of connector.Platform stays unused by the service.
func (f *fakePlatform) Name() platform.Name              { return platform.Mock }
func (f *fakePlatform) Status() connector.Status         { return connector.Status{} }
func (f *fakePlatform) Chat() connector.Chat             { return nil }
func (f *fakePlatform) Moderation() connector.Moderation { return nil }
func (f *fakePlatform) Users() connector.Users           { return nil }
func (f *fakePlatform) Channel(context.Context) (connector.ChannelInfo, error) {
	return connector.ChannelInfo{}, nil
}

// Platform implements userservice.Platforms for platform.Mock only.
func (f *fakePlatform) Platform(p platform.Name) (connector.Platform, bool) {
	if p == platform.Mock {
		return f, true
	}
	return nil, false
}

// fixture is the service against the bus and the fake store.
type fixture struct {
	store     *memStore
	platforms *fakePlatform
	bus       *event.Bus
	svc       *userservice.Service
	// users is the settings section "users" the service reads on the tick;
	// the test changes it to test a changed threshold.
	users settings.Users
	// mu guards users, which the service reads from its goroutine and the
	// test writes from its own.
	mu sync.Mutex
}

// newFixture builds the service; run starts its Run in the test's bubble
// and returns the stop, which the test defers so that the goroutine ends
// with the bubble.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: newMemStore(), platforms: newFakePlatform(), bus: event.NewBus(nil)}
	var err error
	f.svc, err = userservice.New(userservice.Ports{
		Store:     f.store,
		Bus:       f.bus,
		Settings:  f.usersSettings,
		Platforms: f.platforms,
	})
	require.NoError(t, err)
	return f
}

func (f *fixture) usersSettings(context.Context) (settings.Users, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.users, nil
}

func (f *fixture) setUsers(t *testing.T, u settings.Users) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = u
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
		assert.Equal(t, int64(1), f.store.stat(ada.ID).StreamsWatched)
		assert.Equal(t, at, f.store.stat(ada.ID).FirstSeen)
		assert.Equal(t, at, f.store.stat(ada.ID).LastSeen)

		// Bob arrives by message and mentions Ada.
		f.publish(t, eventtype.ChatMessage, &bob,
			eventtype.Details{Message: &eventtype.Message{ID: "m1", Text: "hello @ada"}}, at.Add(time.Second))
		settle(t)
		assert.Contains(t, f.activeLogins(t), "bob")
		assert.Equal(t, int64(1), f.store.stat(bob.ID).Messages)
		assert.Equal(t, int64(1), f.store.stat(ada.ID).Mentions, "Bob mentioned Ada")

		// Ada writes back: her own "@ada" does not count.
		f.publish(t, eventtype.ChatMessage, &ada,
			eventtype.Details{Message: &eventtype.Message{ID: "m2", Text: "hi @bob @ada"}}, at.Add(2*time.Second))
		// A longer login is not a mention of the shorter one.
		adaL := mockUser("ada_l")
		f.publish(t, eventtype.ChatMessage, &adaL,
			eventtype.Details{Message: &eventtype.Message{ID: "m3", Text: "yo @ada_l"}}, at.Add(3*time.Second))
		settle(t)
		assert.Equal(t, int64(1), f.store.stat(ada.ID).Messages)
		assert.Equal(t, int64(1), f.store.stat(bob.ID).Mentions, "Ada mentioned Bob")
		assert.Equal(t, int64(1), f.store.stat(ada.ID).Mentions, "her own mention stays at one, @ada_l does not count")

		// A completed instance counts the command of its user.
		env := event.New(event.Source{Kind: event.SourceSystem, Name: "engine"}, engine.TypeInstanceCompleted,
			engine.Instance{ID: id.New(), UserID: ada.ID, State: engine.StateCompleted})
		require.NoError(t, f.bus.Publish(t.Context(), env))
		settle(t)
		assert.Equal(t, int64(1), f.store.stat(ada.ID).CommandsRun)

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
		assert.Equal(t, int64(1), f.store.stat(ada.ID).WatchMinutes)
		assert.Equal(t, int64(1), f.store.stat(bob.ID).WatchMinutes)

		// Bob leaves; the next minute counts Ada only.
		f.publish(t, eventtype.ChatUserLeave, &bob, eventtype.Details{}, at.Add(65*time.Second))
		time.Sleep(time.Minute)
		settle(t)
		assert.Equal(t, int64(2), f.store.stat(ada.ID).WatchMinutes)
		assert.Equal(t, int64(1), f.store.stat(bob.ID).WatchMinutes, "the leaver stops counting")

		// The stream goes to the grace period: the minute counts nothing.
		f.store.setSession(stream.Session{Platform: platform.Mock, State: stream.StateGrace, Since: at.Add(2 * time.Minute)})
		time.Sleep(time.Minute)
		settle(t)
		assert.Equal(t, int64(2), f.store.stat(ada.ID).WatchMinutes, "grace does not count")
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
		assert.Zero(t, f.store.stat(ada.ID).StreamsWatched)
	})
}

// TestRegularRole covers the regular role from whole hours of watch time
// (users-and-roles.md, B26): granted at the threshold, not below it, on
// the user and thus on every platform.
func TestRegularRole(t *testing.T) {
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
		// Ada has 59 watch minutes, Bob a full hour.
		f.store.seed(ada.ID, 59)
		f.store.seed(bob.ID, 60)
		f.setUsers(t, settings.Users{RegularHours: 1})
		f.publish(t, eventtype.ChatUserJoin, &ada, eventtype.Details{}, at)
		f.publish(t, eventtype.ChatUserJoin, &bob, eventtype.Details{}, at.Add(time.Second))

		// After the first minute, Ada crossed the hour; both are regular.
		time.Sleep(time.Minute)
		settle(t)
		assert.True(t, f.store.regularOf(ada.ID), "59 + 1 minute is a whole hour")
		assert.True(t, f.store.regularOf(bob.ID))

		// A user below the threshold stays without the role.
		cara := mockUser("cara")
		f.publish(t, eventtype.ChatUserJoin, &cara, eventtype.Details{}, at.Add(2*time.Second))
		time.Sleep(time.Minute)
		settle(t)
		assert.False(t, f.store.regularOf(cara.ID))
	})
}

// TestRegularThreshold covers the re-evaluation when the threshold changes
// (users-and-roles.md, B26): the next tick re-evaluates every user, and 0
// revokes the role for everyone.
func TestRegularThreshold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		stop := f.run(t)
		defer stop()
		synctest.Wait()

		ada, bob := mockUser("ada"), mockUser("bob")
		f.store.seed(ada.ID, 60)
		f.store.seed(bob.ID, 120)
		f.setUsers(t, settings.Users{RegularHours: 1})

		// The first tick evaluates everyone, without any contact.
		time.Sleep(time.Minute)
		settle(t)
		assert.True(t, f.store.regularOf(ada.ID))
		assert.True(t, f.store.regularOf(bob.ID))

		// The threshold goes up: Ada falls below, Bob stays.
		f.setUsers(t, settings.Users{RegularHours: 2})
		time.Sleep(time.Minute)
		settle(t)
		assert.False(t, f.store.regularOf(ada.ID))
		assert.True(t, f.store.regularOf(bob.ID))

		// The threshold 0 turns the role off for everyone.
		f.setUsers(t, settings.Users{})
		time.Sleep(time.Minute)
		settle(t)
		assert.False(t, f.store.regularOf(ada.ID))
		assert.False(t, f.store.regularOf(bob.ID))
	})
}

// TestPlatformDataCache covers the follow and subscription cache
// (users-and-roles.md, B10): missing data is fetched on contact and
// stamped, fresh data is not refetched, and stale data is refetched after
// the TTL.
func TestPlatformDataCache(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		stop := f.run(t)
		defer stop()
		// Wait until the service has subscribed, so that the published
		// events below are not lost.
		synctest.Wait()
		followed := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
		f.platforms.setAnswer(user.PlatformData{FollowedAt: followed})
		at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

		// Missing data: the join fetches it and the cache is stamped.
		ada := mockUser("ada")
		f.publish(t, eventtype.ChatUserJoin, &ada, eventtype.Details{}, at)
		settle(t)
		data := f.store.dataOf(platform.Mock, "ada")
		assert.Equal(t, followed, data.FollowedAt)
		require.False(t, data.UpdatedAt.IsZero(), "the cache is stamped")
		assert.Equal(t, 1, f.platforms.callsCount())

		// Fresh cache: the next contact does not refetch.
		f.publish(t, eventtype.ChatMessage, &ada,
			eventtype.Details{Message: &eventtype.Message{ID: "m1", Text: "hi"}}, at.Add(time.Second))
		settle(t)
		assert.Equal(t, 1, f.platforms.callsCount())

		// Stale cache: after the TTL, the next contact refetches.
		f.platforms.setAnswer(user.PlatformData{FollowedAt: at.Add(2 * time.Hour), SubTier: 1})
		time.Sleep(time.Hour + time.Minute)
		f.publish(t, eventtype.ChatMessage, &ada,
			eventtype.Details{Message: &eventtype.Message{ID: "m2", Text: "hi again"}}, at.Add(2*time.Hour))
		settle(t)
		assert.Equal(t, 2, f.platforms.callsCount())
		data = f.store.dataOf(platform.Mock, "ada")
		assert.Equal(t, at.Add(2*time.Hour), data.FollowedAt)
		assert.Equal(t, 1, data.SubTier)
	})
}

// TestRankings covers the exclusion of the channel's accounts from the
// top lists (roadmap 5.2.5): the streamer's and the bot's user are never
// ranked, the list is refilled, excluded users (B7) and zero values are
// absent, and invalid arguments fail.
func TestRankings(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store := newMemStore()

	alice, bob, carol := id.New(), id.New(), id.New()
	streamer, bot := id.New(), id.New()
	dan := id.New()
	require.NoError(t, store.AddStats(ctx, []user.StatsDelta{
		{UserID: alice, Messages: 30},
		{UserID: bob, Messages: 20},
		{UserID: carol, Messages: 10},
		{UserID: streamer, Messages: 999},
		{UserID: bot, Messages: 998},
		{UserID: dan, Messages: 0},
	}))
	// excl is exempt from rankings (B7).
	excl := id.New()
	require.NoError(t, store.AddStats(ctx, []user.StatsDelta{{UserID: excl, Messages: 500}}))
	store.mu.Lock()
	store.excluded[excl] = true
	store.mu.Unlock()

	svc, err := userservice.New(userservice.Ports{
		Store:    store,
		Bus:      event.NewBus(nil),
		Settings: func(context.Context) (settings.Users, error) { return settings.Users{}, nil },
		AccountUsers: func(context.Context) ([]id.ID, error) {
			return []id.ID{streamer, bot}, nil
		},
	})
	require.NoError(t, err)

	// Top 3 by messages: alice, bob, carol. The streamer (999), the bot
	// (998) and the excluded (500) are absent, and dan has 0.
	top, err := svc.Rankings(ctx, user.StatMessages, 3)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{alice, bob, carol}, rankedIDs(top))
	assert.Equal(t, []int64{30, 20, 10}, rankedVals(top))

	// Top 5: only the three non-zero, non-excluded users exist.
	top, err = svc.Rankings(ctx, user.StatMessages, 5)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{alice, bob, carol}, rankedIDs(top))

	// A statistic nobody has: empty, not an error.
	top, err = svc.Rankings(ctx, user.StatCommandsRun, 3)
	require.NoError(t, err)
	assert.Empty(t, top)

	// Invalid arguments.
	_, err = svc.Rankings(ctx, user.Stat("donated_cents"), 1)
	require.ErrorIs(t, err, user.ErrInvalid)
	_, err = svc.Rankings(ctx, user.StatMessages, 0)
	require.ErrorIs(t, err, user.ErrInvalid)
}

func rankedIDs(top []user.Ranked) []id.ID {
	out := make([]id.ID, 0, len(top))
	for _, r := range top {
		out = append(out, r.User.ID)
	}
	return out
}

func rankedVals(top []user.Ranked) []int64 {
	out := make([]int64, 0, len(top))
	for _, r := range top {
		out = append(out, r.Value)
	}
	return out
}
