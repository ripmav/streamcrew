// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationsUpDownUp covers the exit criterion of roadmap phase 2: every
// migration runs forward and backward.
func TestMigrationsUpDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	require.Equal(t, LatestVersion(), s.SchemaVersion())

	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := goose.NewProvider(goose.DialectSQLite3, s.write, fsys, goose.WithDisableGlobalRegistry(true))
	require.NoError(t, err)

	_, err = p.DownTo(ctx, 0)
	require.NoError(t, err)
	var tables int
	require.NoError(t, s.read.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'goose%' AND name NOT LIKE 'sqlite%'").Scan(&tables))
	assert.Zero(t, tables, "down removes every table")

	_, err = p.Up(ctx)
	require.NoError(t, err)
	v, err := p.GetDBVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, LatestVersion(), v)
}

// TestCounterStepMigration covers counters-and-quotes.md B8: counters from
// before the step have the step 1.
func TestCounterStepMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := goose.NewProvider(goose.DialectSQLite3, s.write, fsys, goose.WithDisableGlobalRegistry(true))
	require.NoError(t, err)

	const beforeStep = 5
	_, err = p.DownTo(ctx, beforeStep)
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, `INSERT INTO counters (id, name, value, reset_on_start, created_at, updated_at)
		VALUES ('0190a5e0-0000-7000-8000-000000000001', 'deaths', 4, 0, 0, 0)`)
	require.NoError(t, err)
	_, err = p.Up(ctx)
	require.NoError(t, err)

	c, err := s.Counter(ctx, "deaths")
	require.NoError(t, err)
	assert.Equal(t, int64(4), c.Value)
	assert.Equal(t, int64(1), c.Step)
	require.NoError(t, c.Validate())
}

func TestLatestVersionMatchesFiles(t *testing.T) {
	t.Parallel()
	versions := knownVersions()
	require.NotEmpty(t, versions)
	for i, v := range versions {
		assert.Equal(t, int64(i+1), v, "migrations are numbered without gaps")
	}
	assert.Equal(t, versions[len(versions)-1], LatestVersion())
}

func twoMigrations() (v1, v2 fstest.MapFS) {
	first := &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE a (x INTEGER) STRICT;\n-- +goose Down\nDROP TABLE a;\n")}
	second := &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE b (y INTEGER) STRICT;\n-- +goose Down\nDROP TABLE b;\n")}
	return fstest.MapFS{"0001_a.sql": first}, fstest.MapFS{"0001_a.sql": first, "0002_b.sql": second}
}

func TestBeforeMigrateRunsForExistingDatabases(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "p.db")
	v1, v2 := twoMigrations()

	type call struct{ from, to int64 }
	var calls []call
	hook := WithBeforeMigrate(func(_ context.Context, s *Store, from, to int64) error {
		calls = append(calls, call{from, to})
		assert.Equal(t, from, s.SchemaVersion(), "backups made by the hook carry the old version")
		// The database is still at the old version while the hook runs.
		var n int
		require.NoError(t, s.read.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'b'").Scan(&n))
		assert.Zero(t, n)
		return nil
	})

	s, err := open(ctx, path, v1, hook)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Empty(t, calls, "not for a new database")

	s, err = open(ctx, path, v2, hook)
	require.NoError(t, err)
	assert.Equal(t, int64(2), s.SchemaVersion())
	require.NoError(t, s.Close())
	assert.Equal(t, []call{{1, 2}}, calls)

	s, err = open(ctx, path, v2, hook)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Len(t, calls, 1, "not when nothing is migrated")
}

func TestBeforeMigrateErrorAborts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "p.db")
	v1, v2 := twoMigrations()
	s, err := open(ctx, path, v1)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = open(ctx, path, v2, WithBeforeMigrate(func(context.Context, *Store, int64, int64) error {
		return assert.AnError
	}))
	require.ErrorIs(t, err, assert.AnError)
	info, err := Inspect(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, int64(1), info.SchemaVersion, "not migrated")
}

func TestNewerSchemaIsRejected(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "p.db")
	v1, v2 := twoMigrations()
	s, err := open(ctx, path, v2)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = open(ctx, path, v1)
	require.ErrorIs(t, err, ErrSchemaTooNew)

	info, err := Inspect(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, int64(2), info.SchemaVersion, "Inspect reads newer databases")
}

func TestTranslateConstraintErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()

	_, err = s.write.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('k', 'v')")
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('k', 'w')")
	require.Error(t, err)
	require.ErrorIs(t, translate(err), ErrConflict)
	assert.Equal(t, assert.AnError, translate(assert.AnError), "other errors pass through")
}

func TestFileURI(t *testing.T) {
	t.Parallel()
	got := fileURI("/data/my profile?#.db", nil)
	assert.Equal(t, "file:/data/my%20profile%3F%23.db", got)
}
