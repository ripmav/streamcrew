// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// CooldownEnd returns the end of the cooldown key (requirements.md, B23);
// ok is false if none was started. A cooldown that has ended may still be
// returned until the next PutCooldown forgets it; the caller compares the
// end with the time.
func (s *Store) CooldownEnd(ctx context.Context, key command.CooldownKey) (ends time.Time, ok bool, err error) {
	if err := checkCooldownKey(key); err != nil {
		return time.Time{}, false, err
	}
	ms, err := s.reader().GetCooldownEnd(ctx, sqlcgen.GetCooldownEndParams{
		CommandID: nullID(key.Command),
		GroupID:   nullID(key.Group),
		UserID:    nullID(key.User),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("cooldown %v: %w", key, translate(err))
	}
	return fromMillis(ms), true, nil
}

// PutCooldown sets the end of the cooldown key to ends, stored with
// millisecond precision (requirements.md, B21, B23), and forgets the
// cooldowns that ended at or before now. The command, cooldown group and
// user of key must exist; their cooldowns go when they are deleted.
func (s *Store) PutCooldown(ctx context.Context, key command.CooldownKey, ends, now time.Time) error {
	if err := checkCooldownKey(key); err != nil {
		return err
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		if err := q.DeleteEndedCooldowns(ctx, now.UnixMilli()); err != nil {
			return err
		}
		if err := q.DeleteCooldown(ctx, sqlcgen.DeleteCooldownParams{
			CommandID: nullID(key.Command),
			GroupID:   nullID(key.Group),
			UserID:    nullID(key.User),
		}); err != nil {
			return err
		}
		return q.InsertCooldown(ctx, sqlcgen.InsertCooldownParams{
			CommandID: nullID(key.Command),
			GroupID:   nullID(key.Group),
			UserID:    nullID(key.User),
			EndsAt:    ends.UnixMilli(),
		})
	})
	if err != nil {
		return fmt.Errorf("put cooldown %v: %w", key, err)
	}
	return nil
}

// DeleteCooldown deletes the cooldown key if it still ends at ends, with
// millisecond precision: it takes back a start for a command that was not
// queued (command-engine.md, B15), but not a later start.
func (s *Store) DeleteCooldown(ctx context.Context, key command.CooldownKey, ends time.Time) error {
	if err := checkCooldownKey(key); err != nil {
		return err
	}
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.DeleteCooldownEndingAt(ctx, sqlcgen.DeleteCooldownEndingAtParams{
			CommandID: nullID(key.Command),
			GroupID:   nullID(key.Group),
			UserID:    nullID(key.User),
			EndsAt:    ends.UnixMilli(),
		})
	})
	if err != nil {
		return fmt.Errorf("delete cooldown %v: %w", key, err)
	}
	return nil
}

// checkCooldownKey checks that key names either a command or a cooldown
// group.
func checkCooldownKey(key command.CooldownKey) error {
	if key.Command.IsZero() == key.Group.IsZero() {
		return fmt.Errorf("cooldown %v: needs either a command or a cooldown group", key)
	}
	return nil
}
