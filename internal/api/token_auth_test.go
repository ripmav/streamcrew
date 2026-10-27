// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/apitoken"
)

// fakeTokens is a fixed answer of the token store for the auth tests.
type fakeTokens struct {
	enabled bool
	err     error
	tokens  map[string]apitoken.Token
}

func (f fakeTokens) Enabled(context.Context) (bool, error) { return f.enabled, f.err }

func (f fakeTokens) Validate(_ context.Context, value string) (apitoken.Token, bool) {
	tok, ok := f.tokens[value]
	return tok, ok
}

// do sends a POST to the read-scoped CounterService/List with the given
// bearer token and returns the status code.
func do(t *testing.T, h http.Handler, token string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/streamcrew.v1alpha1.CounterService/List", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// doAs sends one failed-auth request (bogus token) as if it came from
// remote and returns the status code.
func doAs(t *testing.T, h http.Handler, remote string) int {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/streamcrew.v1alpha1.CounterService/List", nil)
	req.RemoteAddr = remote
	req.Header.Set("Authorization", "Bearer sc_bogus")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func authHandler(f fakeTokens) http.Handler {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return tokenAuth(f, newAuthLimiter(10, time.Minute), next)
}

// TestTokenAuthFailsClosed covers the fail-closed contract (code review
// 2026-10-24, S1/V1): an unreadable token store answers 503, an empty store
// 401, and a valid token passes.
func TestTokenAuthFailsClosed(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusServiceUnavailable, do(t, authHandler(fakeTokens{err: errors.New("corrupt")}), ""))
	assert.Equal(t, http.StatusUnauthorized, do(t, authHandler(fakeTokens{}), ""))
	assert.Equal(t, http.StatusUnauthorized, do(t, authHandler(fakeTokens{enabled: true}), ""))
	assert.Equal(t, http.StatusUnauthorized, do(t, authHandler(fakeTokens{enabled: true}), "sc_bogus"))
	assert.Equal(t, http.StatusOK, do(t, authHandler(fakeTokens{
		enabled: true,
		tokens:  map[string]apitoken.Token{"sc_ok": {Scopes: []apitoken.Scope{apitoken.Admin}}},
	}), "sc_ok"))
}

// fakeEventsStream is the api.Stream port of the revocation test.
type fakeEventsStream struct {
	events chan *v1alpha1.Event
}

func (f fakeEventsStream) Stream(context.Context, EventFilter, uint32) <-chan *v1alpha1.Event {
	return f.events
}

func (fakeEventsStream) AnswerPrompt(context.Context, string, string, bool) error { return nil }

func (fakeEventsStream) Simulate(context.Context, string, string, string, string) (string, error) {
	return "", nil
}

// liveTokens is a revocable token store for the stream test.
type liveTokens struct {
	mu     sync.Mutex
	tokens map[string]apitoken.Token
}

func (l *liveTokens) Enabled(context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tokens) > 0, nil
}

func (l *liveTokens) Validate(_ context.Context, value string) (apitoken.Token, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tok, ok := l.tokens[value]
	return tok, ok
}

// revoke removes one token value.
func (l *liveTokens) revoke(value string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.tokens, value)
}

// bearerHeader sets the Authorization header on every request.
func bearerHeader(token string) connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			// The client attaches a CallInfo to the context before the
			// interceptor chain runs.
			if info, ok := connect.CallInfoForClientContext(ctx); ok {
				info.RequestHeader().Set("Authorization", "Bearer "+token)
			}
			return next(ctx, spec)
		}
	}
}

// TestStreamEndsOnTokenRevocation covers the code review finding S12: an
// already-open stream ends when its token is revoked. It runs the real
// handler with the tokenAuth middleware against a real Connect client, so
// the token value travels the request context into the stream handler.
func TestStreamEndsOnTokenRevocation(t *testing.T) {
	t.Parallel()

	tokens := &liveTokens{tokens: map[string]apitoken.Token{
		"sc_live": {Name: "live", Scopes: []apitoken.Scope{apitoken.Read}},
	}}
	events := make(chan *v1alpha1.Event, 2)
	ts := httptest.NewServer(New(ServerConfig{
		Stream: fakeEventsStream{events: events},
		Tokens: tokens,
	}).Handler())
	t.Cleanup(ts.Close)

	cc := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, ts.URL, connecthttp.WithReadMaxBytes(0)), bearerHeader("sc_live"))
	stream, err := streamcrewv1alpha1connect.NewStreamServiceClient(cc).Subscribe(t.Context(), &v1alpha1.SubscribeRequest{})
	require.NoError(t, err)

	events <- &v1alpha1.Event{Type: "feed"}
	var first *v1alpha1.Event
	for first == nil {
		e, err := stream.Receive()
		require.NoError(t, err, "the valid token streams")
		if e.GetType() != "" {
			first = e // skip the header-flush event of the stream
		}
	}
	assert.Equal(t, "feed", first.Type)

	// The token is revoked; the next event must end the stream.
	tokens.revoke("sc_live")
	events <- &v1alpha1.Event{Type: "feed"}

	_, err = stream.Receive()
	require.Error(t, err, "the revoked token ends the stream")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

// TestTokenAuthThrottlesFailedLogins covers the throttle of failed
// authentications; the count is per IP, not per port — a changing port must
// not reset it (Nachprüfung 2026-10-24).
func TestTokenAuthThrottlesFailedLogins(t *testing.T) {
	t.Parallel()
	h := authHandler(fakeTokens{enabled: true})

	// Five failures on one port, five on another of the same IP: the count
	// is shared, the eleventh fails from the other port already.
	for range 5 {
		assert.Equal(t, http.StatusUnauthorized, doAs(t, h, "10.0.0.7:40000"))
		assert.Equal(t, http.StatusUnauthorized, doAs(t, h, "10.0.0.7:41000"))
	}
	assert.Equal(t, http.StatusTooManyRequests, doAs(t, h, "10.0.0.7:40001"), "the count is per IP")

	// Another IP is throttled independently.
	for range 10 {
		assert.Equal(t, http.StatusUnauthorized, doAs(t, h, "10.0.0.8:50000"))
	}
	assert.Equal(t, http.StatusTooManyRequests, doAs(t, h, "10.0.0.8:50001"))
}
