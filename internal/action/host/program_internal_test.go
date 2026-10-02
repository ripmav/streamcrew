// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWords covers actions.md B111: white space separates words, double
// quotes join, without escapes.
func TestWords(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"":                          nil,
		"   ":                       nil,
		"a b\tc\nd":                 {"a", "b", "c", "d"},
		`"a b" c`:                   {"a b", "c"},
		`x"y z"w`:                   {"xy zw"},
		`"" a`:                      {"", "a"},
		`$arg1text "$arg2text x"`:   {"$arg1text", "$arg2text x"},
		"  lead trail  ":            {"lead", "trail"},
		`"tab	inside"`:              {"tab\tinside"},
		`scene "Main Scene" switch`: {"scene", "Main Scene", "switch"},
	} {
		got, err := words(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{`"open`, `a "b c`, `"a" "`} {
		_, err := words(in)
		require.ErrorIs(t, err, errOpenQuote, in)
	}
}

// program returns an action of kind run with the ports of the tests.
func program(t *testing.T, saveOutput bool) ExternalProgram {
	t.Helper()
	return ExternalProgram{
		Kind:   ProgramRun,
		Launch: &LaunchOptions{},
		Wait:   &WaitOptions{SaveOutput: saveOutput},
		ports:  &ports{Ports{Env: []string{}, Logger: slog.New(slog.DiscardHandler)}},
	}
}

// testBinary returns the path of the program of the tests, which TestMain
// of the external tests builds from testdata/helper next to the test binary
// (helper_test.go).
func testBinary(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	require.NoError(t, err)
	name := "streamcrew-host-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(bin), name)
}

// TestTimeout covers actions.md B115: a program that runs past its timeout
// is ended; the output read so far is saved, and the action fails.
func TestTimeout(t *testing.T) {
	t.Parallel()
	p := program(t, true)
	begin := time.Now()
	out, err := p.execute(t.Context(), testBinary(t), []string{"sleep", "1m"}, time.Second)
	require.ErrorIs(t, err, ErrTimeout)
	assert.Less(t, time.Since(begin), 30*time.Second, "the program was ended")
	assert.Equal(t, "started\n", out)
}

// TestTimeoutEndsGroup covers actions.md B115 on Unix: the programs that
// the program started end with it.
func TestTimeoutEndsGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ends only the program itself")
	}
	t.Parallel()
	// The program of the tests writes markers into its working directory,
	// its own directory (actions.md B117).
	id := int(time.Now().UnixNano() % 1_000_000_000)
	late := filepath.Join(filepath.Dir(testBinary(t)), "streamcrew-host-marker-"+strconv.Itoa(id))
	t.Cleanup(func() { _ = os.Remove(late) })
	p := program(t, true)
	out, err := p.execute(t.Context(), testBinary(t), []string{"spawn", strconv.Itoa(id)}, time.Second)
	require.ErrorIs(t, err, ErrTimeout)
	assert.Equal(t, "spawned\n", out)
	time.Sleep(3 * time.Second) // the started program would touch the marker after 2 s
	assert.NoFileExists(t, late)
}

// TestCanceled: when the instance is canceled, the program ends and the
// action returns the error of the context, not a timeout.
func TestCanceled(t *testing.T) {
	t.Parallel()
	p := program(t, false)
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(500*time.Millisecond, cancel)
	_, err := p.execute(ctx, testBinary(t), []string{"sleep", "1m"}, time.Minute)
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, ErrTimeout)
}

// TestOutputBuffer covers actions.md B114: the first bytes are kept, the
// rest is counted.
func TestOutputBuffer(t *testing.T) {
	t.Parallel()
	o := &output{limit: 5}
	n, err := o.Write([]byte("abc"))
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	n, err = o.Write([]byte("defgh"))
	require.NoError(t, err)
	assert.Equal(t, 5, n, "a write never fails")
	assert.Equal(t, "abcde", o.String())
	assert.Equal(t, int64(3), o.dropped)
}

// TestOpenCommand covers actions.md B116: the path is one argument of the
// opener of the system, without a shell.
func TestOpenCommand(t *testing.T) {
	t.Parallel()
	name, args := openCommand("/clips/a b; rm -rf ~.mp4")
	switch runtime.GOOS {
	case "windows":
		assert.Equal(t, "rundll32.exe", name)
		assert.Equal(t, []string{"url.dll,FileProtocolHandler", "/clips/a b; rm -rf ~.mp4"}, args)
	case "darwin":
		assert.Equal(t, "open", name)
		assert.Equal(t, []string{"/clips/a b; rm -rf ~.mp4"}, args)
	default:
		assert.Equal(t, "xdg-open", name)
		assert.Equal(t, []string{"/clips/a b; rm -rf ~.mp4"}, args)
	}
}
