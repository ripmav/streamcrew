// SPDX-License-Identifier: Apache-2.0

package httpclient

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxSnippetBytes is the maximum length of the body excerpt of a
// StatusError.
const maxSnippetBytes = 512

// ErrTooManyRequests marks the 429 answer: a *StatusError with code 429
// finds it via errors.Is (Code-ADR-0014, point 6).
var ErrTooManyRequests = errors.New("httpclient: too many requests")

// RateLimit is the rate-limit information from the response headers
// x-ratelimit-remaining and x-ratelimit-reset (Code-ADR-0014, point 5).
type RateLimit struct {
	// Remaining is the number of requests left in the current window.
	Remaining int
	// Reset is the Unix timestamp (in seconds) at which the window
	// resets; zero if the header is absent.
	Reset time.Time
}

// StatusError is the typed error for every non-2xx answer
// (Code-ADR-0014, point 6). Network errors and timeouts stay standard Go
// errors.
type StatusError struct {
	// StatusCode is the HTTP status code of the answer.
	StatusCode int
	// RateLimit is from the response headers, if present.
	RateLimit RateLimit
	// Snippet is a short, single-line excerpt of the body.
	Snippet string
}

// Error implements error.
func (e *StatusError) Error() string {
	if e.Snippet != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Snippet)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

// Unwrap returns ErrTooManyRequests for a 429, so errors.Is finds it.
func (e *StatusError) Unwrap() error {
	if e.StatusCode == http.StatusTooManyRequests {
		return ErrTooManyRequests
	}
	return nil
}

// statusErrorFrom converts a non-2xx answer into a *StatusError and closes
// the body.
func statusErrorFrom(resp *http.Response) *StatusError {
	rl, _ := rateLimitFromHeaders(resp.Header)
	se := &StatusError{StatusCode: resp.StatusCode, RateLimit: rl}
	if resp.Body != nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxSnippetBytes+1))
		se.Snippet = snippet(b)
	}
	return se
}

// snippet makes a short, single-line excerpt of the body.
func snippet(b []byte) string {
	s := string(b)
	if len(s) > maxSnippetBytes {
		s = s[:maxSnippetBytes] + "…"
	}
	return strings.Join(strings.Fields(s), " ")
}

// rateLimitFromHeaders reads the x-ratelimit-remaining and
// x-ratelimit-reset headers; ok is false without a parsable remaining
// header.
func rateLimitFromHeaders(h http.Header) (RateLimit, bool) {
	s := h.Get("x-ratelimit-remaining")
	if s == "" {
		return RateLimit{}, false
	}
	remaining, err := strconv.Atoi(s)
	if err != nil {
		return RateLimit{}, false
	}
	rl := RateLimit{Remaining: remaining}
	if t := h.Get("x-ratelimit-reset"); t != "" {
		if secs, err := strconv.ParseInt(t, 10, 64); err == nil {
			rl.Reset = time.Unix(secs, 0)
		}
	}
	return rl, true
}
