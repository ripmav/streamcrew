// SPDX-License-Identifier: MIT

// Package supervisor runs long-lived background work with restart policies,
// exponential backoff and an ordered, time-limited shutdown (Code-ADR-0004).
//
// Every long-lived goroutine of streamcrew is a Runnable registered with a
// Supervisor. The supervisor starts the runnables in registration order,
// restarts them after failures, reports every state change and stops them in
// reverse order when its context ends.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

// Backoff between restarts: it starts at MinBackoff, doubles after every
// failed attempt up to MaxBackoff and starts over once a runnable ran for at
// least StableAfter. Half of each delay is random (jitter).
const (
	MinBackoff  = time.Second
	MaxBackoff  = time.Minute
	StableAfter = time.Minute
)

// DefaultShutdownTimeout limits the whole shutdown unless WithShutdownTimeout
// sets another limit.
const DefaultShutdownTimeout = 15 * time.Second

var (
	// ErrShutdownTimeout is returned by Run when the runnables did not stop
	// within the shutdown timeout. The error names the runnable that was
	// being waited for.
	ErrShutdownTimeout = errors.New("shutdown timeout exceeded")
	// ErrStarted is returned when Add or Run is called after Run.
	ErrStarted = errors.New("supervisor already started")
	// ErrCriticalStopped is returned by Run when a critical runnable stopped
	// without an error while the supervisor was running.
	ErrCriticalStopped = errors.New("critical runnable stopped")
)

// Runnable is long-running background work.
//
// Run blocks until the work is done and leaves no goroutines behind. It
// returns nil after its context was canceled, an error if it failed.
// Wrapping the error with Permanent prevents restarts.
type Runnable interface {
	Run(ctx context.Context) error
}

// RunnableFunc adapts a function to Runnable.
type RunnableFunc func(ctx context.Context) error

// Run implements Runnable.
func (f RunnableFunc) Run(ctx context.Context) error {
	return f(ctx)
}

// RestartPolicy decides whether a runnable is started again after Run
// returned while the supervisor is still running.
type RestartPolicy int

const (
	// RestartOnFailure restarts after an error; a nil return ends the
	// runnable. This is the default.
	RestartOnFailure RestartPolicy = iota
	// RestartAlways restarts after an error and after a nil return.
	RestartAlways
	// RestartNever never restarts.
	RestartNever
)

// State is the state of a runnable.
type State string

// States of a runnable.
const (
	// StateStarting: registered, not started yet.
	StateStarting State = "starting"
	// StateRunning: Run has been called and has not returned.
	StateRunning State = "running"
	// StateBackoff: waiting for a restart after Run returned.
	StateBackoff State = "backoff"
	// StateStopped: stopped by the supervisor or finished without error.
	StateStopped State = "stopped"
	// StateFailed: failed and will not be restarted.
	StateFailed State = "failed"
)

// Status reports a state change of a runnable.
type Status struct {
	Name     string
	State    State
	Err      error         // the error that led to StateBackoff or StateFailed
	Restarts int           // number of restarts so far
	Delay    time.Duration // the wait before the next start, in StateBackoff
}

// Option configures a Supervisor.
type Option func(*Supervisor)

// WithStatusFunc registers a function that receives every state change. It is
// called from the goroutines of the runnables, so it must be safe for
// concurrent use and must not block.
func WithStatusFunc(f func(Status)) Option {
	return func(s *Supervisor) { s.status = f }
}

// WithShutdownTimeout limits the time the whole shutdown may take.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Supervisor) { s.shutdownTimeout = d }
}

// RunOption configures a single runnable.
type RunOption func(*entry)

// WithRestartPolicy sets the restart policy of a runnable.
func WithRestartPolicy(p RestartPolicy) RunOption {
	return func(e *entry) { e.policy = p }
}

// WithCritical marks a runnable as critical: if it stops for good while the
// supervisor is running, the supervisor shuts everything down and Run returns
// its error.
func WithCritical() RunOption {
	return func(e *entry) { e.critical = true }
}

type entry struct {
	name     string
	runnable Runnable
	policy   RestartPolicy
	critical bool
}

// Supervisor runs runnables; see the package documentation. The zero value is
// not usable; create one with New.
type Supervisor struct {
	logger          *slog.Logger
	status          func(Status)
	shutdownTimeout time.Duration

	mu      sync.Mutex
	entries []*entry
	started bool
}

// New returns a supervisor that logs through logger. A nil logger discards
// the log output.
func New(logger *slog.Logger, opts ...Option) *Supervisor {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Supervisor{logger: logger, status: func(Status) {}, shutdownTimeout: DefaultShutdownTimeout}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Add registers a runnable under a unique name. It must be called before Run.
func (s *Supervisor) Add(name string, r Runnable, opts ...RunOption) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("add %q: %w", name, ErrStarted)
	}
	for _, e := range s.entries {
		if e.name == name {
			return fmt.Errorf("add %q: name already registered", name)
		}
	}
	e := &entry{name: name, runnable: r}
	for _, opt := range opts {
		opt(e)
	}
	s.entries = append(s.entries, e)
	return nil
}

