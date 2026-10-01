// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/template"
)

// Performer is an action the engine can run (plan §6.9, Code-ADR-0013); the
// action types (roadmap 3.3) implement it. An action without it, such as one
// of an unknown type, is skipped (B70).
type Performer interface {
	command.Action
	// Enabled reports the switch "active": an inactive action is skipped
	// with its child actions and needs no lock (actions.md B1).
	Enabled() bool
	// Perform runs the action and honors the cancellation of ctx. ctx has
	// no deadline: when the time limit of the action runs out, ctx is
	// canceled with ErrTimeLimit as cause (B72).
	Perform(ctx context.Context, run *Run) error
}

// ActionTypes tells the engine what it needs to know about action types;
// the action type registry implements it (Code-ADR-0013).
type ActionTypes interface {
	// VisualAudio reports whether actions of the type share the lock
	// "visual_audio" (B23).
	VisualAudio(actionType string) bool
	// Missing returns the capabilities that actions of the type need and
	// the core does not have; the list is empty if none is missing
	// (actions.md B7, ADR-0013).
	Missing(actionType string) []capability.Capability
}

// ChildOutcome says how a container goes on after Run.PerformChild
// (Code-ADR-0013).
type ChildOutcome string

const (
	// ChildNext means the child action ran, was skipped or failed under the
	// error policy "continue": the container goes on.
	ChildNext ChildOutcome = "next"
	// ChildEnd means the instance ends, because an action asked to stop,
	// failed under the error policy "abort" or the instance was canceled:
	// the container returns nil at once.
	ChildEnd ChildOutcome = "end"
)

var (
	// ErrStop is returned by an action to end its instance early as
	// completed, e.g. the action "end the current command" (B4).
	ErrStop = errors.New("stop the command")
	// ErrTimeLimit is the error of an action that ran past its time limit
	// (B72).
	ErrTimeLimit = errors.New("action time limit exceeded")
	// ErrInvalidTimeLimit is returned by Run.LimitTo for a time limit that
	// is not positive.
	ErrInvalidTimeLimit = errors.New("invalid action time limit")
	// ErrCapability is the error of an action whose type needs a capability
	// the core does not have; the message names it (actions.md B7).
	ErrCapability = errors.New("missing capability")
	// ErrNoAction is returned by the methods of Run that act on the running
	// action when none runs.
	ErrNoAction = errors.New("no action is running")
	// ErrInvalidChild is returned by Run.PerformChild for an index that is
	// not a child action of the running action.
	ErrInvalidChild = errors.New("not a child action of the running action")
)

// Run is an instance as its actions see it (plan §6.9). The actions of an
// instance run one after the other, so they use it without locking.
type Run struct {
	engine *Engine
	in     *instance
	// frames are the running action and the actions that hold it, innermost
	// last.
	frames []*frame
	// end says why the actions end early.
	end runEnd
}

// runEnd says why the actions of a run end before the last one.
type runEnd int

const (
	endNone   runEnd = iota // the actions go on
	endStop                 // an action asked to stop (B4)
	endFailed               // an action failed under the error policy "abort" (B71)
)

// frame is an action while it runs, with the time limit of its own time
// (B72, actions.md B8). Only the goroutine of the instance uses it; the
// timer only cancels.
type frame struct {
	action Performer
	path   []int
	cancel context.CancelCauseFunc
	// limit is the time limit last set, for the error message.
	limit time.Duration
	// left is the time left when the timer last stopped.
	left time.Duration
	// since is when the timer last started.
	since time.Time
	// timer runs while the action runs itself; nil while it stands.
	timer *time.Timer
}

// run starts the timer with the time left.
func (f *frame) run() {
	f.since = time.Now()
	f.timer = time.AfterFunc(f.left, func() { f.cancel(ErrTimeLimit) })
}

// stand stops the timer and keeps the time left.
func (f *frame) stand() {
	if f.timer == nil {
		return
	}
	if f.timer.Stop() {
		f.left -= time.Since(f.since)
	} else {
		f.left = 0 // the timer has fired
	}
	f.timer = nil
}

// InstanceID returns the ID of the instance.
func (r *Run) InstanceID() id.ID {
	return r.in.id
}

// Command returns the version of the command the instance runs (B3). It
// must not be changed.
func (r *Run) Command() command.Command {
	return r.in.cmd
}

// Params returns the parameters of the run (B80). They must not be changed.
func (r *Run) Params() Params {
	return r.in.params
}

// Scope returns the scope of the templates of the run. Actions set the
// values of the run there.
func (r *Run) Scope() *template.Scope {
	return r.in.scope
}

// Path returns the position of the running action in its command: the
// position from 1 at each level, e.g. [3 2] for the second child action of
// the third action (actions.md B9). It is nil while no action runs.
func (r *Run) Path() []int {
	f := r.top()
	if f == nil {
		return nil
	}
	return slices.Clone(f.path)
}

// LimitTo sets the time limit of the running action to d, counted from now
// (actions.md B8), e.g. for a wait whose duration comes from a template. d
// must be positive. If the limit has run out already, LimitTo returns an
// error wrapping ErrTimeLimit.
func (r *Run) LimitTo(d time.Duration) error {
	f := r.top()
	switch {
	case f == nil:
		return ErrNoAction
	case d <= 0:
		return fmt.Errorf("%w: %s", ErrInvalidTimeLimit, d)
	}
	f.stand()
	if f.left <= 0 {
		return fmt.Errorf("%w after %s", ErrTimeLimit, f.limit)
	}
	f.limit, f.left = d, d
	f.run()
	return nil
}

