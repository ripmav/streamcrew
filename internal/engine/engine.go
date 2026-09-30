// SPDX-License-Identifier: Apache-2.0

// Package engine runs commands (spec command-engine.md): it queues instances
// of commands, starts each one when it gets the locks of the lock mode, runs
// its actions in order and keeps the last instances in a history. Every
// state change goes to the event bus.
//
// The engine is a runnable (Code-ADR-0004). It takes instances while Run
// runs; when the context of Run ends, it shuts them down in order (B55).
// Each instance has a goroutine from queuing to its end, owned by the
// engine; Run returns after the last one.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
)

// Limits of the spec.
const (
	// MaxPending is the most instances that wait at the same time (B15).
	MaxPending = 1000
	// HistorySize is the number of instances the history keeps (B60).
	HistorySize = 200
	// DefaultTimeLimit applies to an action whose type sets none (B72).
	DefaultTimeLimit = 60 * time.Second
	// DefaultShutdownTimeout is how long running instances may go on when
	// the core stops, unless WithShutdownTimeout sets another duration
	// (B55).
	DefaultShutdownTimeout = 10 * time.Second
)

var (
	// ErrInvalidOption is returned by New for an option without a value.
	ErrInvalidOption = errors.New("invalid option")
	// ErrAlreadyRunning is returned by a second call of Run.
	ErrAlreadyRunning = errors.New("the command engine runs already")
	// ErrNotRunning is returned for an instance queued before Run.
	ErrNotRunning = errors.New("the command engine is not running")
	// ErrClosed is returned for an instance queued after the core began to
	// stop (B55).
	ErrClosed = errors.New("the command engine is shut down")
	// ErrQueueFull is returned when MaxPending instances wait (B15).
	ErrQueueFull = errors.New("the command queue is full")
	// ErrNotFound is returned for an instance that is neither queued,
	// running nor in the history.
	ErrNotFound = errors.New("command instance not found")
	// ErrInvalidConfig is returned when the settings the engine reads are
	// not valid.
	ErrInvalidConfig = errors.New("invalid command engine settings")
	// ErrInvalidCommand is returned for a command the engine cannot run:
	// an unknown kind or error policy, or an empty action.
	ErrInvalidCommand = errors.New("invalid command")
	// ErrInvalidParams is returned for parameters that contradict each
	// other, e.g. arguments without the text they come from.
	ErrInvalidParams = errors.New("invalid run parameters")
	// ErrUnknownPauseScope is returned for a pause scope the engine does
	// not know.
	ErrUnknownPauseScope = errors.New("unknown pause scope")
)

// Commands loads commands; *command.Service implements it.
type Commands interface {
	// Command returns the current version of a command, or an error
	// wrapping store.ErrNotFound if it was deleted.
	Command(ctx context.Context, commandID id.ID) (command.Command, error)
}

// Publisher publishes events; *event.Bus implements it.
type Publisher interface {
	Publish(ctx context.Context, e event.Envelope) error
}

// Config holds the settings of the profile that the engine reads whenever it
// queues an instance, so changes apply from the next one (B28, B90). Both
// fields are required.
type Config struct {
	// Commands is the settings section "commands".
	Commands settings.Commands
	// Location is the time zone of the profile for templates.
	Location *time.Location
}

// DefaultConfig returns the settings the engine reads unless WithConfig
// sets a function: settings.DefaultCommands and UTC.
func DefaultConfig() Config {
	return Config{Commands: settings.DefaultCommands(), Location: time.UTC}
}

// validate checks cfg.
func (cfg Config) validate() error {
	if err := cfg.Commands.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	if cfg.Location == nil {
		return fmt.Errorf("%w: no time zone", ErrInvalidConfig)
	}
	return nil
}

// Option configures an Engine. An option without a value, such as a nil
// logger, makes New fail with ErrInvalidOption; to keep a default, leave the
// option out.
type Option func(*Engine) error

