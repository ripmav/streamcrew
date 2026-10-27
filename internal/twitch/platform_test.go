// SPDX-License-Identifier: MIT

package twitch_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// fakeAuth is the streamer account of the tests.
type fakeAuth struct {
	mu         sync.Mutex
	ready      bool
	token      string
	tokenFails bool
	// bot gives the fake a bot account (ID 8, login "bot"); off by
	// default, the tests opt in with readyAuthWithBot.
	bot bool
}

func (f *fakeAuth) Streamer(context.Context) (twitch.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return twitch.AccountState{AccountID: "7", Login: "streamer", Ready: f.ready}, nil
}

func (f *fakeAuth) Token(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokenFails {
		return "", errors.New("login required")
	}
	return f.token, nil
}

func (f *fakeAuth) ClientID(context.Context) (string, error) {
	return "client", nil
}

func (f *fakeAuth) Bot(context.Context) (twitch.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.bot {
		return twitch.AccountState{}, nil // no bot account in the tests by default
	}
	return twitch.AccountState{AccountID: "8", Login: "bot", Ready: f.ready}, nil
}

func (f *fakeAuth) BotToken(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.bot {
		return "", errors.New("no bot account")
	}
	return f.token, nil
}

func (f *fakeAuth) setReady(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ready = v
}

func (f *fakeAuth) setTokenFails(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenFails = v
}

// helixServer is a fake Helix for the subscription manager and the
// stream state: it creates the subscriptions and says the stream is
// offline.
type helixServer struct {
	mu      sync.Mutex
	created int
}

func (h *helixServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		switch r.URL.Path {
		case "/streams":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/eventsub/subscriptions":
			switch r.Method {
			case http.MethodGet:
				_, _ = w.Write([]byte(`{"data":[]}`))
			case http.MethodPost:
				h.created++
				w.WriteHeader(http.StatusCreated)
				_, _ = fmt.Fprintf(w, `{"data":{"id":"s%d"}}`, h.created)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

// keepAliveServer is a fake EventSub server that keeps the connection
// alive with session_keepalive frames until the test closes it or the
// drop flag is set.
type keepAliveServer struct {
	drop atomic.Bool
}

func (s *keepAliveServer) handler(_ *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(welcomeJSON("s1", "1"))); err != nil {
			return
		}
		for !s.drop.Load() {
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(keepaliveJSON())); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		_ = conn.Close(websocket.StatusGoingAway, "test")
	}
}

// platformFor builds the platform with the fake auth, the fake Helix
// and the fake EventSub server.
func platformFor(t *testing.T, r *fakeReceiver, a *fakeAuth, ws *keepAliveServer) *twitch.Platform {
	t.Helper()
	hl := &helixServer{}
	hlSrv := httptest.NewTestServer(t, hl.handler(t))
	t.Cleanup(hlSrv.Close)
	wsSrv := httptest.NewTestServer(t, ws.handler(t))
	t.Cleanup(wsSrv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: hlSrv.Client(), Rate: 100, Burst: 100})
	return twitch.NewPlatform(twitch.PlatformOptions{
		Receiver:   r,
		Auth:       a,
		Helix:      hc,
		DialClient: wsSrv.Client(),
		Base:       wsSrv.URL + "/",
		HelixBase:  hlSrv.URL,
		Poll:       10 * time.Millisecond,
	})
}

func TestPlatformStartsWithoutToken(t *testing.T) {
	r := &fakeReceiver{}
	a := &fakeAuth{token: "tok"}
	ws := &keepAliveServer{}
	p := platformFor(t, r, a, ws)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()

	// Without a token the platform stays disconnected.
	time.Sleep(50 * time.Millisecond)
	assert.False(t, p.Status().Streamer)

	// The token appears: the platform connects, reconciles and reports
	// the stream state (offline: the fake Helix has no stream).
	a.setReady(true)
	waitFor(t, 10*time.Second, func() int64 {
		if p.Status().Streamer {
			return 1
		}
		return 0
	})
	r.mu.Lock()
	streams := append([]bool(nil), r.streams...)
	r.mu.Unlock()
	assert.Equal(t, []bool{false}, streams)
	cancel()
	<-done
	assert.False(t, p.Status().Streamer)
}

func TestPlatformStopsOnTokenLoss(t *testing.T) {
	r := &fakeReceiver{}
	a := &fakeAuth{token: "tok", ready: true}
	ws := &keepAliveServer{}
	p := platformFor(t, r, a, ws)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()
	waitFor(t, 10*time.Second, func() int64 {
		if p.Status().Streamer {
			return 1
		}
		return 0
	})

	// The token is lost and the server drops the connection: the session
	// ends (the reconnect would fail without a token), and the platform
	// stays disconnected while the account is not ready.
	a.setTokenFails(true)
	a.setReady(false)
	ws.drop.Store(true)
	// The drop lands on the 10 s drop wait before the token check ends
	// Run: the horizon must cover the wait.
	waitFor(t, 15*time.Second, func() int64 {
		if !p.Status().Streamer {
			return 1
		}
		return 0
	})
	cancel()
	<-done
}

// logBuf is a bytes.Buffer guarded by a mutex: the log handler writes
// from the platform's goroutine while the test reads it.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestPlatformRunLogsNotReadyOnce: an account that is not ready (for
// example login_required after a new required scope, ADR-0014) is
// announced once, not per poll.
func TestPlatformRunLogsNotReadyOnce(t *testing.T) {
	t.Parallel()
	buf := &logBuf{}
	a := &fakeAuth{ready: false}
	p := twitch.NewPlatform(twitch.PlatformOptions{
		Receiver: &fakeReceiver{},
		Auth:     a,
		Logger:   slog.New(slog.NewTextHandler(buf, nil)),
		Poll:     5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()
	waitFor(t, 10*time.Second, func() int64 {
		if strings.Contains(buf.String(), "the streamer account is not ready") {
			return 1
		}
		return 0
	})
	time.Sleep(100 * time.Millisecond) // a few more polls: no repeat
	cancel()
	<-done
	assert.Equal(t, 1, strings.Count(buf.String(), "the streamer account is not ready"))
}
