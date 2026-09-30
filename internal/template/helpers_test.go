// SPDX-License-Identifier: Apache-2.0

package template_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/template"
)

// errPlatform is the error of the failing test identifier.
var errPlatform = errors.New("platform unavailable")

// testArgs are the arguments that $arg<n>text of the test family reads.
func testArgs() []string {
	return []string{"one", "two"}
}

// testFamily returns fictional identifiers for the tests of the engine. The
// real families have their own tests.
func testFamily() template.Family {
	return template.Family{
		Name: "test",
		Identifiers: []template.Identifier{
			{Name: "username", Resolve: constant("Alice")},
			{Name: "time", Resolve: constant("1:45 PM")},
			{Name: "timedigits", Resolve: constant("13:45")},
			// No target user in the tests (B4).
			{Name: "targetusername", Resolve: func(context.Context, *template.Scope) (template.Value, bool, error) {
				return template.Value{}, false, nil
			}},
			// A platform API that does not answer (B23).
			{Name: "followage", Resolve: func(context.Context, *template.Scope) (template.Value, bool, error) {
				return template.Value{}, false, errPlatform
			}},
			// A value that looks like an identifier and needs escaping.
			{Name: "evil", Resolve: constant(`$username <b>&"'`)},
		},
		Patterns: []template.Pattern{{
			Name:     "arg<n>text",
			Prefixes: []string{"arg"},
			Match:    matchArg,
		}},
	}
}

// matchArg matches arg<n>text for the test arguments; n starts at 1.
func matchArg(token string) (int, template.Resolver) {
	rest, ok := strings.CutPrefix(token, "arg")
	if !ok || rest == "" || rest[0] < '1' || rest[0] > '9' {
		return 0, nil
	}
	digits := 1
	for digits < len(rest) && digits < 3 && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if !strings.HasPrefix(rest[digits:], "text") {
		return 0, nil
	}
	n := 0
	for _, c := range rest[:digits] {
		n = n*10 + int(c-'0')
	}
	return len("arg") + digits + len("text"), func(context.Context, *template.Scope) (template.Value, bool, error) {
		args := testArgs()
		if n > len(args) {
			return template.Value{}, false, nil
		}
		return template.TextValue(args[n-1]), true, nil
	}
}

// constant returns a resolver with a fixed text.
func constant(s string) template.Resolver {
	return func(context.Context, *template.Scope) (template.Value, bool, error) {
		return template.TextValue(s), true, nil
	}
}

// counting returns a resolver with a fixed text that counts its calls.
func counting(s string, calls *atomic.Int64) template.Resolver {
	return func(context.Context, *template.Scope) (template.Value, bool, error) {
		calls.Add(1)
		return template.TextValue(s), true, nil
	}
}

// newRegistry returns a registry with the test and character families plus
// extra.
func newRegistry(t testing.TB, extra ...template.Family) *template.Registry {
	t.Helper()
	r, err := template.NewRegistry(append([]template.Family{testFamily(), template.CharacterFamily()}, extra...)...)
	require.NoError(t, err)
	return r
}

// render parses and renders text as plain text and fails the test on an
// error.
func render(t *testing.T, e *template.Engine, text string, s *template.Scope) string {
	t.Helper()
	out, err := e.Render(t.Context(), template.Parse(text), s, template.Text)
	require.NoError(t, err)
	return out
}

// mapSource is a source with fixed names, like global values or counters.
type mapSource map[string]string

func (m mapSource) Match(_ context.Context, _ *template.Scope, token string) (int, template.Resolver, error) {
	for n := len(token); n > 0; n-- {
		if v, ok := m[token[:n]]; ok {
			return n, constant(v), nil
		}
	}
	return 0, nil, nil
}

// failingSource is a source whose names cannot be loaded.
type failingSource struct{ calls *atomic.Int64 }

func (f failingSource) Match(context.Context, *template.Scope, string) (int, template.Resolver, error) {
	f.calls.Add(1)
	return 0, nil, errPlatform
}

// logBuffer returns a logger that writes text lines into the returned
// buffer.
func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// renderGolden renders each template of testdata/<name>.txt, one per line,
// with e and s and compares the templates and results with
// testdata/<name>.golden. Lines with "#" are comments and go into the golden
// file unchanged; empty lines are left out.
func renderGolden(t *testing.T, e *template.Engine, s *template.Scope, name string) {
	t.Helper()
	in, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	require.NoError(t, err)
	var got bytes.Buffer
	for line := range strings.Lines(string(in)) {
		line = strings.TrimRight(line, "\r\n") // also for files with CRLF line ends
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
			got.WriteString(line + "\n")
		default:
			got.WriteString("in:  " + strconv.Quote(line) + "\n")
			got.WriteString("out: " + strconv.Quote(render(t, e, line, s)) + "\n\n")
		}
	}
	golden(t, name, got.Bytes())
}

// golden compares got with testdata/<name>.golden. With
// STREAMCREW_UPDATE_GOLDEN=1 it writes the file first (Code-ADR-0006).
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Clean(filepath.Join("testdata", name+".golden"))
	if os.Getenv("STREAMCREW_UPDATE_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "create it with STREAMCREW_UPDATE_GOLDEN=1")
	assert.Equal(t, string(want), string(got))
}
