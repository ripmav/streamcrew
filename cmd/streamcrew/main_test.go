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
	"github.com/ripmav/streamcrew/internal/commandfile"
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

	gone := filepath.Join(t.TempDir(), "gone") // absolute on every system, and missing
	res = runCLI(t.Context(), t, "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0", "--mode", "server",
		"--grant", "host:process", "--file-root", "gone="+gone, "doctor", "-o", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &results))
	assert.Contains(t, results, doctor.Result{Check: "capabilities", Status: doctor.StatusOK, Detail: "host:process, net:outbound, script"})
	assert.Contains(t, results, doctor.Result{Check: "file roots", Status: doctor.StatusOK, Detail: "gone=" + gone})
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
		{name: "invalid platform", args: []string{"auth", "login", "youtube"}, wantStderr: `must be one of "twitch" but got "youtube"`},
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

	file := filepath.Join(t.TempDir(), "tip.yaml")
	require.NoError(t, os.WriteFile(file, []byte("apiVersion: streamcrew/v1alpha1\nkind: TimerCommand\nmetadata: {name: Tip}\nspec: {}\n"), 0o600))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", file)
	require.Equal(t, cli.ExitOK, res.code, "commands-as-code.md, B31: checking works while the core runs: %s", res.stderr)
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", file)
	assert.Equal(t, cli.ExitFailure, res.code, "commands-as-code.md, B33: importing needs a stopped core")
	assert.Contains(t, res.stderr, "in use by another streamcrew process")
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export")
	assert.Equal(t, cli.ExitOK, res.code, "commands-as-code.md, B35: exporting works while the core runs: %s", res.stderr)

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

// TestSchemaExport covers B30 of commands-as-code.md: "schema export" writes
// the schema of files, by default into schemas/, and schemas/ in the
// repository holds the current one. With STREAMCREW_UPDATE_GOLDEN=1 it
// writes that file first (Code-ADR-0006).
func TestSchemaExport(t *testing.T) {
	update := os.Getenv("STREAMCREW_UPDATE_GOLDEN") != "" // isolate removes it
	isolate(t)
	dir := filepath.Join(t.TempDir(), "out")
	res := runCLI(t.Context(), t, "schema", "export", "--dir", dir)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	path := filepath.Join(dir, commandfile.SchemaFile)
	assert.Equal(t, "wrote "+path+"\n", res.stdout)
	got, err := os.ReadFile(path)
	require.NoError(t, err)

	repo, err := os.OpenRoot(filepath.Join("..", "..", "schemas"))
	require.NoError(t, err)
	defer repo.Close()
	if update {
		require.NoError(t, repo.WriteFile(commandfile.SchemaFile, got, 0o600))
	}
	want, err := repo.ReadFile(commandfile.SchemaFile)
	require.NoError(t, err, "create it with STREAMCREW_UPDATE_GOLDEN=1")
	assert.Equal(t, string(want), string(got), "schemas/ is out of date; update it with STREAMCREW_UPDATE_GOLDEN=1")

	t.Chdir(t.TempDir())
	res = runCLI(t.Context(), t, "schema", "export")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	again, err := os.ReadFile(filepath.Join("schemas", commandfile.SchemaFile))
	require.NoError(t, err, "the default directory is schemas")
	assert.Equal(t, string(got), string(again))
}