// WithLogger sets the logger; without it, the engine logs nothing.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) error {
		if l == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		e.logger = l
		return nil
	}
}

// WithPublisher sets where the events of the engine go (B61); without it,
// the engine publishes none.
func WithPublisher(p Publisher) Option {
	return func(e *Engine) error {
		if p == nil {
			return fmt.Errorf("%w: nil publisher", ErrInvalidOption)
		}
		e.publisher = p
		return nil
	}
}

// WithConfig sets the function that reads the settings; without it, the
// engine uses DefaultConfig.
func WithConfig(fn func(ctx context.Context) (Config, error)) Option {
	return func(e *Engine) error {
		if fn == nil {
			return fmt.Errorf("%w: nil settings function", ErrInvalidOption)
		}
		e.config = fn
		return nil
	}
}

// WithVisualAudio sets which action types are visual or audio for the lock
// mode "visual_audio" (B23); the action type registry (Code-ADR-0013) knows
// it. Without it, no action type is.
func WithVisualAudio(fn func(actionType string) bool) Option {
	return func(e *Engine) error {
		if fn == nil {
			return fmt.Errorf("%w: nil visual and audio function", ErrInvalidOption)
		}
		e.visualAudio = fn
		return nil
	}
}

// WithShutdownTimeout sets how long running instances may go on when the
// core stops (B55); it must be positive. Without it, the engine uses
// DefaultShutdownTimeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(e *Engine) error {
		if d <= 0 {
			return fmt.Errorf("%w: shutdown timeout %s is not positive", ErrInvalidOption, d)
		}
		e.shutdownTimeout = d
		return nil
	}
}

// noPublisher publishes nothing; it stands in without WithPublisher.
type noPublisher struct{}

func (noPublisher) Publish(context.Context, event.Envelope) error { return nil }

// phase is the life cycle of the engine.
type phase int

const (
	phaseIdle     phase = iota // before Run
	phaseRunning               // takes instances
	phaseStopping              // Run's context ended; running instances go on
	phaseClosed                // takes nothing
)

// Engine runs commands. It is safe for concurrent use.
type Engine struct {
	commands        Commands
	publisher       Publisher
	logger          *slog.Logger
	config          func(context.Context) (Config, error)
	visualAudio     func(actionType string) bool
	shutdownTimeout time.Duration

	// wg has the goroutines of the instances.
	wg sync.WaitGroup

	mu    sync.Mutex
	phase phase
	// paused holds back all queued instances (B40).
	paused bool
	// pending are the waiting instances in queue order.
	pending []*instance
	// active are the pending and running instances.
	active map[id.ID]*instance
	// held are the locks of the running instances.
	held map[string]struct{}
	// history has the last HistorySize queued instances, oldest first.
	history []*instance
	// changed is closed and replaced whenever an instance ends.
	changed chan struct{}
}

// New returns an engine that loads commands from commands, e.g. to replay
// an instance with the current version of its command.
func New(commands Commands, opts ...Option) (*Engine, error) {
	if commands == nil {
		return nil, fmt.Errorf("new command engine: %w: nil commands", ErrInvalidOption)
	}
	e := &Engine{
		commands:        commands,
		publisher:       noPublisher{},
		logger:          slog.New(slog.DiscardHandler),
		config:          func(context.Context) (Config, error) { return DefaultConfig(), nil },
		visualAudio:     func(string) bool { return false },
		shutdownTimeout: DefaultShutdownTimeout,
		active:          make(map[id.ID]*instance),
		held:            make(map[string]struct{}),
		changed:         make(chan struct{}),
	}
	var errs []error
	for _, opt := range opts {
		errs = append(errs, opt(e))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new command engine: %w", err)
	}
	return e, nil
}

