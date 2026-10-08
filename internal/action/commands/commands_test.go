// SPDX-License-Identifier: MIT

package commands_test

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/commands"
	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/user"
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

// fixture is a running engine with the command action, its store and its
// log.
type fixture struct {
	t        *testing.T
	harness  *actiontest.Harness
	commands *actiontest.Commands
	reg      *action.Registry
	logs     *logs
	rec      *recorder
}

// newFixture returns a fixture whose engine has the options opts. Create it
// inside synctest.Test.
func newFixture(t *testing.T, opts ...engine.Option) *fixture {
	t.Helper()
	f := &fixture{t: t, commands: actiontest.NewCommands(), logs: &logs{}, rec: &recorder{}}
	f.reg = registry(t, f.commands, f.logs)
	f.harness = actiontest.NewHarnessWith(t, f.reg, f.commands, opts...)
	return f
}

// registry returns the command action with the ports of the tests.
func registry(t *testing.T, switches commands.Switches, log *logs) *action.Registry {
	t.Helper()
	identifiers, err := template.NewRegistry(template.ArgumentFamily(), template.RunFamily())
	require.NoError(t, err)
	ds, err := commands.Descriptors(commands.Ports{
		Templates: template.New(identifiers),
		Switches:  switches,
		Logger:    slog.New(slog.NewTextHandler(log, nil)),
	})
	require.NoError(t, err)
	reg, err := action.NewRegistry(capability.Set{}, ds...)
	require.NoError(t, err)
	return reg
}

// action returns a new command action of kind k for target, a command or
// a group by the kind.
func (f *fixture) action(k commands.Kind, target id.ID) commands.Command {
	f.t.Helper()
	d, ok := f.reg.Descriptor(commands.TypeCommand)
	require.True(f.t, ok)
	a, ok := d.New().(commands.Command)
	require.True(f.t, ok)
	if k != commands.KindRun {
		a.Run = nil
	}
	a.Kind = k
	switch k {
	case commands.KindEnableGroup, commands.KindDisableGroup:
		a.Group = target
	case commands.KindRun, commands.KindEnable, commands.KindDisable, commands.KindToggle, commands.KindStartCooldown:
		a.Command = target
	case commands.KindCancelAll, commands.KindPause, commands.KindUnpause, commands.KindPauseEntrance,
		commands.KindUnpauseEntrance, commands.KindExit:
	}
	require.NoError(f.t, a.Validate())
	return a
}

// run returns a command action that runs target with the options.
func (f *fixture) run(target id.ID, opts commands.RunOptions) commands.Command {
	a := f.action(commands.KindRun, target)
	a.Run = &opts
	require.NoError(f.t, a.Validate())
	return a
}

// start starts a command with the actions.
func (f *fixture) start(name string, p engine.Params, actions ...command.Action) engine.Instance {
	return f.harness.Start(f.harness.Command(name, actions...), p)
}

// enabled reports the switch of the command in the store.
func (f *fixture) enabled(commandID id.ID) bool {
	f.t.Helper()
	cmd, err := f.commands.Command(f.t.Context(), commandID)
	require.NoError(f.t, err)
	return cmd.Enabled
}

// recorder records what its actions do, in order.
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

// get returns the lines so far.
func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.lines)
}

// note returns an action that writes text.
func (r *recorder) note(text string) command.Action {
	return probe{fn: func(context.Context, *engine.Run) error {
		r.add(text)
		return nil
	}}
}

// args returns an action that writes the arguments of its run.
func (r *recorder) args() command.Action {
	return probe{fn: func(_ context.Context, run *engine.Run) error {
		r.add("args: " + strings.Join(run.Params().Args, ","))
		return nil
	}}
}

// fail returns an action that fails.
func (r *recorder) fail() command.Action {
	return probe{fn: func(context.Context, *engine.Run) error { return errors.New("boom") }}
}

// probe is an action that runs fn.
type probe struct {
	fn func(ctx context.Context, run *engine.Run) error
}

func (probe) DocType() string                                      { return "probe" }
func (probe) Validate() error                                      { return nil }
func (probe) Enabled() bool                                        { return true }
func (p probe) Perform(ctx context.Context, run *engine.Run) error { return p.fn(ctx, run) }

// requirements is a fake of engine.Requirements: it rejects while reject is
// set, waits while wait is set, and a cooldown needs a user.
type requirements struct {
	mu        sync.Mutex
	reject    bool
	wait      bool
	cooldowns []string
}

