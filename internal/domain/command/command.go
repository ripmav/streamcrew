// SPDX-License-Identifier: MIT

// Package command is the data model of commands (spec commands.md): kinds,
// triggers, groups, requirements and the ordered list of actions.
// Requirements and actions are polymorphic documents (Code-ADR-0010).
//
// How commands run, queue and lock each other, and when requirements are
// checked, is up to the command engine (roadmap phase 3).
package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
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
	Requirements json.RawMessage
	Actions      json.RawMessage
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
	for _, a := range c.Actions {
		if a == nil {
			return invalid("empty action")
		}
	}
	return nil
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
// (B30) or a group that does not exist.
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
}
