// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"net/http"
	"text/tabwriter"

	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// commandClient returns the CommandService client of the core.
func (e *Env) commandClient() streamcrewv1alpha1connect.CommandServiceClient {
	return streamcrewv1alpha1connect.NewCommandServiceClient(http.DefaultClient, e.server(), e.connectOptions()...)
}

// commandID resolves the name to the command ID of the core.
func (e *Env) commandID(ctx context.Context, name string) (string, error) {
	resp, err := e.commandClient().List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{Prefix: name}))
	if err != nil {
		return "", err
	}
	var found *v1alpha1.CommandSummary
	for _, c := range resp.Msg.Commands {
		if c.Name == name {
			if found != nil {
				return "", fmt.Errorf("the name %q is ambiguous", name)
			}
			found = c
		}
	}
	if found == nil {
		return "", fmt.Errorf("no command named %q", name)
	}
	return found.Id, nil
}

type commandListCmd struct {
	output
	// Kind filters by command kind: chat, event, timer or action_group.
	Kind string `help:"Only show commands of this kind." placeholder:"KIND"`
	// Prefix filters by a case-insensitive name prefix; empty is all.
	Prefix string `help:"Only show commands with this name prefix." placeholder:"PREFIX"`
}

// Run lists the commands of the core.
func (c commandListCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.commandClient().List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{Kind: c.Kind, Prefix: c.Prefix}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Commands)
	}
	if len(resp.Msg.Commands) == 0 {
		fmt.Fprintln(e.Stdout, "no commands")
		return nil
	}
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tENABLED\tTRIGGERS")
	for _, c := range resp.Msg.Commands {
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", c.Name, c.Kind, c.Enabled, join(c.Triggers, " "))
	}
	return tw.Flush()
}

type commandShowCmd struct {
	output
	Name string `arg:"" required:"" help:"The name of the command."`
}

// Run shows one command of the core.
func (c commandShowCmd) Run(ctx context.Context, e *Env) error {
	id, err := e.commandID(ctx, c.Name)
	if err != nil {
		return err
	}
	resp, err := e.commandClient().Get(ctx, connect.NewRequest(&v1alpha1.GetCommandRequest{Id: id}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg)
	}
	s := resp.Msg.Summary
	fmt.Fprintf(e.Stdout, "%s (%s)\n", s.Name, s.Kind)
	if !s.Enabled {
		fmt.Fprintln(e.Stdout, "disabled")
	}
	for _, t := range s.Triggers {
		fmt.Fprintf(e.Stdout, "trigger: %s\n", t)
	}
	if resp.Msg.Document != "" {
		fmt.Fprintln(e.Stdout, resp.Msg.Document)
	}
	return nil
}

type commandRunCmd struct {
	output
	Name    string   `arg:"" required:"" help:"The name of the command."`
	Args    []string `arg:"" optional:"" help:"The arguments, as they would follow the trigger."`
	Channel string   `help:"The channel to run in; empty is the home channel." placeholder:"LOGIN"`
}

// Run runs a command of the core manually.
func (c commandRunCmd) Run(ctx context.Context, e *Env) error {
	id, err := e.commandID(ctx, c.Name)
	if err != nil {
		return err
	}
	resp, err := e.commandClient().Run(ctx, connect.NewRequest(&v1alpha1.RunCommandRequest{Id: id, Args: c.Args, Channel: c.Channel}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg)
	}
	if resp.Msg.InstanceId != "" {
		fmt.Fprintf(e.Stdout, "running as %s\n", resp.Msg.InstanceId)
		return nil
	}
	_, err = fmt.Fprintf(e.Stdout, "not started: %s\n", resp.Msg.Reason)
	return err
}

type commandDeleteCmd struct {
	Name string `arg:"" required:"" help:"The name of the command."`
}

// Run deletes a command of the core.
func (c commandDeleteCmd) Run(ctx context.Context, e *Env) error {
	id, err := e.commandID(ctx, c.Name)
	if err != nil {
		return err
	}
	if _, err := e.commandClient().Delete(ctx, connect.NewRequest(&v1alpha1.DeleteCommandRequest{Id: id})); err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "deleted %s\n", c.Name)
	return nil
}

type commandEnableCmd struct {
	Name string `arg:"" required:"" help:"The name of the command."`
}

// Run activates a command of the core.
func (c commandEnableCmd) Run(ctx context.Context, e *Env) error {
	id, err := e.commandID(ctx, c.Name)
	if err != nil {
		return err
	}
	resp, err := e.commandClient().SetEnabled(ctx, connect.NewRequest(&v1alpha1.SetEnabledCommandRequest{Id: id, Enabled: true}))
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "enabled %s\n", resp.Msg.Command.Name)
	return nil
}

type commandDisableCmd struct {
	Name string `arg:"" required:"" help:"The name of the command."`
}

// Run deactivates a command of the core.
func (c commandDisableCmd) Run(ctx context.Context, e *Env) error {
	id, err := e.commandID(ctx, c.Name)
	if err != nil {
		return err
	}
	resp, err := e.commandClient().SetEnabled(ctx, connect.NewRequest(&v1alpha1.SetEnabledCommandRequest{Id: id, Enabled: false}))
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "disabled %s\n", resp.Msg.Command.Name)
	return nil
}
