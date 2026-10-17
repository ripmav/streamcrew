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

// counterCmd is the "counter" command group: reading and writing the
// counters of a running core through the API (roadmap 6.3).
type counterCmd struct {
	List  counterListCmd  `cmd:"" help:"List the counters of the core."`
	Get   counterGetCmd   `cmd:"" help:"Show one counter."`
	Set   counterSetCmd   `cmd:"" help:"Set the value of a counter."`
	Add   counterAddCmd   `cmd:"" help:"Add an amount to a counter; empty adds one step."`
	Reset counterResetCmd `cmd:"" help:"Reset a counter to zero."`
}

// counterClient returns the CounterService client of the core.
func (e *Env) counterClient() streamcrewv1alpha1connect.CounterServiceClient {
	return streamcrewv1alpha1connect.NewCounterServiceClient(http.DefaultClient, e.server(), e.connectOptions()...)
}

type counterListCmd struct {
	output
	// Prefix filters by a case-insensitive name prefix; empty is all.
	Prefix string `help:"Only show counters with this name prefix." placeholder:"PREFIX"`
}

// Run lists the counters.
func (c counterListCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.counterClient().List(ctx, connect.NewRequest(&v1alpha1.ListCountersRequest{Prefix: c.Prefix}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Counters)
	}
	if len(resp.Msg.Counters) == 0 {
		fmt.Fprintln(e.Stdout, "no counters")
		return nil
	}
	tw := tabwriter.NewWriter(e.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVALUE\tSTEP\tRESET")
	for _, c := range resp.Msg.Counters {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\n", c.Name, c.Value, c.Step, c.ResetOnStart)
	}
	return tw.Flush()
}

type counterGetCmd struct {
	output
	Name string `arg:"" required:"" help:"The name of the counter."`
}

// Run shows one counter.
func (c counterGetCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.counterClient().Get(ctx, connect.NewRequest(&v1alpha1.GetCounterRequest{Name: c.Name}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Counter)
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: %s (step %s)\n", resp.Msg.Counter.Name, resp.Msg.Counter.Value, resp.Msg.Counter.Step)
	return err
}

type counterSetCmd struct {
	output
	Name  string `arg:"" required:"" help:"The name of the counter."`
	Value string `arg:"" required:"" help:"The new value."`
}

// Run sets the value of a counter.
func (c counterSetCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.counterClient().Set(ctx, connect.NewRequest(&v1alpha1.SetCounterRequest{Name: c.Name, Value: c.Value}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Counter)
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: %s\n", resp.Msg.Counter.Name, resp.Msg.Counter.Value)
	return err
}

type counterAddCmd struct {
	output
	Name   string `arg:"" required:"" help:"The name of the counter."`
	Amount string `arg:"" optional:"" help:"The amount to add; empty adds one step."`
}

// Run adds an amount to a counter.
func (c counterAddCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.counterClient().Add(ctx, connect.NewRequest(&v1alpha1.AddCounterRequest{Name: c.Name, Amount: c.Amount}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Counter)
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: %s\n", resp.Msg.Counter.Name, resp.Msg.Counter.Value)
	return err
}

type counterResetCmd struct {
	output
	Name string `arg:"" required:"" help:"The name of the counter."`
}

// Run resets a counter to zero.
func (c counterResetCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.counterClient().Reset(ctx, connect.NewRequest(&v1alpha1.ResetCounterRequest{Name: c.Name}))
	if err != nil {
		return err
	}
	if c.Output == "json" {
		return writeJSON(e.Stdout, resp.Msg.Counter)
	}
	_, err = fmt.Fprintf(e.Stdout, "%s: %s\n", resp.Msg.Counter.Name, resp.Msg.Counter.Value)
	return err
}
