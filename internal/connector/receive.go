// SPDX-License-Identifier: MIT

package connector

import (
	"context"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
)

// Receiver takes what an adapter receives from its platform (spec
// events.md, B11 to B14; Code-ADR-0011, point 4): chat messages, joins,
// events and the state of the stream. The event service implements it: it
// applies the rules of events.md, publishes the events and triggers the
// commands.
//
// An adapter calls it in the order it received things, each message and
// event once (B22, Dedup). The methods do not wait for the commands they
// trigger. An error means the receiver could not take what it got, e.g.
// data that does not fit its event type; the adapter logs it.
type Receiver interface {
	// Message takes a chat message (B13).
	Message(ctx context.Context, m Incoming) error
	// Join takes that a user joined the chat without writing (B14).
	Join(ctx context.Context, p platform.Name, who user.Identity) error
	// Event takes an event that is neither a chat message, nor a join, nor
	// a change of the stream.
	Event(ctx context.Context, e Event) error
	// Stream takes whether the stream on p is live: after every connect
	// and whenever the stream goes online or offline (B11).
	Stream(ctx context.Context, p platform.Name, live bool) error
}

// Incoming is a chat message an adapter received.
type Incoming struct {
	// Platform is the platform of the chat.
	Platform platform.Name
	// Author is the account that wrote it, with its roles in the channel.
	Author user.Identity
	// FromBot reports whether the bot account of the channel wrote it; such
	// a message triggers nothing (B13).
	FromBot bool
	// Message is the message with the platform's ID and its text, which is
	// not empty.
	Message eventtype.Message
}

// Event is an event an adapter received (spec events.md).
type Event struct {
	// Platform is the platform it happened on.
	Platform platform.Name
	// Type is a type of the catalog: one of the platform, or a
	// platform-neutral one for a platform without its own types, such as
	// the mock platform (B2).
	Type event.Type
	// User is the user who caused it and Target its target (B6, B9); nil
	// if the event has none.
	User   *user.Identity
	Target *user.Identity
	// Recipients are the users who got a subscription with a mass gift: the
	// receiver publishes either the mass gift or one gift per recipient
	// (B5). They are empty for the other types.
	Recipients []user.Identity
	// Details are the values of the event (B7, B9).
	Details eventtype.Details
}
