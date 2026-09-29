// SPDX-License-Identifier: Apache-2.0

package vault_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/logging"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/vault"
)

// fakeKeyring is an in-memory keyring; err makes it unavailable.
type fakeKeyring struct {
	entries map[string]string
	err     error
	setErr  error
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{entries: map[string]string{}} }

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.entries[service+"/"+user]
	if !ok {
		return "", vault.ErrKeyNotFound
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, user, value string) error {
	if f.err != nil {
		return f.err
	}
	if f.setErr != nil {
		return f.setErr
	}
	f.entries[service+"/"+user] = value
	return nil
}

func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(dir, "profiles", "main.db"))
	require.NoError(t, err)
	return s
}

func newVault(t *testing.T) (*vault.Vault, *store.Store, *vault.Keys, string) {
	t.Helper()
	dir := t.TempDir()
	s := openStore(t, dir)
	t.Cleanup(func() { _ = s.Close() })
	keys := vault.NewKeys(dir, "", newFakeKeyring(), nil)
	ks, err := keys.Load(t.Context())
	require.NoError(t, err)
	return vault.New(s, ks), s, keys, dir
}

func TestPutGetDelete(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	v, _, _, _ := newVault(t)

	require.NoError(t, v.Put(ctx, "twitch.streamer.access_token", "tok-123"))
	got, err := v.Get(ctx, "twitch.streamer.access_token")
	require.NoError(t, err)
	assert.Equal(t, "tok-123", got.Reveal())
	assert.Equal(t, logging.Redacted, got.String(), "a decrypted value stays masked")

	require.NoError(t, v.Put(ctx, "twitch.streamer.access_token", "tok-456"))
	got, err = v.Get(ctx, "twitch.streamer.access_token")
	require.NoError(t, err)
	assert.Equal(t, "tok-456", got.Reveal())

	require.NoError(t, v.Delete(ctx, "twitch.streamer.access_token"))
	require.NoError(t, v.Delete(ctx, "twitch.streamer.access_token"), "deleting twice is fine")
	_, err = v.Get(ctx, "twitch.streamer.access_token")
	require.ErrorIs(t, err, vault.ErrNotFound)
}

// TestNoPlaintextAtRest covers the exit criterion of roadmap phase 2: tokens
// never appear in plain text in the database or its backups.
func TestNoPlaintextAtRest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	v, s, _, dir := newVault(t)
	// A distinctive value to search for in the files.
	const marker = "value-9f8e7d6c5b4a3210"
	require.NoError(t, v.Put(ctx, "twitch.bot.refresh_token", marker))

	info, err := backup.Create(ctx, s, filepath.Join(dir, "backups"), backup.Request{ProfileID: "main"})
	require.NoError(t, err)

	// Database file plus WAL, as they are on disk while the core runs.
	for _, suffix := range []string{"", "-wal"} {
		data, err := os.ReadFile(s.Path() + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		require.NoError(t, err)
		assert.False(t, bytes.Contains(data, []byte(marker)), "plain token in %s", filepath.Base(s.Path()+suffix))
	}

	zr, err := zip.OpenReader(info.Path)
	require.NoError(t, err)
	defer zr.Close()
	for _, f := range zr.File {
		rc, err := f.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		assert.False(t, bytes.Contains(data, []byte(marker)), "plain token in backup entry %s", f.Name)
	}
}

// swapRepo returns another entry's record, as an attacker with write access
// to the database could.
type swapRepo struct {
	vault.Repository
	from string
}

func (r swapRepo) GetSecret(ctx context.Context, name string) (vault.Record, bool, error) {
	rec, found, err := r.Repository.GetSecret(ctx, r.from)
	rec.Name = name
	return rec, found, err
}

func TestCiphertextsCannotBeSwappedOrTampered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()
	keys := vault.NewKeys(dir, "", newFakeKeyring(), nil)
	ks, err := keys.Load(ctx)
	require.NoError(t, err)
	require.NoError(t, vault.New(s, ks).Put(ctx, "a", "value-a"))

	_, err = vault.New(swapRepo{Repository: s, from: "a"}, ks).Get(ctx, "b")
	require.ErrorContains(t, err, "decryption failed", "the name is authenticated")

	rec, _, err := s.GetSecret(ctx, "a")
	require.NoError(t, err)
	rec.Ciphertext[0] ^= 0xff
	require.NoError(t, s.PutSecret(ctx, rec))
	_, err = vault.New(s, ks).Get(ctx, "a")
	require.ErrorContains(t, err, "decryption failed")
}

