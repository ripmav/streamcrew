// SPDX-License-Identifier: MIT

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCrossOriginProtection covers the CORS guard (roadmap 6.2.2): without
// an Origin the request passes through, a refused origin gets a 403 (also
// for a preflight), and an allowed origin gets the marker header or a
// direct preflight answer.
func TestCrossOriginProtection(t *testing.T) {
	t.Parallel()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	})
	deny := CrossOriginProtection(func(string) bool { return false }, next)
	allow := CrossOriginProtection(func(o string) bool { return o == "http://localhost:5173" }, next)

	// Without an Origin header the request passes through unchanged.
	rec := httptest.NewRecorder()
	deny.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body", rec.Body.String())

	// A refused origin gets a 403 without CORS headers, also for a
	// preflight.
	rec = httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api", nil)
	r.Header.Set("Origin", "http://evil.example")
	deny.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = httptest.NewRecorder()
	r = httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api", nil)
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	deny.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	// An allowed origin gets the marker header.
	rec = httptest.NewRecorder()
	r = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	allow.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))

	// The preflight of an allowed origin is answered directly.
	rec = httptest.NewRecorder()
	r = httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api", nil)
	r.Header.Set("Origin", "http://localhost:5173")
	r.Header.Set("Access-Control-Request-Method", http.MethodPost)
	allow.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "http://localhost:5173", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Authorization")
}
