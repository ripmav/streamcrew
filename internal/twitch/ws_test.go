// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	_, data, err := conn.Read(context.Background())
	require.NoError(t, err)
	return string(data)
}

// frameType reads the next frame from the client and returns its
// "type" field: the client's PONG, because the server only sends the
// next frame after it read the PONG.
func frameType(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	var f struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal([]byte(readText(t, conn)), &f))
	return f.Type
}

func helloJSON(session, interval string) string {
	return `{"type":"hello","payload":{"session_id":"` + session + `","expiry":123,"keepalive_interval_seconds":` + interval + `,"reconnect_url":"/re"}}`
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

func TestRunHelloPingPongAndMessage(t *testing.T) {
	srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
		writeText(t, conn, helloJSON("s1", "10"))
		writeText(t, conn, `{"type":"PING"}`)
		assert.Equal(t, "PONG", frameType(t, conn))
		writeText(t, conn, `{"type":"message","payload":{"id":"m1","event":{"event_type":"channel.follow","version":"2","condition":{"broadcaster_user_id":"7"},"event":{"user_id":"8"}}}}`)
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
	assert.Equal(t, "m1", e.ID)
	assert.Equal(t, "channel.follow", e.Event.EventType)
	assert.Equal(t, "2", e.Event.Version)
	assert.JSONEq(t, `{"user_id":"8"}`, string(e.Event.Event))
	cancel()
	<-done
}

func TestRunSessionReconnectKeepsTheSession(t *testing.T) {
	var reconnects atomic.Int64
	srv := wsServer(t, func(t *testing.T, r *http.Request, conn *websocket.Conn) {
		switch r.URL.Path {
		case "/":
			writeText(t, conn, helloJSON("s1", "10"))
			writeText(t, conn, `{"type":"PING"}`)
			assert.Equal(t, "PONG", frameType(t, conn))
			writeText(t, conn, `{"type":"session_reconnect","payload":{"reconnect_url":"/re","session_id":"s2","reason":"server reboot"}}`)
			hold(conn)
		case "/re":
			reconnects.Store(1)
			assert.Equal(t, "s2", r.URL.Query().Get("eventsub-session-id"))
			assert.Equal(t, "tok", r.URL.Query().Get("access_token"))
			writeText(t, conn, helloJSON("s2", "10"))
			writeText(t, conn, `{"type":"PING"}`)
			assert.Equal(t, "PONG", frameType(t, conn))
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
			fresh.Store(true)
		}
		assert.Equal(t, want, r.URL.Query().Get("access_token"))
		writeText(t, conn, helloJSON("s1", "10"))
		writeText(t, conn, `{"type":"PING"}`)
		assert.Equal(t, "PONG", frameType(t, conn))
		if n == 1 {
			writeText(t, conn, `{"type":"revocation","payload":{"reason":"token revoked"}}`)
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

// The server never sends a PING: the client must close the
// connection after twice the keepalive interval and reconnect at the
// base URL. The interval is 1 s, so the wait is 2 s of real time.
func TestRunNoKeepaliveClosesAndReconnects(t *testing.T) {
	var upgrades atomic.Int64
	srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
		upgrades.Add(1)
		writeText(t, conn, helloJSON("s1", "1"))
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
