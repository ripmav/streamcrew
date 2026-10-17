// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// chatCmd is the "chat" command group: sending and reading chat messages of
// a running core through the API (roadmap 6.3).
type chatCmd struct {
	Send chatSendCmd `cmd:"" help:"Send a chat message of the core; --as sends as the given user of the mock platform."`
}

type chatSendCmd struct {
	// As sends as the user with this login of the mock platform; without
	// --as the bot sends.
	As string `help:"Send as the user with this login (mock platform)." placeholder:"LOGIN"`
	// Channel is the channel to send to; empty is the home channel.
	Channel string `help:"The channel to send to; empty is the home channel." placeholder:"LOGIN"`
	// Streamer sends from the streamer account even if a bot is connected.
	Streamer bool `name:"as-streamer" help:"Send from the streamer account even if a bot is connected."`
	// Text is the message to send.
	Text []string `arg:"" required:"" name:"text"`
}

// Run sends the message to the core.
func (c chatSendCmd) Run(ctx context.Context, e *Env) error {
	text := strings.Join(c.Text, " ")
	if text == "" {
		return &usageError{err: errors.New("the text is empty")}
	}
	if c.As != "" {
		resp, err := e.streamClient().Simulate(ctx, connect.NewRequest(&v1alpha1.SimulateEventRequest{
			EventType: "chat.message",
			User:      c.As,
			Text:      text,
		}))
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Stdout, "sent as %s (event %s)\n", c.As, resp.Msg.EventId)
		return nil
	}
	_, err := e.chatClient().Send(ctx, connect.NewRequest(&v1alpha1.SendChatRequest{
		Text:       text,
		Channel:    c.Channel,
		AsStreamer: c.Streamer,
	}))
	if err != nil {
		return err
	}
	fmt.Fprintln(e.Stdout, "sent")
	return nil
}
