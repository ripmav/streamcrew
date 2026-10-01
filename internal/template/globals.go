// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// MaxGlobals is the largest number of global values (spec actions.md, B57).
const MaxGlobals = 10_000

// ErrTooManyGlobals is returned by Globals.Set for a new name when there
// are MaxGlobals global values already (spec actions.md, B57).
var ErrTooManyGlobals = errors.New("too many global values")

// Globals are the global values that the special identifier action sets
// (B10; spec actions.md, B56): they apply to every later render of all
// instances until an action changes them or the core ends. They are kept in
// memory only, neither stored nor backed up. Globals is a Source and ranks
// before the dynamic names, such as counters (WithSources). It is safe for
// concurrent use; when two instances set the same name, the later value
// wins.
//
// Whether a name hides a built-in identifier is checked where the name is
// defined, when the command is saved (B12).
type Globals struct {
	mu     sync.RWMutex
	values map[string]Value
}

// NewGlobals returns an empty set of global values that holds at most
// MaxGlobals values.
func NewGlobals() *Globals {
	return &Globals{values: make(map[string]Value)}
}

// Set sets the global value name, regardless of case. A new name beyond
// MaxGlobals fails with ErrTooManyGlobals; existing names can always be
// changed (spec actions.md, B57).
func (g *Globals) Set(name string, v Value) error {
	name = strings.ToLower(name)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.values[name]; !ok && len(g.values) >= MaxGlobals {
		return fmt.Errorf("global value %q: %w (at most %d)", name, ErrTooManyGlobals, MaxGlobals)
	}
	g.values[name] = v
	return nil
}

// Len returns the number of global values.
func (g *Globals) Len() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.values)
}

// Match implements Source: it finds the global value with the longest name
// that token starts with. The engine resolves each identifier once per
// render (B21), so a value set during a render applies from the next one on
// for identifiers already resolved.
func (g *Globals) Match(_ context.Context, _ *Scope, token string) (int, Resolver, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.values) == 0 {
		return 0, nil, nil
	}
	for n := len(token); n > 0; n-- {
		if v, ok := g.values[token[:n]]; ok {
			return n, constant(v), nil
		}
	}
	return 0, nil, nil
}
