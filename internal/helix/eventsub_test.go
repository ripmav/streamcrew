// SPDX-License-Identifier: MIT

package helix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

func TestCreateSubscription(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/eventsub/subscriptions", r.URL.Path)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"sub1","status":"websockets_connected","condition":{"broadcaster_user_id":"7"},"eventtype":"channel.follow","version":"2","network":"helix","transport":{"method":"websocket"}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		s, err := c.CreateSubscription(t.Context(), helix.CreateSubscriptionInput{
			Condition: helix.SubscriptionCondition{BroadcasterUserID: "7"},
			EventType: "channel.follow",
			Version:   "2",
		})
		require.NoError(t, err)
		assert.Equal(t, "sub1", s.ID)
		assert.Equal(t, "websockets_connected", s.Status)
		assert.Equal(t, "7", s.Condition.BroadcasterUserID)
		assert.Equal(t, "websocket", s.Transport.Method)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.body, &body))
		assert.Equal(t, "channel.follow", body["eventtype"])
		assert.Equal(t, "2", body["version"])
		cond, ok := body["condition"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"broadcaster_user_id": "7"}, cond)
	})
}

func TestCreateSubscriptionLimitExceeded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"Bad Request","status":400,"message":"Subscription limit exceeded"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "tok")
		_, err := c.CreateSubscription(t.Context(), helix.CreateSubscriptionInput{
			Condition: helix.SubscriptionCondition{BroadcasterUserID: "7"},
			EventType: "channel.follow",
			Version:   "2",
		})
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusBadRequest, se.StatusCode)
		assert.Contains(t, se.Snippet, "Subscription limit exceeded")
	})
}

func TestRevokeSubscription(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/eventsub/subscriptions", r.URL.Path)
			assert.Equal(t, "sub1", r.URL.Query().Get("id"))
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		require.NoError(t, c.RevokeSubscription(t.Context(), "sub1"))
	})
}

func TestGetSubscriptionsPaginates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls int
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/eventsub/subscriptions", r.URL.Path)
			calls++
			switch calls {
			case 1:
				_, _ = w.Write([]byte(`{"data":[{"id":"sub1","status":"enabled","condition":{"broadcaster_user_id":"7"},"eventtype":"channel.follow","version":"2","network":"helix","transport":{"method":"websocket"}}],"pagination":{"cursor":"CUR"}}`))
			case 2:
				assert.Equal(t, "CUR", r.URL.Query().Get("after"))
				_, _ = w.Write([]byte(`{"data":[{"id":"sub2","status":"websockets_connected","condition":{"user_id":"8"},"eventtype":"raid","version":"1","network":"helix","transport":{"method":"websocket"}}],"pagination":{"cursor":""}}`))
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		subs, err := c.GetSubscriptions(t.Context())
		require.NoError(t, err)
		require.Len(t, subs, 2)
		assert.Equal(t, "channel.follow", subs[0].EventType)
		assert.Equal(t, "8", subs[1].Condition.UserID)
	})
}