// Run takes instances until ctx ends, then shuts down (B55): it takes no
// new instances, cancels the queued ones, gives the running ones the
// shutdown timeout to end and cancels them after it. Run returns when the
// last instance has ended; a second call returns ErrAlreadyRunning.
func (e *Engine) Run(ctx context.Context) error {
	e.mu.Lock()
	if e.phase != phaseIdle {
		e.mu.Unlock()
		return fmt.Errorf("run command engine: %w", ErrAlreadyRunning)
	}
	e.phase = phaseRunning
	e.mu.Unlock()

	<-ctx.Done()
	stopCtx := context.WithoutCancel(ctx)
	e.mu.Lock()
	e.phase = phaseStopping
	for _, in := range slices.Clone(e.pending) {
		e.cancelLocked(stopCtx, in)
	}
	e.mu.Unlock()

	grace := time.NewTimer(e.shutdownTimeout)
	defer grace.Stop()
	for {
		e.mu.Lock()
		if len(e.active) == 0 {
			e.phase = phaseClosed
			e.mu.Unlock()
			break
		}
		changed := e.changed
		e.mu.Unlock()

		select {
		case <-changed:
		case <-grace.C:
			e.mu.Lock()
			e.phase = phaseClosed
			e.logger.WarnContext(stopCtx, "command instances still running at shutdown, canceling them",
				"count", len(e.active), "timeout", e.shutdownTimeout)
			for _, in := range e.active {
				e.cancelLocked(stopCtx, in)
			}
			e.mu.Unlock()
		}
	}
	e.wg.Wait()
	return nil
}

// Start queues cmd by hand, e.g. from the user interface or the API (B14):
// also a disabled command, and without its requirements, costs and
// cooldowns. It returns the ID of the instance.
func (e *Engine) Start(ctx context.Context, cmd command.Command, p Params) (id.ID, error) {
	return e.queue(ctx, cmd, SourceManual, p)
}

// Replayed is the result of replaying one instance (B54).
type Replayed struct {
	// From is the instance of the history that was to be replayed.
	From id.ID
	// Instance is the new instance if Err is nil.
	Instance id.ID
	// Err says why From was not replayed: ErrNotFound for an instance that
	// is not in the history, the error of loading its command, e.g. for a
	// deleted one (B102), or why it could not be queued.
	Err error
}

// Replay queues instances of the history again (B54): each as a new
// instance with the same parameters and the current version of its command,
// without requirements, costs and cooldowns. It returns one result per
// instance, in the given order; one that fails does not stop the others.
func (e *Engine) Replay(ctx context.Context, instanceIDs ...id.ID) []Replayed {
	results := make([]Replayed, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		r := Replayed{From: instanceID}
		r.Instance, r.Err = e.replay(ctx, instanceID)
		results = append(results, r)
	}
	return results
}

// replay queues one instance of the history again.
func (e *Engine) replay(ctx context.Context, instanceID id.ID) (id.ID, error) {
	e.mu.Lock()
	in := e.findLocked(instanceID)
	e.mu.Unlock()
	if in == nil {
		return id.ID{}, fmt.Errorf("replay %s: %w", instanceID, ErrNotFound)
	}
	cmd, err := e.commands.Command(ctx, in.cmd.ID)
	if err != nil {
		return id.ID{}, fmt.Errorf("replay %s: %w", instanceID, err)
	}
	replay, err := e.queue(ctx, cmd, SourceReplay, in.params)
	if err != nil {
		return id.ID{}, fmt.Errorf("replay %s: %w", instanceID, err)
	}
	return replay, nil
}

// Cancel cancels an instance (B50): a queued one ends at once, a running one
// as soon as its current action notices that its context is canceled.
// Canceling an ended instance does nothing (B103).
func (e *Engine) Cancel(ctx context.Context, instanceID id.ID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	in := e.findLocked(instanceID)
	if in == nil {
		return fmt.Errorf("cancel %s: %w", instanceID, ErrNotFound)
	}
	e.cancelLocked(ctx, in)
	e.scheduleLocked(ctx)
	return nil
}

// CancelAll cancels all queued and running instances (B51).
func (e *Engine) CancelAll(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, in := range e.active {
		e.cancelLocked(ctx, in)
	}
}

