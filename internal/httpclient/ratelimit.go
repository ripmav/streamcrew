// SPDX-License-Identifier: MIT

package httpclient

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// waitRate blocks until the client-side token bucket has a token and the
// server-side window has reset (Code-ADR-0014, point 5).
func (c *Client) waitRate(ctx context.Context) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	for {
		c.mu.Lock()
		until := c.limited
		c.mu.Unlock()
		now := time.Now()
		if !until.After(now) {
			return nil
		}
		t := time.NewTimer(until.Sub(now))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// noteRateLimit feeds the server-side rate-limit information into the
// client: with remaining = 0, no request goes out before
// x-ratelimit-reset (Code-ADR-0014, point 5).
func (c *Client) noteRateLimit(rl RateLimit) {
	if rl.Remaining > 0 || rl.Reset.IsZero() {
		return
	}
	c.mu.Lock()
	if rl.Reset.After(c.limited) {
		c.limited = rl.Reset
	}
	c.mu.Unlock()
}

// waitUntilRateLimit computes the 429 wait: until x-ratelimit-reset, with
// the fallback Retry-After and, without both, 0 (the backoff applies)
// (Code-ADR-0014, point 4).
func waitUntilRateLimit(h http.Header) time.Duration {
	if s := h.Get("x-ratelimit-reset"); s != "" {
		if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
			return positive(time.Until(time.Unix(secs, 0)))
		}
	}
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return positive(time.Duration(secs) * time.Second)
		}
		if t, err := http.ParseTime(ra); err == nil {
			return positive(time.Until(t))
		}
	}
	return 0
}

func positive(d time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return 0
}
