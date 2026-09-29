// SPDX-License-Identifier: MIT

package supervisor_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/supervisor"
)

// recorder collects status reports and other events in order.
type recorder struct {
	mu     sync.Mutex
	status []supervisor.Status
	events []string
}

func (r *recorder) report(st supervisor.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = append(r.status, st)
}

func (r *recorder) event(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

func (r *recorder) states(name string) []supervisor.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []supervisor.State
	for _, st := range r.status {
		if st.Name == name {
			out = append(out, st.State)
		}
	}
	return out
}

func (r *recorder) last(name string) supervisor.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range slices.Backward(r.status) {
		if v.Name == name {
			return v
		}
	}
	return supervisor.Status{}
}

func (r *recorder) eventList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// blocking returns a runnable that records its start and stop and runs until
// its context ends.
func blocking(rec *recorder, name string) supervisor.Runnable {
	return supervisor.RunnableFunc(func(ctx context.Context) error {
		rec.event("start %s", name)
		<-ctx.Done()
		rec.event("stop %s", name)
		return nil
	})
}

// start runs sup in the background and returns a channel with its result.
func start(ctx context.Context, sup *supervisor.Supervisor) <-chan error {
	errc := make(chan error, 1)
	go func() { errc <- sup.Run(ctx) }()
	return errc
}

func TestStartAndStopOrder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
		for _, name := range []string{"a", "b", "c"} {
			require.NoError(t, sup.Add(name, blocking(rec, name)))
		}
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)

		synctest.Wait()
		for _, name := range []string{"a", "b", "c"} {
			assert.Equal(t, []supervisor.State{supervisor.StateStarting, supervisor.StateRunning}, rec.states(name))
		}

		cancel()
		require.NoError(t, <-errc)
		events := rec.eventList()
		require.Len(t, events, 6)
		assert.ElementsMatch(t, []string{"start a", "start b", "start c"}, events[:3])
		assert.Equal(t, []string{"stop c", "stop b", "stop a"}, events[3:], "stop in reverse order")
		assert.Equal(t, supervisor.StateStopped, rec.last("a").State)
	})
}

func TestRestartWithBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		var starts []time.Time
		fail := errors.New("connection lost")
		r := supervisor.RunnableFunc(func(ctx context.Context) error {
			starts = append(starts, time.Now())
			if len(starts) <= 3 {
				return fail
			}
			<-ctx.Done()
			return nil
		})
		sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
		require.NoError(t, sup.Add("conn", r))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)

		time.Sleep(10 * time.Second)
		synctest.Wait()
		require.Len(t, starts, 4)
		// Delays of 1s, 2s and 4s with jitter: between half and the full delay.
		for i, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
			got := starts[i+1].Sub(starts[i])
			assert.GreaterOrEqual(t, got, want/2, "delay %d", i)
			assert.LessOrEqual(t, got, want, "delay %d", i)
		}
		last := rec.last("conn")
		assert.Equal(t, supervisor.StateRunning, last.State)
		assert.Equal(t, 3, last.Restarts)

		var backoffs []supervisor.Status
		rec.mu.Lock()
		for _, st := range rec.status {
			if st.State == supervisor.StateBackoff {
				backoffs = append(backoffs, st)
			}
		}
		rec.mu.Unlock()
		require.Len(t, backoffs, 3)
		for _, st := range backoffs {
			require.ErrorIs(t, st.Err, fail)
			assert.Positive(t, st.Delay)
		}

		cancel()
		require.NoError(t, <-errc)
	})
}

func TestBackoffResetsAfterStableRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var starts []time.Time
		r := supervisor.RunnableFunc(func(ctx context.Context) error {
			starts = append(starts, time.Now())
			switch len(starts) {
			case 1, 2:
				return errors.New("quick failure")
			case 3:
				// Runs stably, then fails.
				time.Sleep(supervisor.StableAfter)
				return errors.New("late failure")
			}
			<-ctx.Done()
			return nil
		})
		sup := supervisor.New(nil)
		require.NoError(t, sup.Add("conn", r))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)

		time.Sleep(2 * supervisor.StableAfter)
		synctest.Wait()
		require.Len(t, starts, 4)
		assert.LessOrEqual(t, starts[3].Sub(starts[2])-supervisor.StableAfter, supervisor.MinBackoff,
			"after a stable run the backoff starts over")

		cancel()
		require.NoError(t, <-errc)
	})
}

func TestNoRestart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		err       error
		policy    supervisor.RestartPolicy
		wantState supervisor.State
	}{
		{name: "finished", err: nil, policy: supervisor.RestartOnFailure, wantState: supervisor.StateStopped},
		{name: "permanent error", err: supervisor.Permanent(errors.New("port in use")), policy: supervisor.RestartAlways, wantState: supervisor.StateFailed},
		{name: "restart never", err: errors.New("broken"), policy: supervisor.RestartNever, wantState: supervisor.StateFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				rec := &recorder{}
				runs := 0
				r := supervisor.RunnableFunc(func(context.Context) error {
					runs++
					return tc.err
				})
				sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
				require.NoError(t, sup.Add("job", r, supervisor.WithRestartPolicy(tc.policy)))
				ctx, cancel := context.WithCancel(t.Context())
				errc := start(ctx, sup)

				time.Sleep(time.Hour)
				synctest.Wait()
				assert.Equal(t, 1, runs)
				last := rec.last("job")
				assert.Equal(t, tc.wantState, last.State)
				if tc.err != nil {
					require.ErrorIs(t, last.Err, tc.err)
				}

				cancel()
				require.NoError(t, <-errc, "a non-critical runnable does not end the supervisor")
			})
		})
	}
}

