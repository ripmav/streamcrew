// SPDX-License-Identifier: MIT

package config_test

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/config"
)

// applyRec records the calls of the hot applies of the Watcher (roadmap
// 6.5).
type applyRec struct {
	logCalls  []config.LogConfig
	dryRun    atomic.Bool
	dryCalls  []bool
	dev       atomic.Bool
	devCalls  []bool
	corsCalls [][]string
	listen    atomic.Bool
	listenErr atomic.Bool
	listenCfg atomic.Value // holds [3]string
	timeout   atomic.Int64
	timeoutMu sync.Mutex
}

func (r *applyRec) funcs() *config.ApplyFuncs {
	return &config.ApplyFuncs{
		Log: func(lc config.LogConfig) error {
			r.timeoutMu.Lock()
			r.logCalls = append(r.logCalls, lc)
			r.timeoutMu.Unlock()
			return nil
		},
		DryRun: func(on bool) {
			r.dryRun.Store(on)
			r.dryCalls = append(r.dryCalls, on)
		},
		Dev: func(on bool) {
			r.dev.Store(on)
			r.devCalls = append(r.devCalls, on)
		},
		CorsAllow: func(list []string) {
			r.corsCalls = append(r.corsCalls, list)
		},
		Listen: func(addr, cert, key string) error {
			r.listen.Store(true)
			if r.listenErr.Load() {
				return errListen
			}
			r.listenCfg.Store([3]string{addr, cert, key})
			return nil
		},
		ShutdownTimeout: func(d time.Duration) {
			r.timeout.Store(int64(d))
		},
	}
}

var errListen = errors.New("rebind failed")

// TestWatcherAppliesHotSettings verifies the watcher calls the hot applies
// for the settings that changed in the file, and not for the ones that did
// not change (roadmap 6.5).
func TestWatcherAppliesHotSettings(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	rec := &applyRec{}
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "log_level: info\n")
	watch(t, dataDir, nil, rec.funcs(), func(w *watched) {
		w.tick()
		assert.Empty(t, rec.logCalls, "an unchanged file applies nothing")

		w.write("log_level: debug\nlog_component_level: {engine: debug}\ndry_run: true\ndev: true\ncors_allow: [http://a.example]\nshutdown_timeout: 20s\n")
		requireLen(t, rec.logCalls, 1)
		assert.Equal(t, "debug", rec.logCalls[0].Level)
		assert.Equal(t, map[string]string{"engine": "debug"}, rec.logCalls[0].ComponentLevel)
		assert.True(t, rec.dryRun.Load())
		assert.True(t, rec.dev.Load())
		assert.Equal(t, []string{"http://a.example"}, rec.corsCalls[len(rec.corsCalls)-1])
		assert.Equal(t, int64(20*time.Second), rec.timeout.Load())
		assert.False(t, rec.listen.Load(), "the listen address did not change")

		// A change of the listen address calls the rebind.
		w.write("log_level: debug\ndry_run: false\ndev: true\ncors_allow: [http://a.example]\nshutdown_timeout: 20s\nlisten: 127.0.0.1:9000\n")
		assert.True(t, rec.listen.Load())
		assert.Equal(t, [3]string{"127.0.0.1:9000", "", ""}, rec.listenCfg.Load().([3]string))
		assert.False(t, rec.dryRun.Load())
	})
}

// TestWatcherAppliesTheLogFile verifies the log_file setting reaches the log
// apply as text, as a path and as a bool (roadmap 6.5).
func TestWatcherAppliesTheLogFile(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	rec := &applyRec{}
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "log_level: info\n")
	watch(t, dataDir, nil, rec.funcs(), func(w *watched) {
		w.tick()
		assert.Empty(t, rec.logCalls, "an unchanged file applies nothing")

		w.write("log_file: /var/log/streamcrew/custom.log\n")
		requireLen(t, rec.logCalls, 1)
		assert.Equal(t, "/var/log/streamcrew/custom.log", rec.logCalls[0].File)

		w.write("log_file: false\n")
		requireLen(t, rec.logCalls, 2)
		assert.Equal(t, "false", rec.logCalls[1].File)
	})
}

// TestWatcherAppliesFixedOnlyWhenFromFile verifies the watcher does not
// apply a hot setting that is fixed by flag or environment (roadmap 6.5).
func TestWatcherAppliesFixedOnlyWhenFromFile(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	rec := &applyRec{}
	t.Setenv("STREAMCREW_LOG_LEVEL", "info")
	writeFile(t, filepath.Join(dataDir, "config.yaml"), "log_level: info\n")
	watch(t, dataDir, nil, rec.funcs(), func(w *watched) {
		w.write("log_level: debug\ndry_run: true\n")
		assert.Empty(t, rec.logCalls, "log_level is fixed by the environment")
		assert.True(t, rec.dryRun.Load(), "dry_run is not fixed")
	})
}

// TestWatcherKeepsOnApplyError verifies a failing apply keeps the previous
// state and logs the problem (roadmap 6.5).
func TestWatcherKeepsOnApplyError(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	rec := &applyRec{}
	rec.listenErr.Store(true)
	watch(t, dataDir, nil, rec.funcs(), func(w *watched) {
		w.tick()
		w.write("listen: 127.0.0.1:9000\n")
		assert.Contains(t, w.logs.String(), "setting not applied, the previous value stays")
		assert.Contains(t, w.logs.String(), "setting=listen")
	})
}

// TestWatcherRestartOnlySettings verifies the settings that need a restart
// are reported, not applied (roadmap 6.5).
func TestWatcherRestartOnlySettings(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	rec := &applyRec{}
	watch(t, dataDir, nil, rec.funcs(), func(w *watched) {
		w.tick()
		w.write("profile: other\n")
		assert.Contains(t, w.logs.String(), `configuration changes apply after a restart" settings=profile`)
		assert.Empty(t, rec.logCalls, "profile is not a hot apply")
	})
}

// requireLen asserts the length of a slice for a clearer failure.
func requireLen(t *testing.T, calls []config.LogConfig, n int) {
	t.Helper()
	assert.Len(t, calls, n)
}
