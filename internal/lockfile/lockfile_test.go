// SPDX-License-Identifier: MIT

package lockfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/lockfile"
)

func TestSecondAcquireFailsWithHolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "data", "streamcrew.lock")
	first, err := lockfile.Acquire(path)
	require.NoError(t, err)

	// A second open file in the same process conflicts like another process.
	_, err = lockfile.Acquire(path)
	locked, ok := errors.AsType[*lockfile.LockedError](err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, os.Getpid(), locked.PID)
	assert.WithinDuration(t, time.Now(), locked.Since, time.Minute)
	assert.Contains(t, locked.Error(), "PID")
	assert.Equal(t, path, locked.Path)

	require.NoError(t, first.Release())
	require.NoError(t, first.Release(), "releasing twice is harmless")

	again, err := lockfile.Acquire(path)
	require.NoError(t, err, "free again after release")
	require.NoError(t, again.Release())
}

func TestLockFilePermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	path := filepath.Join(t.TempDir(), "streamcrew.lock")
	l, err := lockfile.Acquire(path)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, l.Release()) })
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestLockedErrorWithoutDetails(t *testing.T) {
	t.Parallel()
	e := &lockfile.LockedError{Path: "/data/streamcrew.lock"}
	assert.Equal(t, "locked by another process: /data/streamcrew.lock", e.Error())
}
