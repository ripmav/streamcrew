// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
)

// TestRuntimeFileCoversStartAndStop covers the runtime file (ADR-0005): the
// core writes the address, PID and version into the data directory once the
// HTTP server listens, and removes the file when it stops.
func TestRuntimeFileCoversStartAndStop(t *testing.T) {
	t.Parallel()
	var console bytes.Buffer
	cfg := testConfig(t)
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errRun := make(chan error, 1)
	go func() { errRun <- a.Run(ctx) }()
	select {
	case <-a.HTTPServer().Listening():
	case err := <-errRun:
		t.Fatalf("Run returned early: %v", err)
	}

	path := cfg.RuntimePath()
	deadline := time.Now().Add(10 * time.Second)
	var info app.RuntimeInfo
	var readErr error
	for {
		var data []byte
		data, readErr = os.ReadFile(path)
		if readErr == nil && json.Unmarshal(data, &info) == nil {
			break
		}
		// The file is written in one go, but the read can catch the write
		// between the truncate and the fill: wait for the next attempt.
		if readErr != nil {
			require.ErrorIs(t, readErr, os.ErrNotExist, "the runtime file must appear after listening")
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime file did not appear: %v", readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, mustServerAddr(t, a), info.Address)
	assert.Equal(t, os.Getpid(), info.PID)
	assert.Equal(t, "daemon", info.Mode)
	assert.NotEmpty(t, info.Version)
	assert.NotEmpty(t, info.Profile)
	assert.False(t, info.StartedAt.IsZero())

	cancel()
	require.NoError(t, <-errRun)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "the runtime file is removed on shutdown")
}

// TestDesktopTokenIsCreatedOnce covers the desktop token (ADR-0005): in
// desktop mode the core creates an admin token in the data directory, the
// file is 0600, its value validates against the token store, and a second
// start keeps the existing file.
func TestDesktopTokenIsCreatedOnce(t *testing.T) {
	t.Parallel()
	var console bytes.Buffer
	cfg := testConfig(t)
	cfg.Mode = config.ModeDesktop

	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	require.NoError(t, a.Close())

	path := cfg.DesktopTokenPath()
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	value := bytes.TrimSpace(mustRead(t, path))
	assert.NotEmpty(t, value)

	// A second core with the same data directory keeps the file.
	a2, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	require.NoError(t, a2.Close())
	assert.Equal(t, value, bytes.TrimSpace(mustRead(t, path)))
}

// TestLocalTokenStore covers the local admin token of daemon mode
// (ADR-0013 §5): a deleted token store is not a revocation, the core
// re-registers the value of api.token (Nachprüfung 2026-10-25, N2a); a
// non-empty store that lacks the token keeps it revoked (N2).
func TestLocalTokenStore(t *testing.T) {
	t.Parallel()
	var console bytes.Buffer
	cfg := testConfig(t)

	newCore := func() {
		t.Helper()
		a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
		require.NoError(t, err)
		require.NoError(t, a.Close())
	}
	newCore()
	value := string(bytes.TrimSpace(mustRead(t, cfg.APITokenPath())))
	_, ok := apitoken.Open(cfg.DataDir).Validate(value)
	require.True(t, ok, "the local token validates on the first start")

	// A deleted token store is not a revocation: the core re-registers
	// the value from the file, so the api is not left closed (N2a).
	require.NoError(t, os.Remove(filepath.Join(cfg.DataDir, apitoken.FileName)))
	newCore()
	assert.Equal(t, value, string(bytes.TrimSpace(mustRead(t, cfg.APITokenPath()))), "the file is untouched")
	_, ok = apitoken.Open(cfg.DataDir).Validate(value)
	assert.True(t, ok, "an empty store re-registers the local token")

	// A revoked token stays revoked across restarts, but only when the
	// store still holds other tokens; the file keeps the value (N2).
	store := apitoken.Open(cfg.DataDir)
	_, _, err := store.Create("other", []apitoken.Scope{apitoken.Admin})
	require.NoError(t, err)
	revoked, err := store.Revoke("admin")
	require.NoError(t, err)
	require.True(t, revoked)
	newCore()
	_, ok = apitoken.Open(cfg.DataDir).Validate(value)
	assert.False(t, ok, "a non-empty store keeps the token revoked")
	assert.Equal(t, value, string(bytes.TrimSpace(mustRead(t, cfg.APITokenPath()))), "the file keeps the value")
}

// mustRead reads a file and fails the test on error.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
