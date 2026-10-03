// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"

	"github.com/ripmav/streamcrew/internal/decimal"
)

// Value is the value of an identifier: the text that is inserted and, for
// numbers, the number, which expressions use (B51).
type Value struct {
	// Text is inserted into the rendered text, encoded for its place (B30).
	Text string
	// Number is the numeric value if IsNumber is set (Code-ADR-0020).
	Number   decimal.Decimal
	IsNumber bool
}

// TextValue returns a text value.
func TextValue(s string) Value {
	return Value{Text: s}
}

// IntValue returns a whole number, e.g. a count.
func IntValue(n int64) Value {
	return NumberValue(decimal.New(n))
}

// NumberValue returns a number with its canonical text, e.g. "2.5"
// (Code-ADR-0020, point 7).
func NumberValue(d decimal.Decimal) Value {
	return Value{Text: d.String(), Number: d, IsNumber: true}
}

// Resolver determines the value of an identifier for a render.
//
// ok is false if the identifier has no value in the scope, e.g. $arg3text
// with two arguments; the identifier then stays in the text as written (B4).
// An error has the same effect and is logged as a warning (B23), so an error
// must not contain values from the scope. Resolvers honor cancellation and
// deadline of ctx (B24).
type Resolver func(ctx context.Context, s *Scope) (v Value, ok bool, err error)

// constant returns a resolver that always returns v.
func constant(v Value) Resolver {
	return func(context.Context, *Scope) (Value, bool, error) {
		return v, true, nil
	}
}
