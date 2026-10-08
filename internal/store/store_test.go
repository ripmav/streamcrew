// SPDX-License-Identifier: MIT

package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/store/sqlcgen"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "profiles", "default.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	return s
}

func TestOpenCreatesPrivateFile(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	assert.Equal(t, store.LatestVersion(), s.SchemaVersion())
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	info, err := os.Stat(s.Path())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(s.Path()))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dir.Mode().Perm())
}

func TestMeta(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	_, err := s.Meta(ctx, "profile.name")
	require.ErrorIs(t, err, store.ErrNotFound)

	require.NoError(t, s.SetMeta(ctx, "profile.name", "Main"))
	require.NoError(t, s.SetMeta(ctx, "profile.name", "Main channel"))
	v, err := s.Meta(ctx, "profile.name")
	require.NoError(t, err)
	assert.Equal(t, "Main channel", v)
}

func TestWriteRollsBackOnError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	err := s.Write(ctx, func(q *sqlcgen.Queries) error {
		require.NoError(t, q.SetMeta(ctx, sqlcgen.SetMetaParams{Key: "k", Value: "v"}))
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	_, err = s.Meta(ctx, "k")
	require.ErrorIs(t, err, store.ErrNotFound, "rolled back")
}

func TestReadersSeeCommittedDataDuringWrite(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	require.NoError(t, s.SetMeta(ctx, "k", "committed"))

	inTx := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.Write(ctx, func(q *sqlcgen.Queries) error {
			if err := q.SetMeta(ctx, sqlcgen.SetMetaParams{Key: "k", Value: "uncommitted"}); err != nil {
				return err
			}
			close(inTx)
			<-release
			return nil
		})
	}()
	<-inTx

	// WAL: the reader pool is not blocked by the open write transaction and
	// does not see its changes.
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	v, err := s.Meta(readCtx, "k")
	require.NoError(t, err)
	assert.Equal(t, "committed", v)

	close(release)
	require.NoError(t, <-done)
	v, err = s.Meta(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "uncommitted", v)
}

func TestVacuumIntoAndInspect(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	require.NoError(t, s.SetMeta(ctx, "profile.name", "Main"))

	copyPath := filepath.Join(t.TempDir(), "copy.db")
	require.NoError(t, s.VacuumInto(ctx, copyPath))
	require.Error(t, s.VacuumInto(ctx, copyPath), "the target must not exist")

	info, err := store.Inspect(ctx, copyPath)
	require.NoError(t, err)
	assert.Equal(t, store.LatestVersion(), info.SchemaVersion)
	assert.Equal(t, map[string]string{"profile.name": "Main"}, info.Meta)

	_, err = store.Inspect(ctx, filepath.Join(t.TempDir(), "missing.db"))
	require.Error(t, err)
}

func TestInspectEmptyDatabase(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.db")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	info, err := store.Inspect(t.Context(), path)
	require.NoError(t, err)
	assert.Zero(t, info.SchemaVersion)
	assert.Empty(t, info.Meta)
}

func TestSettingsDocuments(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	_, found, err := s.Settings(ctx, "backups")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, s.PutSettings(ctx, "backups", []byte(`{"type":"backups","schemaVersion":1}`)))
	require.NoError(t, s.PutSettings(ctx, "backups", []byte(`{"type":"backups","schemaVersion":1,"enabled":false}`)))
	doc, found, err := s.Settings(ctx, "backups")
	require.NoError(t, err)
	assert.True(t, found)
	assert.JSONEq(t, `{"type":"backups","schemaVersion":1,"enabled":false}`, string(doc))
}

func TestReadOnlyBackupWhileOpen(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	require.NoError(t, s.SetMeta(ctx, "profile.name", "Main"))

	ro, err := store.OpenReadOnly(ctx, s.Path())
	require.NoError(t, err)
	defer ro.Close()
	assert.Equal(t, store.LatestVersion(), ro.SchemaVersion())
	assert.Equal(t, "Main", ro.Meta("profile.name"))

	dest := filepath.Join(t.TempDir(), "copy.db")
	require.NoError(t, ro.VacuumInto(ctx, dest), "works while the writer is open")
	require.Error(t, ro.VacuumInto(ctx, dest))
	info, err := store.Inspect(ctx, dest)
	require.NoError(t, err)
	assert.Equal(t, "Main", info.Meta["profile.name"])

	_, err = store.OpenReadOnly(ctx, filepath.Join(t.TempDir(), "missing.db"))
	require.Error(t, err)
}

// TestReadOnlyVacuumIntoReportsStatErrors is a regression test for the
// review of PR #24: a target that cannot be checked is not reported as
// existing.
func TestReadOnlyVacuumIntoReportsStatErrors(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	ro, err := store.OpenReadOnly(ctx, s.Path())
	require.NoError(t, err)
	defer ro.Close()

	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	err = ro.VacuumInto(ctx, filepath.Join(file, "copy.db"))
	require.Error(t, err, "the parent is a file")
	assert.NotContains(t, err.Error(), "target exists")
}

// TestAtomically covers the addendum of 2026-10-03 to Code-ADR-0008:
// several writes succeed or fail together, and reads in the transaction
// see the writes before them.
func TestAtomically(t *testing.T) {
	t.Parallel()
	s := openStore(t)
	ctx := t.Context()
	fun := command.Group{ID: id.New(), Name: "Fun"}
	slow := command.CooldownGroup{ID: id.New(), Name: "Slow", Duration: time.Minute}

	err := s.Atomically(ctx, func(tx *store.Store) error {
		require.NoError(t, tx.PutGroup(ctx, fun))
		groups, err := tx.Groups(ctx)
		require.NoError(t, err)
		assert.Len(t, groups, 1, "the transaction sees its own write")
		return tx.Atomically(ctx, func(inner *store.Store) error {
			return inner.PutCooldownGroup(ctx, slow)
		})
	})
	require.NoError(t, err)
	groups, err := s.Groups(ctx)
	require.NoError(t, err)
	assert.Len(t, groups, 1, "committed")
	cooldowns, err := s.CooldownGroups(ctx)
	require.NoError(t, err)
	assert.Len(t, cooldowns, 1, "the nested call is part of it")

	stop := errors.New("stop")
	err = s.Atomically(ctx, func(tx *store.Store) error {
		require.NoError(t, tx.PutGroup(ctx, command.Group{ID: id.New(), Name: "Other"}))
		err := tx.PutCooldownGroup(ctx, command.CooldownGroup{ID: id.New(), Name: "slow", Duration: time.Second})
		require.ErrorIs(t, err, store.ErrConflict, "a conflict inside the transaction")
		require.Error(t, tx.Close())
		require.Error(t, tx.VacuumInto(ctx, filepath.Join(t.TempDir(), "copy.db")))
		return stop
	})
	require.ErrorIs(t, err, stop)
	groups, err = s.Groups(ctx)
	require.NoError(t, err)
	assert.Len(t, groups, 1, "rolled back: Other is gone")
}
