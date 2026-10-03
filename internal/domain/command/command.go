// SPDX-License-Identifier: Apache-2.0

// Package command is the data model of commands (spec commands.md): kinds,
// triggers, groups, requirements and the ordered list of actions.
// Requirements and actions are polymorphic documents (Code-ADR-0010).
//
// How commands run, queue and lock each other, and when requirements are
// checked, is up to the command engine (roadmap phase 3).
package command

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// Kind says what triggers a command (B2).
type Kind string

// The kinds of the start (P0); more follow with their phases (B2).
const (
	// KindChat runs on a chat message that matches one of its triggers.
	KindChat Kind = "chat"
	// KindEvent runs on an event of its event type (B20).
	KindEvent Kind = "event"
	// KindTimer runs on the timer schedule or its group's interval (B21).
	KindTimer Kind = "timer"
	// KindActionGroup runs only when started by another command, the API or
	// by hand (B22).
	KindActionGroup Kind = "action_group"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindChat, KindEvent, KindTimer, KindActionGroup:
		return true
	default:
		return false
	}
}

// ErrorPolicy says what happens when an action of a command fails (spec
// command-engine.md, B71).
type ErrorPolicy string

// Error policies.
const (
	// ErrorContinue runs the next action; the instance completes and keeps
	// the errors in its history. New commands start with it.
	ErrorContinue ErrorPolicy = "continue"
	// ErrorAbort ends the instance as failed.
	ErrorAbort ErrorPolicy = "abort"
)

// Valid reports whether p is a known error policy.
func (p ErrorPolicy) Valid() bool {
	switch p {
	case ErrorContinue, ErrorAbort:
		return true
	default:
		return false
	}
}

// Header holds the fields of a command apart from its requirements and
// actions (B1).
type Header struct {
	ID   id.ID
	Name string
	Kind Kind
	// Enabled commands run automatically; disabled ones only by hand (B3).
	Enabled bool
	// Unlocked commands run at once, even while another command of their
	// lock group runs (B5).
	Unlocked bool
	// GroupID is the group of the command; zero for none (B30).
	GroupID id.ID
	// Triggers of a chat command, without "!", as entered (B10 to B12).
	Triggers []string
	// Wildcard lets the triggers of a chat command match anywhere in a
	// message as whole words (B13).
	Wildcard bool
	// Event is the event type of an event command (B20).
	Event event.Type
	// ErrorPolicy says what happens after an action fails (spec
	// command-engine.md, B71); it must be set.
	ErrorPolicy ErrorPolicy
	// CreatedAt and UpdatedAt are maintained by the service.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Command is a command with decoded requirements and actions.
type Command struct {
	Header
	// Requirements has at most one entry per requirement type.
	Requirements []Requirement
	// Actions run in this order (B4).
	Actions []Action
}

// Record is a command as the repository stores it: requirements and actions
// are JSON arrays of documents, which Codec converts.
type Record struct {
	Header
	Requirements jsontext.Value
	Actions      jsontext.Value
}

// Group is a named collection of commands (B30 to B32).
type Group struct {
	ID id.ID
	// Name is unique, regardless of case.
	Name string
	// TimerInterval is the group's own timer interval; zero for none (B31).
	TimerInterval time.Duration
	// CreatedAt and UpdatedAt are maintained by the service.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CooldownGroup is a named cooldown that commands share (B33): the
// cooldown requirements of the grouped scopes name it and take its
// duration. It is independent of the command groups (B30).
type CooldownGroup struct {
	ID id.ID
	// Name is unique, regardless of case.
	Name string
	// Duration is the duration of the cooldown; it is positive.
	Duration time.Duration
	// CreatedAt and UpdatedAt are maintained by the service.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ErrInvalid is wrapped by validation errors.
var ErrInvalid = errors.New("invalid command")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks a command before it is saved.
func (c Command) Validate() error {
	if err := name(c.Name); err != nil {
		return err
	}
	switch c.Kind {
	case KindChat:
		if err := validTriggers(c.Triggers); err != nil {
			return err
		}
		if c.Event != "" {
			return invalid("a chat command has no event type")
		}
	case KindEvent:
		if _, ok := eventtype.Lookup(c.Event); !ok {
			return invalid("unknown event type %q", c.Event)
		}
		if err := noTriggers(c.Header); err != nil {
			return err
		}
	case KindTimer, KindActionGroup:
		if c.Event != "" {
			return invalid("a %s command has no event type", c.Kind)
		}
		if err := noTriggers(c.Header); err != nil {
			return err
		}
	default:
		return invalid("unknown kind %q", c.Kind)
	}
	if !c.ErrorPolicy.Valid() {
		return invalid("unknown error policy %q", c.ErrorPolicy)
	}

	seen := make(map[string]bool, len(c.Requirements))
	for _, r := range c.Requirements {
		if r == nil {
			return invalid("empty requirement")
		}
		typ := r.DocType()
		if seen[typ] {
			return invalid("requirement %q appears more than once", typ)
		}
		seen[typ] = true
		if err := r.Validate(); err != nil {
			return fmt.Errorf("%w: requirement %q: %w", ErrInvalid, typ, err)
		}
	}
	return ValidateActions(c.Actions)
}

// ValidateActions checks the actions of list and their child actions, at
// most polydoc.MaxDepth levels deep (Code-ADR-0013, points 5 and 7). An
// error names the path of the action, e.g. "3.2".
func ValidateActions(list []Action) error {
	return validActions(list, nil)
}

// validActions checks the actions of list, which are at path, and their
// child actions.
func validActions(list []Action, path []int) error {
	if len(path) >= polydoc.MaxDepth && len(list) > 0 {
		return invalid("actions nested more than %d levels deep", polydoc.MaxDepth)
	}
	for i, a := range list {
		at := append(slices.Clone(path), i+1)
		if a == nil {
			return invalid("empty action at %s", position(at))
		}
		if err := a.Validate(); err != nil {
			return fmt.Errorf("%w: action %s (%s): %w", ErrInvalid, position(at), a.DocType(), err)
		}
		if p, ok := a.(Parent); ok {
			if err := validActions(p.Children(), at); err != nil {
				return err
			}
		}
	}
	return nil
}

// position writes the path of an action as in the spec, e.g. "3.2"
// (actions.md B9).
func position(path []int) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ".")
}

func noTriggers(h Header) error {
	if len(h.Triggers) > 0 || h.Wildcard {
		return invalid("only chat commands have triggers")
	}
	return nil
}

// Validate checks a group before it is saved.
func (g Group) Validate() error {
	if err := name(g.Name); err != nil {
		return err
	}
	if g.TimerInterval < 0 {
		return invalid("negative timer interval")
	}
	return nil
}

// Validate checks a cooldown group before it is saved.
func (g CooldownGroup) Validate() error {
	if err := name(g.Name); err != nil {
		return err
	}
	if g.Duration <= 0 {
		return invalid("the duration of cooldown group %q must be positive", g.Name)
	}
	return nil
}

// name checks the name of a command or group.
func name(v string) error {
	if strings.TrimSpace(v) == "" {
		return invalid("empty name")
	}
	if v != strings.TrimSpace(v) {
		return invalid("name %q has leading or trailing space", v)
	}
	if strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return invalid("name contains a control character")
	}
	return nil
}

