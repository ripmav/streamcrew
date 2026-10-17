// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/config"
)

// TestTokenKong covers the kong structure of the token commands.
func TestTokenKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the token commands are a valid kong structure")
	kctx, err := parser.Parse([]string{"token", "create", "tui", "--scope", "read", "--scope", "control"})
	require.NoError(t, err)
	assert.Equal(t, "token create <name>", kctx.Command())
	assert.Equal(t, "tui", root.Token.Create.Name)
	assert.Equal(t, []string{"read", "control"}, root.Token.Create.Scopes)
	_, err = parser.Parse([]string{"token", "revoke", "tui"})
	require.NoError(t, err)
	assert.Equal(t, "tui", root.Token.Revoke.IDOrName)
}

// TestTokenLifecycle covers create, list and revoke end to end: the value
// is printed once, the token validates, and the revoked one is gone.
func TestTokenLifecycle(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	cfg := config.Config{
		DataDir:         dataDir,
		Mode:            config.ModeDaemon,
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "debug", Format: "json", File: true, MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	env := &Env{Stdout: &syncedLog{}, Stderr: &syncedLog{}, Config: &cfg}

	stdout := &syncedLog{}
	env.Stdout = stdout
	err := (tokenCreateCmd{Name: "tui", Scopes: []string{"control"}}).Run(env)
	require.NoError(t, err)
	value := strings.TrimSpace(stdout.String())
	assert.True(t, strings.HasPrefix(value, "sc_"), "the token has the sc_ prefix")

	s := apitoken.Open(dataDir)
	tok, ok := s.Validate(value)
	require.True(t, ok, "the printed token validates")
	assert.Equal(t, "tui", tok.Name)

	stdout.Reset()
	env.Stdout = stdout
	require.NoError(t, (tokenListCmd{}).Run(env))
	assert.Contains(t, stdout.String(), "tui")

	stdout.Reset()
	env.Stdout = stdout
	require.NoError(t, (tokenRevokeCmd{IDOrName: "tui"}).Run(env))
	assert.Contains(t, stdout.String(), "revoked")
	_, ok = s.Validate(value)
	assert.False(t, ok, "the revoked token is gone")

	err = (tokenRevokeCmd{IDOrName: "tui"}).Run(env)
	require.Error(t, err, "revoking a missing token fails")
}
