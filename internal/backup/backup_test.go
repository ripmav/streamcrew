// SPDX-License-Identifier: Apache-2.0

package backup_test

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/store"
)

func openProfile(t *testing.T, path, name string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, s.SetMeta(t.Context(), "profile.name", name))
	return s
}

// TestBackupRestoreRoundTrip covers the exit criterion of roadmap phase 2.
func TestBackupRestoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "profiles", "main.db")
	backups := filepath.Join(dir, "backups")

	s := openProfile(t, dbPath, "Main")
	info, err := backup.Create(ctx, s, backups, backup.Request{ProfileID: "main", ProfileName: "Main", AppVersion: "v0.1.0"})
	require.NoError(t, err)
	assert.Equal(t, backup.KindManual, info.Manifest.Kind)
	assert.Equal(t, store.LatestVersion(), info.Manifest.SchemaVersion)
	assert.Regexp(t, `main-\d{8}T\d{6}Z\.zip$`, info.Path)
	assert.Len(t, info.Manifest.SHA256, 64)

	// Change the profile after the backup, then restore.
	require.NoError(t, s.SetMeta(ctx, "profile.name", "Changed"))
	require.NoError(t, s.Close())

	m, err := backup.Restore(ctx, info.Path, dbPath, store.LatestVersion())
	require.NoError(t, err)
	assert.Equal(t, "main", m.ProfileID)

	restored, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer restored.Close()
	name, err := restored.Meta(ctx, "profile.name")
	require.NoError(t, err)
	assert.Equal(t, "Main", name, "the state of the backup is back")

	read, err := backup.Read(info.Path)
	require.NoError(t, err)
	assert.Equal(t, info.Manifest, read.Manifest)
}

func TestListNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	s := openProfile(t, filepath.Join(dir, "main.db"), "Main")
	defer s.Close()
	other := openProfile(t, filepath.Join(dir, "other.db"), "Other")
	defer other.Close()

	var created []string
	for _, kind := range []backup.Kind{backup.KindManual, backup.KindScheduled, backup.KindPreMigration} {
		info, err := backup.Create(ctx, s, filepath.Join(dir, "b"), backup.Request{ProfileID: "main", Kind: kind})
		require.NoError(t, err)
		created = append(created, info.Path)
	}
	_, err := backup.Create(ctx, other, filepath.Join(dir, "b"), backup.Request{ProfileID: "other"})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b", "main-garbage.zip"), []byte("not a zip"), 0o600))

	list, err := backup.List(filepath.Join(dir, "b"), "main")
	require.NoError(t, err)
	require.Len(t, list, 3, "only readable backups of this profile")
	assert.Equal(t, backup.KindPreMigration, list[0].Manifest.Kind)
	assert.Equal(t, backup.KindManual, list[2].Manifest.Kind)
	assert.ElementsMatch(t, created, []string{list[0].Path, list[1].Path, list[2].Path}, "same-second backups get distinct names")

	none, err := backup.List(filepath.Join(dir, "missing"), "main")
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestRestoreChecks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	s := openProfile(t, filepath.Join(dir, "main.db"), "Main")
	info, err := backup.Create(ctx, s, dir, backup.Request{ProfileID: "main"})
	require.NoError(t, err)
	require.NoError(t, s.Close())
	target := filepath.Join(dir, "restored.db")

	_, err = backup.Restore(ctx, info.Path, target, store.LatestVersion()-1)
	require.ErrorIs(t, err, backup.ErrSchemaTooNew)
	assert.NoFileExists(t, target)

	tampered := rewriteZip(t, info.Path, func(name string, data []byte) []byte {
		if name == "profile.db" {
			data[len(data)-1] ^= 0xff
		}
		return data
	})
	_, err = backup.Restore(ctx, tampered, target, store.LatestVersion())
	require.ErrorIs(t, err, backup.ErrCorrupt)
	assert.NoFileExists(t, target)
	matches, _ := filepath.Glob(filepath.Join(dir, ".restore-*"))
	assert.Empty(t, matches, "no temporary files left behind")

	future := rewriteZip(t, info.Path, func(name string, data []byte) []byte {
		if name == "manifest.json" {
			return []byte(`{"formatVersion": 99}`)
		}
		return data
	})
	_, err = backup.Restore(ctx, future, target, store.LatestVersion())
	require.ErrorIs(t, err, backup.ErrCorrupt)

	notZip := filepath.Join(dir, "x.zip")
	require.NoError(t, os.WriteFile(notZip, []byte("nope"), 0o600))
	_, err = backup.Restore(ctx, notZip, target, store.LatestVersion())
	require.ErrorIs(t, err, backup.ErrCorrupt)
}

// rewriteZip copies a ZIP file and lets change modify each entry.
func rewriteZip(t *testing.T, path string, change func(name string, data []byte) []byte) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer zr.Close()
	out := filepath.Join(t.TempDir(), "changed.zip")
	f, err := os.Create(out)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for _, e := range zr.File {
		rc, err := e.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		w, err := zw.Create(e.Name)
		require.NoError(t, err)
		_, err = w.Write(change(e.Name, data))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return out
}

