// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	json "encoding/json/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/cli"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/doctor"
	"github.com/ripmav/streamcrew/internal/profile"
)

// isolate points the user config directory to a temporary directory and
// removes all STREAMCREW_* variables, so that neither the files nor the
// environment of the developer leak into a test. Tests using it cannot run in
// parallel.
func isolate(t *testing.T) (userConfigDir string) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, value, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, config.EnvPrefix+"_") {
			t.Setenv(key, value)
			require.NoError(t, os.Unsetenv(key))
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)                                   // macOS
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".cfg")) // Linux, BSD
	t.Setenv("AppData", filepath.Join(home, "AppData"))      // Windows
	dir, err := os.UserConfigDir()
	require.NoError(t, err)
	return dir
}

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(ctx context.Context, t *testing.T, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(ctx, args, process{
		stdout: &stdout,
		stderr: &stderr,
		exit:   func(code int) { t.Fatalf("unexpected exit(%d)", code) },
		// A fixed key keeps the tests away from the system keyring of the
		// developer (ADR-0012).
		secretKey: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32)),
	})
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// pathInfo is the JSON output of "config path".
type pathInfo struct {
	ConfigFile       string `json:"config_file"`
	ConfigFileExists bool   `json:"config_file_exists"`
	DataDir          string `json:"data_dir"`
	LogDir           string `json:"log_dir"`
	Portable         bool   `json:"portable"`
}

func TestVersion(t *testing.T) {
	isolate(t)

	res := runCLI(t.Context(), t, "version")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.True(t, strings.HasPrefix(res.stdout, "streamcrew "), res.stdout)

	res = runCLI(t.Context(), t, "version", "--output", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var info map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &info))
	assert.Contains(t, info, "version")
	assert.Contains(t, info, "go_version")
}

func TestConfigShow(t *testing.T) {
	userDir := isolate(t)
	defaultFile := filepath.Join(userDir, config.AppDirName, config.ConfigFileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(defaultFile), 0o700))
	require.NoError(t, os.WriteFile(defaultFile, []byte("log_format: json\nlog_max_files: 3\n"), 0o600))
	t.Setenv("STREAMCREW_MODE", "server")

	res := runCLI(t.Context(), t, "--log-level=debug", "config", "show")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var view config.FileView
	require.NoError(t, yaml.Unmarshal([]byte(res.stdout), &view))
	assert.Equal(t, config.FileView{
		DataDir:         filepath.Join(userDir, config.AppDirName),
		Mode:            config.ModeServer,
		Listen:          ":8740",
		ShutdownTimeout: "15s",
		LogLevel:        "debug",
		LogFormat:       "json",
		LogFile:         true,
		LogMaxSize:      10,
		LogMaxFiles:     3,
	}, view)

	res = runCLI(t.Context(), t, "config", "show", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var fromJSON config.FileView
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &fromJSON))
	assert.Equal(t, config.ModeServer, fromJSON.Mode)
}

// TestConfigShowRights covers Code-ADR-0019: the rights come from the file
// as YAML lists and objects and are shown as set.
func TestConfigShowRights(t *testing.T) {
	userDir := isolate(t)
	obs := t.TempDir()
	defaultFile := filepath.Join(userDir, config.AppDirName, config.ConfigFileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(defaultFile), 0o700))
	require.NoError(t, os.WriteFile(defaultFile, []byte(
		"grant:\n  - host:input\nrevoke: [script]\nfile_root:\n  obs: "+obs+"\noutbound_allow: [nas, 10.0.0.0/8]\n"), 0o600))

	res := runCLI(t.Context(), t, "config", "show")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var view config.FileView
	require.NoError(t, yaml.Unmarshal([]byte(res.stdout), &view))
	assert.Equal(t, []string{"host:input"}, view.Grant)
	assert.Equal(t, []string{"script"}, view.Revoke)
	assert.Equal(t, map[string]string{"obs": obs}, view.FileRoot)
	assert.Equal(t, []string{"nas", "10.0.0.0/8"}, view.OutboundAllow)
}

