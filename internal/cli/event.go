// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// eventCmd is the "event" command group: simulating events of a running core
// through the API (roadmap 6.3).
type eventCmd struct {
	Simulate eventSimulateCmd `cmd:"" help:"Simulate an event of the mock platform on the core."`
}

type eventSimulateCmd struct {
	// Type is the type of the catalog, e.g. "channel.follow";
	// "chat.message" simulates a chat message of the user.
	Type string `arg:"" required:"" help:"The type of the event, e.g. channel.follow or chat.message." placeholder:"TYPE"`
	// User is the login of the mock platform that causes the event.
	User string `help:"The login of the mock platform that causes the event." placeholder:"LOGIN"`
	// Target is the login of the mock platform the event is about.
	Target string `help:"The login of the mock platform the event is about." placeholder:"LOGIN"`
	// Text is the message text for the type chat.message.
	Text string `help:"The message text for the type chat.message." placeholder:"TEXT"`
}

// Run simulates the event on the core.
func (c eventSimulateCmd) Run(ctx context.Context, e *Env) error {
	resp, err := e.streamClient().Simulate(ctx, &v1alpha1.SimulateEventRequest{
		EventType: c.Type,
		User:      c.User,
		Target:    c.Target,
		Text:      c.Text,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "simulated %s (event %s)\n", c.Type, resp.EventId)
	return nil
}
