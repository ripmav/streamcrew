// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/profile"
)

// startMockCore starts a core with the mock platform in the process and
// returns the base URL of its API and the data directory (with the local
// admin token the api requires).
func startMockCore(t *testing.T) (base, dataDir string) {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:         dir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		ObsPort:         4455, Stream: config.StreamConfig{Buffer: 1024, CatchUp: 256},
		Log: config.LogConfig{Level: "info", Format: "text", MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	var mp *mock.Platform
	a, err := app.New(ctx, cfg,
		app.WithConsole(io.Discard), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver, mock.WithPublisher(b.Publisher))
			return mp, err
		}),
	)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond, "the core to be ready")
	require.Eventually(t, func() bool { return mp.Status().Connected() }, 5*time.Second, 10*time.Millisecond, "the mock platform to connect")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	base = "http://" + mustServerAddr(t, a)
	return base, dir
}

// TestChatEventKong covers the kong structure of the chat and event
// commands.
func TestChatEventKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the chat and event commands are a valid kong structure")
	lines := map[string]string{
		"chat send --as alice hello":                         "chat send <text>",
		"chat send --as-streamer --channel other hello":      "chat send <text>",
		"event simulate channel.follow --user dave":          "event simulate <type>",
		"event simulate chat.message --user alice --text hi": "event simulate <type>",
		"--server http://core:8740 chat send hello world":    "chat send <text>",
		"--token abc123 event simulate channel.follow":       "event simulate <type>",
	}
	for line, want := range lines {
		kctx, err := parser.Parse(strings.Fields(line))
		require.NoError(t, err, "line %q", line)
		assert.Equal(t, want, kctx.Command(), "line %q", line)
	}
	_, err = parser.Parse(strings.Fields("--server http://core:8740 chat send hello world"))
	require.NoError(t, err)
	assert.Equal(t, "http://core:8740", root.Server)
	assert.Equal(t, []string{"hello", "world"}, root.Chat.Send.Text)
	_, err = parser.Parse(strings.Fields("--token abc123 event simulate channel.follow"))
	require.NoError(t, err)
	assert.Equal(t, "abc123", root.APIToken)
}

// TestChatSendAndEventSimulate covers the chat and event API commands
// (roadmap 6.3) against a running core with the mock platform.
func TestChatSendAndEventSimulate(t *testing.T) {
	t.Parallel()
	base, dir := startMockCore(t)

	var out bytes.Buffer
	env := &Env{Stdout: &out, Stderr: io.Discard, Config: &config.Config{DataDir: dir}, Server: base}

	require.NoError(t, chatSendCmd{Text: []string{"hello", "core"}}.Run(t.Context(), env))
	assert.Contains(t, out.String(), "sent")

	out.Reset()
	require.NoError(t, chatSendCmd{As: "alice", Text: []string{"hi"}}.Run(t.Context(), env))
	assert.Contains(t, out.String(), "sent as alice")

	out.Reset()
	require.NoError(t, eventSimulateCmd{Type: "channel.follow", User: "dave"}.Run(t.Context(), env))
	assert.Contains(t, out.String(), "simulated channel.follow")

	// An unreachable core is an error.
	envDown := &Env{Stdout: io.Discard, Stderr: io.Discard, Config: &config.Config{}, Server: "http://127.0.0.1:1"}
	require.Error(t, eventSimulateCmd{Type: "channel.follow"}.Run(t.Context(), envDown))
}
