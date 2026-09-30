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
	}
	require.NoError(t, settings.Save(ctx, svc, cmds))
	gotCmds, err := settings.Load(ctx, svc, settings.DefaultCommands())
	require.NoError(t, err)
	assert.Equal(t, cmds, gotCmds)
	doc, _, err = s.Settings(ctx, "commands")
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"commands","schemaVersion":1,"lockMode":"visual_audio","errorCooldown":"global","errorCooldownDuration":"1m0s","argDelimiter":";"}`, string(doc))
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
