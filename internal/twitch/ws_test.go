// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/twitch"
)

// wsServer starts a test server that accepts a WebSocket per request
// and hands the connection to handle.
func wsServer(t *testing.T, handle func(t *testing.T, r *http.Request, conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		handle(t, r, conn)
	}))
}

// testHandler records the events and the revocations.
type testHandler struct {
	events  chan twitch.Event
	revoked chan struct{}
}

func newTestHandler() *testHandler {
	return &testHandler{
		events:  make(chan twitch.Event, 100),
		revoked: make(chan struct{}, 10),
	}
}

func (h *testHandler) OnMessage(_ context.Context, e twitch.Event) {
	h.events <- e
}

func (h *testHandler) OnRevoked(_ context.Context) {
	select {
	case h.revoked <- struct{}{}:
	default:
	}
}

func (h *testHandler) nextEvent(t *testing.T) twitch.Event {
	t.Helper()
	select {
	case e := <-h.events:
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("no event delivered")
		return twitch.Event{}
	}
}

func writeText(t *testing.T, conn *websocket.Conn, s string) {
	t.Helper()
	require.NoError(t, conn.Write(context.Background(), websocket.MessageText, []byte(s)))
}

func welcomeJSON(session, interval string) string {
	return `{"metadata":{"message_id":"m-welcome","message_type":"session_welcome","message_timestamp":"2026-01-01T00:00:00Z"},"payload":{"session":{"id":"` + session + `","status":"connected","keepalive_timeout_seconds":` + interval + `,"reconnect_url":null,"connected_at":"2026-01-01T00:00:00Z"}}}`
}

func keepaliveJSON() string {
	return `{"metadata":{"message_id":"m-ka","message_type":"session_keepalive","message_timestamp":"2026-01-01T00:00:01Z"},"payload":{}}`
}

func notificationJSON(subType, version string, event string) string {
	return `{"metadata":{"message_id":"m-1","message_type":"notification","message_timestamp":"2026-01-01T00:00:02Z","subscription_type":"` + subType + `","subscription_version":"` + version + `"},"payload":{"subscription":{"id":"sub-1","type":"` + subType + `","version":"` + version + `"},"event":` + event + `}}`
}

// hold keeps the connection open until the other side closes it.
func hold(conn *websocket.Conn) {
	_, _, _ = conn.Read(context.Background())
}

func newClient(t *testing.T, srv *httptest.Server) *twitch.Client {
	t.Helper()
	return twitch.New(twitch.Options{
		Base:       srv.URL + "/",
		Token:      func(context.Context) (string, error) { return "tok", nil },
		HTTPClient: srv.Client(),
	})
}

// waitFor polls f until it returns a value other than zero, giving
// up after horizon (in the fake clock's time when the test runs in
// the synctest bubble).
func waitFor(t *testing.T, horizon time.Duration, f func() int64) {
	t.Helper()
	deadline := time.Now().Add(horizon)
	for f() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the condition was never met")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunWelcomeKeepaliveAndMessage(t *testing.T) {
	srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
		writeText(t, conn, welcomeJSON("s1", "10"))
		writeText(t, conn, keepaliveJSON())
		writeText(t, conn, notificationJSON("channel.follow", "2", `{"user_id":"8"}`))
		// Hold the connection until the test closes it.
		hold(conn)
	})
	defer srv.Close()

	h := newTestHandler()
	c := newClient(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, h))
	}()

	e := h.nextEvent(t)
	assert.NotEmpty(t, e.ID)
	assert.Equal(t, "channel.follow", e.Subscription.Type)
	assert.Equal(t, "2", e.Subscription.Version)
	assert.JSONEq(t, `{"user_id":"8"}`, string(e.Body))
	cancel()
	<-done
}

func TestRunSessionReconnectAtTheGivenURL(t *testing.T) {
	var reconnects atomic.Int64
	srv := wsServer(t, func(t *testing.T, r *http.Request, conn *websocket.Conn) {
		switch r.URL.Path {
		case "/":
			writeText(t, conn, welcomeJSON("s1", "10"))
			// The reconnect URL already carries its own query
			// (the reconnect identifier); the client must merge the
			// access token into it, not replace the query.
			writeText(t, conn, `{"metadata":{"message_id":"m-re","message_type":"session_reconnect"},"payload":{"session":{"id":"s2","status":"reconnecting","keepalive_timeout_seconds":null,"reconnect_url":"/re?reconnect_id=abc","connected_at":"2026-01-01T00:00:00Z"}}}`)
			hold(conn)
		case "/re":
			assert.Equal(t, "abc", r.URL.Query().Get("reconnect_id"))
			assert.Equal(t, "tok", r.URL.Query().Get("access_token"))
			writeText(t, conn, welcomeJSON("s2", "10"))
			// The flag goes up only after the handshake, so the test's
			// cancel() cannot race the reading of the new connection.
			reconnects.Store(1)
			hold(conn)
		}
	})
	defer srv.Close()

	c := newClient(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, newTestHandler()))
	}()
	waitFor(t, 10*time.Second, reconnects.Load)
	cancel()
	<-done
}