func (r *requirements) Apply(_ context.Context, _ command.Command, p engine.Params) (engine.Decision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.reject:
		return engine.Rejected(engine.Rejection{Requirement: "role", Reason: "not now"}), nil
	case r.wait:
		return engine.Waiting(), nil
	default:
		return engine.Met(p), nil
	}
}

func (*requirements) Notify(context.Context, command.Command, engine.Params, engine.Rejection) error {
	return nil
}

func (r *requirements) StartCooldown(_ context.Context, cmd command.Command, p engine.Params) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.User == nil {
		return errors.New("a cooldown per user needs a user")
	}
	r.cooldowns = append(r.cooldowns, cmd.Name)
	return nil
}

// Fixed IDs for the documents of the conformance test.
const (
	someCommand = "0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d"
	someGroup   = "0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1e"
)

func TestConformance(t *testing.T) {
	t.Parallel()
	reg := registry(t, actiontest.NewCommands(), &logs{})
	d, ok := reg.Descriptor(commands.TypeCommand)
	require.True(t, ok)
	cmd, group := `"command":"`+someCommand+`"`, `"group":"`+someGroup+`"`
	doc := func(members ...string) string {
		return `{"type":"command",` + strings.Join(members, ",") + `}`
	}
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "run", Doc: doc(`"kind":"run"`, cmd, `"wait":false`, `"checkRequirements":true`, `"args":{"from":"own","text":"$arg2text x"}`), Valid: true},
		{Name: "run with defaults", Doc: doc(`"kind":"run"`, cmd), Valid: true},
		{Name: "run with the arguments of the caller", Doc: doc(`"kind":"run"`, cmd, `"args":{"from":"caller"}`), Valid: true},
		{Name: "run with empty own arguments", Doc: doc(`"kind":"run"`, cmd, `"args":{"from":"own","text":""}`), Valid: true},
		{Name: "enable", Doc: doc(`"kind":"enable"`, cmd), Valid: true},
		{Name: "toggle", Doc: doc(`"kind":"toggle"`, cmd), Valid: true},
		{Name: "start cooldown", Doc: doc(`"kind":"start_cooldown"`, cmd), Valid: true},
		{Name: "enable group", Doc: doc(`"kind":"enable_group"`, group), Valid: true},
		{Name: "pause", Doc: doc(`"kind":"pause"`), Valid: true},
		{Name: "exit", Doc: doc(`"enabled":false`, `"kind":"exit"`), Valid: true},
		{Name: "run without command", Doc: doc(`"kind":"run"`)},
		{Name: "kind missing", Doc: doc(cmd)},
		{Name: "unknown kind", Doc: doc(`"kind":"restart"`, cmd)},
		{Name: "enable with wait", Doc: doc(`"kind":"enable"`, cmd, `"wait":true`)},
		{Name: "enable with group", Doc: doc(`"kind":"enable"`, cmd, group)},
		{Name: "enable group with command", Doc: doc(`"kind":"enable_group"`, group, cmd)},
		{Name: "pause with command", Doc: doc(`"kind":"pause"`, cmd)},
		{Name: "exit with arguments", Doc: doc(`"kind":"exit"`, `"args":{"from":"caller"}`)},
		{Name: "own arguments without text", Doc: doc(`"kind":"run"`, cmd, `"args":{"from":"own"}`)},
		{Name: "arguments of the caller with text", Doc: doc(`"kind":"run"`, cmd, `"args":{"from":"caller","text":"x"}`)},
		{Name: "unknown source of arguments", Doc: doc(`"kind":"run"`, cmd, `"args":{"from":"chat"}`)},
		{Name: "arguments as text", Doc: doc(`"kind":"run"`, cmd, `"args":"$arg1text"`)},
		{Name: "command not an ID", Doc: doc(`"kind":"enable"`, `"command":"hug"`)},
		{Name: "wait not a switch", Doc: doc(`"kind":"run"`, cmd, `"wait":"yes"`)},
	}}.Run(t)
}

