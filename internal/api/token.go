// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// Tokens checks the API access tokens (roadmap 6.2.2).
type Tokens interface {
	// Enabled reports whether at least one token is stored; the error is
	// non-nil when the token file is unreadable or corrupt.
	Enabled() (bool, error)
	// Validate checks a token value and returns the stored token.
	Validate(value string) (apitoken.Token, bool)
}

// authLimiter throttles repeated failed authentications per source address
// (S3 of the code review 2026-10-24): beyond the limit per window an
// address gets 429 until the window slides past its failures.
type authLimiter struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	fails  map[string][]time.Time
}

func newAuthLimiter(limit int, window time.Duration) *authLimiter {
	return &authLimiter{limit: limit, window: window, fails: make(map[string][]time.Time)}
}

// record stores one failed attempt and reports whether the address is
// throttled now.
func (l *authLimiter) record(addr string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 1024 {
		l.fails = make(map[string][]time.Time) // a fresh map prunes every stale address
	}
	rec := l.fails[addr]
	kept := rec[:0]
	for _, t := range rec {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.fails[addr] = kept
		return true
	}
	l.fails[addr] = append(kept, now)
	return false
}

// tokenAuth protects the API with the stored tokens (ADR-0013 §5: every
// request needs a token). It fails closed: with no stored token no request
// passes, and an unreadable token store refuses everything with 503.
// Failed authentications are throttled per source address.
func tokenAuth(tokens Tokens, limiter *authLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled, err := tokens.Enabled()
		if err != nil {
			writeConnectError(w, http.StatusServiceUnavailable, "unavailable", "the token store is unreadable")
			return
		}
		if !enabled {
			writeConnectError(w, http.StatusUnauthorized, "unauthenticated",
				"no api tokens are stored; create one with 'streamcrew token create' and pass it with --token")
			return
		}
		tok, ok := tokens.Validate(bearerToken(r))
		if !ok {
			if limiter.record(r.RemoteAddr) {
				writeConnectError(w, http.StatusTooManyRequests, "resource_exhausted", "too many failed authentications, slow down")
				return
			}
			writeConnectError(w, http.StatusUnauthorized, "unauthenticated", "a valid bearer token is required")
			return
		}
		need := requiredScope(r.URL.Path)
		if !tok.Satisfies(need) {
			writeConnectError(w, http.StatusForbidden, "permission_denied",
				"the token lacks the scope "+string(need))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken returns the token from the Authorization header.
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return auth[7:]
	}
	return ""
}

// writeConnectError writes a Connect protocol error response, which the
// Connect clients parse into the matching code.
func writeConnectError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	data, err := json.Marshal(map[string]string{"code": code, "message": message})
	if err == nil {
		_, _ = w.Write(data)
	}
}
