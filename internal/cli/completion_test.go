// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompletionKong covers the kong structure of the completion command.
func TestCompletionKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the completion command is a valid kong structure")
	kctx, err := parser.Parse([]string{"completion", "bash"})
	require.NoError(t, err)
	assert.Equal(t, "completion <shell>", kctx.Command())
	assert.Equal(t, completionBash, root.Completion.Shell)
}

// TestCompletionCoversTheTree covers that every script of every shell lists
// every top-level command and the subcommands of its group.
func TestCompletionCoversTheTree(t *testing.T) {
	t.Parallel()
	for shell, script := range map[string]string{
		"bash": bashScript(),
		"zsh":  zshScript(),
		"fish": fishScript(),
	} {
		for _, cmd := range topLevel {
			assert.Contains(t, script, cmd, "%s: %s", shell, cmd)
		}
		for cmd, subs := range commandTree {
			for _, sub := range subs {
				assert.Contains(t, script, sub, "%s: %s %s", shell, cmd, sub)
			}
		}
	}

	// The command runs and prints the script.
	var out bytes.Buffer
	env := &Env{Stdout: &out, Stderr: io.Discard, Config: nil}
	require.NoError(t, completionCmd{Shell: completionBash}.Run(context.Background(), env))
	assert.Contains(t, out.String(), "complete -F _streamcrew streamcrew")
}
