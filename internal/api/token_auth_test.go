// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// fakeTokens is a fixed answer of the token store for the auth tests.
type fakeTokens struct {
	enabled bool
	err     error
	tokens  map[string]apitoken.Token
}

func (f fakeTokens) Enabled() (bool, error) { return f.enabled, f.err }

func (f fakeTokens) Validate(value string) (apitoken.Token, bool) {
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

// TestTokenAuthThrottlesFailedLogins covers the per-address throttle (code
// review 2026-10-24, S3): beyond the limit the address gets 429.
func TestTokenAuthThrottlesFailedLogins(t *testing.T) {
	t.Parallel()
	h := authHandler(fakeTokens{enabled: true})

	for range 10 {
		assert.Equal(t, http.StatusUnauthorized, do(t, h, "sc_bogus"))
	}
	assert.Equal(t, http.StatusTooManyRequests, do(t, h, "sc_bogus"))
}
