// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/domain/id"
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
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
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
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
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
	assert.Equal(t, "4", c.Value.String())
	assert.Equal(t, "1", c.Step.String())
	require.NoError(t, c.Validate())
}

// TestCounterDecimalMigration covers counters-and-quotes.md B5 and B8:
// whole values and steps of counters keep their digits as decimals, and
// the way down cuts decimal places off.
func TestCounterDecimalMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
	require.NoError(t, err)

	const beforeDecimals = 10
	_, err = p.DownTo(ctx, beforeDecimals)
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, `INSERT INTO counters (id, name, value, reset_on_start, created_at, updated_at, step) VALUES
		('0190a5e0-0000-7000-8000-000000000001', 'deaths', -9223372036854775808, 1, 0, 0, 3)`)
	require.NoError(t, err)
	_, err = p.Up(ctx)
	require.NoError(t, err)
	c, err := s.Counter(ctx, "deaths")
	require.NoError(t, err)
	assert.Equal(t, "-9223372036854775808", c.Value.String())
	assert.Equal(t, "3", c.Step.String())
	assert.True(t, c.ResetOnStart)

	_, err = s.write.ExecContext(ctx, `UPDATE counters SET value = '2.75', step = '0.5'`)
	require.NoError(t, err)
	_, err = p.DownTo(ctx, beforeDecimals)
	require.NoError(t, err)
	var value, step int64
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT value, step FROM counters").Scan(&value, &step))
	assert.Equal(t, int64(2), value)
	assert.Equal(t, int64(1), step, "a step below 1 becomes 1")
}

// TestTriggerModeMigration covers commands.md B11, B13 and B14: chat
// commands from before the trigger mode keep their behavior, triggers get
// their keys in exact spelling, and the way down restores the flag.
func TestTriggerModeMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
	require.NoError(t, err)

	const beforeMode = 8
	_, err = p.DownTo(ctx, beforeMode)
	require.NoError(t, err)
	hug, what, follow := id.New(), id.New(), id.New()
	_, err = s.write.ExecContext(ctx, `INSERT INTO commands
		(id, name, kind, enabled, unlocked, wildcard, event_type, requirements, actions, created_at, updated_at) VALUES
		(?, 'hug', 'chat', 1, 0, 0, NULL, '[]', '[]', 0, 0),
		(?, 'what', 'chat', 1, 0, 1, NULL, '[]', '[]', 0, 0),
		(?, 'follow', 'event', 1, 0, 0, 'channel.follow', '[]', '[]', 0, 0)`,
		hug.String(), what.String(), follow.String())
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, `INSERT INTO command_triggers (command_id, position, trigger_text, trigger_key, active) VALUES
		(?, 0, 'Hug', 'hug', 1), (?, 1, 'umarmen', 'umarmen', 1), (?, 0, 'What', 'what', 1)`,
		hug.String(), hug.String(), what.String())
	require.NoError(t, err)
	_, err = p.Up(ctx)
	require.NoError(t, err)

	for cmdID, want := range map[id.ID]command.TriggerMode{
		hug: command.TriggerExclamation, what: command.TriggerWildcard, follow: "",
	} {
		rec, err := s.Command(ctx, cmdID)
		require.NoError(t, err)
		assert.Equal(t, want, rec.TriggerMode, rec.Name)
	}
	keys := func() map[string]int64 {
		rows, err := s.write.QueryContext(ctx, "SELECT trigger_key, wildcard FROM command_triggers")
		require.NoError(t, err)
		defer rows.Close()
		got := map[string]int64{}
		for rows.Next() {
			var (
				key      string
				wildcard int64
			)
			require.NoError(t, rows.Scan(&key, &wildcard))
			got[key] = wildcard
		}
		require.NoError(t, rows.Err())
		return got
	}
	assert.Equal(t, map[string]int64{"!Hug": 0, "!umarmen": 0, "what": 1}, keys())

	_, err = p.DownTo(ctx, beforeMode)
	require.NoError(t, err)
	var wildcards []int64
	require.NoError(t, func() error {
		rows, err := s.write.QueryContext(ctx, "SELECT wildcard FROM commands ORDER BY name")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w int64
			if err := rows.Scan(&w); err != nil {
				return err
			}
			wildcards = append(wildcards, w)
		}
		return rows.Err()
	}())
	assert.Equal(t, []int64{0, 0, 1}, wildcards, "follow, hug, what")
	rows, err := s.write.QueryContext(ctx, "SELECT trigger_key FROM command_triggers ORDER BY trigger_key")
	require.NoError(t, err)
	defer rows.Close()
	var oldKeys []string
	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		oldKeys = append(oldKeys, key)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"hug", "umarmen", "what"}, oldKeys)
}

