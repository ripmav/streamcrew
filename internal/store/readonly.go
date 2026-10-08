// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
	"github.com/ripmav/streamcrew/internal/vault"
)

// ReadOnly is a profile database opened read-only and without migrations,
// e.g. to back it up while the core runs or before it is deleted.
type ReadOnly struct {
	db   *sql.DB
	info Info
}

// OpenReadOnly opens the database at path read-only.
func OpenReadOnly(ctx context.Context, path string) (*ReadOnly, error) {
	info, err := Inspect(ctx, path)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_busy_timeout", strconv.Itoa(busyTimeoutMillis))
	// Not query_only: SQLite rejects VACUUM INTO on such connections.
	db, err := sql.Open("sqlite", fileURI(filepath.Clean(path), q))
	if err != nil {
		return nil, fmt.Errorf("open database read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	return &ReadOnly{db: db, info: info}, nil
}

// Close closes the database.
func (r *ReadOnly) Close() error {
	return r.db.Close()
}

// SchemaVersion returns the schema version of the database.
func (r *ReadOnly) SchemaVersion() int64 {
	return r.info.SchemaVersion
}

// Meta returns a metadata value, or "" if it is not set.
func (r *ReadOnly) Meta(key string) string {
	return r.info.Meta[key]
}

// Accounts returns the metadata of all platform logins, by platform and
// role.
func (r *ReadOnly) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := sqlcgen.New(r.db).ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", translate(err))
	}
	out := make([]Account, 0, len(rows))
	for _, row := range rows {
		out = append(out, accountFrom(row))
	}
	return out, nil
}

// GetSecret returns a vault record; found is false if there is no record
// with the name.
func (r *ReadOnly) GetSecret(ctx context.Context, name string) (vault.Record, bool, error) {
	row, err := sqlcgen.New(r.db).GetSecret(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return vault.Record{}, false, nil
	}
	if err != nil {
		return vault.Record{}, false, fmt.Errorf("get vault entry: %w", translate(err))
	}
	return toRecord(row), true, nil
}

// VacuumInto writes a consistent, compact copy of the database to dest,
// which must not exist.
func (r *ReadOnly) VacuumInto(ctx context.Context, dest string) error {
	switch _, err := os.Stat(dest); {
	case err == nil:
		return fmt.Errorf("vacuum into %s: target exists", dest)
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	if _, err := r.db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("vacuum into %s: %w", dest, err)
	}
	return nil
}
