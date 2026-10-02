// SPDX-License-Identifier: Apache-2.0

package host_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/host"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// update reports whether golden files are written first (Code-ADR-0006).
func update() bool {
	return os.Getenv("STREAMCREW_UPDATE_GOLDEN") != ""
}

// logs is a log that tests read.
type logs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// opener records what it opens.
type opener struct {
	mu    sync.Mutex
	paths []string
	envs  [][]string
	err   error
}

func (o *opener) Open(_ context.Context, path string, env []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.paths = append(o.paths, path)
	o.envs = append(o.envs, env)
	return o.err
}

// env is the environment the tests give programs.
func env() []string {
	return []string{"MARK=present"}
}

// fixture is a running engine with the host types.
type fixture struct {
	t         *testing.T
	reg       *action.Registry
	harness   *actiontest.Harness
	templates *template.Engine
	opener    *opener
	logs      *logs
	lines     *lines
}

// newFixture returns a fixture whose core has the capabilities granted.
// Create it inside synctest.Test.
func newFixture(t *testing.T, granted ...capability.Capability) *fixture {
	t.Helper()
	f := &fixture{t: t, opener: &opener{}, logs: &logs{}, lines: &lines{}}
	f.reg, f.templates = registry(t, f.opener, f.logs, granted...)
	f.harness = actiontest.NewHarness(t, f.reg)
	return f
}

// roots are the released roots of the tests.
type roots map[string]string

func (r roots) Root(name string) (string, bool) {
	dir, ok := r[name]
	return dir, ok
}

// registry returns the host types with a template engine that knows the
// arguments and the values of the run, and no released roots.
func registry(t *testing.T, o host.Opener, log *logs, granted ...capability.Capability) (*action.Registry, *template.Engine) {
	t.Helper()
	return registryWith(t, host.Ports{Opener: o, Roots: roots{}, Logger: slog.New(slog.NewTextHandler(log, nil))}, granted...)
}

// registryWith returns the host types with p, completed by a template
// engine that knows the arguments and the values of the run, the
// environment of the tests and, unless p has one, a source of random
// numbers that always draws the last.
func registryWith(t *testing.T, p host.Ports, granted ...capability.Capability) (*action.Registry, *template.Engine) {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	p.Templates = template.New(identifiers)
	if p.Env == nil {
		p.Env = env()
	}
	if p.IntN == nil {
		p.IntN = func(n int) int { return n - 1 }
	}
	templates := p.Templates
	ds, err := host.Descriptors(p)
	require.NoError(t, err)
	set, err := capability.NewSet(granted...)
	require.NoError(t, err)
	reg, err := action.NewRegistry(set, ds...)
	require.NoError(t, err)
	return reg, templates
}

// program returns an external program action of the document doc,
// without "type".
func (f *fixture) program(doc string) host.ExternalProgram {
	f.t.Helper()
	d, ok := f.reg.Descriptor(host.TypeExternalProgram)
	require.True(f.t, ok)
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err, doc)
	require.NoError(f.t, a.Validate(), doc)
	p, ok := a.(host.ExternalProgram)
	require.True(f.t, ok)
	return p
}

// run returns an action that runs the test program with args and saves
// its output.
func (f *fixture) run(args string) host.ExternalProgram {
	f.t.Helper()
	return f.program(`{"kind":"run","program":` + quote(self(f.t)) + `,"args":` + quote(args) + `,"saveOutput":true}`)
}

// quote returns s as a JSON string.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// show returns an action that renders the output of the last program and
// records it.
func (f *fixture) show() command.Action {
	return probe{fn: func(ctx context.Context, run *engine.Run) error {
		out, err := f.templates.Render(ctx, template.Parse(output), run.Scope(), template.Text)
		f.lines.add(out)
		return err
	}}
}

// start runs a command with the actions and the arguments.
func (f *fixture) start(args []string, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command("x", actions...), engine.Params{Args: args, ArgsText: strings.Join(args, " ")})
}

// lines records what show renders.
type lines struct {
	mu   sync.Mutex
	list []string
}

func (l *lines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.list = append(l.list, line)
}

func (l *lines) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.list...)
}

// probe is an action that runs fn.
type probe struct {
	fn func(ctx context.Context, run *engine.Run) error
}

func (probe) DocType() string                                      { return "probe" }
func (probe) Validate() error                                      { return nil }
func (probe) Enabled() bool                                        { return true }
func (p probe) Perform(ctx context.Context, run *engine.Run) error { return p.fn(ctx, run) }

// output is the template of the output of a program.
const output = "$externalprogramresult"

