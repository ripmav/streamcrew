// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"fmt"
	"strings"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

var _ command.Repository = (*Store)(nil)

// Command implements command.Repository.
func (s *Store) Command(ctx context.Context, commandID id.ID) (command.Record, error) {
	var rec command.Record
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetCommand(ctx, commandID.String())
		if err != nil {
			return err
		}
		triggers, err := q.ListTriggers(ctx, row.ID)
		if err != nil {
			return err
		}
		rec, err = toCommandRecord(row, triggers)
		return err
	})
	if err != nil {
		return command.Record{}, fmt.Errorf("command %s: %w", commandID, err)
	}
	return rec, nil
}

// Commands implements command.Repository: all commands by name.
func (s *Store) Commands(ctx context.Context) ([]command.Record, error) {
	var recs []command.Record
	err := s.readTx(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListCommands(ctx)
		if err != nil {
			return err
		}
		all, err := q.ListAllTriggers(ctx)
		if err != nil {
			return err
		}
		triggers := make(map[string][]string)
		for _, t := range all {
			triggers[t.CommandID] = append(triggers[t.CommandID], t.TriggerText)
		}
		recs = make([]command.Record, 0, len(rows))
		for _, row := range rows {
			rec, err := toCommandRecord(row, triggers[row.ID])
			if err != nil {
				return err
			}
			recs = append(recs, rec)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list commands: %w", err)
	}
	return recs, nil
}

// PutCommand implements command.Repository. The triggers are rewritten with
// the command; they count for uniqueness only while it is an enabled chat
// command (B14).
func (s *Store) PutCommand(ctx context.Context, rec command.Record) error {
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		cmdID := rec.ID.String()
		var eventType sql.NullString
		if rec.Event != "" {
			eventType = sql.NullString{String: string(rec.Event), Valid: true}
		}
		err := q.PutCommand(ctx, sqlcgen.PutCommandParams{
			ID:           cmdID,
			Name:         rec.Name,
			Kind:         string(rec.Kind),
			Enabled:      flag(rec.Enabled),
			Unlocked:     flag(rec.Unlocked),
			GroupID:      nullID(rec.GroupID),
			Wildcard:     flag(rec.Wildcard),
			EventType:    eventType,
			ErrorPolicy:  string(rec.ErrorPolicy),
			Requirements: documents(rec.Requirements),
			Actions:      documents(rec.Actions),
			CreatedAt:    rec.CreatedAt.UnixMilli(),
			UpdatedAt:    rec.UpdatedAt.UnixMilli(),
		})
		if err != nil {
			return err
		}
		if err := q.DeleteTriggers(ctx, cmdID); err != nil {
			return err
		}
		active := flag(rec.Enabled && rec.Kind == command.KindChat)
		for i, t := range rec.Triggers {
			err := q.InsertTrigger(ctx, sqlcgen.InsertTriggerParams{
				CommandID:   cmdID,
				Position:    int64(i),
				TriggerText: t,
				TriggerKey:  command.TriggerKey(t),
				Active:      active,
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("put command %s: %w", rec.ID, err)
	}
	return nil
}

// DeleteCommand implements command.Repository.
func (s *Store) DeleteCommand(ctx context.Context, commandID id.ID) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.DeleteCommand(ctx, commandID.String())
		if err != nil {
			return err
		}
		return notFound(n, "command "+commandID.String())
	})
}

// Group implements command.Repository.
func (s *Store) Group(ctx context.Context, groupID id.ID) (command.Group, error) {
	row, err := s.reader().GetCommandGroup(ctx, groupID.String())
	if err != nil {
		return command.Group{}, fmt.Errorf("command group %s: %w", groupID, translate(err))
	}
	return toGroup(row)
}

// Groups implements command.Repository: all groups by name.
func (s *Store) Groups(ctx context.Context) ([]command.Group, error) {
	rows, err := s.reader().ListCommandGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list command groups: %w", translate(err))
	}
	groups := make([]command.Group, 0, len(rows))
	for _, row := range rows {
		g, err := toGroup(row)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// PutGroup implements command.Repository.
func (s *Store) PutGroup(ctx context.Context, g command.Group) error {
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.PutCommandGroup(ctx, sqlcgen.PutCommandGroupParams{
			ID:            g.ID.String(),
			Name:          g.Name,
			NameKey:       strings.ToLower(g.Name),
			TimerInterval: g.TimerInterval.Milliseconds(),
			CreatedAt:     g.CreatedAt.UnixMilli(),
			UpdatedAt:     g.UpdatedAt.UnixMilli(),
		})
	})
	if err != nil {
		return fmt.Errorf("put command group %s: %w", g.ID, err)
	}
	return nil
}

// DeleteGroup implements command.Repository; the foreign key leaves the
// group's commands without a group (B62).
func (s *Store) DeleteGroup(ctx context.Context, groupID id.ID) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.DeleteCommandGroup(ctx, groupID.String())
		if err != nil {
			return err
		}
		return notFound(n, "command group "+groupID.String())
	})
}

// documents stores a JSON array of documents; nil is the empty array.
func documents(raw jsontext.Value) string {
	if len(raw) == 0 {
		return "[]"
	}
	return string(raw)
}

func toCommandRecord(row sqlcgen.Command, triggers []string) (command.Record, error) {
	cmdID, err := id.Parse(row.ID)
	if err != nil {
		return command.Record{}, err
	}
	groupID, err := parseNullID(row.GroupID)
	if err != nil {
		return command.Record{}, err
	}
	if triggers == nil {
		triggers = []string{}
	}
	return command.Record{
		ID:           cmdID,
		Name:         row.Name,
		Kind:         command.Kind(row.Kind),
		Enabled:      row.Enabled != 0,
		Unlocked:     row.Unlocked != 0,
		GroupID:      groupID,
		Triggers:     triggers,
		Wildcard:     row.Wildcard != 0,
		Event:        event.Type(row.EventType.String),
		ErrorPolicy:  command.ErrorPolicy(row.ErrorPolicy),
		CreatedAt:    fromMillis(row.CreatedAt),
		UpdatedAt:    fromMillis(row.UpdatedAt),
		Requirements: jsontext.Value(row.Requirements),
		Actions:      jsontext.Value(row.Actions),
	}, nil
}

func toGroup(row sqlcgen.CommandGroup) (command.Group, error) {
	groupID, err := id.Parse(row.ID)
	if err != nil {
		return command.Group{}, err
	}
	return command.Group{
		ID:            groupID,
		Name:          row.Name,
		TimerInterval: time.Duration(row.TimerInterval) * time.Millisecond,
		CreatedAt:     fromMillis(row.CreatedAt),
		UpdatedAt:     fromMillis(row.UpdatedAt),
	}, nil
}
