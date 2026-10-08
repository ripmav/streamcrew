// SPDX-License-Identifier: MIT

package app_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/backup"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/counter"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/lockfile"
	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Config{
		DataDir:         filepath.Join(t.TempDir(), "data"),
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "json", File: true, MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	return cfg
}

func status(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode
}

func TestRunStartsReportsReadyAndStops(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- a.Run(ctx) }()

	select {
	case <-a.HTTPServer().Listening():
	case err := <-errc:
		t.Fatalf("Run returned early: %v", err)
	}
	base := "http://" + a.HTTPServer().Addr().String()
	assert.Equal(t, http.StatusOK, status(t, base+"/healthz"))
	require.Eventually(t, a.Ready, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, http.StatusOK, status(t, base+"/readyz"))

	cancel()
	require.NoError(t, <-errc)
	assert.False(t, a.Ready(), "not ready after shutdown")

	assert.Contains(t, console.String(), `"msg":"streamcrew starting"`)
	assert.Contains(t, console.String(), `"msg":"rights","capabilities":"host:fs, host:process, host:audio, net:outbound, script"`)
	assert.Contains(t, console.String(), `"msg":"streamcrew stopped"`)
	logFile, err := os.ReadFile(filepath.Join(cfg.LogDir(), "streamcrew.log"))
	require.NoError(t, err)
	assert.Contains(t, string(logFile), `"msg":"streamcrew stopped"`)
	assert.Contains(t, string(logFile), `"component":"http"`)

	info, err := os.Stat(cfg.DataDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

// TestRights covers Code-ADR-0019: the core has the rights of its mode and
// the configuration, and gives them to their users through App.Rights.
func TestRights(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Mode, cfg.Grant, cfg.OutboundAllow = config.ModeServer, []string{"host:fs"}, []string{"nas"}
	require.NoError(t, cfg.Resolve())
	a, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithConfigFile(config.NewFileResolver("")))
	require.NoError(t, err)
	defer func() { require.NoError(t, a.Close()) }()
	var src capability.Source = a.Rights()
	assert.Equal(t, []capability.Capability{capability.HostFS, capability.NetOutbound, capability.Script}, src.Current().List())
	assert.True(t, a.Rights().Outbound().HasHost("nas"))
}

func TestRunFailsWhenPortIsInUse(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	cfg := testConfig(t)
	cfg.Listen = busy.Addr().String()
	var console bytes.Buffer
	a, err := app.New(t.Context(), cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)

	err = a.Run(t.Context())
	require.Error(t, err)
	assert.ErrorContains(t, err, "listen on")
	assert.Contains(t, console.String(), "streamcrew stopped with an error")
}

func TestNewWithoutLogFile(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Log.File = false
	a, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	require.NoError(t, a.Close())
	require.NoError(t, a.Close(), "closing twice is harmless")
	assert.NoDirExists(t, cfg.LogDir())
}

func TestNewFailsWithoutDataDirectory(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	cfg.DataDir = filepath.Join(blocker, "data")

	_, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	assert.ErrorContains(t, err, "create data directory")
}

func TestDataDirectoryIsLocked(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	first, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	assert.Equal(t, profile.DefaultID, first.Profile().ID, "the default profile is created on first start")

	_, err = app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.ErrorContains(t, err, "in use by another streamcrew process")
	_, locked := errors.AsType[*lockfile.LockedError](err)
	assert.True(t, locked)

	require.NoError(t, first.Close())
	again, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err, "free after Close")
	require.NoError(t, again.Close())
}

func TestUnknownProfileFails(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	cfg.Profile = "missing"
	_, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.ErrorIs(t, err, profile.ErrNotFound)

	cfg.Profile = ""
	a, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err, "the lock was released after the failed start")
	require.NoError(t, a.Close())
}

func TestEventsDuringLifecycle(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	a, err := app.New(t.Context(), cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	sub := a.Bus().Subscribe(t.Context(), event.WithPrefixes("app.", "supervisor."), event.WithBuffer(64))

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- a.Run(ctx) }()
	require.Eventually(t, a.Ready, 5*time.Second, 10*time.Millisecond)
	cancel()
	require.NoError(t, <-errc)

	var seen []string
	for e := range sub.C() {
		if st, ok := event.Payload[supervisor.Status](e); ok {
			seen = append(seen, string(e.Type)+":"+st.Name+":"+string(st.State))
			continue
		}
		seen = append(seen, string(e.Type))
	}
	assert.Equal(t, "app.started", seen[0])
	assert.Contains(t, seen, "supervisor.status:http:running")
	assert.Contains(t, seen, "supervisor.status:backup:running")
	assert.Contains(t, seen, "app.stopping")
	assert.Contains(t, seen, "supervisor.status:http:stopped")
}

// TestNewResetsCounters covers B3 of spec counters-and-quotes.md: counters
// with the reset option are 0 after the core started, the others keep their
// value.
func TestNewResetsCounters(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)
	var console bytes.Buffer
	first, err := app.New(ctx, cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	s, err := store.Open(ctx, path)
	require.NoError(t, err)
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "session", Value: 5, Step: counter.DefaultStep, ResetOnStart: true})
	require.NoError(t, err)
	_, err = s.CreateCounter(ctx, counter.Counter{Name: "total", Value: 5, Step: counter.DefaultStep})
	require.NoError(t, err)
	require.NoError(t, s.Close())

	second, err := app.New(ctx, cfg, app.WithConsole(&console), app.WithKeyring(nil))
	require.NoError(t, err)
	require.NoError(t, second.Close())
	assert.Contains(t, console.String(), `"msg":"counters reset on start"`)

	s, err = store.Open(ctx, path)
	require.NoError(t, err)
	defer s.Close()
	session, err := s.Counter(ctx, "session")
	require.NoError(t, err)
	assert.Zero(t, session.Value)
	total, err := s.Counter(ctx, "total")
	require.NoError(t, err)
	assert.Equal(t, int64(5), total.Value)
}

func TestPreMigrationBackup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "profiles", "main.db"))
	require.NoError(t, err)
	defer s.Close()
	require.NoError(t, s.SetMeta(ctx, profile.MetaName, "Main"))

	hook := app.PreMigrationBackup(app.BackupDir(dir), "v1.2.3", slog.New(slog.DiscardHandler))
	require.NoError(t, hook(ctx, s, 1, 2))

	list, err := backup.List(app.BackupDir(dir), "main")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, backup.KindPreMigration, list[0].Manifest.Kind)
	assert.Equal(t, "Main", list[0].Manifest.ProfileName)
	assert.Equal(t, "v1.2.3", list[0].Manifest.AppVersion)
}
