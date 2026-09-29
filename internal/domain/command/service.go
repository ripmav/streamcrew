// SPDX-License-Identifier: Apache-2.0

package command

import (
	"context"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
)

// Service validates, encodes and stores commands and groups.
type Service struct {
	repo  Repository
	codec *Codec
}

// NewService returns a service on repo.
func NewService(repo Repository, codec *Codec) *Service {
	return &Service{repo: repo, codec: codec}
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

// Save validates and stores a command and returns it as stored. A command
// without an ID is new and gets one.
func (s *Service) Save(ctx context.Context, cmd Command) (Command, error) {
	if err := cmd.Validate(); err != nil {
		return Command{}, err
	}
	if cmd.ID.IsZero() {
		cmd.ID = id.New()
	}
	cmd.CreatedAt, cmd.UpdatedAt = stamps(cmd.CreatedAt)
	rec, err := s.codec.Record(cmd)
	if err != nil {
		return Command{}, err
	}
	if err := s.repo.PutCommand(ctx, rec); err != nil {
		return Command{}, fmt.Errorf("save command %q: %w", cmd.Name, err)
	}
	return s.Command(ctx, cmd.ID)
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
