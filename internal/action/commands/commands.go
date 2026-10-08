// SPDX-License-Identifier: MIT

// Package commands has the command action (spec actions.md, B30 to B38):
// it runs other commands, switches commands and groups on and off, cancels
// and pauses the queue, starts cooldowns and ends its own command. It
// belongs to the category "commands" (Code-ADR-0013).
//
// The action controls the engine through engine.Run and switches commands
// through the port Switches, which command.Service implements.
package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeCommand is the type ID of the command action (Code-ADR-0013,
// point 1).
const TypeCommand = "command"

// Kind is what a command action does (actions.md B30).
type Kind string

// The kinds of the command action.
const (
	// KindRun runs a command (B32, B33). New command actions do this.
	KindRun Kind = "run"
	// KindEnable, KindDisable and KindToggle switch a command (B34).
	KindEnable  Kind = "enable"
	KindDisable Kind = "disable"
	KindToggle  Kind = "toggle"
	// KindEnableGroup and KindDisableGroup switch all commands of a group
	// (B34).
	KindEnableGroup  Kind = "enable_group"
	KindDisableGroup Kind = "disable_group"
	// KindCancelAll cancels all instances, its own too (B35).
	KindCancelAll Kind = "cancel_all"
	// KindPause, KindUnpause, KindPauseEntrance and KindUnpauseEntrance
	// pause and resume the queue or the entrance commands (B36).
	KindPause           Kind = "pause"
	KindUnpause         Kind = "unpause"
	KindPauseEntrance   Kind = "pause_entrance"
	KindUnpauseEntrance Kind = "unpause_entrance"
	// KindStartCooldown starts the cooldown of a command (B37).
	KindStartCooldown Kind = "start_cooldown"
	// KindExit ends its own command (B38).
	KindExit Kind = "exit"
)

// Kinds returns the kinds, in the order editors show them.
func Kinds() []Kind {
	return []Kind{
		KindRun, KindEnable, KindDisable, KindToggle, KindEnableGroup, KindDisableGroup,
		KindCancelAll, KindPause, KindUnpause, KindPauseEntrance, KindUnpauseEntrance,
		KindStartCooldown, KindExit,
	}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// refersToCommand reports whether actions of kind k name a command.
func (k Kind) refersToCommand() bool {
	return slices.Contains([]Kind{KindRun, KindEnable, KindDisable, KindToggle, KindStartCooldown}, k)
}

// refersToGroup reports whether actions of kind k name a group.
func (k Kind) refersToGroup() bool {
	return k == KindEnableGroup || k == KindDisableGroup
}

// Switches switch commands and groups for good (actions.md B34);
// *command.Service implements it.
type Switches interface {
	SwitchCommand(ctx context.Context, commandID id.ID, sw command.Switch) (command.Command, error)
	SwitchGroup(ctx context.Context, groupID id.ID, sw command.Switch) error
}

// Ports are what the command action needs.
type Ports struct {
	// Templates renders the own arguments of a call.
	Templates *template.Engine
	// Switches switches commands and groups.
	Switches Switches
	// Logger records calls whose command did not run (B33).
	Logger *slog.Logger
}

// ports are the ports of the command action.
type ports struct {
	Ports
}

// Descriptors returns the command action with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("command action types: no template engine")
	case p.Switches == nil:
		return nil, errors.New("command action types: no switches")
	case p.Logger == nil:
		return nil, errors.New("command action types: no logger")
	}
	ports := &ports{Ports: p}
	return descriptors(ports), nil
}

// Catalog returns the command types without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): their actions decode,
// validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the command types with ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeCommand,
			Version:  1,
			Category: action.CategoryCommands,
			Schema:   commandSchema(),
		}.WithKinds(KindRun, func(k Kind) (Command, bool) {
			if !k.Valid() {
				return Command{}, false
			}
			c := Command{Common: action.On(), Kind: k, ports: ports}
			if k == KindRun {
				c.Run = &RunOptions{Wait: true, Args: CallerArgs()}
			}
			return c, true
		}),
	}
}

// commandSchema returns the schema of the command action: its kind
// decides whether it names a command, a group or neither, and only run has
// options.
func commandSchema() *schema.Schema {
	cmd := schema.Property{Name: "command", Schema: schema.Reference(schema.UICommand), Required: true}
	group := schema.Property{Name: "group", Schema: schema.Reference(schema.UIGroup), Required: true}
	variants := make([]schema.Variant, 0, len(Kinds()))
	for _, k := range Kinds() {
		v := schema.Variant{Kind: string(k)}
		switch {
		case k == KindRun:
			v.Props = []schema.Property{
				cmd,
				{Name: "wait", Schema: schema.Switch()},
				{Name: "checkRequirements", Schema: schema.Switch()},
				{Name: "args", Schema: argumentsSchema()},
			}
		case k.refersToCommand():
			v.Props = []schema.Property{cmd}
		case k.refersToGroup():
			v.Props = []schema.Property{group}
		}
		variants = append(variants, v)
	}
	return schema.Kinds(nil, variants...)
}

// Command is the command action (actions.md B30 to B38). Which members it
// has depends on its kind: Command for the kinds that name a command,
// Group for those that name a group, Run only for run.
type Command struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// Command is the command of run, enable, disable, toggle and
	// start_cooldown; the zero ID for the other kinds, which have none.
	Command id.ID `json:"command,omitzero"`
	// Group is the group of enable_group and disable_group; the zero ID
	// for the other kinds.
	Group id.ID `json:"group,omitzero"`
	// Run are the options of run; nil for the other kinds.
	Run   *RunOptions `json:",embed"`
	ports *ports
}