// PerformChild runs Children()[i] of the running action, which must be a
// Container (Code-ADR-0013). The child action runs as an action at the top
// level does (actions.md B1, B7–B9): it is skipped if it is inactive, fails
// without running if a capability is missing, has its own time limit,
// follows the error policy of the command and is recorded in the history
// under its path. The time limit of the running action stands meanwhile.
//
// After ChildEnd the container returns nil at once; the engine decides how
// the instance ends. An error means that i is not a child action of the
// running action.
func (r *Run) PerformChild(ctx context.Context, i int) (ChildOutcome, error) {
	f := r.top()
	if f == nil {
		return "", ErrNoAction
	}
	c, ok := f.action.(Container)
	if !ok {
		return "", fmt.Errorf("%w: %s has no child actions", ErrInvalidChild, f.action.DocType())
	}
	children := c.Children()
	if i < 0 || i >= len(children) {
		return "", fmt.Errorf("%w: %d of %d", ErrInvalidChild, i, len(children))
	}
	f.stand()
	defer f.run()
	if !r.engine.step(ctx, r, children[i], append(slices.Clone(f.path), i+1)) {
		return ChildEnd, nil
	}
	return ChildNext, nil
}

// top returns the running action; nil if none runs.
func (r *Run) top() *frame {
	if len(r.frames) == 0 {
		return nil
	}
	return r.frames[len(r.frames)-1]
}

// execute runs the actions of in in order (B70) and ends it.
func (e *Engine) execute(ctx context.Context, in *instance) {
	run := &Run{engine: e, in: in}
	for i, a := range in.cmd.Actions {
		if !e.step(ctx, run, a, []int{i + 1}) {
			break
		}
	}
	state := StateCompleted
	if run.end == endFailed {
		state = StateFailed // B71
	}
	if ctx.Err() != nil {
		state = StateCanceled // B50, B109
	}
	e.finish(ctx, in, state)
}

// step runs the action a at path, at the top level or as a child action,
// and reports whether the actions go on (B70, B71; actions.md B1, B7, B9).
// It decides how the run ends, whatever a container returns after one of
// its child actions ended the run.
func (e *Engine) step(ctx context.Context, run *Run, a command.Action, path []int) bool {
	if ctx.Err() != nil || run.end != endNone {
		return false
	}
	in := run.in
	p, ok := a.(Performer)
	if !ok {
		e.logger.WarnContext(ctx, "unknown action type skipped",
			"instance", in.id, "command", in.cmd.Name, "path", pathText(path), "action_type", a.DocType())
		return true
	}
	if !p.Enabled() {
		return true // actions.md B1
	}
	var err error
	if missing := e.types.Missing(a.DocType()); len(missing) > 0 {
		err = fmt.Errorf("%w: %s", ErrCapability, capabilitiesText(missing))
	} else {
		err = e.perform(ctx, run, p, path)
	}
	switch {
	case ctx.Err() != nil:
		return false
	case run.end != endNone:
		return false // a child action ended the run
	case errors.Is(err, ErrStop):
		run.end = endStop
		return false
	case err == nil:
		return true
	}
	e.logger.WarnContext(ctx, "action failed",
		"instance", in.id, "command", in.cmd.Name, "path", pathText(path), "action_type", a.DocType(), "error", err)
	e.failAction(in, path, a.DocType(), err)
	if abort(in.cmd.ErrorPolicy) {
		run.end = endFailed // B71
		return false
	}
	return true
}

// abort reports whether an instance ends after a failed action (B71). The
// policy was checked when the instance was queued.
func abort(p command.ErrorPolicy) bool {
	switch p {
	case command.ErrorAbort:
		return true
	case command.ErrorContinue:
		return false
	default:
		return true
	}
}

// perform runs one action within its time limit (B72). The limit counts the
// time of the action itself: it stands while the action runs a child action
// or waits for a called command (actions.md B8). A panic in the action is
// its error.
func (e *Engine) perform(ctx context.Context, run *Run, a Performer, path []int) (err error) {
	actx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	f := &frame{action: a, path: path, cancel: cancel, limit: DefaultTimeLimit, left: DefaultTimeLimit}
	f.run()
	run.frames = append(run.frames, f)
	defer func() {
		f.stand()
		run.frames = run.frames[:len(run.frames)-1]
	}()
	defer func() {
		if r := recover(); r != nil {
			e.logger.ErrorContext(ctx, "action panicked", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("action panicked: %v", r)
		}
	}()

	err = a.Perform(actx, run)
	if ctx.Err() == nil && errors.Is(context.Cause(actx), ErrTimeLimit) {
		return fmt.Errorf("%w after %s", ErrTimeLimit, f.limit)
	}
	return err
}

// pathText writes a path as in the spec, e.g. "3.2" (actions.md B9).
func pathText(path []int) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

// capabilitiesText lists capabilities for an error message.
func capabilitiesText(caps []capability.Capability) string {
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = string(c)
	}
	return strings.Join(parts, ", ")
}
