// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// readTx runs fn in a transaction on the reader pool, so that queries that
// belong together see one snapshot. It never commits.
func (s *Store) readTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := s.read.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin read transaction: %w", err)
	}
	return errors.Join(translate(fn(sqlcgen.New(tx))), rollback(tx))
}

// notFound returns ErrNotFound for a write that changed no row.
func notFound(rows int64, what string) error {
	if rows == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

// now returns the current time as stored: UTC with millisecond precision
// (Code-ADR-0009).
func now() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// fromMillis converts a stored time.
func fromMillis(ms int64) time.Time {
	return time.UnixMilli(ms).UTC()
}

// nullMillis stores a zero time as NULL.
func nullMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

// fromNullMillis converts NULL to the zero time.
func fromNullMillis(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return fromMillis(v.Int64)
}

// flag stores a bool as 0 or 1.
func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// nullID stores a zero ID as NULL.
func nullID(v id.ID) sql.NullString {
	if v.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: v.String(), Valid: true}
}

// parseNullID converts NULL to the zero ID.
func parseNullID(v sql.NullString) (id.ID, error) {
	if !v.Valid {
		return id.ID{}, nil
	}
	return id.Parse(v.String)
}
