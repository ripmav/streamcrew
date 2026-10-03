// SPDX-License-Identifier: MIT

package expr

import (
	"strconv"

	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/template"
)

// Kind is the kind of a result.
type Kind int

// The kinds of results (B50).
const (
	Number Kind = iota
	Bool
	Text
)

// Result is the value of an expression: a number, a truth value or text.
type Result struct {
	Kind   Kind
	Number decimal.Decimal
	Bool   bool
	Text   string
}

// String returns the result as text: a number in the canonical form of
// internal/decimal, e.g. "2.5", "true" or "false", or the text.
func (r Result) String() string {
	switch r.Kind {
	case Number:
		return r.Number.String()
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
		return template.NumberValue(r.Number)
	}
	return template.TextValue(r.String())
}
