// SPDX-License-Identifier: MIT

package breaker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The test errors used by fakeEval.
var (
	err500 = errors.New("http 500")
	err404 = errors.New("http 404")
	err429 = errors.New("http 429")
)

// fakeEval classifies the test errors per Code-ADR-0007, point 5.
func fakeEval(err error) Eval {
	switch {
	case err == nil:
		return EvalSuccess
	case errors.Is(err, err429), errors.Is(err, context.Canceled):
		return EvalExcluded
	case errors.Is(err, err404):
		return EvalSuccess
	default:
		return EvalError
	}
}

func newTestBreaker(t *testing.T, log *slog.Logger) *Breaker {
	t.Helper()
	return New("twitch.helix", log, fakeEval)
}

// trip opens b with five consecutive failures.
func trip(t *testing.T, b *Breaker, calls *int) {
	t.Helper()
	for range 5 {
		assert.ErrorIs(t, attempt(t, b, calls, func(context.Context) error { return err500 }), err500)
	}
}

// attempt runs fn through b and counts the calls.
func attempt(t *testing.T, b *Breaker, calls *int, fn func(context.Context) error) error {
	t.Helper()
	return b.Execute(context.Background(), func(ctx context.Context) error {
		*calls++
		return fn(ctx)
	})
}

func TestTripsAfterFiveConsecutiveFailures(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		for range 5 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err500 }), err500)
		}
		// The breaker is open: the function is not run anymore.
		err := attempt(t, b, &calls, func(context.Context) error { return nil })
		var unavailable *UnavailableError
		require.ErrorAs(t, err, &unavailable)
		assert.ErrorIs(t, err, ErrUnavailable)
		assert.Equal(t, "twitch.helix", unavailable.API)
		assert.Equal(t, time.Now().Add(30*time.Second), unavailable.RetryAfter)
		assert.Equal(t, 5, calls)
	})
}

func TestTripsAfterFailureRate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		// Five successes and five failures interleaved: 50 % of 10 requests,
		// never five in a row.
		for range 5 {
			require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err500 }), err500)
		}
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return nil }), ErrUnavailable)
		assert.Equal(t, 10, calls)
	})
}

func TestClientErrorsDoNotTrip(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		// 4xx other than 429 is a success: the service works.
		for range 20 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err404 }), err404)
		}
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		assert.Equal(t, 21, calls)
	})
}

func TestExcludedErrorsDoNotCount(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		// 429 and a canceled context are excluded from the counts.
		for range 20 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err429 }), err429)
		}
		for range 20 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error {
				return context.Canceled
			}), context.Canceled)
		}
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		assert.Equal(t, 41, calls)
	})
}

func TestHalfOpenOnlyAfterFullTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		trip(t, b, &calls)
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return nil }), ErrUnavailable)
		// Exactly the timeout is not enough ...
		time.Sleep(30 * time.Second)
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return nil }), ErrUnavailable)
		// ... more than the timeout opens a probe.
		time.Sleep(time.Second)
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
	})
}

func TestHalfOpenClosesAfterThreeSuccesses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		trip(t, b, &calls)
		time.Sleep(31 * time.Second)
		for range 3 {
			require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		}
		// Closed: normal requests again.
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		assert.Equal(t, 9, calls)
	})
}

func TestHalfOpenFailureReopens(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		trip(t, b, &calls)
		time.Sleep(31 * time.Second)
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err500 }), err500)
		// One failure in the half-open state opens the breaker again.
		err := attempt(t, b, &calls, func(context.Context) error { return nil })
		var unavailable *UnavailableError
		require.ErrorAs(t, err, &unavailable)
		assert.Equal(t, time.Now().Add(30*time.Second), unavailable.RetryAfter)
		assert.Equal(t, 6, calls)
	})
}

func TestHalfOpenAllowsOnlyThreeProbes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		trip(t, b, &calls)
		time.Sleep(31 * time.Second)

		release := make(chan struct{})
		var mu sync.Mutex
		var results []error
		for range 4 {
			go func() {
				err := b.Execute(t.Context(), func(context.Context) error {
					<-release
					return nil
				})
				mu.Lock()
				results = append(results, err)
				mu.Unlock()
			}()
		}
		synctest.Wait()
		mu.Lock()
		require.Len(t, results, 1, "only the rejected probe has returned")
		var unavailable *UnavailableError
		require.ErrorAs(t, results[0], &unavailable)
		assert.True(t, unavailable.RetryAfter.IsZero(), "the half-open time is not known")
		mu.Unlock()

		close(release)
		synctest.Wait()
		// The three probes succeeded: the breaker is closed.
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
	})
}

func TestFailuresFadeOutOfWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		for range 4 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err500 }), err500)
		}
		// The failures are older than the 60 s window.
		time.Sleep(61 * time.Second)
		for range 4 {
			assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err500 }), err500)
		}
		// The old failures do not count anymore: four in a row is not
		// enough to open the breaker.
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		assert.Equal(t, 9, calls)
	})
}

func TestExecuteReturnsFunctionErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newTestBreaker(t, nil)
		var calls int
		// The function's error is returned unchanged; the caller decides
		// what to do with it.
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return err404 }), err404)
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		assert.Equal(t, 2, calls)
	})
}

func TestOptionOverridesOpenTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := New("twitch.auth", nil, fakeEval, WithTimeout(5*time.Second))
		var calls int
		trip(t, b, &calls)
		assert.ErrorIs(t, attempt(t, b, &calls, func(context.Context) error { return nil }), ErrUnavailable)
		time.Sleep(6 * time.Second)
		require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
	})
}

func TestStateChangeLogging(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		b := newTestBreaker(t, log)
		var calls int
		trip(t, b, &calls)
		assert.Contains(t, buf.String(), `msg="circuit breaker open"`)
		assert.Contains(t, buf.String(), "breaker=twitch.helix")
		time.Sleep(31 * time.Second)
		for range 3 {
			require.NoError(t, attempt(t, b, &calls, func(context.Context) error { return nil }))
		}
		assert.Contains(t, buf.String(), `msg="circuit breaker closed"`)
	})
}
