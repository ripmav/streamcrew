// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

var _ counter.Repository = (*Store)(nil)

// Counter implements counter.Repository; the name matches regardless of
// case.
func (s *Store) Counter(ctx context.Context, name string) (counter.Counter, error) {
	row, err := s.reader().GetCounter(ctx, name)
	if err != nil {
		return counter.Counter{}, fmt.Errorf("counter %q: %w", name, translate(err))
	}
	return toCounter(row)
}

// Counters implements counter.Repository: all counters by name.
func (s *Store) Counters(ctx context.Context) ([]counter.Counter, error) {
	rows, err := s.reader().ListCounters(ctx)
	if err != nil {
		return nil, fmt.Errorf("list counters: %w", translate(err))
	}
	out := make([]counter.Counter, 0, len(rows))
	for _, row := range rows {
		c, err := toCounter(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// CreateCounter implements counter.Repository.
func (s *Store) CreateCounter(ctx context.Context, c counter.Counter) (counter.Counter, error) {
	if err := c.Validate(); err != nil {
		return counter.Counter{}, err
	}
	if c.ID.IsZero() {
		c.ID = id.New()
	}
	c.CreatedAt = now()
	c.UpdatedAt = c.CreatedAt
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.InsertCounter(ctx, sqlcgen.InsertCounterParams{
			ID:           c.ID.String(),
			Name:         c.Name,
			Value:        c.Value,
			Step:         c.Step,
			ResetOnStart: flag(c.ResetOnStart),
			CreatedAt:    c.CreatedAt.UnixMilli(),
			UpdatedAt:    c.UpdatedAt.UnixMilli(),
		})
	})
	if err != nil {
		return counter.Counter{}, fmt.Errorf("create counter %q: %w", c.Name, err)
	}
	return c, nil
}

// UpdateCounter implements counter.Repository. fn may rename the counter;
// changes to ID and timestamps are ignored.
func (s *Store) UpdateCounter(ctx context.Context, name string, fn func(*counter.Counter) error) (counter.Counter, error) {
	var c counter.Counter
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetCounter(ctx, name)
		if err != nil {
			return err
		}
		stored, err := toCounter(row)
		if err != nil {
			return err
		}
		c = stored
		if err := fn(&c); err != nil {
			return err
		}
		c.ID, c.CreatedAt, c.UpdatedAt = stored.ID, stored.CreatedAt, now()
		if err := c.Validate(); err != nil {
			return err
		}
		return q.UpdateCounter(ctx, sqlcgen.UpdateCounterParams{
			Name:         c.Name,
			Value:        c.Value,
			Step:         c.Step,
			ResetOnStart: flag(c.ResetOnStart),
			UpdatedAt:    c.UpdatedAt.UnixMilli(),
			ID:           c.ID.String(),
		})
	})
	if err != nil {
		return counter.Counter{}, fmt.Errorf("update counter %q: %w", name, err)
	}
	return c, nil
}

// DeleteCounter implements counter.Repository.
func (s *Store) DeleteCounter(ctx context.Context, name string) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		n, err := q.DeleteCounter(ctx, name)
		if err != nil {
			return err
		}
		return notFound(n, fmt.Sprintf("counter %q", name))
	})
}

// ResetCountersOnStart implements counter.Repository.
func (s *Store) ResetCountersOnStart(ctx context.Context) (int64, error) {
	var n int64
	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		var err error
		n, err = q.ResetCountersOnStart(ctx, now().UnixMilli())
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("reset counters: %w", err)
	}
	return n, nil
}

func toCounter(row sqlcgen.Counter) (counter.Counter, error) {
	cid, err := id.Parse(row.ID)
	if err != nil {
		return counter.Counter{}, err
	}
	return counter.Counter{
		ID:           cid,
		Name:         row.Name,
		Value:        row.Value,
		Step:         row.Step,
		ResetOnStart: row.ResetOnStart != 0,
		CreatedAt:    fromMillis(row.CreatedAt),
		UpdatedAt:    fromMillis(row.UpdatedAt),
	}, nil
}
