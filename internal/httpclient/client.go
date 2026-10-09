// SPDX-License-Identifier: Apache-2.0

// Package httpclient is the resilient HTTP client for external APIs
// (Code-ADR-0014): a retry loop with an attempt count and a total wait
// budget (github.com/sethvargo/go-retry), the circuit breaker of the API
// (internal/breaker, Code-ADR-0007), a client-side token bucket
// (golang.org/x/time/rate) fed by the server's rate-limit headers, and one
// request with a timeout. One Client serves one service API (e.g.
// "twitch.helix"), is created in the composition root (Code-ADR-0002) and
// injected into the adapters (Helix, Auth).
//
// The layers are wrappers around the base http.Client, not an
// http.RoundTripper (Code-ADR-0014, point 1): the retry must see the
// response (status and headers) to decide the wait and the abort.
package httpclient

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/sethvargo/go-retry"
	"golang.org/x/time/rate"

	"github.com/ripmav/streamcrew/internal/breaker"
)

// Standard values (Code-ADR-0014, points 4, 5 and 8).
const (
	// defaultRate is the standard client-side rate limit per second.
	defaultRate = rate.Limit(4)
	// defaultBurst is the standard burst of the token bucket.
	defaultBurst = 8
	// defaultAttempts is the standard maximum of attempts per request,
	// including the first.
	defaultAttempts = 3
	// defaultBackoffBase is the base of the standard exponential backoff.
	defaultBackoffBase = time.Second
	// defaultBudget is the standard total wait budget (backoff and 429
	// waits together).
	defaultBudget = 30 * time.Second
	// requestTimeout is the timeout of a single request.
	requestTimeout = 10 * time.Second
)

// RetryPolicy is the retry behavior of Do (Code-ADR-0014, point 4). A zero
// value means the standard values.
type RetryPolicy struct {
	// Attempts is the maximum number of attempts, including the first.
	Attempts int
	// Base is the base of the exponential backoff (full jitter).
	Base time.Duration
	// Budget is the maximum total wait time of backoff and 429 waits.
	Budget time.Duration
}

// Options configures New. Zero values mean the standard values
// (Code-ADR-0014).
type Options struct {
	// Name is the API name (e.g. "twitch.helix"), used in the logs.
	Name string
	// Base is the injected transport (Code-ADR-0002); if nil, a plain
	// client is used. A single request is limited to 10 s either way
	// (point 8).
	Base *http.Client
	// Breaker is the circuit breaker of the API (Code-ADR-0007); the same
	// instance is shared by all callers of the API. If nil, the layer is
	// skipped.
	Breaker *breaker.Breaker
	// Rate is the client-side rate limit per second (standard 4/s).
	Rate rate.Limit
	// Burst is the burst of the client-side token bucket (standard 8).
	Burst int
	// Retry is the retry behavior (standard: 3 attempts, base 1 s, full
	// jitter, 30 s budget).
	Retry RetryPolicy
	// Logger logs the retries (Code-ADR-0003) and is expected to carry
	// the component attribute; if nil, the log is discarded.
	Logger *slog.Logger
}

// Client is a resilient HTTP client for one external API
// (Code-ADR-0014). A Client is safe for concurrent use.
type Client struct {
	name    string
	base    *http.Client
	brk     *breaker.Breaker
	limiter *rate.Limiter
	retry   RetryPolicy
	log     *slog.Logger
	// mu protects limited.
	mu      sync.Mutex
	limited time.Time // until which, because of x-ratelimit-remaining = 0, requests wait
}

// New creates a Client with the standard values for zero fields.
func New(o Options) *Client {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	base := o.Base
	if base == nil {
		base = &http.Client{}
	}
	lim := o.Rate
	if lim <= 0 {
		lim = defaultRate
	}
	burst := o.Burst
	if burst <= 0 {
		burst = defaultBurst
	}
	p := o.Retry
	if p.Attempts <= 0 {
		p.Attempts = defaultAttempts
	}
	if p.Base <= 0 {
		p.Base = defaultBackoffBase
	}
	if p.Budget <= 0 {
		p.Budget = defaultBudget
	}
	return &Client{
		name:    o.Name,
		base:    base,
		brk:     o.Breaker,
		limiter: rate.NewLimiter(lim, burst),
		retry:   p,
		log:     log,
	}
}

// Response is the answer of Do: the HTTP response plus the rate-limit
// information from the headers (Code-ADR-0014, point 5). The caller closes
// the body.
type Response struct {
	*http.Response
	// RateLimit is from the x-ratelimit-* headers; zero if absent.
	RateLimit RateLimit
}

type withoutRetryKey struct{}

// WithoutRetry disables the retry for the Do and DoJSON calls made with
// the derived context: for one-shot use, e.g. the OAuth code exchange
// (the code is single-use; a retry after a lost answer would fail anyway)
// (Code-ADR-0014, point 3).
func WithoutRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, withoutRetryKey{}, true)
}

func withoutRetry(ctx context.Context) bool {
	v, _ := ctx.Value(withoutRetryKey{}).(bool)
	return v
}

// Evaluator evaluates the result of a request for the breaker of the API
// (Code-ADR-0007, point 5): network errors and 5xx count as failures, the
// other 4xx as successes, 429 and the canceled context of the caller are
// excluded. The composition root injects it into breaker.New.
func Evaluator(err error) breaker.Eval {
	if err == nil {
		return breaker.EvalSuccess
	}
	if se, ok := errors.AsType[*StatusError](err); ok {
		switch {
		case se.StatusCode == http.StatusTooManyRequests:
			return breaker.EvalExcluded
		case se.StatusCode >= 500:
			return breaker.EvalError
		default:
			return breaker.EvalSuccess
		}
	}
	if errors.Is(err, context.Canceled) {
		return breaker.EvalExcluded
	}
	return breaker.EvalError
}

