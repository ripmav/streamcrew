// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/template"
)

// Performer is an action the engine can run (plan §6.9); the action types
// (roadmap 3.3) implement it. Perform honors the cancellation and the
// deadline of ctx. An action without it is skipped (B70).
type Performer interface {
	Perform(ctx context.Context, run *Run) error
}

// TimeLimiter is an action with its own time limit, such as a wait that
// lasts longer than the default (B72).
type TimeLimiter interface {
	// TimeLimit returns the time limit; 0 means DefaultTimeLimit.
	TimeLimit() time.Duration
}

var (
	// ErrStop is returned by an action to end its instance early as
	// completed, e.g. the action "end the current command" (B4).
	ErrStop = errors.New("stop the command")
	// ErrTimeLimit is the error of an action that ran past its time limit
	// (B72).
	ErrTimeLimit = errors.New("action time limit exceeded")
)

// Run is an instance as its actions see it (plan §6.9). The actions of an
// instance run one after the other, so they use it without locking.
type Run struct {
	in *instance
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

// execute runs the actions of in in order (B70) and ends it.
func (e *Engine) execute(ctx context.Context, in *instance) {
	run := &Run{in: in}
	state := StateCompleted
	for i, a := range in.cmd.Actions {
		if ctx.Err() != nil {
			break
		}
		p, ok := a.(Performer)
		if !ok {
			e.logger.WarnContext(ctx, "unknown action type skipped",
				"instance", in.id, "command", in.cmd.Name, "position", i+1, "action_type", a.DocType())
			continue
		}
		err := e.perform(ctx, run, p)
		if ctx.Err() != nil || errors.Is(err, ErrStop) {
			break
		}
		if err == nil {
			continue
		}
		e.logger.WarnContext(ctx, "action failed",
			"instance", in.id, "command", in.cmd.Name, "position", i+1, "action_type", a.DocType(), "error", err)
		e.failAction(in, i+1, a.DocType(), err)
		if in.cmd.ErrorPolicy == command.ErrorAbort {
			state = StateFailed // B71
			break
		}
	}
	if ctx.Err() != nil {
		state = StateCanceled // B50, B109
	}
	e.finish(ctx, in, state)
}

// perform runs one action within its time limit (B72). A panic in the
// action is its error.
func (e *Engine) perform(ctx context.Context, run *Run, a Performer) (err error) {
	limit := DefaultTimeLimit
	if l, ok := a.(TimeLimiter); ok && l.TimeLimit() > 0 {
		limit = l.TimeLimit()
	}
	actx, cancel := context.WithTimeoutCause(ctx, limit, ErrTimeLimit)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			e.logger.ErrorContext(ctx, "action panicked", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("action panicked: %v", r)
		}
	}()

	err = a.Perform(actx, run)
	if ctx.Err() == nil && errors.Is(context.Cause(actx), ErrTimeLimit) {
		return fmt.Errorf("%w after %s", ErrTimeLimit, limit)
	}
	return err
}