// TestCommandValidate covers B31, B32 and B37 of commands-as-code.md: the
// files of a directory are checked against the profile, as text or JSON,
// and errors end with exit code 1; paths without such files are a usage
// error.
func TestCommandValidate(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	require.NoError(t, os.MkdirAll(good, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(good, "hug.yaml"), []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hug}\nspec: {triggers: [hug]}\n"), 0o600))
	res := runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", good)
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stderr, `profile "default" does not exist`)
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "create", "Main").code)
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "use", "main").code)

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", good)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, "1 document in 1 file: 0 errors, 0 warnings\n", res.stdout)

	bad := filepath.Join(dir, "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte(`{"apiVersion":"streamcrew/v1","kind":"TimerCommand","metadata":{"name":"a"},"spec":{}}`), 0o600))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", good, bad)
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Equal(t, bad+":1:15: apiVersion: must be \"streamcrew/v1alpha1\"\n2 documents in 2 files: 1 error, 0 warnings\n", res.stdout)
	assert.Empty(t, res.stderr)

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", "-o", "json", bad)
	assert.Equal(t, cli.ExitFailure, res.code)
	var report struct {
		Files     int              `json:"files"`
		Documents int              `json:"documents"`
		Errors    int              `json:"errors"`
		Warnings  int              `json:"warnings"`
		Problems  []map[string]any `json:"problems"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &report), res.stdout)
	assert.Equal(t, 1, report.Errors)
	assert.Equal(t, 1, report.Files)
	assert.Equal(t, 1, report.Documents)
	assert.Equal(t, 0, report.Warnings)
	require.Len(t, report.Problems, 1)
	assert.Equal(t, map[string]any{"severity": "error", "file": bad, "line": 1.0, "column": 15.0, "path": "apiVersion", "message": `must be "streamcrew/v1alpha1"`}, report.Problems[0])

	notes := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(notes, nil, 0o600))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", notes)
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "extension")
	empty := filepath.Join(dir, "empty")
	require.NoError(t, os.MkdirAll(empty, 0o750))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", empty)
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "no files of commands as code")
}

// TestCommandImport covers B33 and B62 of commands-as-code.md: an import
// creates and then replaces by name; a file with errors imports nothing.
func TestCommandImport(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "create", "Main").code)
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "use", "main").code)
	dir := t.TempDir()
	hug := filepath.Join(dir, "hug.yaml")
	require.NoError(t, os.WriteFile(hug, []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hug}\nspec: {triggers: [hug]}\n"), 0o600))

	res := runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", hug)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, "1 document in 1 file: 0 errors, 0 warnings; imported: 1 new, 0 replaced\n", res.stdout)
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", hug)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, "1 document in 1 file: 0 errors, 0 warnings; imported: 0 new, 1 replaced\n", res.stdout)

	bad := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Other}\nspec: {triggers: [hug]}\n"), 0o600))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", "-o", "json", bad)
	assert.Equal(t, cli.ExitFailure, res.code)
	var report struct {
		Imported bool `json:"imported"`
		Errors   int  `json:"errors"`
		Created  int  `json:"created"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &report), res.stdout)
	assert.False(t, report.Imported)
	assert.Equal(t, 1, report.Errors)
	assert.Zero(t, report.Created)

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", bad)
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Equal(t, bad+":4:19: spec.triggers[0]: the trigger \"!hug\" is used by the active chat command \"Hug\"\n"+
		"1 document in 1 file: 1 error, 0 warnings; nothing imported\n", res.stdout)
}

// TestCommandExport covers B35, B37 and B38 of commands-as-code.md: YAML on
// the standard output, JSON, into a file and one file per document.
func TestCommandExport(t *testing.T) {
	isolate(t)
	dataDir := t.TempDir()
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "create", "Main").code)
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "profile", "use", "main").code)
	dir := t.TempDir()
	hug := filepath.Join(dir, "hug.yaml")
	require.NoError(t, os.WriteFile(hug, []byte(
		"apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hug}\nspec: {triggers: [hug]}\n---\n"+
			"apiVersion: streamcrew/v1alpha1\nkind: CooldownGroup\nmetadata: {name: Hugs}\nspec: {duration: 5s}\n"), 0o600))
	require.Equal(t, cli.ExitOK, runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", hug).code)

	res := runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "hug")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, "apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata:\n  name: Hug\nspec:\n  triggers:\n    - hug\n"+
		"  triggerMode: exclamation\n  enabled: true\n  unlocked: false\n  errorPolicy: continue\n  requirements: {}\n  actions: []\n", res.stdout)

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "--format", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	var list []map[string]any
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &list))
	require.Len(t, list, 2, "all objects")
	assert.Equal(t, "CooldownGroup", list[0]["kind"])

	file := filepath.Join(dir, "all.yaml")
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "--file", file)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, "wrote "+file+"\n", res.stdout)
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "validate", file)
	require.Equal(t, cli.ExitOK, res.code, "the export reads back: %s", res.stdout)

	out := filepath.Join(dir, "out")
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "--dir", out, "--format", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.FileExists(t, filepath.Join(out, "chat-command-hug.v1alpha1.json"))
	assert.FileExists(t, filepath.Join(out, "cooldown-group-hugs.v1alpha1.json"))
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "import", out)
	require.Equal(t, cli.ExitOK, res.code, res.stdout)
	assert.Contains(t, res.stdout, "imported: 0 new, 2 replaced")

	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "--file", file, "--dir", out)
	assert.Equal(t, cli.ExitUsage, res.code)
	res = runCLI(t.Context(), t, "--data-dir", dataDir, "command", "export", "nope")
	assert.Equal(t, cli.ExitFailure, res.code)
	assert.Contains(t, res.stderr, `no command is named "nope"`)
}
