// SPDX-License-Identifier: MIT

// Package flow has the action types that order other actions (spec
// actions.md, B10 to B15): wait, random, group and repeat. They belong to
// the category "flow" (Code-ADR-0013); the conditional follows on its own.
//
// Child actions run through engine.Run.PerformChild, so the engine applies
// the switch "active", the capabilities, the time limits and the error
// policy to them (actions.md B1, B7 to B9).
package flow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// Type IDs (Code-ADR-0013, point 1).
const (
	TypeWait   = "wait"
	TypeRandom = "random"
	TypeGroup  = "group"
	TypeRepeat = "repeat"
)

// waitRange is the duration of a wait in seconds (actions.md B10).
func waitRange() action.Range { return action.Range{Min: 0, Max: 3600} }

// countRange is the count of random and repeat (actions.md B11, B15;
// command-engine.md B74).
func countRange() action.Range { return action.Range{Min: 0, Max: 1000, Integer: true} }

// waitSlack is how much longer than its duration a wait may take before it
// fails (actions.md B8).
const waitSlack = 5 * time.Second

// Ports are what the flow types need.
type Ports struct {
	// Templates renders the identifiers in the expressions of amounts.
	Templates *template.Engine
	// IntN returns a random number from 0 to n-1 and is safe for concurrent
	// use; math/rand/v2.IntN in production.
	IntN func(n int) int
}

// Descriptors returns the flow types with their ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("flow action types: no template engine")
	case p.IntN == nil:
		return nil, errors.New("flow action types: no random numbers")
	}
	ports := &ports{Ports: p, memory: newMemory()}
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeWait,
			Version:  1,
			Category: action.CategoryFlow,
			Schema: schema.Document(
				schema.Property{Name: "seconds", Schema: waitRange().Schema(), Required: true},
			),
		}.WithNew(func() Wait { return Wait{Common: action.On(), ports: ports} }),
		action.Descriptor{
			Type:     TypeRandom,
			Version:  1,
			Category: action.CategoryFlow,
			Schema:   randomSchema(),
		}.WithNew(func() Random {
			return Random{Common: action.On(), Count: action.Fixed(1), Draw: DrawFree, Actions: []command.Action{}, ports: ports}
		}),
		action.Descriptor{
			Type:     TypeGroup,
			Version:  1,
			Category: action.CategoryFlow,
			Schema:   schema.Document(schema.Property{Name: "actions", Schema: schema.Actions()}),
		}.WithNew(func() Group { return Group{Common: action.On(), Actions: []command.Action{}} }),
		action.Descriptor{
			Type:     TypeRepeat,
			Version:  1,
			Category: action.CategoryFlow,
			Schema: schema.Document(
				schema.Property{Name: "count", Schema: countRange().Schema(), Required: true},
				schema.Property{Name: "actions", Schema: schema.Actions()},
			),
		}.WithNew(func() Repeat { return Repeat{Common: action.On(), Actions: []command.Action{}, ports: ports} }),
	}, nil
}

// randomSchema returns the schema of random.
func randomSchema() *schema.Schema {
	draws := make([]string, 0, len(Draws()))
	for _, d := range Draws() {
		draws = append(draws, string(d))
	}
	return schema.Document(
		schema.Property{Name: "count", Schema: countRange().Schema()},
		schema.Property{Name: "draw", Schema: schema.Choice(draws...)},
		schema.Property{Name: "actions", Schema: schema.Actions()},
	)
}

// ports are the ports of the flow types and the memory of random.
type ports struct {
	Ports
	memory *memory
}

// Wait waits before the next action runs (actions.md B10).
type Wait struct {
	action.Common `json:",embed"`
	// Seconds is the duration, fractions allowed, from 0 to 3600.
	Seconds action.Amount `json:"seconds,omitzero"`
	ports   *ports
}

// DocType implements command.Action.
func (Wait) DocType() string { return TypeWait }

// Validate implements command.Action.
func (w Wait) Validate() error {
	return field("seconds", w.Seconds.Validate(waitRange()))
}

// Perform implements engine.Performer. Its time limit is the duration plus
// 5 s, set once the duration is known (actions.md B8); a cancellation ends
// the wait at once (B10).
func (w Wait) Perform(ctx context.Context, run *engine.Run) error {
	seconds, err := w.Seconds.Eval(ctx, w.ports.Templates, run.Scope(), waitRange())
	if err != nil {
		return field("seconds", err)
	}
	d := time.Duration(seconds * float64(time.Second))
	if err := run.LimitTo(d + waitSlack); err != nil {
		return err
	}
	if d == 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Group runs its child actions in order (actions.md B14).
type Group struct {
	action.Common `json:",embed"`
	Actions       []command.Action `json:"actions"`
}

// DocType implements command.Action.
func (Group) DocType() string { return TypeGroup }

// Validate implements command.Action.
func (Group) Validate() error { return nil }

// Children implements command.Parent.
func (g Group) Children() []command.Action { return g.Actions }

// Perform implements engine.Performer.
func (g Group) Perform(ctx context.Context, run *engine.Run) error {
	_, err := runAll(ctx, run, len(g.Actions))
	return err
}

// Repeat runs its child actions in order, Count times (actions.md B15).
type Repeat struct {
	action.Common `json:",embed"`
	// Count is a whole number from 0 to 1000 (command-engine.md B74).
	Count   action.Amount    `json:"count,omitzero"`
	Actions []command.Action `json:"actions"`
	ports   *ports
}

// DocType implements command.Action.
func (Repeat) DocType() string { return TypeRepeat }

// Validate implements command.Action.
func (r Repeat) Validate() error {
	return field("count", r.Count.Validate(countRange()))
}

// Children implements command.Parent.
func (r Repeat) Children() []command.Action { return r.Actions }

// Perform implements engine.Performer. A count out of range fails before
// the first pass (actions.md B15).
func (r Repeat) Perform(ctx context.Context, run *engine.Run) error {
	count, err := r.Count.Eval(ctx, r.ports.Templates, run.Scope(), countRange())
	if err != nil {
		return field("count", err)
	}
	for range int(count) {
		next, err := runAll(ctx, run, len(r.Actions))
		if err != nil || next == engine.ChildEnd {
			return err
		}
	}
	return nil
}

// runAll runs the child actions 0 to n-1 of the running action in order
// and says whether the instance goes on.
func runAll(ctx context.Context, run *engine.Run, n int) (engine.ChildOutcome, error) {
	for i := range n {
		next, err := run.PerformChild(ctx, i)
		if err != nil || next == engine.ChildEnd {
			return next, err
		}
	}
	return engine.ChildNext, nil
}

// field names the field of an error (actions.md B4, B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
