// SPDX-License-Identifier: Apache-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Service validates, encodes and stores commands and groups.
type Service struct {
	repo   Repository
	codec  *Codec
	checks Checks
}

// NewService returns a service on repo that checks the actions of commands
// with checks when it saves them (Code-ADR-0013, point 7).
func NewService(repo Repository, codec *Codec, checks Checks) (*Service, error) {
	switch {
	case repo == nil:
		return nil, errors.New("new command service: no repository")
	case codec == nil:
		return nil, errors.New("new command service: no codec")
	}
	if err := checks.validate(); err != nil {
		return nil, fmt.Errorf("new command service: %w", err)
	}
	return &Service{repo: repo, codec: codec, checks: checks}, nil
}

// Command returns a command.
func (s *Service) Command(ctx context.Context, commandID id.ID) (Command, error) {
	rec, err := s.repo.Command(ctx, commandID)
	if err != nil {
		return Command{}, err
	}
	return s.codec.Command(rec)
}

// Commands returns all commands.
func (s *Service) Commands(ctx context.Context) ([]Command, error) {
	recs, err := s.repo.Commands(ctx)
	if err != nil {
		return nil, err
	}
	cmds := make([]Command, 0, len(recs))
	for _, rec := range recs {
		cmd, err := s.codec.Command(rec)
		if err != nil {
			return nil, err
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// Save validates and stores a command and returns it as stored, with the
// warnings about it. A command without an ID is new and gets one.
//
// Besides the command itself, Save checks its requirements (requirements.md,
// B80, B81) and its actions (Code-ADR-0013, point 7): cooldown groups,
// commands and groups they refer to must exist, the identifiers of
// arguments and the names of result values must not hide built-in
// identifiers, and counters the actions name are created if missing.
// Currencies, ranks and items that do not exist, missing capabilities and
// unknown roots for files do not stop the save; they come back as warnings,
// those of the requirements first.
func (s *Service) Save(ctx context.Context, cmd Command) (Saved, error) {
	if err := cmd.Validate(); err != nil {
		return Saved{}, err
	}
	if cmd.ID.IsZero() {
		cmd.ID = id.New()
	}
	warnings, err := s.checkRequirements(ctx, cmd)
	if err != nil {
		return Saved{}, err
	}
	actionWarnings, err := s.checkActions(ctx, cmd)
	if err != nil {
		return Saved{}, err
	}
	warnings = append(warnings, actionWarnings...)
	cmd.CreatedAt, cmd.UpdatedAt = stamps(cmd.CreatedAt)
	rec, err := s.codec.Record(cmd)
	if err != nil {
		return Saved{}, err
	}
	if err := s.repo.PutCommand(ctx, rec); err != nil {
		return Saved{}, fmt.Errorf("save command %q: %w", cmd.Name, err)
	}
	stored, err := s.Command(ctx, cmd.ID)
	if err != nil {
		return Saved{}, err
	}
	return Saved{Command: stored, Warnings: warnings}, nil
}

// Delete deletes a command, unless the actions of another command refer to
// it (B8): then it returns an *InUseError, and the command can be switched
// off instead. A reference of the command to itself does not count.
func (s *Service) Delete(ctx context.Context, commandID id.ID) error {
	rec, err := s.repo.Command(ctx, commandID)
	if err != nil {
		return err
	}
	users, err := s.users(ctx, func(cmd Command) []string {
		if cmd.ID == commandID {
			return nil
		}
		return actionsReferring(cmd.Actions, commandID)
	})
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &InUseError{What: fmt.Sprintf("command %q", rec.Name), Users: users, Switch: true}
	}
	return s.repo.DeleteCommand(ctx, commandID)
}

// Group returns a group.
func (s *Service) Group(ctx context.Context, groupID id.ID) (Group, error) {
	return s.repo.Group(ctx, groupID)
}

// Groups returns all groups.
func (s *Service) Groups(ctx context.Context) ([]Group, error) {
	return s.repo.Groups(ctx)
}

// SaveGroup validates and stores a group and returns it as stored. A group
// without an ID is new and gets one.
func (s *Service) SaveGroup(ctx context.Context, g Group) (Group, error) {
	if err := g.Validate(); err != nil {
		return Group{}, err
	}
	if g.ID.IsZero() {
		g.ID = id.New()
	}
	g.CreatedAt, g.UpdatedAt = stamps(g.CreatedAt)
	if err := s.repo.PutGroup(ctx, g); err != nil {
		return Group{}, fmt.Errorf("save group %q: %w", g.Name, err)
	}
	return s.repo.Group(ctx, g.ID)
}

// DeleteGroup deletes a group; its commands stay without a group (B62).
// If a command action of a command refers to it, it returns an
// *InUseError (B8).
func (s *Service) DeleteGroup(ctx context.Context, groupID id.ID) error {
	g, err := s.repo.Group(ctx, groupID)
	if err != nil {
		return err
	}
	users, err := s.users(ctx, func(cmd Command) []string {
		return actionsReferring(cmd.Actions, groupID)
	})
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &InUseError{What: fmt.Sprintf("command group %q", g.Name), Users: users}
	}
	return s.repo.DeleteGroup(ctx, groupID)
}

// CooldownGroup returns a cooldown group.
func (s *Service) CooldownGroup(ctx context.Context, groupID id.ID) (CooldownGroup, error) {
	return s.repo.CooldownGroup(ctx, groupID)
}

// CooldownGroups returns all cooldown groups.
func (s *Service) CooldownGroups(ctx context.Context) ([]CooldownGroup, error) {
	return s.repo.CooldownGroups(ctx)
}

// SaveCooldownGroup validates and stores a cooldown group and returns it as
// stored (B33). A cooldown group without an ID is new and gets one. A new
// duration applies to cooldowns that start afterwards; running ones keep
// their end (requirements.md, B23).
func (s *Service) SaveCooldownGroup(ctx context.Context, g CooldownGroup) (CooldownGroup, error) {
	if err := g.Validate(); err != nil {
		return CooldownGroup{}, err
	}
	if g.ID.IsZero() {
		g.ID = id.New()
	}
	g.CreatedAt, g.UpdatedAt = stamps(g.CreatedAt)
	if err := s.repo.PutCooldownGroup(ctx, g); err != nil {
		return CooldownGroup{}, fmt.Errorf("save cooldown group %q: %w", g.Name, err)
	}
	return s.repo.CooldownGroup(ctx, g.ID)
}

// DeleteCooldownGroup deletes a cooldown group, unless the cooldown of a
// command names it: then it returns an *InUseError (B8, B64).
func (s *Service) DeleteCooldownGroup(ctx context.Context, groupID id.ID) error {
	g, err := s.repo.CooldownGroup(ctx, groupID)
	if err != nil {
		return err
	}
	users, err := s.users(ctx, func(cmd Command) []string {
		for _, r := range cmd.Requirements {
			if c, ok := r.(CooldownRequirement); ok && c.Group == groupID {
				return []string{"cooldown"}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(users) > 0 {
		return &InUseError{What: fmt.Sprintf("cooldown group %q", g.Name), Users: users}
	}
	return s.repo.DeleteCooldownGroup(ctx, groupID)
}

// ErrInUse is matched by an *InUseError: what was to be deleted is in use.
var ErrInUse = errors.New("in use")

// Usage is a command that refers to something, and where: e.g. "action
// 2.1" (actions.md, B9) or "cooldown".
type Usage struct {
	CommandID id.ID
	Name      string
	Where     []string
}

// InUseError reports that the commands Users refer to what was to be
// deleted (B8).
type InUseError struct {
	// What names it, e.g. `command "Wave"`.
	What  string
	Users []Usage
	// Switch says that it can be switched off instead, as a command can.
	Switch bool
}

// Error implements error, e.g. `command "Wave" is in use by "Hug" (action
// 2), so it cannot be deleted; switch it off instead`.
func (e *InUseError) Error() string {
	users := make([]string, len(e.Users))
	for i, u := range e.Users {
		users[i] = fmt.Sprintf("%q (%s)", u.Name, strings.Join(u.Where, ", "))
	}
	msg := fmt.Sprintf("%s is in use by %s, so it cannot be deleted", e.What, strings.Join(users, ", "))
	if e.Switch {
		msg += "; switch it off instead"
	}
	return msg
}

// Is reports whether target is ErrInUse.
func (e *InUseError) Is(target error) bool { return target == ErrInUse }

// users returns the commands for which where finds places that refer to
// something, in the order of the repository.
func (s *Service) users(ctx context.Context, where func(Command) []string) ([]Usage, error) {
	cmds, err := s.Commands(ctx)
	if err != nil {
		return nil, err
	}
	var users []Usage
	for _, cmd := range cmds {
		if places := where(cmd); len(places) > 0 {
			users = append(users, Usage{CommandID: cmd.ID, Name: cmd.Name, Where: places})
		}
	}
	return users, nil
}

// actionsReferring returns the places of the actions in list and below
// that refer to the command or group target, e.g. "action 2.1"; IDs are
// unique across kinds.
func actionsReferring(list []Action, target id.ID) []string {
	var places []string
	_ = eachAction(list, nil, func(path []int, a Action) error {
		r, ok := a.(Referrer)
		if !ok {
			return nil
		}
		if slices.ContainsFunc(r.References(), func(ref Reference) bool { return ref.ID == target }) {
			places = append(places, "action "+position(path))
		}
		return nil
	})
	return places
}

// stamps returns the creation and update time of a save, in UTC with the
// millisecond precision of the database (Code-ADR-0009).
func stamps(created time.Time) (createdAt, updatedAt time.Time) {
	updatedAt = now()
	if created.IsZero() {
		created = updatedAt
	}
	return created, updatedAt
}