func TestRunRevocationReconnectsWithAFreshToken(t *testing.T) {
	var dials, tokenCalls atomic.Int64
	var fresh atomic.Bool
	srv := wsServer(t, func(t *testing.T, r *http.Request, conn *websocket.Conn) {
		n := dials.Add(1)
		want := "tok1"
		if n > 1 {
			want = "tok2"
		}
		assert.Equal(t, want, r.URL.Query().Get("access_token"))
		writeText(t, conn, welcomeJSON("s1", "10"))
		if n > 1 {
			// After the handshake, so the test's cancel() cannot race
			// the reading of the fresh connection.
			fresh.Store(true)
		}
		if n == 1 {
			writeText(t, conn, `{"metadata":{"message_id":"m-rev","message_type":"revocation"},"payload":{"revocation":{"reason":"token revoked"}}}`)
		}
		hold(conn)
	})
	defer srv.Close()

	h := newTestHandler()
	c := twitch.New(twitch.Options{
		Base: srv.URL + "/",
		Token: func(context.Context) (string, error) {
			if tokenCalls.Add(1) > 1 {
				return "tok2", nil
			}
			return "tok1", nil
		},
		HTTPClient: srv.Client(),
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, h))
	}()
	waitFor(t, 10*time.Second, func() int64 {
		if fresh.Load() {
			return 1
		}
		return 0
	})
	select {
	case <-h.revoked:
	default:
		t.Fatal("the revocation was not reported")
	}
	cancel()
	<-done
}

// The server never sends a frame after the welcome: the client must
// close the connection after twice the keepalive interval and
// reconnect at the base URL. The interval is 1 s, so the wait is 2 s
// of real time.
func TestRunNoKeepaliveClosesAndReconnects(t *testing.T) {
	var upgrades atomic.Int64
	srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
		upgrades.Add(1)
		writeText(t, conn, welcomeJSON("s1", "1"))
		hold(conn)
	})
	defer srv.Close()

	c := newClient(t, srv)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, newTestHandler()))
	}()
	waitFor(t, 10*time.Second, func() int64 {
		if upgrades.Load() > 1 {
			return 1
		}
		return 0
	})
	cancel()
	<-done
}

// The first dial fails with 500: Run must retry at the same (base) URL,
// not at an empty one, and the handshake must carry the
// Twitch-Session-Id header.
func TestRunDialFailureRetriesAtTheBaseURL(t *testing.T) {
	var dials, up atomic.Int64
	var mu sync.Mutex
	sessionIDs := make([]string, 0, 2)
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sessionIDs = append(sessionIDs, r.Header.Get("Twitch-Session-Id"))
		mu.Unlock()
		if dials.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		require.NoError(t, conn.Write(r.Context(), websocket.MessageText, []byte(welcomeJSON("s1", "10"))))
		// After the handshake, so the test's cancel() cannot race the
		// reading of the new connection.
		up.Store(1)
		_, _, _ = conn.Read(r.Context())
	}))
	defer srv.Close()

	c := twitch.New(twitch.Options{
		Base:       srv.URL + "/",
		Token:      func(context.Context) (string, error) { return "tok", nil },
		HTTPClient: srv.Client(),
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, newTestHandler()))
	}()
	waitFor(t, 10*time.Second, up.Load)
	cancel()
	<-done
	mu.Lock()
	ids := append([]string(nil), sessionIDs...)
	mu.Unlock()
	assert.GreaterOrEqual(t, dials.Load(), int64(2), "the client did not retry after the failed dial")
	require.NotEmpty(t, ids)
	for _, id := range ids {
		assert.NotEmpty(t, id, "the handshake must carry the Twitch-Session-Id header")
	}
}

func TestRunMissingTokenEndsWithoutBackoff(t *testing.T) {
	var dials atomic.Int64
	srv := wsServer(t, func(_ *testing.T, _ *http.Request, conn *websocket.Conn) {
		dials.Add(1)
		hold(conn)
	})
	defer srv.Close()

	c := twitch.New(twitch.Options{
		Base:       srv.URL + "/",
		Token:      func(context.Context) (string, error) { return "", errors.New("login required") },
		HTTPClient: srv.Client(),
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, newTestHandler()) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, twitch.ErrToken)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not end")
	}
	assert.Zero(t, dials.Load())
}