// TestRun covers actions.md B32 and B33 for the outcomes in which the
// command runs.
func TestRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		r := f.rec
		called := f.harness.Command("called", r.note("called"), r.args())
		caller := engine.Params{Args: []string{"a", "z"}, ArgsText: "a z"}

		in := f.start("wait", caller, f.action(commands.KindRun, called.ID), r.note("after"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"called", "args: a,z", "after"}, r.get(), "a new action waits and passes on the caller's arguments")

		r = &recorder{}
		f.rec = r
		called = f.harness.Command("queued", r.note("called"))
		in = f.start("no wait", caller, f.run(called.ID, commands.RunOptions{Args: commands.CallerArgs()}), r.note("after"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"after", "called"}, r.get(), "the queued command runs after the caller")

		r = &recorder{}
		called = f.harness.Command("own arguments", r.args())
		in = f.start("own", caller,
			f.run(called.ID, commands.RunOptions{Wait: true, Args: commands.OwnArgs("$arg2text  b\tc")}),
			f.run(called.ID, commands.RunOptions{Wait: true, Args: commands.OwnArgs("")}),
			f.run(called.ID, commands.RunOptions{Wait: true, Args: commands.OwnArgs("$arg1text$arg2text")}))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"args: z,b,c", "args: ", "args: az"}, r.get(), "B32: split at white space; an empty text is no arguments")
	})
}

// TestRunWithoutRunning covers actions.md B33: a disabled command, a
// rejection and a waiting threshold let the action succeed without the
// command running, and the core logs them. Completed and queued are in
// TestRun.
func TestRunWithoutRunning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := &requirements{reject: true}
		f := newFixture(t, engine.WithRequirements(reqs))
		r := f.rec
		off := f.harness.Command("off", r.note("not run"))
		off.Enabled = false
		f.harness.Put(off)

		in := f.start("disabled", engine.Params{}, f.action(commands.KindRun, off.ID), r.note("after"))
		assert.Empty(t, in.Errors)
		assert.Contains(t, f.logs.String(), "outcome=disabled")

		guarded := f.harness.Command("guarded", r.note("not run"))
		in = f.start("rejected", engine.Params{},
			f.run(guarded.ID, commands.RunOptions{Wait: true, CheckRequirements: true, Args: commands.CallerArgs()}), r.note("after"))
		assert.Empty(t, in.Errors)
		assert.Contains(t, f.logs.String(), "outcome=rejected")
		assert.Equal(t, []string{"after", "after"}, r.get())

		reqs.mu.Lock()
		reqs.reject, reqs.wait = false, true
		reqs.mu.Unlock()
		in = f.start("waiting", engine.Params{},
			f.run(guarded.ID, commands.RunOptions{Wait: false, CheckRequirements: true, Args: commands.CallerArgs()}), r.note("after"))
		assert.Empty(t, in.Errors)
		assert.Contains(t, f.logs.String(), "outcome=waiting")
		assert.Equal(t, []string{"after", "after", "after"}, r.get())

		in = f.start("unchecked", engine.Params{}, f.action(commands.KindRun, guarded.ID))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"after", "after", "after", "not run"}, r.get(), "a new action does not check requirements")
	})
}

// TestRunFails covers actions.md B31, B33 and B208: errors of the call let
// the action fail.
func TestRunFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		r := f.rec

		failing := f.harness.Command("failing", r.fail())
		failing.ErrorPolicy = command.ErrorAbort
		f.harness.Put(failing)
		in := f.start("waits for a failure", engine.Params{}, f.action(commands.KindRun, failing.ID), r.note("after"))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "command: ")
		assert.Equal(t, []string{"after"}, r.get(), "under continue the caller goes on")

		self := f.harness.Command("self")
		self.Actions = []command.Action{f.action(commands.KindRun, self.ID)}
		f.harness.Put(self)
		in = f.harness.Start(self, engine.Params{})
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "already in the chain of calls", "B208")

		in = f.start("missing", engine.Params{}, f.action(commands.KindRun, id.New()))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "not found", "B31: the command is gone")
	})
}

// TestExit covers actions.md B38 and B209.
func TestExit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		r := f.rec

		in := f.start("exit", engine.Params{}, r.note("before"), f.action(commands.KindExit, id.ID{}), r.note("not run"))
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"before"}, r.get())

		r = &recorder{}
		called := f.harness.Command("called", r.note("x1"), f.action(commands.KindExit, id.ID{}), r.note("x2"))
		in = f.start("caller", engine.Params{}, f.action(commands.KindRun, called.ID), r.note("caller goes on"))
		assert.Equal(t, engine.StateCompleted, in.State)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"x1", "caller goes on"}, r.get(), "B209: only the called command ends")
	})
}