func TestRestartAlwaysAfterNil(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runs := 0
		r := supervisor.RunnableFunc(func(ctx context.Context) error {
			runs++
			if runs < 3 {
				return nil
			}
			<-ctx.Done()
			return nil
		})
		sup := supervisor.New(nil)
		require.NoError(t, sup.Add("poller", r, supervisor.WithRestartPolicy(supervisor.RestartAlways)))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)

		time.Sleep(time.Minute)
		synctest.Wait()
		assert.Equal(t, 3, runs)

		cancel()
		require.NoError(t, <-errc)
	})
}

func TestPanicIsRestarted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		runs := 0
		r := supervisor.RunnableFunc(func(ctx context.Context) error {
			runs++
			if runs == 1 {
				panic("boom")
			}
			<-ctx.Done()
			return nil
		})
		sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
		require.NoError(t, sup.Add("fragile", r))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)

		time.Sleep(supervisor.MinBackoff)
		synctest.Wait()
		assert.Equal(t, 2, runs)

		var panicErr *supervisor.PanicError
		rec.mu.Lock()
		for _, st := range rec.status {
			if p, ok := errors.AsType[*supervisor.PanicError](st.Err); ok {
				panicErr = p
			}
		}
		rec.mu.Unlock()
		require.NotNil(t, panicErr)
		assert.Equal(t, "boom", panicErr.Value)
		assert.Contains(t, string(panicErr.Stack), "supervisor_test")
		assert.Equal(t, "panic: boom", panicErr.Error())

		cancel()
		require.NoError(t, <-errc)
	})
}

func TestCriticalFailureStopsEverything(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		fail := errors.New("address already in use")
		sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
		require.NoError(t, sup.Add("worker", blocking(rec, "worker")))
		require.NoError(t, sup.Add("http", supervisor.RunnableFunc(func(context.Context) error {
			time.Sleep(time.Second)
			return supervisor.Permanent(fail)
		}), supervisor.WithCritical()))

		err := sup.Run(t.Context())
		require.ErrorIs(t, err, fail)
		assert.ErrorContains(t, err, "http")
		assert.Equal(t, []string{"start worker", "stop worker"}, rec.eventList())
		assert.Equal(t, supervisor.StateFailed, rec.last("http").State)
		assert.Equal(t, supervisor.StateStopped, rec.last("worker").State)
	})
}

func TestCriticalFinishStopsEverything(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sup := supervisor.New(nil)
		require.NoError(t, sup.Add("server", supervisor.RunnableFunc(func(context.Context) error {
			return nil
		}), supervisor.WithCritical()))

		err := sup.Run(t.Context())
		require.ErrorIs(t, err, supervisor.ErrCriticalStopped)
	})
}

func TestShutdownTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		release := make(chan struct{})
		sup := supervisor.New(nil, supervisor.WithShutdownTimeout(5*time.Second))
		require.NoError(t, sup.Add("polite", blocking(rec, "polite")))
		require.NoError(t, sup.Add("stubborn", supervisor.RunnableFunc(func(context.Context) error {
			<-release // ignores its context
			return nil
		})))
		require.NoError(t, sup.Add("last", blocking(rec, "last")))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)
		synctest.Wait()

		begin := time.Now()
		cancel()
		err := <-errc
		require.ErrorIs(t, err, supervisor.ErrShutdownTimeout)
		assert.ErrorContains(t, err, "stubborn")
		assert.NotContains(t, err.Error(), "polite")
		assert.Equal(t, 5*time.Second, time.Since(begin))
		synctest.Wait()
		assert.Contains(t, rec.eventList(), "stop polite", "the remaining runnables are canceled at once")

		close(release)
	})
}

func TestStopDuringBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		sup := supervisor.New(nil, supervisor.WithStatusFunc(rec.report))
		require.NoError(t, sup.Add("flaky", supervisor.RunnableFunc(func(context.Context) error {
			return errors.New("down")
		})))
		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)
		synctest.Wait()
		require.Equal(t, supervisor.StateBackoff, rec.last("flaky").State)

		begin := time.Now()
		cancel()
		require.NoError(t, <-errc)
		assert.Zero(t, time.Since(begin), "no need to wait for the backoff")
		assert.Equal(t, supervisor.StateStopped, rec.last("flaky").State)
	})
}

func TestRegistrationErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sup := supervisor.New(nil)
		noop := supervisor.RunnableFunc(func(ctx context.Context) error { <-ctx.Done(); return nil })
		require.NoError(t, sup.Add("a", noop))
		require.ErrorContains(t, sup.Add("a", noop), "already registered")

		ctx, cancel := context.WithCancel(t.Context())
		errc := start(ctx, sup)
		synctest.Wait()
		require.ErrorIs(t, sup.Add("b", noop), supervisor.ErrStarted)
		require.ErrorIs(t, sup.Run(ctx), supervisor.ErrStarted)

		cancel()
		require.NoError(t, <-errc)
	})
}

func TestEmptySupervisor(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.NoError(t, supervisor.New(nil).Run(ctx))
}

func TestPermanent(t *testing.T) {
	t.Parallel()
	base := errors.New("bad credentials")
	assert.NoError(t, supervisor.Permanent(nil))
	assert.False(t, supervisor.IsPermanent(base))
	assert.False(t, supervisor.IsPermanent(nil))

	perm := supervisor.Permanent(base)
	assert.True(t, supervisor.IsPermanent(perm))
	assert.True(t, supervisor.IsPermanent(fmt.Errorf("login: %w", perm)))
	require.ErrorIs(t, perm, base)
	assert.Equal(t, base.Error(), perm.Error())
}