func TestConfigPath(t *testing.T) {
	userDir := isolate(t)
	dataDir := filepath.Join(userDir, config.AppDirName)

	res := runCLI(t.Context(), t, "config", "path")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, filepath.Join(dataDir, config.ConfigFileName)+" (not present)")
	assert.Contains(t, res.stdout, filepath.Join(dataDir, "logs"))

	explicit := filepath.Join(t.TempDir(), "streamcrew.yaml")
	require.NoError(t, os.WriteFile(explicit, []byte("log_level: warn\n"), 0o600))
	res = runCLI(t.Context(), t, "--config", explicit, "--data-dir", t.TempDir(), "config", "path", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var info pathInfo
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &info))
	assert.Equal(t, explicit, info.ConfigFile)
	assert.True(t, info.ConfigFileExists)
	assert.False(t, info.Portable)
}

func TestDoctor(t *testing.T) {
	isolate(t)

	res := runCLI(t.Context(), t, "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0", "doctor", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var results []doctor.Result
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &results))
	assert.NotEmpty(t, results)
	assert.False(t, doctor.Failed(results))

	res = runCLI(t.Context(), t, "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0", "--mode", "server",
		"--grant", "host:process", "--file-root", "gone=/does/not/exist", "doctor", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &results))
	assert.Contains(t, results, doctor.Result{Check: "capabilities", Status: doctor.StatusOK, Detail: "host:process, net:outbound, script"})
	assert.Contains(t, results, doctor.Result{Check: "file roots", Status: doctor.StatusOK, Detail: "gone=/does/not/exist"})
	assert.Contains(t, results, doctor.Result{Check: "rights", Status: doctor.StatusWarn, Detail: "host:process is on in server mode"})
	assert.Contains(t, results, doctor.Result{Check: "rights", Status: doctor.StatusWarn, Detail: "the file roots have no effect without host:fs"})

	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	res = runCLI(t.Context(), t, "--data-dir", blocker, "--listen", "127.0.0.1:0", "doctor")
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stdout, "fail")
	assert.Empty(t, res.stderr, "the check results are the report")
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		args       []string
		wantStderr string
	}{
		{name: "unknown flag", args: []string{"--no-such-flag", "version"}, wantStderr: "unknown flag"},
		{name: "invalid enum", args: []string{"--mode=kiosk", "config", "show"}, wantStderr: "--mode"},
		{name: "invalid listen address", args: []string{"--listen=nowhere", "config", "show"}, wantStderr: "--listen"},
		{name: "unknown key in config file", file: "lg_level: debug\n", args: []string{"config", "show"}, wantStderr: `unknown key "lg_level"`},
		{name: "missing config file", args: []string{"--config=/does/not/exist.yaml", "config", "show"}, wantStderr: "read config file"},
		{name: "unknown capability", args: []string{"--grant=host:root", "config", "show"}, wantStderr: `--grant: unknown capability "host:root"`},
		{name: "relative file root", file: "file_root:\n  obs: obs\n", args: []string{"config", "show"}, wantStderr: "--file-root obs"},
		{name: "invalid allowlist", args: []string{"--outbound-allow=*.local", "doctor"}, wantStderr: "--outbound-allow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			userDir := isolate(t)
			if tc.file != "" {
				path := filepath.Join(userDir, config.AppDirName, config.ConfigFileName)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte(tc.file), 0o600))
			}
			res := runCLI(t.Context(), t, tc.args...)
			assert.Equal(t, cli.ExitUsage, res.code)
			assert.Contains(t, res.stderr, tc.wantStderr)
		})
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func httpStatus(ctx context.Context, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestServe covers the exit criterion of milestone M0: serve starts, reports
// itself healthy and ready, and stops cleanly when its context ends (as on
// SIGINT or SIGTERM).
func TestServe(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan result, 1)
	go func() {
		done <- runCLI(ctx, t, "--data-dir", dataDir, "--listen", addr, "--log-format", "json", "serve")
	}()

	require.Eventually(t, func() bool {
		return httpStatus(t.Context(), "http://"+addr+"/readyz") == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, http.StatusOK, httpStatus(t.Context(), "http://"+addr+"/healthz"))

	cancel()
	res := <-done
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stderr, `"msg":"streamcrew stopped"`)
	assert.FileExists(t, filepath.Join(dataDir, "logs", "streamcrew.log"))
}