func TestConformance(t *testing.T) {
	t.Parallel()
	reg, _ := registry(t, &opener{}, &logs{})
	d, ok := reg.Descriptor(host.TypeExternalProgram)
	require.True(t, ok)
	assert.Equal(t, []capability.Capability{capability.HostProcess}, d.Capabilities)
	assert.Equal(t, []string{host.ResultOutput}, d.Results)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "run", Doc: `{"type":"external_program","kind":"run","program":"/usr/bin/obs-cli","args":"scene \"Main Scene\" $arg1text","showWindow":false,"timeout":"$arg2text * 2","saveOutput":true}`, Valid: true},
		{Name: "start", Doc: `{"type":"external_program","kind":"start","program":"notepad","args":"","showWindow":true}`, Valid: true},
		{Name: "start with the defaults", Doc: `{"type":"external_program","kind":"start","program":"notepad"}`, Valid: true},
		{Name: "run with the defaults", Doc: `{"type":"external_program","enabled":false,"kind":"run","program":"backup.sh"}`, Valid: true},
		{Name: "open", Doc: `{"type":"external_program","kind":"open","path":"C:\\clips\\$arg1text.mp4"}`, Valid: true},
		{Name: "kind missing", Doc: `{"type":"external_program","program":"x"}`},
		{Name: "unknown kind", Doc: `{"type":"external_program","kind":"shell","program":"x"}`},
		{Name: "program missing", Doc: `{"type":"external_program","kind":"start"}`},
		{Name: "empty program", Doc: `{"type":"external_program","kind":"run","program":""}`},
		{Name: "start with a timeout", Doc: `{"type":"external_program","kind":"start","program":"x","timeout":10}`},
		{Name: "start saves no output", Doc: `{"type":"external_program","kind":"start","program":"x","saveOutput":false}`},
		{Name: "timeout 0", Doc: `{"type":"external_program","kind":"run","program":"x","timeout":0}`},
		{Name: "timeout too long", Doc: `{"type":"external_program","kind":"run","program":"x","timeout":3601}`},
		{Name: "open with a program", Doc: `{"type":"external_program","kind":"open","path":"x","program":"y"}`},
		{Name: "open waits not", Doc: `{"type":"external_program","kind":"open","path":"x","saveOutput":true}`},
		{Name: "open without path", Doc: `{"type":"external_program","kind":"open"}`},
		{Name: "empty path", Doc: `{"type":"external_program","kind":"open","path":""}`},
	}}.Run(t)

	// The schema cannot see an unclosed quote; Validate rejects it.
	_, err := d.Decode([]byte(`{"kind":"start","program":"x","args":"\"a b"}`), json.DefaultOptionsV2())
	require.NoError(t, err)
	a, _ := d.Decode([]byte(`{"kind":"start","program":"x","args":"\"a b"}`), json.DefaultOptionsV2())
	require.ErrorContains(t, a.Validate(), "args: invalid action: a double quote is not closed")
}

// TestArguments covers actions.md B111 and B219: the arguments are split
// into words before rendering, so an inserted value stays one argument,
// and no shell sees it.
func TestArguments(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		in := f.start([]string{"x; rm -rf ~", "two words"},
			f.run(`echo $arg1text "quoted $arg2text" a"b c"d "" plain`), f.show())
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"x; rm -rf ~\nquoted two words\nab cd\n\nplain\n"}, f.lines.get())
	})
}

// TestOutput covers actions.md B113 and B114: standard output and standard
// error are saved together; an exit code other than 0 lets the action fail
// after the output is saved.
func TestOutput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		in := f.start(nil, f.run("exit 3"), f.show(), f.run("exit 0"), f.show())
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "program: exit code 3", in.Errors[0].Message)
		assert.Equal(t, []int{1}, in.Errors[0].Path)
		got := f.lines.get()
		require.Len(t, got, 2)
		for _, out := range got {
			assert.Contains(t, out, "bye\n")
			assert.Contains(t, out, "on stderr\n")
		}
	})
}

// TestOutputLimit covers actions.md B114: at most 1 MiB is saved, the rest
// is dropped with a warning.
func TestOutputLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		in := f.start(nil, f.run("flood 1100000"), f.show())
		assert.Empty(t, in.Errors)
		assert.Len(t, f.lines.get()[0], host.MaxOutput)
		assert.Contains(t, f.logs.String(), "program output cut")
		assert.Contains(t, f.logs.String(), "dropped_bytes=51425")
	})
}

// TestWithoutSaving: without saveOutput no result value is set.
func TestWithoutSaving(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		quiet := f.program(`{"kind":"run","program":` + quote(self(t)) + `,"args":"echo hi"}`)
		in := f.start(nil, quiet, f.show())
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{output}, f.lines.get())
	})
}

