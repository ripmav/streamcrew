// SPDX-License-Identifier: Apache-2.0

// Package values has the action types that change values (spec
// actions.md, B40 to B57): counter now, special_identifier to follow. They
// belong to the category "values" (Code-ADR-0013).
package values

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeCounter is the type ID of the counter action (Code-ADR-0013,
// point 1).
const TypeCounter = "counter"

// maxExact is the largest whole number that expressions compute exactly:
// they compute with float64 (template.md, B50), which holds every whole
// number up to 2^53 - 1.
const maxExact = 1<<53 - 1

// amountRange is the range of the amount of add and the value of set
// (actions.md B4, B40): whole numbers that expressions compute exactly.
// The counter itself holds 64 bits (counters-and-quotes.md, B5).
func amountRange() action.Range {
	return action.Range{Min: -maxExact, Max: maxExact, Integer: true}
}

// CounterKind is what a counter action does (actions.md B40).
type CounterKind string

// The kinds of the counter action.
const (
	// CounterAdd adds an amount, which may be negative. New counter
	// actions add 1.
	CounterAdd CounterKind = "add"
	// CounterSet sets the value.
	CounterSet CounterKind = "set"
	// CounterReset sets the value to 0.
	CounterReset CounterKind = "reset"
)

// CounterKinds returns the kinds of the counter action, in the order
// editors show them.
func CounterKinds() []CounterKind {
	return []CounterKind{CounterAdd, CounterSet, CounterReset}
}

// Valid reports whether k is a known kind.
func (k CounterKind) Valid() bool {
	return slices.Contains(CounterKinds(), k)
}

// Counters change counters in one transaction (counters-and-quotes.md,
// B6); *store.Store implements it.
type Counters interface {
	UpdateCounter(ctx context.Context, name string, fn func(*counter.Counter) error) (counter.Counter, error)
}

// Ports are what the value types need.
type Ports struct {
	// Templates renders the identifiers in the expressions of amounts.
	Templates *template.Engine
	// Counters changes counters.
	Counters Counters
}

// ports are the ports of the value types.
type ports struct {
	Ports
}

// Descriptors returns the value types with their ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("value action types: no template engine")
	case p.Counters == nil:
		return nil, errors.New("value action types: no counters")
	}
	ports := &ports{Ports: p}
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeCounter,
			Version:  1,
			Category: action.CategoryValues,
			Schema:   counterSchema(),
		}.WithKinds(CounterAdd, func(k CounterKind) (Counter, bool) {
			if !k.Valid() {
				return Counter{}, false
			}
			c := Counter{Common: action.On(), Kind: k, ports: ports}
			if k == CounterAdd {
				c.Amount = action.Fixed(1)
			}
			return c, true
		}),
	}, nil
}

// counterSchema returns the schema of the counter action: add has an
// amount, set a value, reset neither.
func counterSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{{Name: "counter", Schema: schema.CounterName(), Required: true}},
		schema.Variant{Kind: string(CounterAdd), Props: []schema.Property{
			{Name: "amount", Schema: amountRange().Schema()},
		}},
		schema.Variant{Kind: string(CounterSet), Props: []schema.Property{
			{Name: "value", Schema: amountRange().Schema(), Required: true},
		}},
		schema.Variant{Kind: string(CounterReset)},
	)
}

// Counter changes a counter (actions.md B40 to B43). Following actions see
// the new value through $<name> and $<name>display, which the template
// engine reads from the store; the action sets no result values.
type Counter struct {
	action.Common `json:",embed"`
	Kind          CounterKind `json:"kind"`
	// Counter is the name of the counter, regardless of case. Saving
	// creates a counter that does not exist with the value 0 (B41).
	Counter string `json:"counter"`
	// Amount is what add adds, a whole number, also negative; no amount for
	// the other kinds.
	Amount action.Amount `json:"amount,omitzero"`
	// Value is what set sets, a whole number; no amount for the other
	// kinds.
	Value action.Amount `json:"value,omitzero"`
	ports *ports
}

// DocType implements command.Action.
func (Counter) DocType() string { return TypeCounter }

// Validate implements command.Action.
func (c Counter) Validate() error {
	if !c.Kind.Valid() {
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
	if err := counter.ValidateName(c.Counter); err != nil {
		return field("counter", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	if err := only(c.Kind == CounterAdd, c.Amount, "add has an amount"); err != nil {
		return field("amount", err)
	}
	return field("value", only(c.Kind == CounterSet, c.Value, "set has a value"))
}

// only checks an amount that only some kinds have: valid if the kind has
// it, missing otherwise.
func only(has bool, a action.Amount, what string) error {
	switch {
	case has:
		return a.Validate(amountRange())
	case !a.IsZero():
		return fmt.Errorf("%w: only %s", action.ErrInvalid, what)
	default:
		return nil
	}
}

// References implements command.Referrer: saving creates a counter that
// does not exist (B41).
func (c Counter) References() []command.Reference {
	return []command.Reference{{Kind: command.RefCounter, Name: c.Counter}}
}

// Perform implements engine.Performer. The change is stored at once and is
// atomic; a missing counter and a result beyond 64 bits let the action
// fail, and the value stays as it was (B41, B42).
func (c Counter) Perform(ctx context.Context, run *engine.Run) error {
	var change func(*counter.Counter) error
	switch c.Kind {
	case CounterAdd:
		delta, err := c.Amount.Eval(ctx, c.ports.Templates, run.Scope(), amountRange())
		if err != nil {
			return field("amount", err)
		}
		change = func(k *counter.Counter) error { return k.Add(int64(delta)) }
	case CounterSet:
		v, err := c.Value.Eval(ctx, c.ports.Templates, run.Scope(), amountRange())
		if err != nil {
			return field("value", err)
		}
		change = func(k *counter.Counter) error {
			k.Set(int64(v))
			return nil
		}
	case CounterReset:
		change = func(k *counter.Counter) error {
			k.Reset()
			return nil
		}
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
	_, err := c.ports.Counters.UpdateCounter(ctx, c.Counter, change)
	return field("counter", err)
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
