// SPDX-License-Identifier: MIT

package twitch_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/breaker"
	"github.com/ripmav/streamcrew/internal/httpclient"
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
	events      chan twitch.Event
	revoked     chan struct{}
	subsRevoked chan twitch.RevokedSubscription
}

func newTestHandler() *testHandler {
	return &testHandler{
		events:      make(chan twitch.Event, 100),
		revoked:     make(chan struct{}, 10),
		subsRevoked: make(chan twitch.RevokedSubscription, 10),
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

func (h *testHandler) OnSubscriptionRevoked(_ context.Context, sub twitch.RevokedSubscription) {
	select {
	case h.subsRevoked <- sub:
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

func notificationJSON(event string) string {
	return `{"metadata":{"message_id":"m-1","message_type":"notification","message_timestamp":"2026-01-01T00:00:02Z","subscription_type":"channel.follow","subscription_version":"2"},"payload":{"subscription":{"id":"sub-1","type":"channel.follow","version":"2"},"event":` + event + `}}`
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
		writeText(t, conn, notificationJSON(`{"user_id":"8"}`))
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
	var reconnects, oldClosed, dialed atomic.Int64
	release := make(chan struct{})
	srv := wsServer(t, func(t *testing.T, r *http.Request, conn *websocket.Conn) {
		switch r.URL.Path {
		case "/":
			writeText(t, conn, welcomeJSON("s1", "10"))
			writeText(t, conn, `{"metadata":{"message_id":"m-re","message_type":"session_reconnect"},"payload":{"session":{"id":"s2","status":"reconnecting","keepalive_timeout_seconds":null,"reconnect_url":"/re?reconnect_id=abc","connected_at":"2026-01-01T00:00:00Z"}}}`)
			// The old session stays open until the client closes it
			// (after the new session's welcome).
			_, _, _ = conn.Read(context.Background())
			oldClosed.Store(1)
		case "/re":
			// The reconnect URL is used as is: the reconnect identity
			// stays, the access token is not merged in.
			assert.Equal(t, "abc", r.URL.Query().Get("reconnect_id"))
			assert.Empty(t, r.URL.Query().Get("access_token"))
			dialed.Store(1)
			// Hold the new session back: while its welcome is missing,
			// the old session must still be open (no event loss).
			<-release
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
	waitFor(t, 10*time.Second, dialed.Load)
	// The new session has no welcome yet: the old connection is still
	// open (the docs: do not close it until the new welcome).
	assert.Zero(t, oldClosed.Load(), "the old session must stay open until the new welcome")
	close(release)
	waitFor(t, 10*time.Second, reconnects.Load)
	// The old session is closed once the new one has the welcome.
	waitFor(t, 10*time.Second, oldClosed.Load)
	cancel()
	<-done
}

// The old session delivers events while the new session's welcome is
// still outstanding (code review 2026-10-24, T7): the frames must not
// pile up in the buffer or be lost, the client keeps reading the old
// connection until the new welcome arrives.
func TestRunSessionReconnectDeliversOldSessionEvents(t *testing.T) {
	var dialed, welcomed atomic.Int64
	release := make(chan struct{})
	srv := wsServer(t, func(t *testing.T, r *http.Request, conn *websocket.Conn) {
		switch r.URL.Path {
		case "/":
			writeText(t, conn, welcomeJSON("s1", "10"))
			writeText(t, conn, notificationJSON(`{"user_id":"8"}`))
			writeText(t, conn, `{"metadata":{"message_id":"m-re","message_type":"session_reconnect"},"payload":{"session":{"id":"s2","status":"reconnecting","keepalive_timeout_seconds":null,"reconnect_url":"/re?reconnect_id=abc","connected_at":"2026-01-01T00:00:00Z"}}}`)
			// The event that arrives while the new session's welcome is
			// outstanding: the client must deliver it from the old
			// session, not wait for the new one.
			writeText(t, conn, notificationJSON(`{"user_id":"9"}`))
			_, _, _ = conn.Read(context.Background())
		case "/re":
			dialed.Store(1)
			<-release
			writeText(t, conn, welcomeJSON("s2", "10"))
			welcomed.Store(1)
			hold(conn)
		}
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
	waitFor(t, 10*time.Second, dialed.Load)
	e1 := h.nextEvent(t)
	assert.JSONEq(t, `{"user_id":"8"}`, string(e1.Body))
	e2 := h.nextEvent(t)
	assert.JSONEq(t, `{"user_id":"9"}`, string(e2.Body))
	assert.Zero(t, welcomed.Load(), "the second event was delivered while the new welcome was missing")
	close(release)
	waitFor(t, 10*time.Second, welcomed.Load)
	cancel()
	<-done
}

// The reader goroutine of the connection ends with the loop (code
// review 2026-10-24, T7): after the cancel, no goroutine of the client
// is left behind, the count is back at the level before the cancel.
func TestRunLoopCleansUpTheReader(t *testing.T) {
	up := make(chan struct{})
	srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
		writeText(t, conn, welcomeJSON("s1", "10"))
		hold(conn)
	})
	defer srv.Close()

	c := twitch.New(twitch.Options{
		Base:       srv.URL + "/",
		Token:      func(context.Context) (string, error) { return "tok", nil },
		HTTPClient: srv.Client(),
		Session: func(string) {
			select {
			case <-up:
			default:
				close(up)
			}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, c.Run(ctx, newTestHandler()))
	}()
	<-up // the session is up, the reader goroutine runs
	time.Sleep(200 * time.Millisecond)
	base := runtime.NumGoroutine()
	cancel()
	<-done
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= base
	}, 5*time.Second, 50*time.Millisecond, "a goroutine of the client leaked")
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
		assert.Equal(t, "10", r.URL.Query().Get("keepalive_timeout_seconds"))
		writeText(t, conn, welcomeJSON("s1", "10"))
		if n > 1 {
			// After the handshake, so the test's cancel() cannot race
			// the reading of the fresh connection.
			fresh.Store(true)
		}
		if n == 1 {
			writeText(t, conn, `{"metadata":{"message_id":"m-rev","message_type":"revocation","subscription_type":"channel.follow","subscription_version":"2"},"payload":{"subscription":{"id":"sub-1","status":"authorization_revoked","type":"channel.follow","version":"2"}}}`)
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
		DropWait:   100 * time.Millisecond,
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

// A revocation with the status user_removed (the user no longer
// exists) or version_removed (the type and version are no longer
// supported) does not tear down the session (the docs): the
// subscription is reported to the handler, the connection stays and
// keeps delivering events.
func TestRunRevocationKeepsTheSession(t *testing.T) {
	for _, status := range []string{"user_removed", "version_removed"} {
		t.Run(status, func(t *testing.T) {
			var dials atomic.Int64
			srv := wsServer(t, func(t *testing.T, _ *http.Request, conn *websocket.Conn) {
				dials.Add(1)
				writeText(t, conn, welcomeJSON("s1", "10"))
				writeText(t, conn, `{"metadata":{"message_id":"m-rev","message_type":"revocation","subscription_type":"channel.follow","subscription_version":"2"},"payload":{"subscription":{"id":"sub-1","status":"`+status+`","type":"channel.follow","version":"2"}}}`)
				writeText(t, conn, notificationJSON(`{"user_id":"8"}`))
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
			select {
			case sub := <-h.subsRevoked:
				assert.Equal(t, status, sub.Status)
				assert.Equal(t, "channel.follow", sub.Type)
			case <-time.After(10 * time.Second):
				t.Fatal("the subscription revocation was not reported")
			}
			// The token revocation must not fire, and the session keeps
			// delivering events on the same connection.
			e := h.nextEvent(t)
			assert.Equal(t, "channel.follow", e.Subscription.Type)
			select {
			case <-h.revoked:
				t.Fatal("the token revocation must not be reported")
			default:
			}
			cancel()
			<-done
			assert.Equal(t, int64(1), dials.Load(), "the session must not reconnect")
		})
	}
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

	// The test asserts the keepalive supervision (the 2 s close), not
	// the drop wait: the short wait keeps the reconnect fast.
	c := twitch.New(twitch.Options{
		Base:       srv.URL + "/",
		Token:      func(context.Context) (string, error) { return "tok", nil },
		HTTPClient: srv.Client(),
		DropWait:   200 * time.Millisecond,
	})
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

// The first dial fails with 500: Run must wait the fixed 10 s drop
// wait, then retry at the same (base) URL, and the handshake must
// carry the Twitch-Session-Id header. The 10 s run in the fake clock's
// time (synctest bubble, in-memory network).
func TestRunDialFailureRetriesAtTheBaseURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var dials, up atomic.Int64
		var mu sync.Mutex
		sessionIDs := make([]string, 0, 2)
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/", r.URL.Path, "the retry must dial the base URL")
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
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, newTestHandler()) }()
		// Before the 10 s drop wait is over, there must be no second dial.
		time.Sleep(5 * time.Second)
		assert.Equal(t, int64(1), dials.Load(), "the client dialed before the drop wait was over")
		waitFor(t, 30*time.Second, up.Load)
		cancel()
		assert.NoError(t, <-done)
		mu.Lock()
		ids := append([]string(nil), sessionIDs...)
		mu.Unlock()
		assert.GreaterOrEqual(t, dials.Load(), int64(2), "the client did not retry after the failed dial")
		require.NotEmpty(t, ids)
		for _, id := range ids {
			assert.NotEmpty(t, id, "the handshake must carry the Twitch-Session-Id header")
		}
	})
}

// Busy-loop regression (log analysis 2026-10-21, F1): the old backoff
// (base 1 s, full jitter, 30 s lifetime budget) handed back a zero wait
// with a discarded stop flag once the budget ran out, so Run spun the
// dial in a tight loop (~286 dials/s) while the breaker was open. With
// the fixed 10 s drop wait, the dials stay at the 10 s rhythm, and an
// open breaker with a known retry-after (here +30 s) is probed at its
// own pace: no dial before the retry-after, the probe after.
func TestRunBreakerOpenWaitsTheRetryAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var attempts atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		brk := breaker.New("test.eventsub", nil, httpclient.Evaluator)
		c := twitch.New(twitch.Options{
			Base:       srv.URL + "/",
			Token:      func(context.Context) (string, error) { return "tok", nil },
			HTTPClient: srv.Client(),
			Breaker:    brk,
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, newTestHandler()) }()
		// The dials land every 10 s (the drop wait): t≈0, 10, 20, 30, 40.
		// After the fifth failure the breaker is open, retry-after t≈70.
		time.Sleep(55 * time.Second)
		assert.Equal(t, int64(5), attempts.Load(), "the client must not dial before the breaker's retry-after")
		// The probe goes out at the retry-after (the breaker's open
		// timeout, +30 s), not before and not in a loop: exactly one
		// more attempt once the retry-after has passed.
		time.Sleep(50 * time.Second)
		assert.Equal(t, int64(6), attempts.Load(), "the client probed the breaker early or spun")
		cancel()
		assert.NoError(t, <-done)
	})
}

// The drop wait applies after every drop, also after a previously
// successful session (there is no lifetime budget to exhaust):
// welcome, drop, 10 s, dial, drop, 10 s, dial, and so on.
func TestRunDropWaitAfterASuccessfulSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var dials atomic.Int64
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dials.Add(1)
			conn, err := websocket.Accept(w, r, nil)
			require.NoError(t, err)
			require.NoError(t, conn.Write(r.Context(), websocket.MessageText, []byte(welcomeJSON("s1", "10"))))
			// The session dies right after the welcome; the client must
			// come back at the fixed 10 s rhythm, not faster.
			_ = conn.Close(websocket.StatusServiceRestart, "")
		}))
		defer srv.Close()

		c := twitch.New(twitch.Options{
			Base:       srv.URL + "/",
			Token:      func(context.Context) (string, error) { return "tok", nil },
			HTTPClient: srv.Client(),
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- c.Run(ctx, newTestHandler()) }()
		time.Sleep(5 * time.Second)
		assert.Equal(t, int64(1), dials.Load(), "the second dial came before the 10 s drop wait")
		time.Sleep(10 * time.Second)
		assert.Equal(t, int64(2), dials.Load(), "the 10 s rhythm broke after a successful session")
		time.Sleep(10 * time.Second)
		assert.Equal(t, int64(3), dials.Load(), "the drop wait must not accumulate a budget")
		cancel()
		assert.NoError(t, <-done)
	})
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

// lockedWriter serializes the writes of the log handler and the reads of
// the tests on the same buffer.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// TestClientBreakerOpens covers that the handshake failures run through
// the circuit breaker and open it (Code-ADR-0007); the dials then stop
// until the breaker goes half-open.
func TestClientBreakerOpens(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	var (
		bufMu sync.Mutex
		buf   bytes.Buffer
	)
	contains := func(s string) bool {
		bufMu.Lock()
		defer bufMu.Unlock()
		return strings.Contains(buf.String(), s)
	}
	log := slog.New(slog.NewTextHandler(&lockedWriter{mu: &bufMu, w: &buf}, nil))
	brk := breaker.New("test.eventsub", log, httpclient.Evaluator)
	c := twitch.New(twitch.Options{
		Base:     "ws://" + addr,
		Token:    func(context.Context) (string, error) { return "tok", nil },
		Breaker:  brk,
		Logger:   log,
		DropWait: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, &testHandler{}) }()
	require.Eventually(t, func() bool {
		return contains("circuit breaker open")
	}, 20*time.Second, 50*time.Millisecond)
	cancel()
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not end")
	}
}
