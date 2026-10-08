// SPDX-License-Identifier: MIT

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/config"
)

type testCLI struct {
	config.Config

	Run struct{} `cmd:""`
}

// parse parses args like the streamcrew command does. The tests using it
// cannot run in parallel because kong reads the process environment.
func parse(t *testing.T, defaults config.Defaults, args ...string) (config.Config, *config.FileResolver) {
	t.Helper()
	file := config.NewFileResolver(defaults.ConfigFile())
	var cli testCLI
	opts := append(config.KongOptions(defaults, file), kong.Exit(func(code int) {
		t.Fatalf("kong exited with code %d", code)
	}))
	parser, err := kong.New(&cli, opts...)
	require.NoError(t, err)
	_, err = parser.Parse(append(args, "run"))
	require.NoError(t, err)
	return cli.Config, file
}

// clearEnv removes all STREAMCREW_* variables for the duration of the test,
// so that the environment of the developer does not leak into it.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, config.EnvPrefix+"_") {
			t.Setenv(key, value) // restores the variable after the test
			require.NoError(t, os.Unsetenv(key))
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestParseDefaults(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()

	cfg, file := parse(t, config.Defaults{DataDir: dataDir})
	require.NoError(t, file.Err())
	require.NoError(t, cfg.Resolve())

	assert.Empty(t, file.Path())
	assert.Equal(t, dataDir, cfg.DataDir)
	assert.Equal(t, config.ModeDaemon, cfg.Mode)
	assert.Equal(t, "127.0.0.1:8740", cfg.Listen)
	assert.False(t, cfg.Dev)
	assert.Equal(t, 15*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, config.LogConfig{Level: "info", Format: "text", File: true, MaxSize: 10, MaxFiles: 5}, cfg.Log)
	assert.Equal(t, filepath.Join(dataDir, "logs"), cfg.LogDir())
}

func TestParsePrecedence(t *testing.T) {
	tests := []struct {
		name            string
		file, env, flag bool
		wantLevel       string
	}{
		{name: "default", wantLevel: "info"},
		{name: "file over default", file: true, wantLevel: "debug"},
		{name: "env over file", file: true, env: true, wantLevel: "warn"},
		{name: "flag over env and file", file: true, env: true, flag: true, wantLevel: "error"},
		{name: "flag over file", file: true, flag: true, wantLevel: "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			dataDir := t.TempDir()
			if tc.file {
				writeFile(t, filepath.Join(dataDir, config.ConfigFileName), "log_level: debug\nlog_max_size: 20\n")
			}
			if tc.env {
				t.Setenv("STREAMCREW_LOG_LEVEL", "warn")
			}
			var args []string
			if tc.flag {
				args = append(args, "--log-level=error")
			}

			cfg, file := parse(t, config.Defaults{DataDir: dataDir}, args...)
			require.NoError(t, file.Err())
			assert.Equal(t, tc.wantLevel, cfg.Log.Level)
			if tc.file {
				// Settings without an env variable or flag still come from the file.
				assert.Equal(t, 20, cfg.Log.MaxSize)
			}
		})
	}
}

func TestParseFileValueTypes(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	writeFile(t, filepath.Join(dataDir, config.ConfigFileName), `
# comments are allowed
mode: server
listen: "0.0.0.0:9000"
shutdown_timeout: 3s
log_component_level:
  supervisor: debug
  http: warn
log_file: false
log_max_files: 2
`)

	cfg, file := parse(t, config.Defaults{DataDir: dataDir})
	require.NoError(t, file.Err())
	require.NoError(t, cfg.Resolve())

	assert.Equal(t, filepath.Join(dataDir, config.ConfigFileName), file.Path())
	assert.Equal(t, config.ModeServer, cfg.Mode)
	assert.Equal(t, "0.0.0.0:9000", cfg.Listen)
	assert.Equal(t, 3*time.Second, cfg.ShutdownTimeout)
	assert.Equal(t, map[string]string{"supervisor": "debug", "http": "warn"}, cfg.Log.ComponentLevel)
	assert.False(t, cfg.Log.File)
	assert.Equal(t, 2, cfg.Log.MaxFiles)
}

