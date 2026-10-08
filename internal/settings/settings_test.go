// SPDX-License-Identifier: MIT

package settings_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

func newService(t *testing.T) (*settings.Service, *store.Store) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "p.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	svc, err := settings.New(s)
	require.NoError(t, err)
	return svc, s
}

func TestDefaultsWhenNeverSaved(t *testing.T) {
	t.Parallel()
	svc, _ := newService(t)
	b, err := settings.Load(t.Context(), svc, settings.DefaultBackups())
	require.NoError(t, err)
	assert.Equal(t, settings.Backups{Enabled: true, At: "04:00", KeepDaily: 7, KeepWeekly: 4, KeepMonthly: 12}, b)

	tz, err := settings.Load(t.Context(), svc, settings.DefaultTime())
	require.NoError(t, err)
	loc, err := tz.Location()
	require.NoError(t, err)
	assert.Equal(t, time.Local, loc, "empty means the system time zone")

	c, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, settings.Commands{
		LockMode:              settings.LockPerCommandType,
		ErrorCooldown:         settings.ErrorCooldownPerCommand,
		ErrorCooldownDuration: polydoc.Duration(10 * time.Second),
		ArgDelimiter:          "|",
		EntranceMediaGap:      polydoc.Duration(5 * time.Second),
	}, c, "B90")
}

func TestSaveAndLoad(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, s := newService(t)

	want := settings.Backups{Enabled: false, At: "23:30", KeepDaily: 3, KeepWeekly: 2, KeepMonthly: 1}
	require.NoError(t, settings.Save(ctx, svc, want))
	got, err := settings.Load(ctx, svc, settings.DefaultBackups())
	require.NoError(t, err)
	assert.Equal(t, want, got)

	doc, _, err := s.Settings(ctx, "backups")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"backups","schemaVersion":1,"enabled":false,"at":"23:30","keepDaily":3,"keepWeekly":2,"keepMonthly":1}`, string(doc))

	require.NoError(t, settings.Save(ctx, svc, settings.Time{TimeZone: "Europe/Berlin"}))
	tz, err := settings.Load(ctx, svc, settings.DefaultTime())
	require.NoError(t, err)
	loc, err := tz.Location()
	require.NoError(t, err)
	assert.Equal(t, "Europe/Berlin", loc.String())

	cmds := settings.Commands{
		LockMode:              settings.LockVisualAudio,
		ErrorCooldown:         settings.ErrorCooldownGlobal,
		ErrorCooldownDuration: polydoc.Duration(time.Minute),
		ArgDelimiter:          ";",
		EntranceMediaGap:      polydoc.Duration(1500 * time.Millisecond),
	}
	require.NoError(t, settings.Save(ctx, svc, cmds))
	gotCmds, err := settings.Load(ctx, svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, cmds, gotCmds)
	doc, _, err = s.Settings(ctx, "commands")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"commands","schemaVersion":2,"lockMode":"visual_audio","errorCooldown":"global","errorCooldownDuration":"1m0s","argDelimiter":";","entranceMediaGap":"1.5s"}`, string(doc))
}

func TestValidation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := newService(t)
	for _, s := range []settings.Section{
		settings.Backups{At: "25:00"},
		settings.Backups{At: "4 Uhr"},
		settings.Backups{At: "04:00", KeepDaily: -1},
		settings.Time{TimeZone: "Mars/Olympus"},
		withCommands(func(c *settings.Commands) { c.LockMode = "per_user" }),
		withCommands(func(c *settings.Commands) { c.LockMode = "" }),
		withCommands(func(c *settings.Commands) { c.ErrorCooldown = "sometimes" }),
		withCommands(func(c *settings.Commands) { c.ErrorCooldownDuration = -1 }),
		withCommands(func(c *settings.Commands) { c.ArgDelimiter = "" }),
		withCommands(func(c *settings.Commands) { c.ArgDelimiter = "\n" }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = 0 }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(999 * time.Millisecond) }),
		withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(time.Minute + time.Millisecond) }),
	} {
		assert.Error(t, settings.Save(ctx, svc, s), "%+v", s)
	}
}

