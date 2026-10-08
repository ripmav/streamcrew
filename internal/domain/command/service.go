// SPDX-License-Identifier: MIT

package command

import (
	"context"
	"errors"
	"fmt"
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
// Besides the command itself, Save checks its actions (Code-ADR-0013,
// point 7): commands and groups they refer to must exist, the names of
// their result values must not hide built-in identifiers, and counters they
// name are created if missing. Missing capabilities and unknown roots for
// files do not stop the save; they come back as warnings.
func (s *Service) Save(ctx context.Context, cmd Command) (Saved, error) {
	if err := cmd.Validate(); err != nil {
		return Saved{}, err
	}
	if cmd.ID.IsZero() {
		cmd.ID = id.New()
	}
	warnings, err := s.checkActions(ctx, cmd)
	if err != nil {
		return Saved{}, err
	}
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

// Delete deletes a command.
func (s *Service) Delete(ctx context.Context, commandID id.ID) error {
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
func (s *Service) DeleteGroup(ctx context.Context, groupID id.ID) error {
	return s.repo.DeleteGroup(ctx, groupID)
}

// stamps returns the creation and update time of a save, in UTC with the
// millisecond precision of the database (Code-ADR-0009).
func stamps(created time.Time) (createdAt, updatedAt time.Time) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	if created.IsZero() {
		created = now
	}
	return created, now
}