func TestKeySources(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	t.Run("environment", func(t *testing.T) {
		t.Parallel()
		key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
		kr := newFakeKeyring()
		ks, err := vault.NewKeys(t.TempDir(), key, kr, nil).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, vault.SourceEnv, ks.Source())
		assert.Empty(t, kr.entries, "the keyring is not touched")

		_, err = vault.NewKeys(t.TempDir(), "too-short", kr, nil).Load(ctx)
		require.Error(t, err)
	})

	t.Run("keyring, created once", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		kr := newFakeKeyring()
		first, err := vault.NewKeys(dir, "", kr, nil).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, vault.SourceKeyring, first.Source())
		assert.Len(t, kr.entries, 1)
		assert.NoFileExists(t, filepath.Join(dir, vault.KeyFileName))

		again, err := vault.NewKeys(dir, "", kr, nil).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, first.Current, again.Current)
	})

	t.Run("no keyring: key file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var logs bytes.Buffer
		logger := slogText(&logs)
		kr := &fakeKeyring{err: errors.New("no secret service")}
		first, err := vault.NewKeys(dir, "", kr, logger).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, vault.SourceFile, first.Source())
		assert.Contains(t, logs.String(), "keyring not available")
		assertPrivate(t, filepath.Join(dir, vault.KeyFileName))

		again, err := vault.NewKeys(dir, "", nil, nil).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, first.Current, again.Current, "the file is found again")
	})

	t.Run("keyring rejects writes: key file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		kr := newFakeKeyring()
		kr.setErr = errors.New("locked")
		ks, err := vault.NewKeys(dir, "", kr, nil).Load(ctx)
		require.NoError(t, err)
		assert.Equal(t, vault.SourceFile, ks.Source())
	})

	t.Run("corrupt key file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, vault.KeyFileName), []byte(`{"current":"x","keys":{}}`), 0o600))
		_, err := vault.NewKeys(dir, "", nil, nil).Load(ctx)
		require.Error(t, err)
	})
}

func TestRotate(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	v, s, keys, _ := newVault(t)
	for name, value := range map[string]string{"a": "value-a", "b": "value-b"} {
		require.NoError(t, v.Put(ctx, name, logging.Secret(value)))
	}
	before, err := keys.Load(ctx)
	require.NoError(t, err)

	rotated, err := vault.Rotate(ctx, keys, before, s)
	require.NoError(t, err)

	after, err := keys.Load(ctx)
	require.NoError(t, err)
	assert.Equal(t, rotated.Current, after.Current)
	assert.NotEqual(t, before.Current, after.Current)
	assert.Equal(t, []string{after.Current}, after.IDs(), "the old key is gone")
	recs, err := s.ListSecrets(ctx)
	require.NoError(t, err)
	for _, rec := range recs {
		assert.Equal(t, after.Current, rec.KeyID)
	}
	reloaded := vault.New(s, after)
	for name, value := range map[string]string{"a": "value-a", "b": "value-b"} {
		got, err := reloaded.Get(ctx, name)
		require.NoError(t, err)
		assert.Equal(t, value, got.Reveal())
	}
}

// failingReplace makes the re-encryption fail, as a crash would.
type failingReplace struct{ vault.Repository }

func (failingReplace) ReplaceSecrets(context.Context, []vault.Record) error {
	return assert.AnError
}

func TestInterruptedRotationKeepsSecretsReadable(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	v, s, keys, _ := newVault(t)
	require.NoError(t, v.Put(ctx, "a", "value-a"))
	ks, err := keys.Load(ctx)
	require.NoError(t, err)

	_, err = vault.Rotate(ctx, keys, ks, failingReplace{s})
	require.ErrorIs(t, err, assert.AnError)

	after, err := keys.Load(ctx)
	require.NoError(t, err)
	assert.Len(t, after.IDs(), 2, "old and new key are both kept")
	got, err := vault.New(s, after).Get(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, "value-a", got.Reveal())
}

func TestRotateWithEnvironmentKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := openStore(t, dir)
	defer s.Close()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	keys := vault.NewKeys(dir, key, nil, nil)
	ks, err := keys.Load(t.Context())
	require.NoError(t, err)
	_, err = vault.Rotate(t.Context(), keys, ks, s)
	require.ErrorContains(t, err, "STREAMCREW_SECRET_KEY")
}

func assertPrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func slogText(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// TestRotateCoversAllProfiles: the key belongs to the data directory, so a
// rotation must re-encrypt every profile before the old key goes.
func TestRotateCoversAllProfiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	keys := vault.NewKeys(dir, "", newFakeKeyring(), nil)
	ks, err := keys.Load(ctx)
	require.NoError(t, err)

	var stores []*store.Store
	for _, id := range []string{"main", "second"} {
		s, err := store.Open(ctx, filepath.Join(dir, "profiles", id+".db"))
		require.NoError(t, err)
		defer s.Close()
		require.NoError(t, vault.New(s, ks).Put(ctx, "token", logging.Secret("value-"+id)))
		stores = append(stores, s)
	}

	rotated, err := vault.Rotate(ctx, keys, ks, stores[0], stores[1])
	require.NoError(t, err)
	for i, id := range []string{"main", "second"} {
		got, err := vault.New(stores[i], rotated).Get(ctx, "token")
		require.NoError(t, err)
		assert.Equal(t, "value-"+id, got.Reveal())
	}
}
