// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// fakeHelix is the state and the call log of the fake Helix server.
type fakeHelix struct {
	mu            sync.Mutex
	subs          map[string]helix.EventSubSubscription
	created       []helix.CreateSubscriptionInput
	revoked       []string
	failCreate400 map[string]bool
	failCreate409 map[string]bool
}

func newFakeHelix() *fakeHelix {
	return &fakeHelix{
		subs:          map[string]helix.EventSubSubscription{},
		failCreate400: map[string]bool{},
		failCreate409: map[string]bool{},
	}
}

// handler is the test server handler of the fake Helix.
func (f *fakeHelix) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/eventsub/subscriptions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			data := []helix.EventSubSubscription{}
			for _, s := range f.subs {
				data = append(data, s)
			}
			_, _ = fmt.Fprintf(w, `{"data":%s}`, mustJSON(t, data))
		case http.MethodPost:
			var in helix.CreateSubscriptionInput
			require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
			f.created = append(f.created, in)
			if f.failCreate400[in.EventType] {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"limit"}`))
				return
			}
			if f.failCreate409[in.EventType] {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"Conflict","message":"subscription already exists"}`))
				return
			}
			id := "id-" + in.EventType
			f.subs[id] = helix.EventSubSubscription{
				ID:        id,
				Status:    "enabled",
				Condition: in.Condition,
				EventType: in.EventType,
				Version:   in.Version,
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"data":%s}`, mustJSON(t, f.subs[id]))
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			_, ok := f.subs[id]
			require.True(t, ok)
			delete(f.subs, id)
			f.revoked = append(f.revoked, id)
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func newManager(t *testing.T, f *fakeHelix, interval time.Duration) *twitch.Manager {
	t.Helper()
	srv := httptest.NewTestServer(t, f.handler(t))
	t.Cleanup(srv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: srv.Client(), Rate: 100, Burst: 100})
	c := helix.New(hc, helix.Options{ClientID: "test", Token: func(context.Context) (string, error) { return "tok", nil }, BaseURL: srv.URL})
	return twitch.NewManager(twitch.ManagerOptions{
		Helix:     c,
		Channels:  []string{"7"},
		Whisper:   "7",
		Moderator: "7", // the home channel: the token owner is the broadcaster
		Interval:  interval,
	})
}

// seed puts a subscription into the fake, as if it existed.
func (f *fakeHelix) seed(t *testing.T, eventType, version string, c helix.SubscriptionCondition) {
	t.Helper()
	f.seedStatus(t, eventType, version, c, "enabled")
}

// seedStatus puts a subscription with the given status into the fake.
func (f *fakeHelix) seedStatus(t *testing.T, eventType, version string, c helix.SubscriptionCondition, status string) {
	t.Helper()
	id := "seed-" + eventType
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = helix.EventSubSubscription{
		ID:        id,
		Status:    status,
		Condition: c,
		EventType: eventType,
		Version:   version,
	}
}

// TestReconcileRepointsTheStaleSubscriptions covers F10: a drop of the
// session disables its subscriptions (the status is no longer
// "enabled"); the reconciliation revokes them and re-creates them for
// the current session, while the enabled desired state is kept.
func TestReconcileRepointsTheStaleSubscriptions(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	// The session dropped: the own subscriptions are disabled.
	f.seedStatus(t, "channel.follow", "2", helix.SubscriptionCondition{BroadcasterUserID: "7", ModeratorUserID: "7"}, "websocket_disconnected")
	f.seedStatus(t, "channel.raid", "1", helix.SubscriptionCondition{ToBroadcasterUserID: "7"}, "websocket_disconnected")
	// The desired state that the drop left enabled is kept, not
	// recreated (the session_reconnect carries it over).
	f.seed(t, "channel.cheer", "1", helix.SubscriptionCondition{BroadcasterUserID: "7"})
	require.NoError(t, m.Reconcile(t.Context()))

	// The stale ones are revoked.
	require.Len(t, f.revoked, 2)
	assert.Contains(t, f.revoked, "seed-channel.follow")
	assert.Contains(t, f.revoked, "seed-channel.raid")
	// The stale ones are re-created, the enabled one is not.
	created := map[string]bool{}
	for _, in := range f.created {
		created[in.EventType] = true
	}
	assert.True(t, created["channel.follow"])
	assert.True(t, created["channel.raid"])
	assert.False(t, created["channel.cheer"])
	// The second run: nothing to do (the re-created ones answer as
	// enabled).
	createdBefore := len(f.created)
	require.NoError(t, m.Reconcile(t.Context()))
	assert.Len(t, f.created, createdBefore)
	assert.Len(t, f.revoked, 2)
}

func TestReconcileCreatesTheMissingSubscriptions(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	require.NoError(t, m.Reconcile(t.Context()))

	require.Len(t, f.created, 25)
	// The exact table: versions and conditions (the current ones of the
	// EventSub Reference, plan 2026-10-22-eventsub-reference-fix.md).
	got := map[string]struct {
		version string
		cond    helix.SubscriptionCondition
	}{}
	for _, in := range f.created {
		got[in.EventType] = struct {
			version string
			cond    helix.SubscriptionCondition
		}{in.Version, in.Condition}
	}
	assert.Equal(t, "2", got["channel.follow"].version)
	assert.Equal(t, "1", got["channel.chat.message"].version)
	assert.Equal(t, "1", got["stream.online"].version)
	assert.Equal(t, "1", got["channel.cheer"].version)
	assert.Equal(t, "2", got["channel.moderate"].version)
	assert.Equal(t, "2", got["channel.hype_train.begin"].version)
	assert.Equal(t, "1", got["channel.shared_chat.begin"].version)
	assert.True(t, got["channel.ad_break.begin"].version == "1")
	assert.True(t, got["channel.goal.begin"].version == "1")
	assert.True(t, got["channel.charity_campaign.progress"].version == "1")
	assert.Equal(t, helix.SubscriptionCondition{BroadcasterUserID: "7", ModeratorUserID: "7"}, got["channel.follow"].cond)
	assert.Equal(t, helix.SubscriptionCondition{ToBroadcasterUserID: "7"}, got["channel.raid"].cond)
	assert.Equal(t, helix.SubscriptionCondition{BroadcasterUserID: "7", UserID: "7"}, got["channel.chat.message"].cond)
	assert.Equal(t, helix.SubscriptionCondition{BroadcasterUserID: "7", ModeratorUserID: "7"}, got["channel.moderate"].cond)
	assert.Equal(t, helix.SubscriptionCondition{BroadcasterUserID: "7"}, got["channel.goal.begin"].cond)
	assert.Equal(t, helix.SubscriptionCondition{UserID: "7"}, got["user.whisper.message"].cond)

	// The second run: nothing to do.
	require.NoError(t, m.Reconcile(t.Context()))
	assert.Len(t, f.created, 25)
	assert.Empty(t, f.revoked)
}

func TestReconcileAdditionalChannels(t *testing.T) {
	t.Parallel()
	f := newFakeHelix()
	srv := httptest.NewTestServer(t, f.handler(t))
	t.Cleanup(srv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: srv.Client(), Rate: 100, Burst: 100})
	c := helix.New(hc, helix.Options{ClientID: "test", Token: func(context.Context) (string, error) { return "tok", nil }, BaseURL: srv.URL})
	m := twitch.NewManager(twitch.ManagerOptions{
		Helix:     c,
		Channels:  []string{"100", "200"},
		Whisper:   "999", // the bot account
		Moderator: "999", // the additional channels run with the bot token
		Specs:     twitch.ChannelSpecs(),
	})
	require.NoError(t, m.Reconcile(t.Context()))

	// 19 channel specs per channel (no stream, no goal — the goal
	// condition must match the token owner), 2 channels, plus the one
	// whisper.
	require.Len(t, f.created, 39)
	// No stream or goal subscriptions for the additional channels
	// (B10, plan 2026-10-22-eventsub-reference-fix.md F7).
	for _, in := range f.created {
		assert.NotEqual(t, "stream.online", in.EventType)
		assert.NotEqual(t, "stream.offline", in.EventType)
		assert.NotEqual(t, "channel.goal.begin", in.EventType)
	}
	// The channel events are subscribed per additional channel.
	var follow []helix.SubscriptionCondition
	for _, in := range f.created {
		if in.EventType == "channel.follow" {
			follow = append(follow, in.Condition)
		}
	}
	assert.ElementsMatch(t, []helix.SubscriptionCondition{
		{BroadcasterUserID: "100", ModeratorUserID: "999"},
		{BroadcasterUserID: "200", ModeratorUserID: "999"},
	}, follow)
	// The whisper goes to the bot account.
	found := false
	for _, in := range f.created {
		if in.EventType == "user.whisper.message" {
			found = true
			assert.Equal(t, helix.SubscriptionCondition{UserID: "999"}, in.Condition)
		}
	}
	assert.True(t, found)
}

func TestReconcileRevokesTheSurplus(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	f.seed(t, "channel.poll.begin", "1", helix.SubscriptionCondition{BroadcasterUserID: "7"})
	f.seed(t, "channel.follow", "2", helix.SubscriptionCondition{BroadcasterUserID: "7", ModeratorUserID: "7"})
	// A leftover of the old table (pre 2026-10-22): the old name with
	// the old condition is surplus and must be revoked.
	f.seed(t, "channel.hype_train.start", "1", helix.SubscriptionCondition{BroadcasterUserID: "7"})
	require.NoError(t, m.Reconcile(t.Context()))

	require.Len(t, f.revoked, 2)
	assert.Contains(t, f.revoked, "seed-channel.poll.begin")
	assert.Contains(t, f.revoked, "seed-channel.hype_train.start")
	// channel.follow is in the desired state: kept, not recreated.
	created := map[string]bool{}
	for _, in := range f.created {
		created[in.EventType] = true
	}
	assert.False(t, created["channel.follow"])
	assert.True(t, created["channel.raid"])
	assert.True(t, created["channel.hype_train.begin"])
}

func TestReconcileSkipsThe400(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	f.failCreate400["channel.cheer"] = true
	require.NoError(t, m.Reconcile(t.Context()))

	// channel.cheer was attempted but not created; the rest is created.
	created := map[string]bool{}
	for _, in := range f.created {
		created[in.EventType] = true
	}
	assert.True(t, created["channel.cheer"])
	assert.True(t, created["channel.follow"])
	f.mu.Lock()
	_, hasCheer := f.subs["id-channel.cheer"]
	f.mu.Unlock()
	assert.False(t, hasCheer)
	assert.Len(t, f.created, 25)
}

func TestReconcileSkipsThe409(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	f.failCreate409["channel.cheer"] = true
	require.NoError(t, m.Reconcile(t.Context()))

	// channel.cheer was attempted but not created; the rest is created.
	created := map[string]bool{}
	for _, in := range f.created {
		created[in.EventType] = true
	}
	assert.True(t, created["channel.cheer"])
	assert.True(t, created["channel.follow"])
	f.mu.Lock()
	_, hasCheer := f.subs["id-channel.cheer"]
	f.mu.Unlock()
	assert.False(t, hasCheer)
	assert.Len(t, f.created, 25)
}

func TestRunReconcilesAtTheStartAndOnTheInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFakeHelix()
		m := newManager(t, f, time.Minute)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			assert.NoError(t, m.Run(ctx))
		}()

		// The start reconciliation.
		waitFor(t, 2*time.Minute, func() int64 {
			f.mu.Lock()
			defer f.mu.Unlock()
			return int64(len(f.subs))
		})

		// A new subscription appears (e.g. after a revocation that
		// dropped some); the interval run recreates it.
		f.mu.Lock()
		delete(f.subs, "id-channel.raid")
		f.mu.Unlock()

		waitFor(t, 2*time.Minute, func() int64 {
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, ok := f.subs["id-channel.raid"]; ok {
				return 1
			}
			return 0
		})
		cancel()
		<-done
	})
}
