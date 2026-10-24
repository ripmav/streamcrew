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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/config"
)

// TestCommandUserKong covers the kong structure of the API subcommands of
// command and user.
func TestCommandUserKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the command and user API subcommands are a valid kong structure")
	for line, want := range map[string]string{
		"command list --kind chat":                        "command list",
		"command show hug":                                "command show <name>",
		"command run hug a b --channel other":             "command run <name> <args>",
		"command delete hug":                              "command delete <name>",
		"command enable hug":                              "command enable <name>",
		"command disable hug":                             "command disable <name>",
		"user list --stat messages --limit 10":            "user list",
		"user show alice --platform mock":                 "user show <login>",
		"user set alice --platform mock --role moderator": "user set <login>",
	} {
		kctx, err := parser.Parse(strings.Fields(line))
		require.NoError(t, err, "line %q", line)
		assert.Equal(t, want, kctx.Command(), "line %q", line)
	}
}

// TestCommandUserAPICommands covers the command and user API commands
// (roadmap 6.3) against a running core with the mock platform.
func TestCommandUserAPICommands(t *testing.T) {
	t.Parallel()
	base, dir := startMockCore(t)
	ctx := t.Context()

	// A command the commands work on.
	cc := streamcrewv1alpha1connect.NewCommandServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(apiToken(t, dir))))
	_, err := cc.Import(ctx, &v1alpha1.ImportCommandsRequest{
		Document: mockConsoleExampleCommands,
		Format:   "yaml",
	})
	require.NoError(t, err)

	var out bytes.Buffer
	env := &Env{Stdout: &out, Stderr: io.Discard, Config: &config.Config{DataDir: dir}, Server: base}

	// command list shows the imported command.
	require.NoError(t, commandListCmd{}.Run(ctx, env))
	assert.Contains(t, out.String(), "Hello")

	out.Reset()
	require.NoError(t, commandShowCmd{Name: "Hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "Hello (chat)")
	assert.Contains(t, out.String(), "trigger: hello")

	// JSON output is machine readable.
	out.Reset()
	require.NoError(t, commandShowCmd{Output: "json", Name: "Hello"}.Run(ctx, env))
	var shown v1alpha1.GetCommandResponse
	require.NoError(t, json.Unmarshal(out.Bytes(), &shown))
	assert.Equal(t, "Hello", shown.Summary.Name)

	// run starts an instance of the command.
	out.Reset()
	require.NoError(t, commandRunCmd{Name: "Hello", Args: []string{"world"}}.Run(ctx, env))
	assert.Contains(t, out.String(), "running as")

	// disable and enable flip the switch.
	out.Reset()
	require.NoError(t, commandDisableCmd{Name: "Hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "disabled Hello")
	out.Reset()
	require.NoError(t, commandShowCmd{Name: "Hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "disabled")
	out.Reset()
	require.NoError(t, commandEnableCmd{Name: "Hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "enabled Hello")

	// A user shows up after a simulated chat message.
	_, err = simulateEvent(ctx, env, "chat.message", "alice", "hi")
	require.NoError(t, err)
	out.Reset()
	require.NoError(t, userListCmd{}.Run(ctx, env))
	assert.Contains(t, out.String(), "alice")
	out.Reset()
	require.NoError(t, userShowCmd{Login: "alice", Platform: "mock"}.Run(ctx, env))
	assert.Contains(t, out.String(), "mock: alice")
	out.Reset()
	require.NoError(t, userSetCmd{Login: "alice", Platform: "mock", Role: "moderator"}.Run(ctx, env))
	assert.Contains(t, out.String(), "updated")

	// An unknown name is an error.
	require.Error(t, commandShowCmd{Name: "nope"}.Run(ctx, env))
	require.Error(t, userShowCmd{Login: "nope", Platform: "mock"}.Run(ctx, env))

	// delete removes the command.
	out.Reset()
	require.NoError(t, commandDeleteCmd{Name: "Hello"}.Run(ctx, env))
	assert.Contains(t, out.String(), "deleted Hello")
	require.Error(t, commandShowCmd{Name: "Hello"}.Run(ctx, env))
}

// simulateEvent sends a Simulate request through the stream client.
func simulateEvent(ctx context.Context, e *Env, eventType, user, text string) (*v1alpha1.SimulateEventResponse, error) {
	return e.streamClient().Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: eventType,
		User:      user,
		Text:      text,
	})
}