func info(kind backup.Kind, created string) backup.Info {
	ts, err := time.Parse(time.RFC3339, created)
	if err != nil {
		panic(err)
	}
	return backup.Info{Path: created + string(kind), Manifest: backup.Manifest{Kind: kind, CreatedAt: ts}}
}

func TestExpired(t *testing.T) {
	t.Parallel()
	// Newest first, as List returns them.
	backups := []backup.Info{
		info(backup.KindScheduled, "2026-09-29T04:00:00Z"), // Tue, week 40
		info(backup.KindScheduled, "2026-09-28T04:00:00Z"), // Mon, week 40
		info(backup.KindScheduled, "2026-09-27T04:00:00Z"), // Sun, week 39
		info(backup.KindManual, "2026-09-26T12:00:00Z"),
		info(backup.KindScheduled, "2026-09-26T04:00:00Z"), // Sat, week 39
		info(backup.KindScheduled, "2026-09-20T04:00:00Z"), // Sun, week 38
		info(backup.KindScheduled, "2026-08-31T04:00:00Z"), // August
		info(backup.KindScheduled, "2026-07-15T04:00:00Z"), // July
	}
	expired := backup.Expired(backups, backup.Policy{Daily: 2, Weekly: 2, Monthly: 2}, time.UTC)
	var paths []string
	for _, e := range expired {
		paths = append(paths, e.Path)
	}
	// Kept: 29th, 28th (daily); week 40 → 29th, week 39 → 27th (weekly);
	// September → 29th, August → 31st (monthly). The manual one is never
	// removed.
	assert.Equal(t, []string{
		"2026-09-26T04:00:00Zscheduled",
		"2026-09-20T04:00:00Zscheduled",
		"2026-07-15T04:00:00Zscheduled",
	}, paths)

	assert.Len(t, backup.Expired(backups, backup.Policy{}, time.UTC), 7, "keep nothing automatic")
}

type fakeSource struct{ schema int64 }

func (f fakeSource) VacuumInto(_ context.Context, dest string) error {
	return os.WriteFile(dest, []byte("SQLite format 3\x00"), 0o600)
}
func (f fakeSource) SchemaVersion() int64 { return f.schema }

func TestSchedulerDailyWithCatchUpAndRetention(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// The bubble starts at 2000-01-01 00:00 UTC.
		dir := t.TempDir()
		sch := backup.Schedule{Enabled: true, Hour: 4, Policy: backup.Policy{Daily: 3}, Location: time.UTC}
		s := backup.NewScheduler(fakeSource{schema: 1}, dir, backup.Request{ProfileID: "main"},
			func(context.Context) (backup.Schedule, error) { return sch, nil }, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()

		time.Sleep(3 * time.Hour)
		synctest.Wait()
		list, err := backup.List(dir, "main")
		require.NoError(t, err)
		assert.Empty(t, list, "not before 04:00")

		time.Sleep(time.Hour + time.Minute)
		synctest.Wait()
		list, err = backup.List(dir, "main")
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, backup.KindScheduled, list[0].Manifest.Kind)
		assert.Equal(t, time.Date(2000, 1, 1, 4, 0, 0, 0, time.UTC), list[0].Manifest.CreatedAt)

		time.Sleep(5 * 24 * time.Hour)
		synctest.Wait()
		list, err = backup.List(dir, "main")
		require.NoError(t, err)
		assert.Len(t, list, 3, "one per day, three kept")

		cancel()
		require.NoError(t, <-done)
	})
}

func TestSchedulerCatchesUpAfterDowntime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(10 * time.Hour) // the core starts at 10:00, after the 04:00 slot
		dir := t.TempDir()
		sch := backup.Schedule{Enabled: true, Hour: 4, Policy: backup.Policy{Daily: 7}, Location: time.UTC}
		s := backup.NewScheduler(fakeSource{schema: 1}, dir, backup.Request{ProfileID: "main"},
			func(context.Context) (backup.Schedule, error) { return sch, nil }, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		synctest.Wait()

		list, err := backup.List(dir, "main")
		require.NoError(t, err)
		assert.Len(t, list, 1, "the missed backup is made at once")
		cancel()
		require.NoError(t, <-done)
	})
}

func TestSchedulerDisabled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		s := backup.NewScheduler(fakeSource{}, dir, backup.Request{ProfileID: "main"},
			func(context.Context) (backup.Schedule, error) {
				return backup.Schedule{Enabled: false, Location: time.UTC}, nil
			}, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		time.Sleep(48 * time.Hour)
		synctest.Wait()
		list, err := backup.List(dir, "main")
		require.NoError(t, err)
		assert.Empty(t, list)
		cancel()
		require.NoError(t, <-done)
	})
}
