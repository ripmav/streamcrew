// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"errors"

	"github.com/ripmav/streamcrew/internal/event"
)

// Event types of the engine (B61). They are meant for frontends and logs;
// they are not in the domain catalog (internal/domain/eventtype), so event
// commands cannot react to them (B62).
const (
	// TypeInstanceQueued and the other instance types carry an Instance.
	TypeInstanceQueued    event.Type = "command.instance.queued"
	TypeInstanceStarted   event.Type = "command.instance.started"
	TypeInstanceCompleted event.Type = "command.instance.completed"
	TypeInstanceFailed    event.Type = "command.instance.failed"
	TypeInstanceCanceled  event.Type = "command.instance.canceled"
	// TypeQueuePaused and TypeQueueResumed carry a QueuePause.
	TypeQueuePaused  event.Type = "command.queue.paused"
	TypeQueueResumed event.Type = "command.queue.resumed"
)

// PauseScope says what a pause holds back (B40, B41).
type PauseScope string

// Pause scopes.
const (
	// PauseAll holds back all queued instances (B40).
	PauseAll PauseScope = "all"
)

// QueuePause is the payload of "command.queue.paused" and
// "command.queue.resumed".
type QueuePause struct {
	Scope PauseScope `json:"scope"`
}

// RegisterEvents adds the event types of the engine to c.
func RegisterEvents(c *event.Catalog) error {
	return errors.Join(
		event.Register[Instance](c, TypeInstanceQueued),
		event.Register[Instance](c, TypeInstanceStarted),
		event.Register[Instance](c, TypeInstanceCompleted),
		event.Register[Instance](c, TypeInstanceFailed),
		event.Register[Instance](c, TypeInstanceCanceled),
		event.Register[QueuePause](c, TypeQueuePaused),
		event.Register[QueuePause](c, TypeQueueResumed),
	)
}

// instanceEvent returns the event type of an end state.
func instanceEvent(s State) event.Type {
	switch s {
	case StateFailed:
		return TypeInstanceFailed
	case StateCanceled:
		return TypeInstanceCanceled
	default:
		return TypeInstanceCompleted
	}
}

// publishLocked publishes an event of the engine. Publishing under e.mu
// keeps the events in the order of the state changes (B61); the bus never
// blocks. e.mu is held.
func (e *Engine) publishLocked(ctx context.Context, typ event.Type, payload any) {
	env := event.New(event.Source{Kind: event.SourceSystem, Name: "engine"}, typ, payload)
	if err := e.publisher.Publish(ctx, env); err != nil {
		e.logger.ErrorContext(ctx, "publishing an event failed", "type", typ, "error", err)
	}
}
