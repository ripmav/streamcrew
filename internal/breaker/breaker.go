// SPDX-License-Identifier: Apache-2.0

// Package breaker is the thin layer over github.com/sony/gobreaker/v2
// (Code-ADR-0007): one named circuit breaker per external service API, with
// the standard values, a unified evaluation of request results, logging of
// state changes and the domain error ErrUnavailable. Adapters use only this
// package; gobreaker types do not appear outside internal/breaker and
// internal/httpclient.
package breaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker/v2"
)

// Eval is the evaluation of a request result (Code-ADR-0007, point 5).
type Eval int

const (
	// EvalSuccess is a result where the service works (no error, HTTP 2xx,
	// 3xx, and 4xx other than 429).
	EvalSuccess Eval = iota
	// EvalError is a result where the service is disturbed (network error,
	// request timeout, HTTP 5xx).
	EvalError
	// EvalExcluded is a result that does not count (HTTP 429, canceled
	// context of the caller).
	EvalExcluded
)

// Evaluator classifies the error of a request; a nil error is EvalSuccess.
type Evaluator func(error) Eval

// Standard values (Code-ADR-0007, point 4).
const (
	// defaultInterval is the length of the rolling count window.
	defaultInterval = 60 * time.Second
	// defaultBucketPeriod is the step of the rolling window.
	defaultBucketPeriod = 10 * time.Second
	// defaultConsecutiveFailures opens the breaker after at least this
	// many errors in a row.
	defaultConsecutiveFailures = 5
	// defaultWindowRequests opens the breaker after at least this many
	// requests in the window ...
	defaultWindowRequests = 10
	// defaultWindowFailureRate ... with at least this percentage of
	// failures.
	defaultWindowFailureRate = 50
	// defaultOpenTimeout is how long the breaker stays open.
	defaultOpenTimeout = 30 * time.Second
	// defaultHalfOpenRequests is the number of probe requests allowed in
	// the half-open state.
	defaultHalfOpenRequests = 3
)

// ErrUnavailable is the domain error of a rejected or disturbed external
// API (Code-ADR-0007, point 7). Callers check it with errors.Is.
var ErrUnavailable = errors.New("unavailable")

// UnavailableError is an ErrUnavailable with the name of the API and, when
// known, the earliest time for a new attempt.
type UnavailableError struct {
	// API is the name of the breaker, e.g. "twitch.helix".
	API string
	// RetryAfter is the earliest time for a new attempt while the breaker
	// is open. It is zero when the time is not known (half-open and full).
	RetryAfter time.Time
}

// Error implements error.
func (e *UnavailableError) Error() string {
	if e.RetryAfter.IsZero() {
		return "unavailable: " + e.API
	}
	return fmt.Sprintf("unavailable: %s: retry after %s", e.API, e.RetryAfter.UTC().Format(time.RFC3339))
}

// Unwrap returns ErrUnavailable for errors.Is.
func (e *UnavailableError) Unwrap() error { return ErrUnavailable }

// Breaker is a named circuit breaker for one external API. A Breaker is
// safe for concurrent use.
type Breaker struct {
	name string
	log  *slog.Logger
	cb   *gobreaker.CircuitBreaker[struct{}]
	// openUntil holds the Unix nanos until which the breaker is open, or
	// 0. The state-change callback runs under the gobreaker lock, so the
	// write is atomic.
	openUntil atomic.Int64
}

// New creates a Breaker named name (e.g. "twitch.helix") with the standard
// values (Code-ADR-0007, point 4), the given evaluation of request results
// (point 5), and logging of the state changes. A nil logger discards; a nil
// evaluator counts every error as a failure.
func New(name string, logger *slog.Logger, eval Evaluator, opts ...Option) *Breaker {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if eval == nil {
		eval = func(err error) Eval {
			if err == nil {
				return EvalSuccess
			}
			return EvalError
		}
	}

	cfg := config{
		interval:     defaultInterval,
		bucketPeriod: defaultBucketPeriod,
		timeout:      defaultOpenTimeout,
		maxRequests:  defaultHalfOpenRequests,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	b := &Breaker{name: name, log: logger.With("breaker", name)}
	b.cb = gobreaker.NewCircuitBreaker[struct{}](gobreaker.Settings{
		Name:         name,
		Interval:     cfg.interval,
		BucketPeriod: cfg.bucketPeriod,
		Timeout:      cfg.timeout,
		MaxRequests:  cfg.maxRequests,
		ReadyToTrip:  defaultReadyToTrip,
		IsExcluded: func(err error) bool {
			return err != nil && eval(err) == EvalExcluded
		},
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			return eval(err) == EvalSuccess
		},
		OnStateChange: func(_ string, from, to gobreaker.State) {
			b.onStateChange(from, to, cfg.timeout)
		},
	})
	return b
}

// onStateChange logs a state change and tracks when the breaker becomes
// half-open. It runs under the gobreaker lock, so it must only log and
// write the atomic, never call the breaker (Code-ADR-0007, point 8).
func (b *Breaker) onStateChange(from, to gobreaker.State, timeout time.Duration) {
	switch to {
	case gobreaker.StateOpen:
		b.openUntil.Store(time.Now().Add(timeout).UnixNano())
		b.log.Warn("circuit breaker open", "from", from.String())
	case gobreaker.StateClosed:
		b.openUntil.Store(0)
		b.log.Info("circuit breaker closed", "from", from.String())
	default:
		// half-open: nothing to log or track.
	}
}

// Execute runs fn through the breaker. If the breaker is open or half-open
// and full, fn is not run and Execute returns a *UnavailableError with the
// name of the API and, while open, the earliest time for a new attempt.
// Errors from fn are returned unchanged; their evaluation only affects the
// state of the breaker.
func (b *Breaker) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	_, err := b.cb.Execute(func() (struct{}, error) {
		return struct{}{}, fn(ctx)
	})
	if err == nil {
		return nil
	}
	if !errors.Is(err, gobreaker.ErrOpenState) && !errors.Is(err, gobreaker.ErrTooManyRequests) {
		return err
	}
	unavailable := &UnavailableError{API: b.name}
	if errors.Is(err, gobreaker.ErrOpenState) {
		if ns := b.openUntil.Load(); ns > 0 {
			unavailable.RetryAfter = time.Unix(0, ns)
		}
	}
	return unavailable
}

// config holds the overridable standard values.
type config struct {
	interval     time.Duration
	bucketPeriod time.Duration
	timeout      time.Duration
	maxRequests  uint32
}

// Option overrides a standard value of New (Code-ADR-0007, point 4:
// overridable per API in the code, no user setting for now).
type Option func(*config)

// WithInterval sets the length of the count window.
func WithInterval(d time.Duration) Option {
	return func(c *config) { c.interval = d }
}

// WithBucketPeriod sets the step of the rolling window.
func WithBucketPeriod(d time.Duration) Option {
	return func(c *config) { c.bucketPeriod = d }
}

// WithTimeout sets how long the breaker stays open.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithMaxRequests sets the number of probe requests allowed in the
// half-open state.
func WithMaxRequests(n uint32) Option {
	return func(c *config) { c.maxRequests = n }
}

// defaultReadyToTrip opens the breaker after at least 5 errors in a row, or
// after at least 10 requests in the window with at least 50 % failures
// (Code-ADR-0007, point 4).
func defaultReadyToTrip(counts gobreaker.Counts) bool {
	if counts.ConsecutiveFailures >= defaultConsecutiveFailures {
		return true
	}
	return counts.Requests >= defaultWindowRequests &&
		counts.TotalFailures*100 >= counts.Requests*defaultWindowFailureRate
}