func TestParseExplicitConfigFile(t *testing.T) {
	t.Run("flag replaces the default file", func(t *testing.T) {
		clearEnv(t)
		dataDir := t.TempDir()
		writeFile(t, filepath.Join(dataDir, config.ConfigFileName), "log_level: debug\nlog_format: json\n")
		explicit := filepath.Join(t.TempDir(), "other.yaml")
		writeFile(t, explicit, "log_level: warn\n")

		cfg, file := parse(t, config.Defaults{DataDir: dataDir}, "--config", explicit)
		require.NoError(t, file.Err())
		assert.Equal(t, explicit, file.Path())
		assert.Equal(t, "warn", cfg.Log.Level)
		assert.Equal(t, "text", cfg.Log.Format, "the default file must not be read")
	})

	t.Run("environment variable", func(t *testing.T) {
		clearEnv(t)
		explicit := filepath.Join(t.TempDir(), "env.yaml")
		writeFile(t, explicit, "log_level: warn\n")
		t.Setenv("STREAMCREW_CONFIG", explicit)

		cfg, file := parse(t, config.Defaults{DataDir: t.TempDir()})
		require.NoError(t, file.Err())
		assert.Equal(t, explicit, file.Path())
		assert.Equal(t, "warn", cfg.Log.Level)
	})

	t.Run("missing explicit file is an error", func(t *testing.T) {
		clearEnv(t)
		missing := filepath.Join(t.TempDir(), "missing.yaml")

		_, file := parse(t, config.Defaults{DataDir: t.TempDir()}, "--config", missing)
		require.ErrorIs(t, file.Err(), os.ErrNotExist)
	})
}

func TestParseFileErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr []string
	}{
		{
			name:    "unknown keys",
			content: "lg_level: debug\nconfig: other.yaml\nlog_level: info\n",
			wantErr: []string{`unknown key "config"`, `unknown key "lg_level"`},
		},
		{
			name:    "invalid YAML",
			content: "log_level: [debug\n",
			wantErr: []string{"config file"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			dataDir := t.TempDir()
			writeFile(t, filepath.Join(dataDir, config.ConfigFileName), tc.content)

			cfg, file := parse(t, config.Defaults{DataDir: dataDir})
			require.Error(t, file.Err())
			for _, want := range tc.wantErr {
				assert.ErrorContains(t, file.Err(), want)
			}
			assert.Equal(t, "info", cfg.Log.Level, "a broken file must not supply values")
		})
	}
}

func TestViewRoundTrip(t *testing.T) {
	clearEnv(t)
	dataDir := t.TempDir()
	want, file := parse(t, config.Defaults{DataDir: dataDir},
		"--profile=second", "--mode=server", "--listen=:9001", "--shutdown-timeout=7s", "--log-level=debug",
		"--log-component-level=supervisor=warn", "--log-format=json", "--no-log-file",
		"--log-max-size=3", "--log-max-files=1")
	require.NoError(t, file.Err())

	// The output of "config show" is a valid configuration file for the same
	// configuration.
	out, err := yaml.Marshal(want.View())
	require.NoError(t, err)
	writeFile(t, filepath.Join(dataDir, config.ConfigFileName), string(out))

	got, file := parse(t, config.Defaults{DataDir: t.TempDir()}, "--config", filepath.Join(dataDir, config.ConfigFileName))
	require.NoError(t, file.Err())
	got.ConfigFile = ""
	assert.Equal(t, want, got)
}

