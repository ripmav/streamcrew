// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
)

// logs is a log that tests read.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// watched is a core configuration with a running watcher.
type watched struct {
	t    *testing.T
	path string
	live *config.Live
	logs *logs
}

// watch parses the configuration like the streamcrew command does, then,
// inside the bubble of synctest.Test, runs a watcher of the file until the
// test ends.
func watch(t *testing.T, dataDir string, args []string, run func(w *watched)) {
	t.Helper()
	cfg, file := parse(t, config.Defaults{DataDir: dataDir}, args...)
	require.NoError(t, file.Err())
	require.NoError(t, cfg.Resolve())
	r, err := cfg.Rights()
	require.NoError(t, err)
	synctest.Test(t, func(t *testing.T) {
		w := &watched{t: t, path: filepath.Join(dataDir, "config.yaml"), live: config.NewLive(r), logs: &logs{}}
		watcher := config.NewWatcher(file, cfg, w.live, slog.New(slog.NewTextHandler(w.logs, nil)))
		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() { assert.NoError(t, watcher.Run(ctx)) })
		defer func() {
			cancel()
			wg.Wait()
		}()
		synctest.Wait()
		run(w)
	})
}

// write writes the configuration file and waits for the next comparison.
func (w *watched) write(content string) {
	w.t.Helper()
	require.NoError(w.t, os.WriteFile(w.path, []byte(content), 0o600))
	w.tick()
}

// tick lets one ReloadInterval pass.
func (w *watched) tick() {
	time.Sleep(config.ReloadInterval)
	synctest.Wait()
}

// has reports whether the current rights hold the capability.
func (w *watched) has(c capability.Capability) bool {
	return w.live.Current().Has(c)
}

// TestWatcherReloads covers Code-ADR-0019, point 5: changes of the rights
// in the file apply without a restart, all four together.
func TestWatcherReloads(t *testing.T) {
	clearEnv(t)
	dataDir, obs := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "mode: server\n")
	watch(t, dataDir, nil, func(w *watched) {
		assert.False(t, w.has(capability.HostFS))
		w.tick()
		assert.NotContains(t, w.logs.String(), "reloaded", "an unchanged file changes nothing")

		w.write("mode: server\ngrant: [host:fs]\nfile_root:\n  obs: " + obs + "\noutbound_allow: [nas]\n")
		assert.True(t, w.has(capability.HostFS))
		assert.True(t, w.live.HasRoot("obs"))
		assert.True(t, w.live.Outbound().HasHost("nas"))
		assert.Contains(t, w.logs.String(), "rights reloaded from the configuration file")
		assert.Contains(t, w.logs.String(), `capabilities="host:fs, net:outbound, script"`)
		assert.Contains(t, w.logs.String(), "host:fs is on in server mode", "warnings of the new rights")

		w.write("mode: server\nrevoke: [net:outbound]\n")
		assert.False(t, w.has(capability.HostFS), "a removed key returns to the default")
		assert.False(t, w.has(capability.NetOutbound))
		assert.False(t, w.live.HasRoot("obs"))
		assert.Empty(t, w.live.Outbound().Entries())
	})
}

// TestWatcherKeepsOnErrors covers Code-ADR-0019, point 5: an invalid,
// unreadable or missing file changes nothing; the problem is logged once.
func TestWatcherKeepsOnErrors(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "grant: [host:input]\n")
	watch(t, dataDir, nil, func(w *watched) {
		require.True(t, w.has(capability.HostInput))
		for _, content := range []string{
			"grant: [host:root]\n",
			"grant: [host:fs]\nrevoke: [host:fs]\n",
			"grant: [host:fs]\nfile_root:\n  obs: relative\n",
			"grant: [host:fs]\nunknown_key: 1\n",
			"grant: {a: b}\n",
			"grant: [1]\n",
			"file_root: [x]\n",
			"grant: [host:fs\n",
		} {
			w.write(content)
			assert.True(t, w.has(capability.HostInput), content)
		}
		assert.Equal(t, 8, strings.Count(w.logs.String(), "configuration file not reloaded"))

		w.tick()
		assert.Equal(t, 8, strings.Count(w.logs.String(), "configuration file not reloaded"), "logged once per problem")

		require.NoError(t, os.Remove(w.path))
		w.tick()
		w.tick()
		assert.True(t, w.has(capability.HostInput), "a missing file keeps the rights")
		assert.Equal(t, 9, strings.Count(w.logs.String(), "configuration file not reloaded"))

		w.write("grant: [host:input]\n")
		w.write("revoke: [script]\n")
		assert.False(t, w.has(capability.HostInput))
		assert.False(t, w.has(capability.Script))

		w.write("grant: [host:root]\n")
		w.write("revoke: []\n")
		w.write("grant: [host:root]\n")
		assert.Equal(t, 11, strings.Count(w.logs.String(), "configuration file not reloaded"),
			"a problem that comes back after a valid file is logged again")
	})
}

// TestWatcherFixedSettings covers Code-ADR-0019, point 5: settings set by
// flag or environment variable stay fixed; other changed settings need a
// restart, which the log says.
func TestWatcherFixedSettings(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "log_level: info\n")
	t.Setenv("STREAMCREW_REVOKE", "script")
	watch(t, dataDir, []string{"--outbound-allow", "nas"}, func(w *watched) {
		assert.Contains(t, w.logs.String(), "setting=revoke")
		assert.Contains(t, w.logs.String(), "setting=outbound_allow")
		assert.NotContains(t, w.logs.String(), "setting=grant")

		w.write("log_level: debug\nlisten: 127.0.0.1:9000\nrevoke: []\noutbound_allow: []\ngrant: [host:input]\n")
		assert.True(t, w.has(capability.HostInput), "grant comes from the file")
		assert.False(t, w.has(capability.Script), "revoke is fixed by the environment")
		assert.True(t, w.live.Outbound().HasHost("nas"), "outbound_allow is fixed by the flag")
		assert.Contains(t, w.logs.String(), `configuration changes apply after a restart" settings="listen, log_level"`)
	})
}

// TestWatcherNewFile covers Code-ADR-0019, point 5: a file created after
// the start counts as well.
func TestWatcherNewFile(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	watch(t, dataDir, nil, func(w *watched) {
		w.tick()
		assert.Empty(t, w.logs.String(), "no file is no problem")
		w.write("grant: [host:input]\n")
		assert.True(t, w.has(capability.HostInput))
	})
}