// Pause pauses scope until Resume. PauseAll holds back all queued
// instances (B40): none starts, not even an unlocked one; running instances
// go on, and commands are still queued. Pausing again does nothing.
func (e *Engine) Pause(ctx context.Context, scope PauseScope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	paused, err := e.pausedLocked(scope)
	if err != nil || *paused {
		return err
	}
	*paused = true
	e.publishLocked(ctx, TypeQueuePaused, QueuePause{Scope: scope})
	return nil
}

// Resume ends the pause of scope (B40); the queued instances start in their
// order. Resuming again does nothing.
func (e *Engine) Resume(ctx context.Context, scope PauseScope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	paused, err := e.pausedLocked(scope)
	if err != nil || !*paused {
		return err
	}
	*paused = false
	e.publishLocked(ctx, TypeQueueResumed, QueuePause{Scope: scope})
	e.scheduleLocked(ctx)
	return nil
}

// Paused reports whether scope is paused.
func (e *Engine) Paused(scope PauseScope) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	paused, err := e.pausedLocked(scope)
	if err != nil {
		return false, err
	}
	return *paused, nil
}

// pausedLocked returns the flag of scope. e.mu is held.
func (e *Engine) pausedLocked(scope PauseScope) (*bool, error) {
	switch scope {
	case PauseAll:
		return &e.paused, nil
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknownPauseScope, scope)
	}
}

// History returns the last HistorySize queued instances, oldest first
// (B60).
func (e *Engine) History() []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	h := make([]Instance, 0, len(e.history))
	for _, in := range e.history {
		h = append(h, in.snapshot())
	}
	return h
}

// Instance returns an instance that waits, runs or is in the history.
func (e *Engine) Instance(instanceID id.ID) (Instance, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in := e.findLocked(instanceID)
	if in == nil {
		return Instance{}, false
	}
	return in.snapshot(), true
}

// queue queues an instance of cmd.
func (e *Engine) queue(ctx context.Context, cmd command.Command, src Source, p Params) (id.ID, error) {
	if err := checkRun(cmd, p); err != nil {
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, err)
	}
	cfg, err := e.readConfig(ctx)
	if err != nil {
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, err)
	}
	locks, err := e.locks(cmd, cfg.Commands.LockMode)
	if err != nil {
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, err)
	}
	in := newInstance(cmd, src, withTarget(p), cfg, locks)

	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.phase {
	case phaseIdle:
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, ErrNotRunning)
	case phaseRunning:
	default:
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, ErrClosed)
	}
	if len(e.pending) >= MaxPending {
		e.logger.WarnContext(ctx, "command queue full, command dropped",
			"command", cmd.Name, "source", src, "pending", len(e.pending))
		return id.ID{}, fmt.Errorf("queue command %q: %w", cmd.Name, ErrQueueFull)
	}

	// The instance outlives the request that queued it; it ends through its
	// own cancel function.
	ictx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	in.cancel = cancel
	e.active[in.id] = in
	e.pending = append(e.pending, in)
	e.history = append(e.history, in)
	if len(e.history) > HistorySize {
		e.history = slices.Delete(e.history, 0, len(e.history)-HistorySize)
	}
	e.publishLocked(ctx, TypeInstanceQueued, in.snapshot())
	e.wg.Go(func() { e.await(ictx, in) })
	e.scheduleLocked(ctx)
	return in.id, nil
}

