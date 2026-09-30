// SPDX-License-Identifier: MIT

package template

import (
	"context"
	"strconv"
)

// Value is the value of an identifier: the text that is inserted and, for
// numbers, the number, which expressions use (B51).
type Value struct {
	// Text is inserted into the rendered text, encoded for its place (B30).
	Text string
	// Number is the numeric value if IsNumber is set.
	Number   float64
	IsNumber bool
}

// TextValue returns a text value.
func TextValue(s string) Value {
	return Value{Text: s}
}

// IntValue returns a whole number, e.g. a counter.
func IntValue(n int64) Value {
	return Value{Text: strconv.FormatInt(n, 10), Number: float64(n), IsNumber: true}
}

// FloatValue returns a number with the shortest text that reads back as f.
func FloatValue(f float64) Value {
	return Value{Text: strconv.FormatFloat(f, 'f', -1, 64), Number: f, IsNumber: true}
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
