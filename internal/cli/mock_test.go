// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/profile"
)

// mockConsoleExampleCommands are the example commands as code of the test
// (roadmap 3.6): a chat command and an event command for "app.started".
const mockConsoleExampleCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hello}
spec:
  triggers: [hello]
  actions:
    - {type: chat, kind: message, message: "Hello $userdisplayname!"}
---
apiVersion: streamcrew/v1alpha1
kind: EventCommand
metadata: {name: Greet}
spec:
  event: app.started
  actions:
    - {type: chat, kind: message, message: "Core started"}
`

// TestMockConsole is the end-to-end test of the mock console (roadmap 3.6):
// the example commands as YAML are loaded into a copy of the profile, the
// console, driven by a script, simulates a message of a user, and the chat
// commands of the message and of "app.started" run and reach the platform.
func TestMockConsole(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := config.Config{
		DataDir:         dataDir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "json", File: true, MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())

	// A profile the console can copy.
	mgr := profile.NewManager(dataDir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	// The files the console reads.
	dir := t.TempDir()
	yamlFile := filepath.Join(dir, "hello.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(yamlFile, []byte(mockConsoleExampleCommands), 0o600))
	scriptFile := filepath.Join(dir, "console.txt")
	script := "user add alice\nchat send --as alice !hello\nexit\n"
	require.NoError(t, os.WriteFile(scriptFile, []byte(script), 0o600))

	var out, errOut bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errOut, Config: &cfg, SecretKey: ""}
	cmd := mockCmd{
		Commands: []string{yamlFile},
		Script:   scriptFile,
		Streamer: "streamy",
		Bot:      "bot",
		appOpts:  []app.Option{app.WithKeyring(nil)},
	}
	require.NoError(t, cmd.Run(ctx, env))

	// The import loaded the commands into the copy, the user was added, and
	// the message was delivered to the platform.
	outStr := out.String()
	assert.Contains(t, outStr, "imported into the copy: 2 new, 0 replaced")
	assert.Contains(t, outStr, "user alice added")
	assert.Contains(t, outStr, ": delivered")

	// The simulated message triggered the chat command, and the event
	// command of "app.started" ran when the core was ready; the chat
	// actions of both reached the platform (the console log on the stderr).
	logs := errOut.String()
	assert.Contains(t, logs, `"text":"Hello alice!"`)
	assert.Contains(t, logs, `"text":"Core started"`)
}

// TestMockConsoleBadLine covers that an error of a console line is reported
// and the console keeps running.
func TestMockConsoleBadLine(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dataDir := filepath.Join(t.TempDir(), "data")
	cfg := config.Config{
		DataDir:         dataDir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "json", File: true, MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dataDir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	dir := t.TempDir()
	scriptFile := filepath.Join(dir, "console.txt")
	script := "frobble\nuser add alice\nexit\n"
	require.NoError(t, os.WriteFile(scriptFile, []byte(script), 0o600))

	var out, errOut bytes.Buffer
	env := &Env{Stdout: &out, Stderr: &errOut, Config: &cfg, SecretKey: ""}
	cmd := mockCmd{Script: scriptFile, appOpts: []app.Option{app.WithKeyring(nil)}}
	require.NoError(t, cmd.Run(ctx, env))
	assert.Contains(t, errOut.String(), "unknown command \"frobble\"")
	assert.Contains(t, out.String(), "user alice added")
}