// readConfig reads and checks the settings.
func (e *Engine) readConfig(ctx context.Context) (Config, error) {
	cfg, err := e.config(ctx)
	if err != nil {
		return Config{}, fmt.Errorf("read settings: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// checkRun checks that the engine can run cmd with p: a known kind and error
// policy, no empty action, and parameters that agree with each other.
func checkRun(cmd command.Command, p Params) error {
	if !cmd.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidCommand, cmd.Kind)
	}
	if !cmd.ErrorPolicy.Valid() {
		return fmt.Errorf("%w: unknown error policy %q", ErrInvalidCommand, cmd.ErrorPolicy)
	}
	if slices.Contains(cmd.Actions, nil) {
		return fmt.Errorf("%w: empty action", ErrInvalidCommand)
	}
	if len(p.Args) > 0 && p.ArgsText == "" {
		return fmt.Errorf("%w: arguments without the text after the trigger", ErrInvalidParams)
	}
	if len(p.Emotes) > 0 && p.Message == "" {
		return fmt.Errorf("%w: emotes without a message", ErrInvalidParams)
	}
	return nil
}

// withTarget returns p with the target of the run (B81): the one the caller
// knows, e.g. of an event, and otherwise the triggering user.
func withTarget(p Params) Params {
	if p.Target == nil {
		p.Target = p.User
	}
	return p
}

// await waits until in gets its locks, then runs it. If in is canceled while
// it waits, Cancel has ended it already.
func (e *Engine) await(ctx context.Context, in *instance) {
	select {
	case <-in.start:
	case <-ctx.Done():
		e.mu.Lock()
		started := in.state == StateRunning
		e.mu.Unlock()
		if !started {
			return
		}
	}
	e.execute(ctx, in)
}

// scheduleLocked starts the queued instances that get all their locks
// (B26): in queue order, and an instance does not overtake an earlier one
// that waits for one of its locks. e.mu is held.
func (e *Engine) scheduleLocked(ctx context.Context) {
	if e.paused || len(e.pending) == 0 {
		return
	}
	// blocked has the locks of the running instances and those that earlier
	// waiting instances wait for.
	blocked := make(map[string]struct{}, len(e.held))
	for l := range e.held {
		blocked[l] = struct{}{}
	}
	waiting := e.pending[:0]
	for _, in := range e.pending {
		free := !slices.ContainsFunc(in.locks, func(l string) bool {
			_, taken := blocked[l]
			return taken
		})
		for _, l := range in.locks {
			blocked[l] = struct{}{}
		}
		if !free {
			waiting = append(waiting, in)
			continue
		}
		for _, l := range in.locks {
			e.held[l] = struct{}{}
		}
		in.state = StateRunning
		in.startedAt = time.Now()
		e.publishLocked(ctx, TypeInstanceStarted, in.snapshot())
		close(in.start)
	}
	clear(e.pending[len(waiting):])
	e.pending = waiting
}

// cancelLocked cancels in: a pending instance ends here, a running one when
// it notices; an ended one stays as it is. The caller schedules. e.mu is
// held.
func (e *Engine) cancelLocked(ctx context.Context, in *instance) {
	switch in.state {
	case StatePending:
		e.pending = slices.DeleteFunc(e.pending, func(p *instance) bool { return p == in })
		e.endLocked(ctx, in, StateCanceled)
	case StateRunning:
		in.cancel()
	default:
	}
}

// finish ends a running instance.
func (e *Engine) finish(ctx context.Context, in *instance, state State) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range in.locks {
		delete(e.held, l)
	}
	e.endLocked(ctx, in, state)
	e.scheduleLocked(ctx)
}

// endLocked puts in into its end state. e.mu is held.
func (e *Engine) endLocked(ctx context.Context, in *instance, state State) {
	in.state = state
	in.endedAt = time.Now()
	in.cancel()
	delete(e.active, in.id)
	e.publishLocked(ctx, instanceEvent(state), in.snapshot())
	close(e.changed)
	e.changed = make(chan struct{})
}

// findLocked returns an active instance or one of the history; nil if there
// is none. e.mu is held.
func (e *Engine) findLocked(instanceID id.ID) *instance {
	if in, ok := e.active[instanceID]; ok {
		return in
	}
	for _, in := range e.history {
		if in.id == instanceID {
			return in
		}
	}
	return nil
}

// failAction records a failed action of in (B60).
func (e *Engine) failAction(in *instance, position int, actionType string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	in.errors = append(in.errors, ActionError{Position: position, Type: actionType, Message: err.Error()})
}
