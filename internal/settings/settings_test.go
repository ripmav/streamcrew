// SPDX-License-Identifier: Apache-2.0

package settings_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	} {
		assert.Error(t, settings.Save(ctx, svc, s), "%+v", s)
	}
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
