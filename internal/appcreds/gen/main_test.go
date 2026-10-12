// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRepoRoot checks the go.mod walk from an arbitrary subdirectory —
// go run does not set the working directory to the package, so the
// generated file must land in the repository no matter where the command
// is started (ADR-0024).
func TestRepoRoot(t *testing.T) {
	t.Parallel()
	// Resolve the temp directory's symlinks: on macOS it sits under
	// "/var/folders", a symlink to "/private/var/folders", and the working
	// directory after a Chdir is resolved, so the walked-up root would not
	// match the unresolved path.
	root := t.TempDir()
	var err error
	root, err = filepath.EvalSymlinks(root)
	require.NoError(t, err)
	orig, err := os.Getwd()
	require.NoError(t, err)
	sub := filepath.Join(root, "internal", "appcreds", "gen")
	require.NoError(t, os.MkdirAll(sub, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/x\n"), 0o600))
	require.NoError(t, os.Chdir(sub))
	// Leave the temp directory before it is removed: on Windows a
	// directory that is the working directory cannot be deleted.
	t.Cleanup(func() { _ = os.Chdir(orig) })

	got, err := repoRoot()
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(root), got)
}