func TestResolve(t *testing.T) {
	t.Parallel()
	valid := func() config.Config {
		return config.Config{
			DataDir:         "/data",
			Mode:            config.ModeDaemon,
			ShutdownTimeout: time.Second,
			Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1},
		}
	}

	tests := []struct {
		name       string
		modify     func(*config.Config)
		wantListen string
		wantErr    []string
	}{
		{name: "daemon default listen", modify: func(*config.Config) {}, wantListen: "127.0.0.1:8740"},
		{name: "desktop default listen", modify: func(c *config.Config) { c.Mode = config.ModeDesktop }, wantListen: "127.0.0.1:8740"},
		{name: "server default listen", modify: func(c *config.Config) { c.Mode = config.ModeServer }, wantListen: ":8740"},
		{name: "free port", modify: func(c *config.Config) { c.Listen = "127.0.0.1:0" }, wantListen: "127.0.0.1:0"},
		{name: "dev on loopback", modify: func(c *config.Config) { c.Dev = true; c.Listen = "localhost:1" }, wantListen: "localhost:1"},
		{name: "dev on IPv6 loopback", modify: func(c *config.Config) { c.Dev = true; c.Listen = "[::1]:1" }, wantListen: "[::1]:1"},
		{
			name:    "dev on all interfaces",
			modify:  func(c *config.Config) { c.Dev = true; c.Mode = config.ModeServer },
			wantErr: []string{"--dev"},
		},
		{name: "unknown mode", modify: func(c *config.Config) { c.Mode = "kiosk" }, wantErr: []string{"--mode"}},
		{name: "no data dir", modify: func(c *config.Config) { c.DataDir = "" }, wantErr: []string{"--data-dir"}},
		{name: "listen without port", modify: func(c *config.Config) { c.Listen = "localhost" }, wantErr: []string{"--listen"}},
		{name: "listen with bad port", modify: func(c *config.Config) { c.Listen = "localhost:http" }, wantErr: []string{"--listen"}},
		{name: "shutdown timeout", modify: func(c *config.Config) { c.ShutdownTimeout = 0 }, wantErr: []string{"--shutdown-timeout"}},
		{
			name: "log settings",
			modify: func(c *config.Config) {
				c.Log = config.LogConfig{
					Level:          "loud",
					ComponentLevel: map[string]string{"": "info", "http": "chatty"},
					Format:         "xml",
					MaxSize:        0,
					MaxFiles:       -1,
				}
			},
			wantErr: []string{
				"--log-level", "empty component name", "--log-component-level http",
				"--log-format", "--log-max-size", "--log-max-files",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid()
			tc.modify(&cfg)
			err := cfg.Resolve()
			if len(tc.wantErr) == 0 {
				require.NoError(t, err)
				assert.Equal(t, tc.wantListen, cfg.Listen)
				return
			}
			require.Error(t, err)
			for _, want := range tc.wantErr {
				assert.ErrorContains(t, err, want)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"debug", "info", "warn", "error"} {
		level, err := config.ParseLevel(name)
		require.NoError(t, err)
		assert.Equal(t, strings.ToUpper(name), level.String())
	}
	_, err := config.ParseLevel("INFO")
	assert.Error(t, err)
}

func TestDetectDefaults(t *testing.T) {
	t.Run("portable", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, config.PortableMarker), "")

		d, err := config.DetectDefaults(filepath.Join(dir, "streamcrew"))
		require.NoError(t, err)
		assert.Equal(t, config.Defaults{DataDir: filepath.Join(dir, config.PortableDataDir), Portable: true}, d)
		assert.Equal(t, filepath.Join(dir, config.PortableDataDir, config.ConfigFileName), d.ConfigFile())
	})

	t.Run("user config dir", func(t *testing.T) {
		t.Parallel()
		d, err := config.DetectDefaults(filepath.Join(t.TempDir(), "streamcrew"))
		if err != nil {
			t.Skipf("no user config dir in this environment: %v", err)
		}
		userDir, err := os.UserConfigDir()
		require.NoError(t, err)
		assert.Equal(t, config.Defaults{DataDir: filepath.Join(userDir, config.AppDirName)}, d)
	})

	t.Run("no data dir", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, config.Defaults{}.ConfigFile())
	})
}
