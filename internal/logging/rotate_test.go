// SPDX-License-Identifier: MIT

package logging_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/logging"
)

// backups returns the names of the rotated files in dir, oldest first.
func backups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "streamcrew-") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func TestRotatingFileRotatesAndPrunes(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "logs")
	path := filepath.Join(dir, "streamcrew.log")
	f, err := logging.OpenRotatingFile(path, 10, 2)
	require.NoError(t, err)

	for _, line := range []string{"aaaa\n", "bbbb\n", "cccc\n", "dddd\n", "eeee\n", "ffff\n", "gggg\n"} {
		n, err := f.Write([]byte(line))
		require.NoError(t, err)
		require.Equal(t, len(line), n)
	}
	require.NoError(t, f.Close())

	// Two lines fit into 10 bytes: ab, cd, ef rotated, g current. Only the
	// two newest rotated files are kept.
	assert.Equal(t, "gggg\n", read(t, path))
	names := backups(t, dir)
	require.Len(t, names, 2)
	assert.Equal(t, "cccc\ndddd\n", read(t, filepath.Join(dir, names[0])))
	assert.Equal(t, "eeee\nffff\n", read(t, filepath.Join(dir, names[1])))
	for _, name := range names {
		assert.Regexp(t, `^streamcrew-\d{8}T\d{6}\.\d{9}Z\.log$`, name)
	}
}

func TestRotatingFileAppendsToExistingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "streamcrew.log")
	require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o600))

	f, err := logging.OpenRotatingFile(path, 100, 1)
	require.NoError(t, err)
	_, err = f.Write([]byte("new\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	assert.Equal(t, "old\nnew\n", read(t, path))
}

func TestRotatingFileOversizedWrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "streamcrew.log")
	f, err := logging.OpenRotatingFile(path, 4, 5)
	require.NoError(t, err)

	for _, line := range []string{"a\n", "0123456789\n", "b\n"} {
		_, err := f.Write([]byte(line))
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	assert.Equal(t, "b\n", read(t, path))
	names := backups(t, dir)
	require.Len(t, names, 2)
	assert.Equal(t, "a\n", read(t, filepath.Join(dir, names[0])))
	assert.Equal(t, "0123456789\n", read(t, filepath.Join(dir, names[1])))
}

func TestRotatingFileKeepsNoBackups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f, err := logging.OpenRotatingFile(filepath.Join(dir, "streamcrew.log"), 2, 0)
	require.NoError(t, err)
	for range 3 {
		_, err := f.Write([]byte("x\n"))
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	assert.Empty(t, backups(t, dir))
}

func TestRotatingFileIgnoresForeignFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	foreign := []string{"streamcrew-notes.log", "streamcrew-20260101T000000.000000000Z.txt", "other.log"}
	for _, name := range foreign {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}
	f, err := logging.OpenRotatingFile(filepath.Join(dir, "streamcrew.log"), 2, 0)
	require.NoError(t, err)
	for range 3 {
		_, err := f.Write([]byte("x\n"))
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())

	for _, name := range foreign {
		assert.FileExists(t, filepath.Join(dir, name))
	}
}

func TestRotatingFileConcurrentWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "streamcrew.log")
	f, err := logging.OpenRotatingFile(path, 64, 1000)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				_, err := f.Write([]byte("0123456789\n"))
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()
	require.NoError(t, f.Close())

	total := len(read(t, path))
	for _, name := range backups(t, dir) {
		content := read(t, filepath.Join(dir, name))
		assert.LessOrEqual(t, len(content), 64)
		total += len(content)
	}
	assert.Equal(t, 8*50*11, total, "no line may be lost")
}

func TestRotatingFileWriteAfterClose(t *testing.T) {
	t.Parallel()
	f, err := logging.OpenRotatingFile(filepath.Join(t.TempDir(), "streamcrew.log"), 10, 1)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, f.Close(), "closing twice is harmless")

	_, err = f.Write([]byte("x"))
	assert.Error(t, err)
}

func TestRotatingFilePermissions(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes do not apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "logs")
	path := filepath.Join(dir, "streamcrew.log")
	f, err := logging.OpenRotatingFile(path, 10, 1)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	dirInfo, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	fileInfo, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

func TestOpenRotatingFileErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := logging.OpenRotatingFile(filepath.Join(dir, "streamcrew.log"), 0, 1)
	require.ErrorContains(t, err, "max size")

	_, err = logging.OpenRotatingFile(dir+string(filepath.Separator), 10, 1)
	require.ErrorContains(t, err, "no file name")
}
