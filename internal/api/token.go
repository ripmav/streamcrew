// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"encoding/json/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// Tokens checks the API access tokens (roadmap 6.2.2).
type Tokens interface {
	// Enabled reports whether at least one token is stored; the error is
	// non-nil when the token store cannot be read.
	Enabled(ctx context.Context) (bool, error)
	// Validate checks a token value and returns the stored token.
	Validate(ctx context.Context, value string) (apitoken.Token, bool)
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

// record stores one failed attempt of the source IP and reports whether
// the address is throttled now. Only failures within the window count.
func (l *authLimiter) record(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	rec := l.fails[ip]
	kept := rec[:0]
	for _, t := range rec {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.fails[ip] = kept
		return true
	}
	if len(l.fails) > 1024 {
		l.prune(now)
	}
	l.fails[ip] = append(kept, now)
	return false
}

// prune removes the addresses without a recent failure, so the map does
// not grow unboundedly.
func (l *authLimiter) prune(now time.Time) {
	for addr, rec := range l.fails {
		fresh := false
		for _, t := range rec {
			if now.Sub(t) < l.window {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(l.fails, addr)
		}
	}
}

// sourceIP is the IP part of the remote address: a changing port must not
// reset the failure count (the Nachprüfung of the code review
// 2026-10-24, S3).
func sourceIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// tokenAuth protects the API with the stored tokens (ADR-0013 §5: every
// request needs a token). It fails closed: with no stored token no request
// passes, and an unreadable token store refuses everything with 503.
// Failed authentications are throttled per source address.
// tokenKey is the context key of the validated token value, which the
// stream handler rechecks per event so a revocation ends open streams
// (code review 2026-10-24, S12).
type tokenKey struct{}

// tokenValue returns the validated token value of the request.
func tokenValue(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(tokenKey{}).(string)
	return v, ok
}

func tokenAuth(tokens Tokens, limiter *authLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enabled, err := tokens.Enabled(r.Context())
		if err != nil {
			writeConnectError(w, http.StatusServiceUnavailable, "unavailable", "the token store is unreadable")
			return
		}
		if !enabled {
			writeConnectError(w, http.StatusUnauthorized, "unauthenticated",
				"no api tokens are stored; create one with 'streamcrew token create' and pass it with --token")
			return
		}
		value := bearerToken(r)
		tok, ok := tokens.Validate(r.Context(), value)
		if !ok {
			if limiter.record(sourceIP(r.RemoteAddr)) {
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tokenKey{}, value)))
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