// TestSwitch covers actions.md B34.
func TestSwitch(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		target := f.harness.Command("target")

		f.start("disable", engine.Params{}, f.action(commands.KindDisable, target.ID))
		assert.False(t, f.enabled(target.ID))
		in := f.start("disable again", engine.Params{}, f.action(commands.KindDisable, target.ID))
		assert.Empty(t, in.Errors, "the target state is no failure")
		assert.False(t, f.enabled(target.ID))
		f.start("toggle", engine.Params{}, f.action(commands.KindToggle, target.ID))
		assert.True(t, f.enabled(target.ID))
		f.start("toggle again", engine.Params{}, f.action(commands.KindToggle, target.ID))
		assert.False(t, f.enabled(target.ID))
		f.start("enable", engine.Params{}, f.action(commands.KindEnable, target.ID))
		assert.True(t, f.enabled(target.ID))

		in = f.start("disable and run", engine.Params{},
			f.action(commands.KindDisable, target.ID), f.action(commands.KindRun, target.ID))
		assert.Empty(t, in.Errors)
		assert.Contains(t, f.logs.String(), "outcome=disabled", "it acts on the next call at once")

		in = f.start("missing", engine.Params{}, f.action(commands.KindEnable, id.New()))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "command: ")

		group := id.New()
		f.commands.AddGroup(group)
		a, b := f.harness.Command("a"), f.harness.Command("b")
		a.GroupID, b.GroupID = group, group
		b.Enabled = false
		f.harness.Put(a)
		f.harness.Put(b)
		f.start("enable group", engine.Params{}, f.action(commands.KindEnableGroup, group))
		assert.True(t, f.enabled(a.ID))
		assert.True(t, f.enabled(b.ID))
		f.start("disable group", engine.Params{}, f.action(commands.KindDisableGroup, group))
		assert.False(t, f.enabled(a.ID))
		assert.False(t, f.enabled(b.ID))
		assert.False(t, f.enabled(target.ID), "only the commands of the group")

		in = f.start("missing group", engine.Params{}, f.action(commands.KindEnableGroup, id.New()))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "group: ")
	})
}

// TestCancelAll covers actions.md B35.
func TestCancelAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		r := f.rec
		in := f.start("cancel all", engine.Params{}, f.action(commands.KindCancelAll, id.ID{}), r.note("not run"))
		assert.Equal(t, engine.StateCanceled, in.State, "its own instance too")
		assert.Empty(t, r.get())
	})
}

// TestPause covers actions.md B36.
func TestPause(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		r := f.rec
		paused := func(scope engine.PauseScope) command.Action {
			return probe{fn: func(context.Context, *engine.Run) error {
				p, err := f.harness.Engine().Paused(scope)
				r.add(string(scope) + " paused: " + map[bool]string{true: "yes", false: "no"}[p])
				return err
			}}
		}

		in := f.start("break", engine.Params{},
			f.action(commands.KindPause, id.ID{}), paused(engine.PauseAll),
			f.action(commands.KindUnpause, id.ID{}), paused(engine.PauseAll),
			f.action(commands.KindPauseEntrance, id.ID{}), paused(engine.PauseEntrance),
			f.action(commands.KindUnpauseEntrance, id.ID{}), paused(engine.PauseEntrance))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"all paused: yes", "all paused: no", "entrance paused: yes", "entrance paused: no"}, r.get())
	})
}

