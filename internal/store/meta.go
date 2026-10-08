// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

// Meta returns a metadata value of the profile, e.g. its display name.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	v, err := s.reader().GetMeta(ctx, key)
	if err != nil {
		return "", fmt.Errorf("meta %q: %w", key, translate(err))
	}
	return v, nil
}

// SetMeta sets a metadata value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	return s.Write(ctx, func(q *sqlcgen.Queries) error {
		return q.SetMeta(ctx, sqlcgen.SetMetaParams{Key: key, Value: value})
	})
}

// Info describes a database file without opening it for writing.
type Info struct {
	// SchemaVersion is the applied schema version; 0 for an empty database.
	SchemaVersion int64
	// Meta holds the metadata of the profile.
	Meta map[string]string
}

// Inspect reads the schema version and metadata of the database at path. It
// opens the file read-only and never migrates, so it also works for backups
// and for databases of a newer version.
func Inspect(ctx context.Context, path string) (Info, error) {
	if _, err := os.Stat(path); err != nil {
		return Info{}, fmt.Errorf("inspect database: %w", err)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	q.Set("_query_only", "1")
	q.Set("_busy_timeout", "5000")
	db, err := sql.Open("sqlite", fileURI(filepath.Clean(path), q))
	if err != nil {
		return Info{}, fmt.Errorf("inspect database: %w", err)
	}
	defer db.Close()

	version, err := schemaVersion(ctx, db)
	if err != nil {
		return Info{}, fmt.Errorf("inspect database: %w", err)
	}
	info := Info{SchemaVersion: version, Meta: map[string]string{}}
	if ok, err := tableExists(ctx, db, "meta"); err != nil || !ok {
		return info, err
	}
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM meta")
	if err != nil {
		return Info{}, fmt.Errorf("inspect database: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Info{}, fmt.Errorf("inspect database: %w", err)
		}
		info.Meta[k] = v
	}
	if err := rows.Err(); err != nil {
		return Info{}, fmt.Errorf("inspect database: %w", err)
	}
	return info, nil
}

// schemaVersion reads the version from goose's table like goose does: the
// newest applied row per version wins; a missing table means version 0.
func schemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	if ok, err := tableExists(ctx, db, "goose_db_version"); err != nil || !ok {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, "SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	seen := map[int64]bool{}
	for rows.Next() {
		var v int64
		var applied bool
		if err := rows.Scan(&v, &applied); err != nil {
			return 0, err
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		if applied && v > 0 {
			return v, rows.Err()
		}
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	return 0, nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("inspect database: %w", err)
	}
	return n > 0, nil
}
