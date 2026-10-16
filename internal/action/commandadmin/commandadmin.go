// SPDX-License-Identifier: MIT

// Package commandadmin is the command-admin action (roadmap 5.6, AddCommand,
// UpdateCommand, DisableCommand): it adds, updates and disables a chat
// command from the arguments of the run. It is meant for the ready-made
// admin commands, which restrict it by role.
package commandadmin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/ripmav/streamcrew/internal/action"
	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
)

// TypeCommandAdmin is the type ID of the command-admin action.
const TypeCommandAdmin = "command_admin"

// Kind is what a command-admin action does.
type Kind string

// The kinds of the command-admin action.
const (
	// KindAdd adds a chat command whose trigger is the first argument and
	// whose answer is the rest (AddCommand).
	KindAdd Kind = "add"
	// KindUpdate changes the answer of an existing chat command (UpdateCommand).
	KindUpdate Kind = "update"
	// KindDisable disables an existing command (DisableCommand).
	KindDisable Kind = "disable"
)

// Kinds returns the kinds of the command-admin action.
func Kinds() []Kind { return []Kind{KindAdd, KindUpdate, KindDisable} }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return slices.Contains(Kinds(), k) }

// Commands is the command service the action manages commands through.
// *command.Service implements it.
type Commands interface {
	// Commands lists the commands of the profile.
	Commands(ctx context.Context) ([]command.Command, error)
	// Save creates or updates a command.
	Save(ctx context.Context, cmd command.Command) (command.Saved, error)
	// SwitchCommand changes the active switch of a command.
	SwitchCommand(ctx context.Context, commandID id.ID, sw command.Switch) (command.Command, error)
}

// Ports are what the command-admin type needs.
type Ports struct {
	// Commands manages the commands.
	Commands Commands
	// Logger records the changes.
	Logger *slog.Logger
}

// ports are the ports of the command-admin type.
type ports struct {
	Ports
}

// Descriptors returns the command-admin type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	if p.Commands == nil {
		return nil, errors.New("command-admin action types: no commands")
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the command-admin type without ports, for the type catalog
// and commands as code (Code-ADR-0013, point 3).
func Catalog() []action.Descriptor { return descriptors(nil) }

// descriptors returns the command-admin type with ports, which are nil in
// the catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeCommandAdmin,
			Version:  1,
			Category: action.CategoryCommands,
			Schema:   commandAdminSchema(),
		}.WithKinds(KindAdd, func(k Kind) (CommandAdmin, bool) {
			if !k.Valid() {
				return CommandAdmin{}, false
			}
			return CommandAdmin{Common: action.On(), Kind: k, ports: ports}, true
		}),
	}
}

// commandAdminSchema returns the schema of the command-admin action.
func commandAdminSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{},
	)
}

// CommandAdmin is the command-admin action (roadmap 5.6, AddCommand,
// UpdateCommand, DisableCommand).
type CommandAdmin struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	ports         *ports
}

// DocType implements command.Action.
func (a CommandAdmin) DocType() string { return TypeCommandAdmin }

// Validate implements command.Action.
func (a CommandAdmin) Validate() error {
	if !a.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, a.Kind)
	}
	return nil
}

// Perform implements engine.Performer: it reads the name (first argument)
// and the answer (the rest) and adds, updates or disables the command.
func (a CommandAdmin) Perform(ctx context.Context, run *engine.Run) error {
	name, answer, ok := splitArgs(run.Params().Args)
	if !ok {
		return fmt.Errorf("%w: the command name is the first argument", action.ErrInvalid)
	}
	switch a.Kind {
	case KindAdd:
		return a.add(ctx, name, answer)
	case KindUpdate:
		return a.update(ctx, name, answer)
	case KindDisable:
		return a.disable(ctx, name)
	default:
		return fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, a.Kind)
	}
}

// add creates a chat command named name that answers with answer.
func (a CommandAdmin) add(ctx context.Context, name, answer string) error {
	if answer == "" {
		return fmt.Errorf("%w: the answer is the text after the name", action.ErrInvalid)
	}
	if _, found := a.find(ctx, name); found {
		return fmt.Errorf("%w: a command with the trigger %q exists", action.ErrInvalid, name)
	}
	cmd := command.Command{
		Name:        name,
		Kind:        command.KindChat,
		Enabled:     true,
		TriggerMode: command.TriggerExclamation,
		Triggers:    []string{name},
		ErrorPolicy: command.ErrorContinue,
		Actions: []command.Action{
			chataction.Chat{
				Kind:    chataction.KindMessage,
				Message: action.Template(answer),
				Chat:    &chataction.MessageOptions{},
			},
		},
	}
	if _, err := a.ports.Commands.Save(ctx, cmd); err != nil {
		return fmt.Errorf("add command %q: %w", name, err)
	}
	a.ports.Logger.InfoContext(ctx, "command added", "command", name)
	return nil
}

// update changes the answer of the chat command named name to answer.
func (a CommandAdmin) update(ctx context.Context, name, answer string) error {
	if answer == "" {
		return fmt.Errorf("%w: the answer is the text after the name", action.ErrInvalid)
	}
	cmd, found := a.find(ctx, name)
	if !found {
		return fmt.Errorf("%w: no command with the trigger %q", action.ErrInvalid, name)
	}
	for i, act := range cmd.Actions {
		c, ok := act.(chataction.Chat)
		if !ok {
			continue
		}
		c.Message = action.Template(answer)
		cmd.Actions[i] = c
		break
	}
	if _, err := a.ports.Commands.Save(ctx, cmd); err != nil {
		return fmt.Errorf("update command %q: %w", name, err)
	}
	a.ports.Logger.InfoContext(ctx, "command updated", "command", name)
	return nil
}

// disable turns off the command named name.
func (a CommandAdmin) disable(ctx context.Context, name string) error {
	cmd, found := a.find(ctx, name)
	if !found {
		return fmt.Errorf("%w: no command with the trigger %q", action.ErrInvalid, name)
	}
	if _, err := a.ports.Commands.SwitchCommand(ctx, cmd.ID, command.SwitchOff); err != nil {
		return fmt.Errorf("disable command %q: %w", name, err)
	}
	a.ports.Logger.InfoContext(ctx, "command disabled", "command", name)
	return nil
}

// find returns the chat command whose triggers include name, regardless of
// case for ASCII letters; ok is false if there is none or several.
func (a CommandAdmin) find(ctx context.Context, name string) (command.Command, bool) {
	all, err := a.ports.Commands.Commands(ctx)
	if err != nil {
		a.ports.Logger.WarnContext(ctx, "listing the commands failed", "error", err)
		return command.Command{}, false
	}
	var found command.Command
	n := 0
	for _, c := range all {
		if c.Kind != command.KindChat {
			continue
		}
		for _, tr := range c.Triggers {
			if strings.EqualFold(tr, name) {
				found, n = c, n+1
			}
		}
	}
	return found, n == 1
}

// splitArgs returns the first argument (the name) and the rest joined by a
// space (the answer); ok is false if there is no argument.
func splitArgs(args []string) (name, answer string, ok bool) {
	if len(args) == 0 {
		return "", "", false
	}
	return args[0], strings.Join(args[1:], " "), true
}