// Do executes the request through the layers (Code-ADR-0014, point 2):
// retry loop (attempts and wait budget) → breaker → rate limiter → single
// request with timeout. It returns the response on a 2xx, and the caller
// closes the body. Every non-2xx answer becomes a *StatusError, network
// errors stay standard Go errors. An open or full half-open breaker
// rejects the request before it goes out (breaker.ErrUnavailable, not
// retried); WithoutRetry(ctx) limits Do to a single attempt.
func (c *Client) Do(ctx context.Context, req *http.Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("httpclient: Do: nil request")
	}
	if req.Body != nil && req.GetBody == nil {
		return nil, errors.New("httpclient: Do: request body is not replayable (GetBody is nil)")
	}
	st := &retryState{}
	var resp *Response
	err := retry.Do(ctx, c.backoff(st, withoutRetry(ctx)), func(ctx context.Context) error {
		//nolint:bodyclose // the body is closed by statusErrorFrom in attempt (non-2xx) or by the Do caller (2xx)
		r, wait, err := c.attempt(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, breaker.ErrUnavailable) {
				// A disturbed API is not retried (point 4).
				return err
			}
			se, isStatus := errors.AsType[*StatusError](err)
			if isStatus {
				switch {
				case se.StatusCode == http.StatusTooManyRequests:
					st.override = wait
					if st.override > 0 {
						// The server names the time: log it, wait
						// without jitter (point 4).
						st.limited = true
						c.log.InfoContext(ctx, "rate limited", "api", c.name, "wait", st.override)
					}
				case se.StatusCode >= 500:
				default:
					// 4xx other than 429: the service works, no retry
					// (point 4).
					return se
				}
				st.last = se
				return retry.RetryableError(se)
			}
			st.last = err
			return retry.RetryableError(err)
		}
		rl, _ := rateLimitFromHeaders(r.Header)
		resp = &Response{Response: r, RateLimit: rl}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// DoJSON is Do plus: close the body and decode it into v with
// encoding/json/v2 (Code-ADR-0014, point 3; Code-ADR-0018).
func (c *Client) DoJSON(ctx context.Context, req *http.Request, v any) error {
	resp, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.UnmarshalRead(resp.Body, v)
}

// attempt runs one request through the breaker (Code-ADR-0014, point 2)
// and returns the 2xx response. The breaker sees the typed result, so its
// evaluation counts 5xx as failures and excludes 429 (Code-ADR-0007,
// point 5). wait is the server-named 429 wait (until x-ratelimit-reset,
// fallback Retry-After), for the backoff override.
func (c *Client) attempt(ctx context.Context, req *http.Request) (*http.Response, time.Duration, error) {
	var resp *http.Response
	var wait time.Duration
	fn := func(ctx context.Context) error {
		r, err := c.send(ctx, req)
		if err != nil {
			return err
		}
		if rl, hasLimit := rateLimitFromHeaders(r.Header); hasLimit {
			c.noteRateLimit(rl)
		}
		if r.StatusCode/100 == 2 {
			resp = r
			return nil
		}
		wait = waitUntilRateLimit(r.Header)
		// The typed error is closed by statusErrorFrom and is what the
		// breaker evaluates.
		return statusErrorFrom(r)
	}
	if c.brk != nil {
		return resp, wait, c.brk.Execute(ctx, fn)
	}
	return resp, wait, fn(ctx)
}

// send waits for the rate limit and performs a single request with a
// timeout (Code-ADR-0014, points 5 and 8).
func (c *Client) send(ctx context.Context, req *http.Request) (*http.Response, error) {
	if err := c.waitRate(ctx); err != nil {
		return nil, err
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r := req.Clone(rctx)
	if req.Body != nil {
		b, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = b
	}
	//nolint:gosec // G704: the request targets the URL given by the caller of Do
	return c.base.Do(r)
}

// backoff composes the backoff of the retry loop (Code-ADR-0014, point 4):
// the 429 override (server-named wait) or the exponential backoff with
// full jitter, limited by the attempt count and the total wait budget.
// Each wait is logged, unless the 429 wait was already logged as INFO.
func (c *Client) backoff(st *retryState, noRetry bool) retry.Backoff {
	attempts := c.retry.Attempts
	if noRetry {
		attempts = 1
	}
	exp := retry.WithFullJitter(retry.NewExponential(c.retry.Base))
	base := retry.BackoffFunc(func() (time.Duration, bool) {
		if d := st.takeOverride(); d > 0 {
			return d, false
		}
		return exp.Next()
	})
	b := retry.WithMaxRetries(uint64(attempts-1), base)
	b = retry.WithMaxDuration(c.retry.Budget, b)
	return retry.BackoffFunc(func() (time.Duration, bool) {
		d, stop := b.Next()
		if stop {
			return 0, true
		}
		if st.limited {
			st.limited = false
		} else {
			c.log.Warn("retrying request", "api", c.name, "attempt", st.nextAttempt(), "wait", d, "err", st.last)
		}
		return d, false
	})
}

// retryState is the per-Do-call state between the attempt function and the
// backoff.
type retryState struct {
	override time.Duration // the 429 wait (until x-ratelimit-reset)
	last     error         // the last failure, for the log
	limited  bool          // the last failure was a logged 429
	calls    int           // the number of Next() calls
}

func (st *retryState) takeOverride() time.Duration {
	d := st.override
	st.override = 0
	return d
}

func (st *retryState) nextAttempt() int {
	st.calls++
	return st.calls + 1 // the attempt that runs after this wait
}