func TestCommandsModes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := newService(t)
	for _, m := range []settings.LockMode{
		settings.LockPerCommandType, settings.LockPerActionType, settings.LockVisualAudio,
		settings.LockSingular, settings.LockNone,
	} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.LockMode = m })), m)
	}
	for _, m := range []settings.ErrorCooldown{
		settings.ErrorCooldownPerCommand, settings.ErrorCooldownGlobal, settings.ErrorCooldownOff,
	} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.ErrorCooldown = m })), m)
	}
	assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.ErrorCooldownDuration = 0 })),
		"0 holds back no message")
	for _, gap := range []time.Duration{settings.MinEntranceMediaGap, settings.MaxEntranceMediaGap} {
		assert.NoError(t, settings.Save(ctx, svc, withCommands(func(c *settings.Commands) { c.EntranceMediaGap = polydoc.Duration(gap) })),
			"B43: %s", gap)
	}
}

// TestCommandsVersion1: stored version 1 documents are migrated; they get
// the default gap of B43.
func TestCommandsVersion1(t *testing.T) {
	t.Parallel()
	v1 := `{"type":"commands","schemaVersion":1,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";"}`
	svc, err := settings.New(fakeRepo{doc: []byte(v1)})
	require.NoError(t, err)
	got, err := settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, settings.Commands{
		LockMode:              settings.LockSingular,
		ErrorCooldown:         settings.ErrorCooldownOff,
		ErrorCooldownDuration: 0,
		ArgDelimiter:          ";",
		EntranceMediaGap:      polydoc.Duration(settings.DefaultEntranceMediaGap),
	}, got)

	withGap := `{"type":"commands","schemaVersion":1,"lockMode":"singular","errorCooldown":"off","errorCooldownDuration":"0s","argDelimiter":";","entranceMediaGap":"2s"}`
	svc, err = settings.New(fakeRepo{doc: []byte(withGap)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultCommands())
	require.Error(t, err, "version 1 never had the gap")
}

// withCommands returns the default commands section changed by edit.
func withCommands(edit func(c *settings.Commands)) settings.Commands {
	c := settings.DefaultCommands()
	edit(&c)
	return c
}

type fakeRepo struct{ doc []byte }

func (f fakeRepo) Settings(context.Context, string) ([]byte, bool, error) { return f.doc, true, nil }
func (fakeRepo) PutSettings(context.Context, string, []byte) error        { return nil }

func TestNewerStoredVersionFallsBackToDefaults(t *testing.T) {
	t.Parallel()
	svc, err := settings.New(fakeRepo{doc: []byte(`{"type":"backups","schemaVersion":9,"future":true}`)})
	require.NoError(t, err)
	b, err := settings.Load(t.Context(), svc, settings.DefaultBackups())
	require.Error(t, err)
	assert.Equal(t, settings.DefaultBackups(), b)
}

func TestUnloadableTimeZoneFallsBackToUTC(t *testing.T) {
	t.Parallel()
	loc, err := settings.Time{TimeZone: "Mars/Olympus"}.Location()
	require.Error(t, err)
	assert.Equal(t, time.UTC, loc)
}

// TestTimeZoneSystem: the system time zone has its own name instead of an
// empty one (Code-ADR-0017), in version 2 of the section.
func TestTimeZoneSystem(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, s := newService(t)

	assert.Equal(t, settings.Time{TimeZone: settings.TimeZoneSystem}, settings.DefaultTime())
	require.NoError(t, settings.Save(ctx, svc, settings.DefaultTime()))
	doc, _, err := s.Settings(ctx, "time")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"time","schemaVersion":2,"timeZone":"system"}`, string(doc))
	loc, err := settings.DefaultTime().Location()
	require.NoError(t, err)
	assert.Equal(t, time.Local, loc)

	require.Error(t, settings.Save(ctx, svc, settings.Time{}), "an empty name is not the system zone")
	loc, err = settings.Time{}.Location()
	require.Error(t, err)
	assert.Equal(t, time.UTC, loc)
}

// TestTimeVersion1: stored version 1 documents are migrated; their empty
// name becomes "system".
func TestTimeVersion1(t *testing.T) {
	t.Parallel()
	for doc, want := range map[string]string{
		`{"type":"time","schemaVersion":1,"timeZone":""}`:              settings.TimeZoneSystem,
		`{"type":"time","schemaVersion":1,"timeZone":"Europe/Berlin"}`: "Europe/Berlin",
	} {
		svc, err := settings.New(fakeRepo{doc: []byte(doc)})
		require.NoError(t, err)
		got, err := settings.Load(t.Context(), svc, settings.DefaultTime())
		require.NoError(t, err, doc)
		assert.Equal(t, want, got.TimeZone, doc)
	}
	svc, err := settings.New(fakeRepo{doc: []byte(`{"type":"time","schemaVersion":1}`)})
	require.NoError(t, err)
	_, err = settings.Load(t.Context(), svc, settings.DefaultTime())
	require.Error(t, err, "version 1 always had the field")
}
