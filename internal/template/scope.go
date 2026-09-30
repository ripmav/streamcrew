// SPDX-License-Identifier: Apache-2.0

package template

import "strings"

// Scope holds what a render needs besides the template: the values of the
// run, such as local values of actions and event values (B10), and, during a
// render, the state of that render.
//
// The zero value is an empty scope. Render works on a copy with fresh state,
// so a scope can serve all renders of a run; it must not be changed while a
// render uses it.
type Scope struct {
	values map[string]Value
	render *renderState
}

// renderState is the state of one render.
type renderState struct {
	// cache holds the result per identifier (B21).
	cache map[string]result
	// memo holds the results of Scope.Memo.
	memo map[string]memoResult
	// failed marks sources whose Match failed; they are skipped for the rest
	// of the render.
	failed map[int]bool
}

// result is a resolved identifier.
type result struct {
	v  Value
	ok bool
}

// memoResult is a result of Scope.Memo.
type memoResult struct {
	v   any
	err error
}

// SetValue sets a value of the run under name, e.g. a local value of an
// action or an event value; name is case-insensitive. Values of the run rank
// first among the sources (B10). Whether a name collides with a built-in
// identifier is checked where the name is defined (Registry.Reserved, B12).
func (s *Scope) SetValue(name string, v Value) {
	if s.values == nil {
		s.values = make(map[string]Value)
	}
	s.values[strings.ToLower(name)] = v
}

// Memo returns the result of fn for key and calls fn at most once per
// render; later calls with the same key get the first result, including an
// error. Families use it for data that several identifiers share, such as
// the stream state or a random user (B22). Keys start with the name of the
// family. Outside a render, Memo calls fn every time.
func (s *Scope) Memo[T any](key string, fn func() (T, error)) (T, error) {
	if s.render == nil {
		return fn()
	}
	if m, ok := s.render.memo[key]; ok {
		if v, ok := m.v.(T); ok {
			return v, m.err
		}
		// Another type under the same key is a bug in a family; do not
		// share the result.
		return fn()
	}
	v, err := fn()
	if s.render.memo == nil {
		s.render.memo = make(map[string]memoResult)
	}
	s.render.memo[key] = memoResult{v: v, err: err}
	return v, err
}

// forRender returns a copy of s with fresh render state; s may be nil.
func (s *Scope) forRender() *Scope {
	var c Scope
	if s != nil {
		c = *s
	}
	c.render = &renderState{}
	return &c
}

// value returns the value of the run with the longest name that token
// starts with.
func (s *Scope) value(token string) (int, Value) {
	if len(s.values) == 0 {
		return 0, Value{}
	}
	for n := len(token); n > 0; n-- {
		if v, ok := s.values[token[:n]]; ok {
			return n, v
		}
	}
	return 0, Value{}
}
