// SPDX-License-Identifier: MIT

package profile_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
)

func TestSlugAndValidID(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, want string }{
		{"Main Channel", "main-channel"},
		{"  Größe & Übung!  ", "groesse-uebung"},
		{"twitch.tv/ripmav", "twitch-tv-ripmav"},
		{"***", ""},
		{strings.Repeat("abc ", 20), "abc-abc-abc-abc-abc-abc-abc-abc"},
	}
	for _, tc := range tests {
		got := profile.Slug(tc.name)
		assert.Equal(t, tc.want, got, tc.name)
		if got != "" {
			assert.True(t, profile.ValidID(got), got)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a_b", "ä", strings.Repeat("a", 33), "a/b", ".."} {
		assert.False(t, profile.ValidID(bad), bad)
	}
}

func TestCreateListRenameDelete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := profile.NewManager(t.TempDir())

	list, err := m.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, list)

	before := time.Now().Truncate(time.Millisecond)
	main, err := m.Create(ctx, "Main Channel", "")
	require.NoError(t, err)
	assert.Equal(t, "main-channel", main.ID)
	assert.Equal(t, "Main Channel", main.Name)
	assert.WithinRange(t, main.CreatedAt, before, time.Now())
	assert.Equal(t, time.UTC, main.CreatedAt.Location())

	// Stored as Unix milliseconds like every time in the database
	// (Code-ADR-0009), not as RFC 3339 text.
	info, err := store.Inspect(ctx, main.Path)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatInt(main.CreatedAt.UnixMilli(), 10), info.Meta[profile.MetaCreatedAt])

	second, err := m.Create(ctx, "Main Channel", "")
	require.NoError(t, err)
	assert.Equal(t, "main-channel-2", second.ID, "a taken derived ID gets a number")

	_, err = m.Create(ctx, "Other", "main-channel")
	require.ErrorIs(t, err, profile.ErrExists)
	_, err = m.Create(ctx, "Other", "Bad ID")
	require.ErrorIs(t, err, profile.ErrInvalidID)
	_, err = m.Create(ctx, "  ", "")
	require.Error(t, err)

	require.NoError(t, m.Rename(ctx, "main-channel", "Hauptkanal"))
	got, err := m.Get(ctx, "main-channel")
	require.NoError(t, err)
	assert.Equal(t, "Hauptkanal", got.Name)
	assert.Equal(t, "main-channel", got.ID, "renaming keeps the ID")

	list, err = m.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, []string{"main-channel", "main-channel-2"}, []string{list[0].ID, list[1].ID})

	require.NoError(t, m.Delete("main-channel-2"))
	assert.NoFileExists(t, m.Path("main-channel-2"))
	require.ErrorIs(t, m.Delete("main-channel-2"), profile.ErrNotFound)
	_, err = m.Get(ctx, "main-channel-2")
	require.ErrorIs(t, err, profile.ErrNotFound)
}

func TestActiveAndResolve(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir := t.TempDir()
	m := profile.NewManager(dataDir)

	active, err := m.Active()
	require.NoError(t, err)
	assert.Equal(t, profile.DefaultID, active)

	p, err := m.Resolve(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, profile.DefaultID, p.ID, "the default profile is created on first use")
	assert.True(t, p.Active)
	assert.FileExists(t, filepath.Join(dataDir, "profiles", "default.db"))

	_, err = m.Resolve(ctx, "missing")
	require.ErrorIs(t, err, profile.ErrNotFound)
	_, err = m.Resolve(ctx, "Bad")
	require.ErrorIs(t, err, profile.ErrInvalidID)

	other, err := m.Create(ctx, "Other", "")
	require.NoError(t, err)
	require.ErrorIs(t, m.SetActive("missing"), profile.ErrNotFound)
	require.NoError(t, m.SetActive(other.ID))
	p, err = m.Resolve(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, "other", p.ID)
	p, err = m.Resolve(ctx, profile.DefaultID)
	require.NoError(t, err)
	assert.Equal(t, profile.DefaultID, p.ID, "an override wins over the active profile")
	assert.False(t, p.Active)

	require.Error(t, m.Delete("other"), "the active profile cannot be deleted")

	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "profiles", "active"), []byte("../evil\n"), 0o600))
	_, err = m.Active()
	require.ErrorIs(t, err, profile.ErrInvalidID)
}

func TestListIgnoresForeignFiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := profile.NewManager(t.TempDir())
	_, err := m.Create(ctx, "Main", "")
	require.NoError(t, err)
	for _, name := range []string{"notes.txt", "Bad Name.db", "main.db-wal"} {
		require.NoError(t, os.WriteFile(filepath.Join(m.Dir(), name), nil, 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(m.Dir(), "dir.db"), 0o700))

	list, err := m.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "main", list[0].ID)
}
