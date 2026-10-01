// SPDX-License-Identifier: Apache-2.0

// Package counter is the model of counters (spec counters-and-quotes.md, B1
// to B8): named whole numbers of a profile, e.g. deaths in a game, that
// actions change and identifiers print.
//
// The identifiers $<name> and $<name>display (B1, B4) come from the counter
// source of the template engine (internal/template).
package counter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Counter is a named whole number (B1, B5).
type Counter struct {
	ID id.ID
	// Name is also the name of the counter's identifier, "$<name>", and
	// unique per profile regardless of case (B1).
	Name  string
	Value int64
	// Step is what Increment adds and Decrement subtracts, at least 1 (B8).
	// New counters have DefaultStep.
	Step int64
	// ResetOnStart sets the value to 0 when the core starts (B3).
	ResetOnStart bool
	// CreatedAt and UpdatedAt are maintained by the repository.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrOverflow is returned when a change would leave the range of int64
// (B43).
var ErrOverflow = errors.New("counter value out of range")

// ErrInvalid is wrapped by validation errors.
var ErrInvalid = errors.New("invalid counter")

// maxNameLen is the maximum length of a counter name.
const maxNameLen = 64

// DefaultStep is the step of a new counter (B8).
const DefaultStep = 1

// New returns a new counter with the name: value 0, DefaultStep, not reset
// on start.
func New(name string) Counter {
	return Counter{Name: name, Step: DefaultStep}
}

// Validate checks a counter before it is stored: a valid name (ValidateName)
// and a step of at least 1 (B8).
func (c Counter) Validate() error {
	if err := ValidateName(c.Name); err != nil {
		return err
	}
	if c.Step < 1 {
		return fmt.Errorf("%w: counter %q: step %d: want at least 1", ErrInvalid, c.Name, c.Step)
	}
	return nil
}

// ValidateName checks the name of a counter: 1 to 64 ASCII letters and
// digits, because the template engine reads identifier names from these
// characters only (B7, plan §6.10).
func ValidateName(name string) error {
	if name == "" || len(name) > maxNameLen {
		return fmt.Errorf("%w: name %q: want 1 to %d characters", ErrInvalid, name, maxNameLen)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("%w: name %q: only ASCII letters and digits are allowed", ErrInvalid, name)
		}
	}
	return nil
}

// ErrReserved is wrapped when the name of a counter collides with a
// built-in identifier (B7).
var ErrReserved = errors.New("counter name collides with a built-in identifier")

// Reserver reports whether a name collides with a built-in identifier and
// with which one; *template.Registry implements it.
type Reserver interface {
	Reserved(name string) (builtIn string, reserved bool)
}

// CheckReserved checks that neither $<name> nor $<name>display collides with
// a built-in identifier (B7; spec template.md, B12, B74). Whoever creates or
// renames a counter calls it after Validate; counters from an import keep
// their names, and the rules of the template engine decide (B74).
func (c Counter) CheckReserved(r Reserver) error {
	for _, name := range []string{c.Name, c.Name + "display"} {
		if builtIn, reserved := r.Reserved(name); reserved {
			return fmt.Errorf("%w: name %q collides with $%s", ErrReserved, c.Name, builtIn)
		}
	}
	return nil
}

// Add adds delta, which may be negative (B2). If the result would leave the
// range of int64, Add returns ErrOverflow and the value stays as it was
// (B43).
func (c *Counter) Add(delta int64) error {
	if delta > 0 && c.Value > math.MaxInt64-delta || delta < 0 && c.Value < math.MinInt64-delta {
		return fmt.Errorf("counter %q: %d %+d: %w", c.Name, c.Value, delta, ErrOverflow)
	}
	c.Value += delta
	return nil
}

// Increment adds the step (B2, B8). Like Add, it returns ErrOverflow and
// keeps the value if the result would leave the range of int64 (B43).
func (c *Counter) Increment() error {
	return c.Add(c.Step)
}

// Decrement subtracts the step (B2, B8), like Increment.
func (c *Counter) Decrement() error {
	return c.Add(-c.Step)
}

// Set sets the value (B2).
func (c *Counter) Set(v int64) {
	c.Value = v
}

// Reset sets the value to 0 (B2).
func (c *Counter) Reset() {
	c.Value = 0
}

// Repository stores counters; *store.Store implements it. Methods return an
// error wrapping store.ErrNotFound for a missing counter and
// store.ErrConflict for a name in use (B40). Every change is stored at once
// (B6).
type Repository interface {
	Counter(ctx context.Context, name string) (Counter, error)
	Counters(ctx context.Context) ([]Counter, error)
	// CreateCounter stores a new counter and returns it as stored; a zero ID
	// is replaced with a new one.
	CreateCounter(ctx context.Context, c Counter) (Counter, error)
	// UpdateCounter changes a counter in one transaction: fn gets the stored
	// counter and changes it, e.g. with Add, Increment, Set or Reset; an
	// error from fn, such as the ErrOverflow of Add, discards the change
	// (B43).
	UpdateCounter(ctx context.Context, name string, fn func(*Counter) error) (Counter, error)
	DeleteCounter(ctx context.Context, name string) error
	// ResetCountersOnStart sets the counters with ResetOnStart to 0 (B3) and
	// returns how many there were.
	ResetCountersOnStart(ctx context.Context) (int64, error)
}
