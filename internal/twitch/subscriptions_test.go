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
}

func newFakeHelix() *fakeHelix {
	return &fakeHelix{
		subs:          map[string]helix.EventSubSubscription{},
		failCreate400: map[string]bool{},
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
			id := "id-" + in.EventType
			f.subs[id] = helix.EventSubSubscription{
				ID:        id,
				Status:    "websocket_pending",
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
		AccountID: "7",
		Interval:  interval,
	})
}

// seed puts a subscription into the fake, as if it existed.
func (f *fakeHelix) seed(t *testing.T, eventType, version string, c helix.SubscriptionCondition) {
	t.Helper()
	id := "seed-" + eventType
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = helix.EventSubSubscription{
		ID:        id,
		Status:    "websockets_connected",
		Condition: c,
		EventType: eventType,
		Version:   version,
	}
}

func TestReconcileCreatesTheMissingSubscriptions(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	require.NoError(t, m.Reconcile(t.Context()))

	require.Len(t, f.created, 13)
	// The exact table: versions and conditions.
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
	assert.Equal(t, "2", got["channel.chat.message"].version)
	assert.Equal(t, "1", got["stream.online"].version)
	assert.Equal(t, "1", got["channel.cheer"].version)
	assert.Equal(t, "2", got["channel.moderate"].version)
	assert.Equal(t, "1", got["channel.shared_chat.begin"].version)
	assert.Equal(t, helix.SubscriptionCondition{BroadcasterUserID: "7"}, got["channel.follow"].cond)
	assert.Equal(t, helix.SubscriptionCondition{UserID: "7"}, got["user.whisper.message"].cond)

	// The second run: nothing to do.
	require.NoError(t, m.Reconcile(t.Context()))
	assert.Len(t, f.created, 13)
	assert.Empty(t, f.revoked)
}

func TestReconcileRevokesTheSurplus(t *testing.T) {
	f := newFakeHelix()
	m := newManager(t, f, 0)
	f.seed(t, "channel.poll.begin", "1", helix.SubscriptionCondition{BroadcasterUserID: "7"})
	f.seed(t, "channel.follow", "2", helix.SubscriptionCondition{BroadcasterUserID: "7"})
	require.NoError(t, m.Reconcile(t.Context()))

	require.Len(t, f.revoked, 1)
	assert.Equal(t, "seed-channel.poll.begin", f.revoked[0])
	// channel.follow is in the desired state: kept, not recreated.
	created := map[string]bool{}
	for _, in := range f.created {
		created[in.EventType] = true
	}
	assert.False(t, created["channel.follow"])
	assert.True(t, created["channel.raid"])
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
	assert.Len(t, f.created, 13)
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
