// SPDX-License-Identifier: Apache-2.0

// Package counter is the model of counters (spec counters-and-quotes.md, B1
// to B7): named whole numbers of a profile, e.g. deaths in a game, that
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

// Validate checks a counter before it is stored: the name consists of 1 to
// 64 ASCII letters and digits, because the template engine reads identifier
// names from these characters only (B7, plan §6.10).
func (c Counter) Validate() error {
	if c.Name == "" || len(c.Name) > maxNameLen {
		return fmt.Errorf("%w: name %q: want 1 to %d characters", ErrInvalid, c.Name, maxNameLen)
	}
	for _, r := range c.Name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return fmt.Errorf("%w: name %q: only ASCII letters and digits are allowed", ErrInvalid, c.Name)
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
// range of int64, the value stops at the limit and Add returns ErrOverflow
// (B43); storing the clamped value is up to the caller.
func (c *Counter) Add(delta int64) error {
	switch {
	case delta > 0 && c.Value > math.MaxInt64-delta:
		c.Value = math.MaxInt64
		return fmt.Errorf("counter %q: %w", c.Name, ErrOverflow)
	case delta < 0 && c.Value < math.MinInt64-delta:
		c.Value = math.MinInt64
		return fmt.Errorf("counter %q: %w", c.Name, ErrOverflow)
	}
	c.Value += delta
	return nil
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
	// counter and changes it with Add, Set or Reset; an error from fn
	// discards the change. To store the value an overflow stopped at (B43),
	// fn must not return the ErrOverflow of Add but report it by other means,
	// as TestCounterOverflowStopsAtLimit in internal/store shows.
	UpdateCounter(ctx context.Context, name string, fn func(*Counter) error) (Counter, error)
	DeleteCounter(ctx context.Context, name string) error
	// ResetCountersOnStart sets the counters with ResetOnStart to 0 (B3) and
	// returns how many there were.
	ResetCountersOnStart(ctx context.Context) (int64, error)
}
