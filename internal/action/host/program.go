// SPDX-License-Identifier: MIT

package host

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeExternalProgram is the type ID of the external program action
// (Code-ADR-0013, point 1).
const TypeExternalProgram = "external_program"

// ResultOutput is the fixed name of the result value with the output of a
// program (actions.md B5, B114). [Interop] It follows the original and may
// be replaced after the legal assessment (roadmap Gate O, O.1).
const ResultOutput = "externalprogramresult"

// DefaultTimeout is the time limit in seconds of a new action that waits
// (B110).
const DefaultTimeout = 30

// MaxOutput is how much output the action keeps, 1 MiB (B114); the rest is
// dropped with a warning in the log.
const MaxOutput = 1 << 20

// timeLimitExtra is how much longer than its timeout an action that waits
// may take as a whole (actions.md B8).
const timeLimitExtra = 5 * time.Second

// waitDelay is how long the action waits for the output of a program that
// has ended or was ended, e.g. if a program it started keeps the output
// open.
const waitDelay = time.Second

// timeoutRange is the range of the timeout (B110).
func timeoutRange() action.Range {
	return action.WholeBetween(1, 3600)
}

// ErrTimeout is the error of a program that ran longer than its timeout;
// the action ended it (B115).
var ErrTimeout = errors.New("the program ran longer than its timeout")

// ProgramKind says how the action starts a program (actions.md B110). The
// options "wait" and "open with the system" of the original are kinds,
// because "save output" and the timeout only apply to waiting and opening
// excludes both (B116; Code-ADR-0017).
type ProgramKind string

// The kinds of the external program action.
const (
	// ProgramStart starts the program and is done (B112); it runs on its
	// own, also after the core ends. New actions do this.
	ProgramStart ProgramKind = "start"
	// ProgramRun starts the program and waits until it ends, at most for
	// its timeout (B113 to B115).
	ProgramRun ProgramKind = "run"
	// ProgramOpen opens a path with the program the system assigns to it
	// (B116).
	ProgramOpen ProgramKind = "open"
)

// ProgramKinds returns the kinds, in the order editors show them.
func ProgramKinds() []ProgramKind {
	return []ProgramKind{ProgramStart, ProgramRun, ProgramOpen}
}

// Valid reports whether k is a known kind.
func (k ProgramKind) Valid() bool {
	return slices.Contains(ProgramKinds(), k)
}

// launches reports whether actions of kind k start a program themselves.
func (k ProgramKind) launches() bool {
	return k == ProgramStart || k == ProgramRun
}

