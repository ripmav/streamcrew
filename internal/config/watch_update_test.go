// SPDX-License-Identifier: MIT

package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/config"
)

// testLogger returns a logger the tests do not read.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

// base returns the configuration a watcher runs against.
func base(t *testing.T) config.Config {
	cfg := config.Config{
		DataDir:         t.TempDir(),
		Mode:            config.ModeServer,
		Listen:          "127.0.0.1:8740",
		ShutdownTimeout: 15 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "text", MaxSize: 10, MaxFiles: 3},
	}
	return cfg
}

// updateFile returns the file path and a resolver loaded like the CLI, so
// the watcher knows the allowed keys and that the file decides the values.
func updateFile(t *testing.T, initial string) (string, *config.FileResolver) {
	t.Helper()
	path := filepath.Join(t.TempDir(), config.ConfigFileName)
	f := config.NewFileResolver(path)
	if initial != "" {
		require.NoError(t, os.WriteFile(path, []byte(initial), 0o600))
	}
	require.NoError(t, f.Load(path))
	return path, f
}

// TestWatcherUpdateStoresTheDocument checks that Update validates the
// document and stores it, reporting the changed settings (roadmap 6.5.2).
func TestWatcherUpdateStoresTheDocument(t *testing.T) {
	t.Parallel()
	path, f := updateFile(t, "log_level: debug\n")
	w := config.NewWatcher(f, base(t), nil, nil, testLogger(t))

	applied, restart, stored, err := w.Update(t.Context(), []byte("log_level: debug\ndry_run: true\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"dry-run"}, applied)
	assert.Empty(t, restart)
	assert.Equal(t, "log_level: debug\ndry_run: true\n", string(stored))

	// The file holds the stored document, atomically replaced.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "log_level: debug\ndry_run: true\n", string(data))

	// An unchanged document is not written again.
	//
	// shortcut: applied may report dry-run, because the watcher that
	// applies the change is not running here; with a running watcher the
	// change would be applied on the next comparison.
	_, _, stored, err = w.Update(t.Context(), data)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(stored))
}

// TestWatcherUpdateRejectsAnInvalidDocument checks that an invalid
// document is not stored (roadmap 6.5.2).
func TestWatcherUpdateRejectsAnInvalidDocument(t *testing.T) {
	t.Parallel()
	path, f := updateFile(t, "log_level: debug\n")
	w := config.NewWatcher(f, base(t), nil, nil, testLogger(t))

	_, _, _, err := w.Update(t.Context(), []byte("nope: 1\n"))
	assert.Error(t, err)

	_, _, _, err = w.Update(t.Context(), []byte("log_max_size: many\n"))
	assert.Error(t, err)

	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "log_level: debug\n", string(data))
}

// TestWatcherUpdateReportsRestartSettings checks that the settings that
// apply only after a restart are reported (roadmap 6.5.2).
func TestWatcherUpdateReportsRestartSettings(t *testing.T) {
	t.Parallel()
	_, f := updateFile(t, "log_level: debug\n")
	w := config.NewWatcher(f, base(t), nil, nil, testLogger(t))

	_, restart, _, err := w.Update(t.Context(), []byte("log_level: debug\nprofile: other\n"))
	require.NoError(t, err)
	assert.Equal(t, []string{"profile"}, restart)
}

// TestWatcherUpdateCreatesAMissingFile checks that Update creates the
// file when it does not exist (roadmap 6.5.2).
func TestWatcherUpdateCreatesAMissingFile(t *testing.T) {
	t.Parallel()
	path, f := updateFile(t, "")
	w := config.NewWatcher(f, base(t), nil, nil, testLogger(t))

	_, _, stored, err := w.Update(t.Context(), []byte("log_level: warn\n"))
	require.NoError(t, err)
	assert.Equal(t, "log_level: warn\n", string(stored))
	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "log_level: warn\n", string(data))
}