// TestDirectoryAndEnvironment covers actions.md B117: the program runs in
// the directory it lies in and gets the environment of the ports only.
func TestDirectoryAndEnvironment(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		in := f.start(nil, f.run("pwd"), f.show(), f.run("env"), f.show())
		assert.Empty(t, in.Errors)
		got := f.lines.get()
		dir, err := filepath.EvalSymlinks(filepath.Dir(self(t)))
		require.NoError(t, err)
		wd, err := filepath.EvalSymlinks(strings.TrimSpace(got[0]))
		require.NoError(t, err)
		assert.Equal(t, dir, wd)
		assert.Contains(t, got[1], "MARK=present\n")
		assert.NotContains(t, got[1], "PATH=", "nothing of the environment of the core")
	})
}

// TestStart covers actions.md B112: the action starts the program and is
// done.
func TestStart(t *testing.T) {
	t.Parallel()
	id, path := marker(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		start := f.program(`{"kind":"start","program":` + quote(self(t)) + `,"args":"touch ` + strconv.Itoa(id) + `"}`)
		in := f.start(nil, start, f.show())
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{output}, f.lines.get(), "start saves no output")
	})
	data, err := os.ReadFile(path)
	require.NoError(t, err, "the program ran")
	assert.Equal(t, "touched", string(data))
}

// TestOpen covers actions.md B116: open hands the rendered path and the
// environment to the opener.
func TestOpen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		in := f.start([]string{"clip 1"}, f.program(`{"kind":"open","path":"/clips/$arg1text.mp4"}`))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"/clips/clip 1.mp4"}, f.opener.paths)
		assert.Equal(t, [][]string{env()}, f.opener.envs)

		f.opener.err = errors.New("no program for .mp4")
		in = f.start([]string{"clip 1"}, f.program(`{"kind":"open","path":"/clips/$arg1text.mp4"}`))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, "path: no program for .mp4", in.Errors[0].Message)
	})
}

// TestFails: a program that cannot be found, an empty program or path
// after rendering and a timeout out of range let the action fail.
func TestFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, capability.HostProcess)
		p := map[string]string{"blank": " ", "zero": "0"}
		params := engine.Params{Values: map[string]template.Value{}}
		for k, v := range p {
			params.Values[k] = template.TextValue(v)
		}
		in := f.harness.Start(f.harness.Command("x",
			f.program(`{"kind":"start","program":"/does/not/exist"}`),
			f.program(`{"kind":"run","program":"$blank"}`),
			f.program(`{"kind":"open","path":"$blank"}`),
			f.program(`{"kind":"run","program":`+quote(self(t))+`,"args":"echo","timeout":"$zero"}`),
		), params)
		require.Len(t, in.Errors, 4)
		assert.Contains(t, in.Errors[0].Message, "program: exec: ")
		assert.Equal(t, "program: invalid action: empty after rendering", in.Errors[1].Message)
		assert.Equal(t, "path: invalid action: empty after rendering", in.Errors[2].Message)
		assert.Equal(t, "timeout: invalid action: 0 is not between 1 and 3600", in.Errors[3].Message)
		assert.Empty(t, f.opener.paths)
	})
}

// TestCapability covers actions.md B7: without host:process the action
// does not run.
func TestCapability(t *testing.T) {
	t.Parallel()
	id, path := marker(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		in := f.start(nil, f.program(`{"kind":"start","program":`+quote(self(t))+`,"args":"touch `+strconv.Itoa(id)+`"}`))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "host:process")
	})
	assert.NoFileExists(t, path)
}

func TestPorts(t *testing.T) {
	t.Parallel()
	full := host.Ports{Templates: template.New(nil), Env: []string{}, Opener: &opener{}, Roots: roots{},
		IntN: func(int) int { return 0 }, Logger: slog.New(slog.DiscardHandler)}
	_, err := host.Descriptors(full)
	require.NoError(t, err, "an empty environment is allowed")
	for name, edit := range map[string]func(*host.Ports){
		"templates": func(p *host.Ports) { p.Templates = nil },
		"env":       func(p *host.Ports) { p.Env = nil },
		"opener":    func(p *host.Ports) { p.Opener = nil },
		"roots":     func(p *host.Ports) { p.Roots = nil },
		"random":    func(p *host.Ports) { p.IntN = nil },
		"logger":    func(p *host.Ports) { p.Logger = nil },
	} {
		p := full
		edit(&p)
		_, err := host.Descriptors(p)
		require.Error(t, err, name)
	}
	assert.NotNil(t, host.SystemOpener())
}
