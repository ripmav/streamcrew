// SPDX-License-Identifier: Apache-2.0

package flow

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// Random draws Count of its child actions and runs them in the order they
// were drawn (actions.md B11 to B13).
type Random struct {
	action.Common `json:",inline"`
	// Count is how many draws there are, a whole number from 0 to 1000; a
	// new random action draws once.
	Count action.Amount `json:"count,omitzero"`
	// Unique draws each child action at most once per run (B12).
	Unique bool `json:"unique"`
	// Remember keeps child actions drawn until all were drawn once, across
	// runs (B13); only with Unique.
	Remember bool             `json:"remember"`
	Actions  []command.Action `json:"actions"`
	ports    *ports
}

// DocType implements command.Action.
func (Random) DocType() string { return TypeRandom }

// Validate implements command.Action.
func (r Random) Validate() error {
	if r.Remember && !r.Unique {
		return field("remember", errors.New("only with unique"))
	}
	return field("count", r.Count.Validate(countRange()))
}

// Children implements command.Parent.
func (r Random) Children() []command.Action { return r.Actions }

// Perform implements engine.Performer. Only active child actions take part
// in the draw; inactive ones and those of unknown types do not.
func (r Random) Perform(ctx context.Context, run *engine.Run) error {
	count, err := r.Count.Eval(ctx, r.ports.Templates, run.Scope(), countRange())
	if err != nil {
		return field("count", err)
	}
	drawn := r.draw(run, int(count))
	for _, i := range drawn {
		next, err := run.PerformChild(ctx, i)
		if err != nil || next == engine.ChildEnd {
			return err
		}
	}
	return nil
}

// draw returns the indexes of the child actions to run, in the order they
// were drawn.
func (r Random) draw(run *engine.Run, count int) []int {
	candidates := r.active()
	if len(candidates) == 0 {
		return []int{} // B203
	}
	if !r.Unique {
		drawn := make([]int, count)
		for i := range drawn {
			drawn[i] = candidates[r.ports.IntN(len(candidates))]
		}
		return drawn
	}
	if !r.Remember {
		return drawUnique(candidates, count, r.ports.IntN)
	}
	cmd := run.Command()
	key := memoryKey{command: cmd.ID, path: pathKey(run.Path())}
	return r.ports.memory.draw(key, cmd.UpdatedAt, candidates, count, r.ports.IntN)
}

// active returns the indexes of the child actions that can run.
func (r Random) active() []int {
	var active []int
	for i, a := range r.Actions {
		if p, ok := a.(interface{ Enabled() bool }); ok && p.Enabled() {
			active = append(active, i)
		}
	}
	return active
}

// drawUnique draws up to count of pool without repeats; it stops when pool
// is used up (B12).
func drawUnique(pool []int, count int, intN func(int) int) []int {
	pool = slices.Clone(pool)
	drawn := make([]int, 0, min(count, len(pool)))
	for len(drawn) < count && len(pool) > 0 {
		i := intN(len(pool))
		drawn = append(drawn, pool[i])
		pool = slices.Delete(pool, i, i+1)
	}
	return drawn
}

// memory keeps the child actions that random actions with Remember have
// drawn, per command and action (B13). It lives in memory only: it starts
// anew when the core starts or the command changes.
type memory struct {
	mu      sync.Mutex
	entries map[memoryKey]*memoryEntry
}

// memoryKey names a random action: its command and its path in it.
type memoryKey struct {
	command id.ID
	path    string
}

// memoryEntry is what one random action has drawn so far.
type memoryEntry struct {
	// version is the change time of the command the draws belong to.
	version time.Time
	drawn   map[int]bool
}

func newMemory() *memory {
	return &memory{entries: make(map[memoryKey]*memoryEntry)}
}

// draw draws up to count of candidates that key has not drawn yet and
// remembers them. When every candidate has been drawn once, the draw starts
// anew: at the start of a run if nothing is left, and after a run that used
// up the rest. A command of another version starts anew as well.
func (m *memory) draw(key memoryKey, version time.Time, candidates []int, count int, intN func(int) int) []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok || !e.version.Equal(version) {
		e = &memoryEntry{version: version, drawn: make(map[int]bool)}
		m.entries[key] = e
	}
	pool := slices.DeleteFunc(slices.Clone(candidates), func(i int) bool { return e.drawn[i] })
	if len(pool) == 0 {
		clear(e.drawn)
		pool = slices.Clone(candidates)
	}
	drawn := drawUnique(pool, count, intN)
	for _, i := range drawn {
		e.drawn[i] = true
	}
	if !slices.ContainsFunc(candidates, func(i int) bool { return !e.drawn[i] }) {
		clear(e.drawn)
	}
	return drawn
}

// pathKey writes a path as a map key, e.g. "3.2".
func pathKey(path []int) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}