// programSchema returns the schema of the external program action.
func programSchema() *schema.Schema {
	launch := []schema.Property{
		{Name: "program", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
		{Name: "args", Schema: schema.Template()},
		{Name: "showWindow", Schema: schema.Switch()},
	}
	return schema.Kinds(nil,
		schema.Variant{Kind: string(ProgramStart), Props: launch},
		schema.Variant{Kind: string(ProgramRun), Props: append(slices.Clone(launch),
			schema.Property{Name: "timeout", Schema: timeoutRange().Schema()},
			schema.Property{Name: "saveOutput", Schema: schema.Switch()},
		)},
		schema.Variant{Kind: string(ProgramOpen), Props: []schema.Property{
			{Name: "path", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
		}},
	)
}

// ExternalProgram is the external program action (actions.md B110 to
// B117). Which members it has depends on its kind; members of other kinds
// are nil.
type ExternalProgram struct {
	action.Common `json:",embed"`
	Kind          ProgramKind `json:"kind"`
	// Launch are the program and its arguments of start and run.
	Launch *LaunchOptions `json:",embed"`
	// Wait are the options of run.
	Wait *WaitOptions `json:",embed"`
	// Open is the path of open.
	Open  *OpenOptions `json:",embed"`
	ports *ports
}

// LaunchOptions are the program and arguments of the kinds start and run.
type LaunchOptions struct {
	// Program is the path of the program, or a name the system finds in
	// its search path, as a template; a new action has none.
	Program action.Template `json:"program,omitzero"`
	// Args are the arguments as a template. They are split into words
	// before rendering, and each word becomes one argument (B111).
	Args action.Template `json:"args"`
	// ShowWindow shows the window of the program on systems that have
	// windows (B116); a new action does not.
	ShowWindow bool `json:"showWindow"`
}

// WaitOptions are the options of the kind run.
type WaitOptions struct {
	// Timeout is the time limit in seconds, from 1 to 3 600; a new action
	// waits 30 seconds (B110, B115).
	Timeout action.Amount `json:"timeout"`
	// SaveOutput sets the output as $externalprogramresult (B114); a new
	// action does not.
	SaveOutput bool `json:"saveOutput"`
}

// OpenOptions are the options of the kind open.
type OpenOptions struct {
	// Path is the path to open, as a template; a new action has none.
	Path action.Template `json:"path,omitzero"`
}

// DocType implements command.Action.
func (ExternalProgram) DocType() string { return TypeExternalProgram }

// Validate implements command.Action: the members fit the kind, and the
// quotes of the arguments are closed.
func (p ExternalProgram) Validate() error {
	k := p.Kind
	switch {
	case !k.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, k))
	case k.launches() != (p.Launch != nil):
		return field("program", fmt.Errorf("%w: only start and run have a program and arguments", action.ErrInvalid))
	case (k == ProgramRun) != (p.Wait != nil):
		return field("timeout", fmt.Errorf("%w: only run has a timeout and saveOutput", action.ErrInvalid))
	case (k == ProgramOpen) != (p.Open != nil):
		return field("path", fmt.Errorf("%w: only open has a path", action.ErrInvalid))
	case p.Open != nil && p.Open.Path == "":
		return field("path", fmt.Errorf("%w: empty path", action.ErrInvalid))
	case p.Launch == nil:
		return nil
	case p.Launch.Program == "":
		return field("program", fmt.Errorf("%w: empty program", action.ErrInvalid))
	}
	if _, err := words(string(p.Launch.Args)); err != nil {
		return field("args", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	if p.Wait != nil {
		return field("timeout", p.Wait.Timeout.Validate(timeoutRange()))
	}
	return nil
}

// Perform implements engine.Performer. The program, each word of the
// arguments and the timeout come from one render (B3, B111).
func (p ExternalProgram) Perform(ctx context.Context, run *engine.Run) error {
	if p.Kind == ProgramOpen {
		path, err := p.ports.Templates.Render(ctx, p.Open.Path.Parse(), run.Scope(), template.Text)
		if err != nil {
			return err
		}
		if strings.TrimSpace(path) == "" {
			return field("path", fmt.Errorf("%w: empty after rendering", action.ErrInvalid))
		}
		return field("path", p.ports.Opener.Open(ctx, path, p.ports.env()))
	}

	args, err := words(string(p.Launch.Args))
	if err != nil {
		return field("args", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	ts := make([]template.Template, 0, 1+len(args))
	ts = append(ts, p.Launch.Program.Parse())
	for _, w := range args {
		ts = append(ts, template.Parse(w))
	}
	var timeout []template.Template
	if p.Wait != nil {
		if timeout, err = p.Wait.Timeout.Templates(); err != nil {
			return field("timeout", err)
		}
	}
	rendered, err := p.ports.Templates.RenderEach(ctx, append(ts, timeout...), run.Scope())
	if err != nil {
		return err
	}
	texts := make([]string, len(rendered))
	for i, r := range rendered {
		texts[i] = r.Text
	}
	program, args := texts[0], texts[1:1+len(args)]
	if strings.TrimSpace(program) == "" {
		return field("program", fmt.Errorf("%w: empty after rendering", action.ErrInvalid))
	}

	if p.Kind == ProgramStart {
		return field("program", p.start(ctx, program, args))
	}
	seconds, err := p.Wait.Timeout.EvalWithTexts(texts[1+len(args):], timeoutRange())
	if err != nil {
		return field("timeout", err)
	}
	limit, err := action.Seconds(seconds)
	if err != nil {
		return field("timeout", err)
	}
	if err := run.LimitTo(limit + timeLimitExtra); err != nil { // B8
		return err
	}
	return p.run(ctx, run, program, args, limit)
}

// command returns the command of the program with its arguments, without a
// shell: the program runs in the directory it lies in and gets the
// environment of the ports (B111, B117).
func (p ExternalProgram) command(ctx context.Context, program string, args []string) (*exec.Cmd, error) {
	path, err := exec.LookPath(program)
	if err != nil {
		return nil, err
	}
	if path, err = filepath.Abs(path); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = filepath.Dir(path)
	cmd.Env = p.ports.env()
	return cmd, nil
}

// start starts the program on its own and returns (B112).
func (p ExternalProgram) start(ctx context.Context, program string, args []string) error {
	// The program outlives the action and the core: no context ends it.
	cmd, err := p.command(context.WithoutCancel(ctx), program, args)
	if err != nil {
		return err
	}
	cmd.SysProcAttr = detachedAttr(p.Launch.ShowWindow)
	if err := cmd.Start(); err != nil {
		return err
	}
	p.ports.Logger.DebugContext(ctx, "program started", "program", cmd.Path, "pid", cmd.Process.Pid)
	reap(cmd)
	return nil
}

// reap waits for cmd in a goroutine of its own, so that the system can
// release the process when it ends. The program may outlive the core
// (B112); the goroutine then ends with the process of the core, which is
// why nothing waits for it.
func reap(cmd *exec.Cmd) {
	go func() { _ = cmd.Wait() }() // the exit of a program that runs on its own does not matter (B112)
}

// run runs the program, waits until it ends and saves the output with
// SaveOutput, also if the program fails (B113 to B115).
func (p ExternalProgram) run(ctx context.Context, run *engine.Run, program string, args []string, limit time.Duration) error {
	out, err := p.execute(ctx, program, args, limit)
	if p.Wait.SaveOutput {
		run.Scope().SetValue(ResultOutput, template.TextValue(out))
	}
	return err
}

// execute runs the program and waits until it ends, at most for limit; then
// it ends the program (B115). With SaveOutput it returns standard output and
// standard error together, the first MaxOutput bytes as valid UTF-8, also
// if the program fails (B113, B114).
func (p ExternalProgram) execute(ctx context.Context, program string, args []string, limit time.Duration) (string, error) {
	ctx2, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd, err := p.command(ctx2, program, args)
	if err != nil {
		return "", field("program", err)
	}
	cmd.SysProcAttr = waitedAttr(p.Launch.ShowWindow)
	cmd.Cancel = func() error { return kill(cmd.Process) }
	cmd.WaitDelay = waitDelay
	var out *output
	if p.Wait.SaveOutput {
		out = &output{limit: MaxOutput}
		cmd.Stdout, cmd.Stderr = out, out
	}

	err = cmd.Run()
	var text string
	if out != nil {
		text = strings.ToValidUTF8(out.String(), "\uFFFD")
		if out.dropped > 0 {
			p.ports.Logger.WarnContext(ctx, "program output cut", "program", cmd.Path, "dropped_bytes", out.dropped)
		}
	}
	var exit *exec.ExitError
	switch {
	case err == nil:
		return text, nil
	case ctx.Err() != nil:
		return text, ctx.Err()
	case errors.Is(ctx2.Err(), context.DeadlineExceeded):
		return text, field("timeout", fmt.Errorf("%w of %s", ErrTimeout, limit))
	case errors.As(err, &exit):
		return text, field("program", fmt.Errorf("exit code %d", exit.ExitCode()))
	default:
		return text, field("program", err)
	}
}

// output keeps the first limit bytes written to it and counts the rest
// (B114). Standard output and standard error share it.
type output struct {
	mu      sync.Mutex
	buf     []byte
	limit   int
	dropped int64
}

func (o *output) Write(b []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	keep := min(len(b), o.limit-len(o.buf))
	o.buf = append(o.buf, b[:keep]...)
	o.dropped += int64(len(b) - keep)
	return len(b), nil
}

func (o *output) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.buf)
}

// systemOpener opens paths with the program of the operating system.
type systemOpener struct{}

// SystemOpener returns the opener of the operating system (B116): open on
// macOS, xdg-open on other Unix systems and the file protocol handler on
// Windows. The path is one argument; there is no shell.
func SystemOpener() Opener {
	return systemOpener{}
}

// Open implements Opener. The program that opens path outlives the
// action, so the end of ctx does not end it.
func (systemOpener) Open(ctx context.Context, path string, env []string) error {
	name, args := openCommand(path)
	cmd := exec.CommandContext(context.WithoutCancel(ctx), name, args...)
	cmd.Env = append([]string{}, env...)
	cmd.SysProcAttr = detachedAttr(true)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	reap(cmd)
	return nil
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
