// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ripmav/streamcrew/internal/apitoken"
)

// Tokens checks the API access tokens (roadmap 6.2.2).
type Tokens interface {
	// Enabled reports whether at least one token is stored.
	Enabled() bool
	// Validate checks a token value and returns the stored token.
	Validate(value string) (apitoken.Token, bool)
}

// tokenAuth protects the API with the stored tokens: while no token is
// stored, the API is open (bootstrap, loopback default per ADR-0003); once
// a token exists, every request needs a valid bearer token with the scope
// the method requires.
func tokenAuth(tokens Tokens, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tokens.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		tok, ok := tokens.Validate(bearerToken(r))
		if !ok {
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
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
