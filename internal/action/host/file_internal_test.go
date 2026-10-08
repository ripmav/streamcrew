// SPDX-License-Identifier: MIT

package host

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileConcurrentAppend covers actions.md B109 and B218: instances that
// append to the same file at the same time lose no line.
func TestFileConcurrentAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()
	p := &ports{}
	f, ok := newFile(p, FileAppend)
	require.True(t, ok)

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			in := inputs{path: "log.txt", text: strconv.Itoa(i)}
			assert.NoError(t, p.locked(dir, in.path, func() error { return f.change(nil, root, in) }))
		})
	}
	wg.Wait()

	data, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	slices.SortFunc(lines, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	want := make([]string, n)
	for i := range want {
		want[i] = strconv.Itoa(i)
	}
	assert.Equal(t, want, lines)
	assert.Empty(t, p.files.held, "no lock is left")
}

// TestFileLocks: accesses to different files do not wait for each other,
// and a lock is released for the next access.
func TestFileLocks(t *testing.T) {
	t.Parallel()
	var l fileLocks
	unlockA := l.lock("a")
	unlockB := l.lock("b") // would block if the files shared a lock
	unlockA()
	unlockA2 := l.lock("a")
	unlockA2()
	unlockB()
	assert.Empty(t, l.held)
}

// TestSplitAndJoinLines covers actions.md B103.
func TestSplitAndJoinLines(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"":             {},
		"\n":           {""},
		"a":            {"a"},
		"a\n":          {"a"},
		"a\n\n":        {"a", ""},
		"a\r\nb\r\n":   {"a", "b"},
		"a\nb":         {"a", "b"},
		"a\r\n\r\nb\n": {"a", "", "b"},
		"x\ry\n":       {"x\ry"},
	} {
		assert.Equal(t, want, splitLines(in), "%q", in)
	}
	assert.Empty(t, joinLines(nil))
	assert.Equal(t, "a\n\nb\n", joinLines([]string{"a", "", "b"}))
}
