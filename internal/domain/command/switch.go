// SPDX-License-Identifier: MIT

package command

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Switch says how to change the switch "active" of commands, e.g. from the
// command action (spec actions.md, B34).
type Switch string

// The ways to change the switch.
const (
	// SwitchOn enables a command.
	SwitchOn Switch = "on"
	// SwitchOff disables a command.
	SwitchOff Switch = "off"
	// SwitchToggle enables a disabled command and disables an enabled one.
	SwitchToggle Switch = "toggle"
)

// Switches returns the ways to change the switch.
func Switches() []Switch {
	return []Switch{SwitchOn, SwitchOff, SwitchToggle}
}

// Valid reports whether s is a known way to change the switch.
func (s Switch) Valid() bool {
	return slices.Contains(Switches(), s)
}

// Apply returns the switch of a command after s, for a command that is
// enabled or not.
func (s Switch) Apply(enabled bool) (bool, error) {
	switch s {
	case SwitchOn:
		return true, nil
	case SwitchOff:
		return false, nil
	case SwitchToggle:
		return !enabled, nil
	default:
		return false, fmt.Errorf("%w: unknown switch %q", ErrInvalid, s)
	}
}

// SwitchCommand changes the switch "active" of a command for good, as a
// change in the user interface does, and returns the command as stored
// (spec actions.md, B34). A command that has the target state already stays
// unchanged, its change time too. Enabling a chat command whose trigger an
// enabled chat command uses fails with store.ErrConflict (B14).
func (s *Service) SwitchCommand(ctx context.Context, commandID id.ID, sw Switch) (Command, error) {
	if !sw.Valid() {
		return Command{}, fmt.Errorf("%w: unknown switch %q", ErrInvalid, sw)
	}
	if err := s.repo.SwitchCommands(ctx, []id.ID{commandID}, sw, now()); err != nil {
		return Command{}, fmt.Errorf("switch command %s %s: %w", commandID, sw, err)
	}
	return s.Command(ctx, commandID)
}

// SwitchGroup changes the switch "active" of all commands of a group as
// SwitchCommand does, all or none (spec actions.md, B34). A group without
// commands is no error.
func (s *Service) SwitchGroup(ctx context.Context, groupID id.ID, sw Switch) error {
	if !sw.Valid() {
		return fmt.Errorf("%w: unknown switch %q", ErrInvalid, sw)
	}
	if _, err := s.repo.Group(ctx, groupID); err != nil {
		return fmt.Errorf("switch command group %s %s: %w", groupID, sw, err)
	}
	recs, err := s.repo.Commands(ctx)
	if err != nil {
		return fmt.Errorf("switch command group %s %s: %w", groupID, sw, err)
	}
	var ids []id.ID
	for _, rec := range recs {
		if rec.GroupID == groupID {
			ids = append(ids, rec.ID)
		}
	}
	if err := s.repo.SwitchCommands(ctx, ids, sw, now()); err != nil {
		return fmt.Errorf("switch command group %s %s: %w", groupID, sw, err)
	}
	return nil
}

// now returns the current time as the store keeps it: UTC, in
// milliseconds.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}