// TestStartCooldown covers actions.md B37.
func TestStartCooldown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reqs := &requirements{}
		f := newFixture(t, engine.WithRequirements(reqs))
		target := f.harness.Command("target")

		in := f.start("with a user", engine.Params{User: &user.User{ID: id.New()}}, f.action(commands.KindStartCooldown, target.ID))
		assert.Empty(t, in.Errors)
		assert.Equal(t, []string{"target"}, reqs.cooldowns)

		in = f.start("without a user", engine.Params{}, f.action(commands.KindStartCooldown, target.ID))
		require.Len(t, in.Errors, 1)
		assert.Contains(t, in.Errors[0].Message, "command: start cooldown of command \"target\": a cooldown per user needs a user")
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()
	reg := registry(t, actiontest.NewCommands(), &logs{})
	d, ok := reg.Descriptor(commands.TypeCommand)
	require.True(t, ok)
	c, ok := d.New().(commands.Command)
	require.True(t, ok)
	assert.Equal(t, commands.KindRun, c.Kind, "a new action runs a command")
	assert.Equal(t, &commands.RunOptions{Wait: true, CheckRequirements: false, Args: commands.CallerArgs()}, c.Run,
		"B32: it waits, does not check requirements and passes on the caller's arguments")
	require.ErrorContains(t, c.Validate(), "command: invalid action: run needs a command")

	someID := id.New()
	for _, tc := range []struct {
		name   string
		change func(c *commands.Command)
		want   string
	}{
		{"unknown kind", func(c *commands.Command) { c.Kind = "restart" }, `kind: invalid action: unknown kind "restart"`},
		{"run without options", func(c *commands.Command) { c.Run = nil }, "kind: invalid action: run needs its options"},
		{"enable with options", func(c *commands.Command) { c.Kind = commands.KindEnable }, "only run has wait, checkRequirements and args"},
		{"group with a command", func(c *commands.Command) { c.Kind, c.Run, c.Group = commands.KindEnableGroup, nil, someID }, "command: invalid action: enable_group names no command"},
		{"group without group", func(c *commands.Command) { c.Kind, c.Run, c.Command = commands.KindDisableGroup, nil, id.ID{} }, "group: invalid action: disable_group needs a group"},
		{"run with a group", func(c *commands.Command) { c.Group = someID }, "group: invalid action: run names no group"},
		{"pause with a command", func(c *commands.Command) { c.Kind, c.Run = commands.KindPause, nil }, "command: invalid action: pause names no command"},
		{"caller's arguments with text", func(c *commands.Command) { c.Run.Args = commands.Arguments{From: commands.ArgsOfCaller, Text: "x"} }, "args: invalid action: the arguments of the caller have no text"},
		{"unknown source", func(c *commands.Command) { c.Run.Args = commands.Arguments{From: "chat"} }, `args: invalid action: unknown source of arguments "chat"`},
	} {
		c, ok := d.New().(commands.Command)
		require.True(t, ok)
		c.Command = someID
		require.NoError(t, c.Validate(), tc.name)
		tc.change(&c)
		assert.ErrorContains(t, c.Validate(), tc.want, tc.name)
	}

	for _, k := range commands.Kinds() {
		require.True(t, k.Valid(), k)
	}
	assert.Len(t, commands.Kinds(), 13, "B30")
	for _, s := range commands.ArgsSources() {
		require.True(t, s.Valid(), s)
	}
}

// TestReferences: saving checks the command or group of the action (B31).
func TestReferences(t *testing.T) {
	t.Parallel()
	c := commands.Command{Kind: commands.KindToggle, Command: id.New()}
	assert.Equal(t, []command.Reference{{Kind: command.RefCommand, ID: c.Command}}, c.References())
	g := commands.Command{Kind: commands.KindDisableGroup, Group: id.New()}
	assert.Equal(t, []command.Reference{{Kind: command.RefGroup, ID: g.Group}}, g.References())
	assert.Empty(t, commands.Command{Kind: commands.KindCancelAll}.References())
}

func TestArgumentsJSON(t *testing.T) {
	t.Parallel()
	for want, args := range map[string]commands.Arguments{
		`{"from":"caller"}`:          commands.CallerArgs(),
		`{"from":"own","text":""}`:   commands.OwnArgs(""),
		`{"from":"own","text":"$a"}`: commands.OwnArgs("$a"),
	} {
		data, err := json.Marshal(args)
		require.NoError(t, err)
		assert.JSONEq(t, want, string(data))
		var back commands.Arguments
		require.NoError(t, json.Unmarshal(data, &back))
		assert.Equal(t, args, back)
	}
	_, err := json.Marshal(commands.Arguments{From: commands.ArgsOfCaller, Text: "x"})
	require.ErrorIs(t, err, action.ErrInvalid)
	for _, doc := range []string{`{"from":"own"}`, `{"from":"caller","text":""}`, `{"from":"chat"}`, `{}`, `{"from":"own","text":"x","extra":1}`} {
		var a commands.Arguments
		require.Error(t, json.Unmarshal([]byte(doc), &a), doc)
	}
}

func TestDescriptorsNeedPorts(t *testing.T) {
	t.Parallel()
	full := commands.Ports{Templates: template.New(nil), Switches: actiontest.NewCommands(), Logger: slog.New(slog.DiscardHandler)}
	_, err := commands.Descriptors(full)
	require.NoError(t, err)
	for name, change := range map[string]func(p *commands.Ports){
		"templates": func(p *commands.Ports) { p.Templates = nil },
		"switches":  func(p *commands.Ports) { p.Switches = nil },
		"logger":    func(p *commands.Ports) { p.Logger = nil },
	} {
		p := full
		change(&p)
		_, err := commands.Descriptors(p)
		require.Error(t, err, name)
	}
}
