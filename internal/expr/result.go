// SPDX-License-Identifier: MIT

package expr

import (
	"strconv"

	"github.com/ripmav/streamcrew/internal/template"
)

// Kind is the kind of a result.
type Kind int

// The kinds of results (Code-ADR-0012, point 8).
const (
	Number Kind = iota
	Bool
	Text
)

// Result is the value of an expression: a finite number, a truth value or
// text.
type Result struct {
	Kind   Kind
	Number float64
	Bool   bool
	Text   string
}

// String returns the result as text: a number with the shortest digits that
// read back as the same number, "true" or "false", or the text.
func (r Result) String() string {
	switch r.Kind {
	case Number:
		return strconv.FormatFloat(r.Number, 'f', -1, 64)
	case Bool:
		return strconv.FormatBool(r.Bool)
	default:
		return r.Text
	}
}

// Value returns the result as a value for templates, e.g. for a local value
// that the special identifier action sets.
func (r Result) Value() template.Value {
	if r.Kind == Number {
		return template.FloatValue(r.Number)
	}
	return template.TextValue(r.String())
}