// RunOptions are the options of the kind run (actions.md B32).
type RunOptions struct {
	// Wait runs the called command as part of this instance and waits for
	// it; a new action waits.
	Wait bool `json:"wait"`
	// CheckRequirements checks the requirements of the called command with
	// the user of this run; a new action does not.
	CheckRequirements bool `json:"checkRequirements"`
	// Args are the arguments of the called command; a new action passes on
	// those of the caller.
	Args Arguments `json:"args"`
}

// DocType implements command.Action.
func (Command) DocType() string { return TypeCommand }

var _ engine.WaitingCaller = Command{}

// WaitsFor implements engine.WaitingCaller: run with waiting runs the
// called command as part of its own instance, so its actions count for the
// locks (command-engine.md, B22, B23).
func (c Command) WaitsFor() (id.ID, bool) {
	return c.Command, c.Kind == KindRun && c.Run != nil && c.Run.Wait
}

// Validate implements command.Action. That the command or group exists,
// saving checks (B31).
func (c Command) Validate() error {
	if !c.Kind.Valid() {
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
	switch {
	case c.Kind.refersToCommand() && c.Command.IsZero():
		return field("command", fmt.Errorf("%w: %s needs a command", action.ErrInvalid, c.Kind))
	case !c.Kind.refersToCommand() && !c.Command.IsZero():
		return field("command", fmt.Errorf("%w: %s names no command", action.ErrInvalid, c.Kind))
	case c.Kind.refersToGroup() && c.Group.IsZero():
		return field("group", fmt.Errorf("%w: %s needs a group", action.ErrInvalid, c.Kind))
	case !c.Kind.refersToGroup() && !c.Group.IsZero():
		return field("group", fmt.Errorf("%w: %s names no group", action.ErrInvalid, c.Kind))
	case c.Kind == KindRun && c.Run == nil:
		return field("kind", fmt.Errorf("%w: run needs its options", action.ErrInvalid))
	case c.Kind != KindRun && c.Run != nil:
		return field("kind", fmt.Errorf("%w: only run has wait, checkRequirements and args", action.ErrInvalid))
	case c.Run != nil:
		return field("args", c.Run.Args.Validate())
	default:
		return nil
	}
}

// References implements command.Referrer: saving rejects unknown commands
// and groups (B31).
func (c Command) References() []command.Reference {
	switch {
	case c.Kind.refersToCommand():
		return []command.Reference{{Kind: command.RefCommand, ID: c.Command}}
	case c.Kind.refersToGroup():
		return []command.Reference{{Kind: command.RefGroup, ID: c.Group}}
	default:
		return []command.Reference{}
	}
}

// Perform implements engine.Performer.
func (c Command) Perform(ctx context.Context, run *engine.Run) error {
	switch c.Kind {
	case KindRun:
		return c.call(ctx, run)
	case KindEnable:
		return c.switchCommand(ctx, command.SwitchOn)
	case KindDisable:
		return c.switchCommand(ctx, command.SwitchOff)
	case KindToggle:
		return c.switchCommand(ctx, command.SwitchToggle)
	case KindEnableGroup:
		return field("group", c.ports.Switches.SwitchGroup(ctx, c.Group, command.SwitchOn))
	case KindDisableGroup:
		return field("group", c.ports.Switches.SwitchGroup(ctx, c.Group, command.SwitchOff))
	case KindCancelAll:
		run.CancelAll(ctx)
		return nil
	case KindPause:
		return run.Pause(ctx, engine.PauseAll)
	case KindUnpause:
		return run.Resume(ctx, engine.PauseAll)
	case KindPauseEntrance:
		return run.Pause(ctx, engine.PauseEntrance)
	case KindUnpauseEntrance:
		return run.Resume(ctx, engine.PauseEntrance)
	case KindStartCooldown:
		return field("command", run.StartCooldown(ctx, c.Command))
	case KindExit:
		return engine.ErrStop
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
}

// call runs the command (B32, B33). Outcomes in which the command does not
// run are no failure; the core logs them, and the engine tells the user
// about a rejection.
func (c Command) call(ctx context.Context, run *engine.Run) error {
	if c.Run == nil {
		return field("kind", fmt.Errorf("%w: run needs its options", action.ErrInvalid))
	}
	opts := engine.CallOptions{Wait: c.Run.Wait, CheckRequirements: c.Run.CheckRequirements}
	if c.Run.Args.From == ArgsOwn {
		text, err := c.ports.Templates.Render(ctx, c.Run.Args.Text.Parse(), run.Scope(), template.Text)
		if err != nil {
			return field("args", err)
		}
		opts.OwnArgs, opts.Args = true, strings.Fields(text)
	}
	res, err := run.Call(ctx, c.Command, opts)
	if err != nil {
		return field("command", err)
	}
	switch res.Outcome {
	case engine.OutcomeCompleted, engine.OutcomeQueued:
	case engine.OutcomeDisabled, engine.OutcomeRejected, engine.OutcomeWaiting:
		c.ports.Logger.InfoContext(ctx, "called command did not run",
			"command", c.Command, "outcome", res.Outcome, "instance", run.InstanceID())
	}
	return nil
}

// switchCommand switches the command (B34).
func (c Command) switchCommand(ctx context.Context, sw command.Switch) error {
	_, err := c.ports.Switches.SwitchCommand(ctx, c.Command, sw)
	return field("command", err)
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
