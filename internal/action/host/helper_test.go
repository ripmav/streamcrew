// SPDX-License-Identifier: MIT

package host_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestMain builds the program the tests start, testdata/helper, next to the
// test binary. It is a program of its own, not the test binary, so that a
// test that goes wrong can never make it run the tests again and start
// itself without end.
func TestMain(m *testing.M) {
	if err := buildHelper(); err != nil {
		fmt.Fprintln(os.Stderr, "build the helper program:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// helperName is the file name of the program of the tests.
func helperName() string {
	if runtime.GOOS == "windows" {
		return "streamcrew-host-helper.exe"
	}
	return "streamcrew-host-helper"
}

// helperPath returns where the program of the tests lies: next to the test
// binary, in a directory of its own for each test run.
func helperPath() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(bin), helperName()), nil
}

// buildHelper builds testdata/helper to helperPath.
func buildHelper() error {
	path, err := helperPath()
	if err != nil {
		return err
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, goTool, "build", "-o", path, "./testdata/helper").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// self returns the path of the program of the tests.
func self(t *testing.T) string {
	t.Helper()
	path, err := helperPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// markerName returns the name of the marker file number id, as
// testdata/helper writes it into its working directory, its own directory
// (actions.md B117).
func markerName(id int) string {
	return "streamcrew-host-marker-" + strconv.Itoa(id)
}

// marker returns a new marker: its number for the program and its path for
// the test. The test removes it when it ends.
func marker(t *testing.T) (id int, path string) {
	t.Helper()
	id = int(time.Now().UnixNano() % 1_000_000_000)
	path = filepath.Join(filepath.Dir(self(t)), markerName(id))
	t.Cleanup(func() { _ = os.Remove(path) })
	return id, path
}