// Run starts all runnables and blocks until ctx ends or a critical runnable
// stops for good. It then stops the runnables in reverse registration order
// and returns once all have stopped or the shutdown timeout is exceeded. Run
// may be called only once.
func (s *Supervisor) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return ErrStarted
	}
	s.started = true
	entries := s.entries
	s.mu.Unlock()

	for _, e := range entries {
		s.report(Status{Name: e.name, State: StateStarting})
	}

	// Each runnable gets its own context, independent of ctx, so that they
	// can be stopped one after the other.
	fatal := make(chan error, len(entries))
	children := make([]*child, len(entries))
	for i, e := range entries {
		cctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		c := &child{entry: e, sup: s, cancel: cancel, done: make(chan struct{}), fatal: fatal}
		children[i] = c
		go c.loop(cctx) // owned by Run: waited for via c.done in shutdown
	}

	var runErr error
	select {
	case <-ctx.Done():
		s.logger.InfoContext(ctx, "shutting down")
	case runErr = <-fatal:
		s.logger.ErrorContext(ctx, "critical runnable stopped, shutting down", "error", runErr)
	}
	return errors.Join(runErr, s.shutdown(ctx, children))
}

// shutdown stops the children in reverse order within the shutdown timeout.
func (s *Supervisor) shutdown(ctx context.Context, children []*child) error {
	deadline := time.NewTimer(s.shutdownTimeout)
	defer deadline.Stop()

	for i, c := range slices.Backward(children) {
		c.cancel()
		select {
		case <-c.done:
			continue
		case <-deadline.C:
		}
		// Out of time: c did not stop. Cancel the runnables that have not
		// had their turn yet all at once, without waiting for them.
		for _, rest := range children[:i] {
			rest.cancel()
		}
		name := c.entry.name
		s.logger.ErrorContext(ctx, "shutdown timeout exceeded", "runnable", name, "timeout", s.shutdownTimeout)
		return fmt.Errorf("%w after %s: %q did not stop", ErrShutdownTimeout, s.shutdownTimeout, name)
	}
	s.logger.InfoContext(ctx, "all runnables stopped")
	return nil
}

func (s *Supervisor) report(st Status) {
	s.status(st)
}

// child runs one entry, restarting it according to its policy.
type child struct {
	entry  *entry
	sup    *Supervisor
	cancel context.CancelFunc
	done   chan struct{}
	fatal  chan<- error
}

func (c *child) loop(ctx context.Context) {
	defer close(c.done)
	name := c.entry.name
	logger := c.sup.logger.With("runnable", name)
	delay := MinBackoff

	for restarts := 0; ; restarts++ {
		c.sup.report(Status{Name: name, State: StateRunning, Restarts: restarts})
		logger.DebugContext(ctx, "runnable started", "restarts", restarts)
		started := time.Now()
		err := runRecovering(ctx, c.entry.runnable)

		if ctx.Err() != nil {
			c.stopped(ctx, logger, restarts, err)
			return
		}
		switch {
		case err == nil && c.entry.policy != RestartAlways:
			logger.InfoContext(ctx, "runnable finished")
			c.sup.report(Status{Name: name, State: StateStopped, Restarts: restarts})
			c.stopForGood(ErrCriticalStopped)
			return
		case err != nil && (IsPermanent(err) || c.entry.policy == RestartNever):
			logger.ErrorContext(ctx, "runnable failed", errorArgs(err)...)
			c.sup.report(Status{Name: name, State: StateFailed, Err: err, Restarts: restarts})
			c.stopForGood(err)
			return
		}

		if time.Since(started) >= StableAfter {
			delay = MinBackoff
		}
		wait := jitter(delay)
		c.sup.report(Status{Name: name, State: StateBackoff, Err: err, Restarts: restarts, Delay: wait})
		if err != nil {
			logger.ErrorContext(ctx, "runnable failed, restarting", append(errorArgs(err), "delay", wait)...)
		} else {
			logger.InfoContext(ctx, "runnable finished, restarting", "delay", wait)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.stopped(ctx, logger, restarts, nil)
			return
		case <-timer.C:
		}
		delay = min(delay*2, MaxBackoff)
	}
}

// stopped reports a runnable stopped by the supervisor. An error returned
// while stopping is logged but does not make it a failure.
func (c *child) stopped(ctx context.Context, logger *slog.Logger, restarts int, err error) {
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.ErrorContext(ctx, "runnable returned an error while stopping", errorArgs(err)...)
	}
	logger.DebugContext(ctx, "runnable stopped")
	c.sup.report(Status{Name: c.entry.name, State: StateStopped, Err: err, Restarts: restarts})
}

// stopForGood tells Run to shut down if the runnable is critical.
func (c *child) stopForGood(err error) {
	if c.entry.critical {
		c.fatal <- fmt.Errorf("%s: %w", c.entry.name, err)
	}
}

// errorArgs returns the log attributes of a failure: the error and, after a
// panic, the stack trace.
func errorArgs(err error) []any {
	args := []any{"error", err}
	if p, ok := errors.AsType[*PanicError](err); ok {
		args = append(args, "stack", string(p.Stack))
	}
	return args
}

// runRecovering calls r.Run and turns a panic into a *PanicError.
func runRecovering(ctx context.Context, r Runnable) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	return r.Run(ctx)
}
