// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/profile"
)

// startCore starts a core without a platform in the process and returns
// the base URL of its API.
func startCore(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:         dir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	a, err := app.New(ctx, cfg, app.WithConsole(io.Discard), app.WithKeyring(nil))
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond, "the core to be ready")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return "http://" + mustServerAddr(t, a)
}

// TestCounterKong covers the kong structure of the counter commands.
func TestCounterKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the counter commands are a valid kong structure")
	for line, want := range map[string]string{
		"counter list":                         "counter list",
		"counter list --prefix he":             "counter list",
		"counter get hello":                    "counter get <name>",
		"counter set hello 5":                  "counter set <name> <value>",
		"counter add hello 3":                  "counter add <name> <amount>",
		"counter add hello":                    "counter add <name>",
		"counter reset hello":                  "counter reset <name>",
		"counter list -o json":                 "counter list",
		"--server http://core:1 counter get x": "counter get <name>",
	} {
		kctx, err := parser.Parse(strings.Fields(line))
		require.NoError(t, err, "line %q", line)
		assert.Equal(t, want, kctx.Command(), "line %q", line)
	}
}

// TestCounterCommands covers the counter API commands (roadmap 6.3)
// against a running core, in text and in JSON.
func TestCounterCommands(t *testing.T) {
	t.Parallel()
	base := startCore(t)
	ctx := t.Context()

	// A counter the commands work on.
	cc := streamcrewv1alpha1connect.NewCounterServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))
	_, err := cc.Create(ctx, &v1alpha1.CreateCounterRequest{Name: "hello", Value: "10", Step: "2"})
	require.NoError(t, err)

	var out bytes.Buffer
	env := &Env{Stdout: &out, Stderr: io.Discard, Config: &config.Config{}, Server: base}

	require.NoError(t, counterListCmd{}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello")
	assert.Contains(t, out.String(), "10")

	out.Reset()
	require.NoError(t, counterGetCmd{Name: "hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello: 10")

	// JSON output is machine readable.
	out.Reset()
	require.NoError(t, counterGetCmd{Output: "json", Name: "hello"}.Run(ctx, env))
	var view v1alpha1.CounterView
	require.NoError(t, json.Unmarshal(out.Bytes(), &view))
	assert.Equal(t, "hello", view.Name)
	assert.Equal(t, "10", view.Value)

	require.NoError(t, counterSetCmd{Name: "hello", Value: "7"}.Run(ctx, env))
	out.Reset()
	require.NoError(t, counterGetCmd{Name: "hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello: 7")

	out.Reset()
	require.NoError(t, counterAddCmd{Name: "hello", Amount: "5"}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello: 12")

	out.Reset()
	require.NoError(t, counterAddCmd{Name: "hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello: 14", "an empty amount adds one step")

	out.Reset()
	require.NoError(t, counterResetCmd{Name: "hello"}.Run(ctx, env))
	out.Reset()
	require.NoError(t, counterGetCmd{Name: "hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "hello: 0")

	// An unknown counter is an error.
	out.Reset()
	require.Error(t, counterGetCmd{Name: "nope"}.Run(ctx, env))
}

// mustServerAddr waits for the core's http server to listen and returns
// its address: the core is ready while the first listener is still coming
// up, so the address can lag readiness by a moment.
func mustServerAddr(t *testing.T, a *app.App) string {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := a.HTTPServer().Addr()
		return ok
	}, 10*time.Second, 10*time.Millisecond, "the http server to listen")
	addr, _ := a.HTTPServer().Addr()
	return addr.String()
}