func TestServePortInUse(t *testing.T) {
	isolate(t)
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer busy.Close()

	res := runCLI(t.Context(), t, "--data-dir", t.TempDir(), "--listen", busy.Addr().String(), "--no-log-file", "serve")
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stderr, "streamcrew stopped with an error")
	assert.NotContains(t, res.stderr, "streamcrew: ", "the error is logged once, not printed again")
}

func TestProfileCommands(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	streamcrew := func(args ...string) result {
		return runCLI(t.Context(), t, append([]string{"--data-dir", dataDir}, args...)...)
	}

	res := streamcrew("profile", "create", "Main Channel")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, "created profile main-channel")
	require.Equal(t, cli.ExitOK, streamcrew("profile", "create", "Second", "--id", "second").code)

	res = streamcrew("profile", "use", "second")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	res = streamcrew("profile", "rename", "main-channel", "Hauptkanal")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)

	res = streamcrew("profile", "list", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var list []profile.Profile
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &list))
	require.Len(t, list, 2)
	assert.Equal(t, "Hauptkanal", list[0].Name)
	assert.True(t, list[1].Active)

	res = streamcrew("profile", "list")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, "*       second")

	res = streamcrew("profile", "delete", "main-channel")
	assert.Equal(t, cli.ExitUsage, res.code, "deleting needs --yes")
	res = streamcrew("profile", "delete", "main-channel", "--yes")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, "backup:")
	res = streamcrew("--profile", "main-channel", "backup", "list", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, `"kind": "pre-delete"`, "the deleted profile was backed up")

	res = streamcrew("--profile", "Bad ID", "profile", "list")
	assert.Equal(t, cli.ExitUsage, res.code)
}

func TestBackupCommands(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	streamcrew := func(args ...string) result {
		return runCLI(t.Context(), t, append([]string{"--data-dir", dataDir}, args...)...)
	}
	require.Equal(t, cli.ExitOK, streamcrew("profile", "create", "Default", "--id", "default").code)

	res := streamcrew("backup", "create")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	file := strings.TrimSpace(res.stdout)
	assert.FileExists(t, file)

	res = streamcrew("backup", "list")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, "manual")

	res = streamcrew("backup", "restore", file)
	assert.Equal(t, cli.ExitUsage, res.code, "restoring needs --yes")
	res = streamcrew("backup", "restore", file, "--yes")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, "backed up the current state")
	assert.Contains(t, res.stdout, "restored profile default")

	// Restore into a new profile.
	res = streamcrew("--profile", "copy", "backup", "restore", file, "--yes")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	res = streamcrew("profile", "list", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, `"id": "copy"`)

	res = streamcrew("--profile", "missing", "backup", "create")
	assert.Equal(t, cli.ExitFailure, res.code)
}

func TestCommandsRefuseWhileCoreRuns(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan result, 1)
	go func() {
		done <- runCLI(ctx, t, "--data-dir", dataDir, "--listen", addr, "--no-log-file", "serve")
	}()
	require.Eventually(t, func() bool {
		return httpStatus(t.Context(), "http://"+addr+"/readyz") == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond)

	res := runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "create", "Other")
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stderr, "in use by another streamcrew process")

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "backup", "create")
	require.Equal(t, cli.ExitOK, res.code, "backups work while the core runs: %s", res.stderr)

	cancel()
	require.Equal(t, cli.ExitOK, (<-done).code)
}

func TestVaultRotateWithEnvironmentKey(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "create", "Main").code)
	res := runCLI(t.Context(), t, "--data-dir", dataDir, "secret", "rotate")
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stderr, "STREAMCREW_SECRET_KEY")
}
