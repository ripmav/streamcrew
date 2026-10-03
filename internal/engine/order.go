// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/ripmav/streamcrew/internal/domain/command"
)

// Decide makes a prepared decision about the requirements of a command
// (B10, B16): it checks what has to be checked at that moment, e.g. a
// cooldown, and only for VerdictMet charges the costs and starts the
// cooldowns, which Decision.Revert takes back. An error means it could not
// decide. The engine calls it once.
type Decide func(ctx context.Context) (Decision, error)

// turn is the place of a trigger in the order of the decisions (B16): it
// decides and queues after the trigger before it. A nil turn has no place
// and waits for nothing.
type turn struct {
	prev <-chan struct{}
	next chan struct{}
	once sync.Once
}

// takeTurnLocked returns the next place in the order of the decisions.
// e.mu is held.
func (e *Engine) takeTurnLocked() *turn {
	t := &turn{prev: e.lastTurn, next: make(chan struct{})}
	e.lastTurn = t.next
	return t
}

// wait blocks until the trigger before t is done.
func (t *turn) wait() {
	if t != nil && t.prev != nil {
		<-t.prev
	}
}

// done lets the trigger after t go on, as soon as the one before t is
// done; until then it blocks. Calling it again does nothing.
func (t *turn) done() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.wait()
		close(t.next)
	})
}

// prepare does the work of a decision that may take long, for many
// triggers at the same time (B16): with findTarget, the target user of p
// (B81), then Requirements.Prepare, both through lookup (B17). It returns p
// with the target and the decision to make in turn.
func (e *Engine) prepare(ctx context.Context, cmd command.Command, p Params, lookup *userLookup, findTarget bool) (Params, Decide, error) {
	if findTarget {
		p = e.lookupTarget(ctx, lookup, p)
	}
	if e.requirements == nil {
		return p, func(context.Context) (Decision, error) { return Met(p), nil }, nil
	}
	decide, err := e.requirements.Prepare(ctx, cmd, p, lookup)
	if err != nil {
		return p, nil, fmt.Errorf("check requirements: %w", err)
	}
	return p, decide, nil
}

// decide makes a prepared decision and checks it. It takes back what an
// invalid decision applied.
func (e *Engine) decide(ctx context.Context, cmd command.Command, decide Decide) (Decision, error) {
	d, err := decide(ctx)
	if err != nil {
		return Decision{}, fmt.Errorf("check requirements: %w", err)
	}
	if err := d.validate(); err != nil {
		e.revert(ctx, cmd, d)
		return Decision{}, err
	}
	return d, nil
}

// Submit triggers req like Trigger, but returns as soon as the trigger has
// its place in the queue and in the order of the decisions (B15, B16).
// Callers that trigger commands one after another, e.g. for the messages of
// the chat, submit them in this order and need not wait for the decisions:
// the preparations run at the same time, and the commands are still queued
// in the order of the triggers. done gets the result of Trigger from a
// goroutine of the engine and must not block for long; for a disabled
// command, Submit calls it before it returns. Submit returns the errors
// Trigger returns before the requirements are checked, and done is not
// called then.
func (e *Engine) Submit(ctx context.Context, req Request, done func(Result, error)) error {
	if done == nil {
		return fmt.Errorf("submit command %q: %w: nil callback", req.Command.Name, ErrInvalidOption)
	}
	// The trigger outlives the request that submitted it; its lookups have
	// their own time limits (B17).
	tctx := context.WithoutCancel(ctx)
	t, err := e.enter(ctx, req, func(t *trigger) {
		e.wg.Go(func() { done(t.finish(tctx)) })
	})
	if err != nil {
		return err
	}
	if t.ended {
		done(t.result, nil)
	}
	return nil
}