// Repository stores commands and groups; *store.Store implements it. Methods
// return an error wrapping store.ErrNotFound for a missing command or group
// and store.ErrConflict for a trigger that an enabled chat command already
// uses (B14), a second command for an event type (B20), a group name in use
// (B30) or a cooldown group name in use (B33), or a group that does not
// exist.
type Repository interface {
	Command(ctx context.Context, commandID id.ID) (Record, error)
	Commands(ctx context.Context) ([]Record, error)
	// PutCommand inserts or replaces a command; CreatedAt is kept from the
	// first insert.
	PutCommand(ctx context.Context, rec Record) error
	DeleteCommand(ctx context.Context, commandID id.ID) error
	Group(ctx context.Context, groupID id.ID) (Group, error)
	Groups(ctx context.Context) ([]Group, error)
	// PutGroup inserts or replaces a group; CreatedAt is kept from the first
	// insert.
	PutGroup(ctx context.Context, g Group) error
	// DeleteGroup deletes a group; its commands stay without a group (B62).
	DeleteGroup(ctx context.Context, groupID id.ID) error
	CooldownGroup(ctx context.Context, groupID id.ID) (CooldownGroup, error)
	CooldownGroups(ctx context.Context) ([]CooldownGroup, error)
	// PutCooldownGroup inserts or replaces a cooldown group; CreatedAt is
	// kept from the first insert.
	PutCooldownGroup(ctx context.Context, g CooldownGroup) error
	// DeleteCooldownGroup deletes a cooldown group; commands whose
	// cooldown names it become faulty (B64).
	DeleteCooldownGroup(ctx context.Context, groupID id.ID) error
	// SwitchCommands changes the switch "active" of the commands by sw, in
	// one transaction: all or none. A command whose switch does not change
	// keeps its UpdatedAt; the others get updatedAt.
	SwitchCommands(ctx context.Context, commandIDs []id.ID, sw Switch, updatedAt time.Time) error
}