// TestFineRolesMigration covers users-and-roles.md, B20: the stored roles
// of platform accounts move to the levels of their platform, and back.
func TestFineRolesMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
	require.NoError(t, err)

	const beforeRoles = 9
	_, err = p.DownTo(ctx, beforeRoles)
	require.NoError(t, err)
	ada := id.New()
	_, err = s.write.ExecContext(ctx, `INSERT INTO users (id, title, notes, excluded, regular, created_at, updated_at)
		VALUES (?, '', '', 0, 0, 0, 0)`, ada.String())
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, `INSERT INTO user_stats (user_id, watch_minutes, messages, commands_run, mentions,
		streams_watched, donated_cents, strikes) VALUES (?, 0, 0, 0, 0, 0, 0, 0)`, ada.String())
	require.NoError(t, err)
	for _, ident := range []struct{ platform, id, roles string }{
		{"twitch", "t1", `["user","creator","follower","vip","subscriber","platform_staff","moderator"]`},
		{"youtube", "y1", `["follower","subscriber"]`},
		{"kick", "k1", `["vip","editor"]`},
		{"twitch", "t2", `[]`},
	} {
		_, err = s.write.ExecContext(ctx, `INSERT INTO user_identities (platform, platform_user_id, user_id, login,
			display_name, color, avatar_url, roles, sub_tier, created_at, updated_at)
			VALUES (?, ?, ?, ?, '', '', '', ?, 0, 0, 0)`, ident.platform, ident.id, ada.String(), ident.id, ident.roles)
		require.NoError(t, err)
	}
	_, err = p.Up(ctx)
	require.NoError(t, err)

	u, err := s.User(ctx, ada)
	require.NoError(t, err)
	got := map[string]string{}
	for _, i := range u.Identities {
		got[i.PlatformUserID] = i.Roles.String()
	}
	assert.Equal(t, map[string]string{
		"t1": "user,twitch_affiliate,follower,twitch_vip,subscriber,twitch_global_mod,moderator",
		"y1": "youtube_subscriber,youtube_member",
		"k1": "kick_vip,editor",
		"t2": "",
	}, got)

	_, err = s.write.ExecContext(ctx, `UPDATE user_identities SET roles = '["twitch_partner","follower","youtube_subscriber","kick_og","vpzone_ambassador","twitch_staff"]'
		WHERE platform_user_id = 't2'`)
	require.NoError(t, err)
	_, err = p.DownTo(ctx, beforeRoles)
	require.NoError(t, err)
	var back string
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT roles FROM user_identities WHERE platform_user_id = 't2'").Scan(&back))
	assert.JSONEq(t, `["creator","follower","vip","platform_staff"]`, back, "the way down merges the levels")
}

// TestCounterNameKeyMigration covers counters-and-quotes.md, B1: counters
// from before the name key are found regardless of case.
func TestCounterNameKeyMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
	require.NoError(t, err)

	const beforeKey = 11
	_, err = p.DownTo(ctx, beforeKey)
	require.NoError(t, err)
	_, err = s.write.ExecContext(ctx, `INSERT INTO counters (id, name, value, reset_on_start, created_at, updated_at, step) VALUES
		('0190a5e0-0000-7000-8000-000000000001', 'Deaths', '4', 0, 0, 0, '1')`)
	require.NoError(t, err)
	_, err = p.Up(ctx)
	require.NoError(t, err)
	c, err := s.Counter(ctx, "DEATHS")
	require.NoError(t, err)
	assert.Equal(t, "Deaths", c.Name)
	assert.Equal(t, "4", c.Value.String())
	_, err = s.CreateCounter(ctx, counter.New("deaths"))
	require.Error(t, err, "the key is unique")
	_, err = p.DownTo(ctx, beforeKey)
	require.NoError(t, err)
}

// TestCommandNameKeyMigration covers commands.md, B7: stored commands get
// their name key; of names that differ in case only the oldest stays, the
// others get the first free suffix, and the key is unique afterwards.
func TestCommandNameKeyMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	defer s.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	require.NoError(t, err)
	p, err := newProvider(s.write, fsys, goMigrations(), s.logger)
	require.NoError(t, err)

	const beforeKeys = 12
	_, err = p.DownTo(ctx, beforeKeys)
	require.NoError(t, err)
	for i, name := range []string{"hug", "HUG", "hug (2)", "Ärger", "ärger", "Hug"} {
		_, err = s.write.ExecContext(ctx, `INSERT INTO commands (id, name, kind, enabled, unlocked, trigger_mode, requirements, actions, created_at, updated_at)
			VALUES (?, ?, 'timer', 1, 0, NULL, '[]', '[]', ?, 0)`, id.New().String(), name, i)
		require.NoError(t, err)
	}
	_, err = p.Up(ctx)
	require.NoError(t, err)

	recs, err := s.Commands(ctx)
	require.NoError(t, err)
	var names []string
	for _, rec := range recs {
		names = append(names, rec.Name)
	}
	assert.ElementsMatch(t, []string{"hug", "HUG (3)", "hug (2)", "Ärger", "ärger (2)", "Hug (4)"}, names)
	_, err = s.write.ExecContext(ctx, `INSERT INTO commands (id, name, name_key, kind, enabled, unlocked, requirements, actions, created_at, updated_at)
		VALUES (?, 'ÄRGER', 'ärger', 'timer', 1, 0, '[]', '[]', 0, 0)`, id.New().String())
	require.Error(t, err, "the key is unique")

	_, err = p.DownTo(ctx, beforeKeys)
	require.NoError(t, err)
	var columns int
	require.NoError(t, s.write.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('commands') WHERE name = 'name_key'").Scan(&columns))
	assert.Zero(t, columns, "the way down drops the column")
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

	s, err := open(ctx, path, v1, nil, hook)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Empty(t, calls, "not for a new database")

	s, err = open(ctx, path, v2, nil, hook)
	require.NoError(t, err)
	assert.Equal(t, int64(2), s.SchemaVersion())
	require.NoError(t, s.Close())
	assert.Equal(t, []call{{1, 2}}, calls)

	s, err = open(ctx, path, v2, nil, hook)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	assert.Len(t, calls, 1, "not when nothing is migrated")
}

func TestBeforeMigrateErrorAborts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "p.db")
	v1, v2 := twoMigrations()
	s, err := open(ctx, path, v1, nil)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = open(ctx, path, v2, nil, WithBeforeMigrate(func(context.Context, *Store, int64, int64) error {
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
	s, err := open(ctx, path, v2, nil)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	_, err = open(ctx, path, v1, nil)
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
