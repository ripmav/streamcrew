// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
)

// Delivery says what became of a simulated message or event.
type Delivery string

// Deliveries.
const (
	// Delivered means the platform handed it to the receiver.
	Delivered Delivery = "delivered"
	// Duplicate means the platform got its ID before and dropped it, as
	// adapters do with a repeated message (events.md, B22).
	Duplicate Delivery = "duplicate"
)

// Sent is what became of a simulated message or event.
type Sent struct {
	// ID is the platform's ID of the message or event.
	ID       string
	Delivery Delivery
}

// Say simulates a chat message that the user with the login name writes,
// with a new ID. A login name the platform does not know becomes a new user
// without roles; a message of the bot account is marked as such (events.md,
// B13). The text must not be empty.
func (p *Platform) Say(ctx context.Context, login, text string) (Sent, error) {
	return p.SayWithID(ctx, login, text, p.newID())
}

// SayWithID simulates a chat message like Say, with the platform's ID
// messageID, e.g. to simulate a repeat after a lost connection (events.md,
// B22).
func (p *Platform) SayWithID(ctx context.Context, login, text, messageID string) (Sent, error) {
	switch {
	case text == "":
		return Sent{}, errors.New("say: empty message")
	case messageID == "":
		return Sent{}, errors.New("say: empty message ID")
	}
	if err := p.connected(); err != nil {
		return Sent{}, fmt.Errorf("say: %w", err)
	}
	author, err := p.account(login)
	if err != nil {
		return Sent{}, fmt.Errorf("say: %w", err)
	}
	sent := Sent{ID: messageID, Delivery: Duplicate}
	if !p.dedup.First(messageID) {
		p.logger.InfoContext(ctx, "mock message dropped as a repeat", "id", messageID)
		return sent, nil
	}
	sent.Delivery = Delivered
	p.setPresent(author.Login, true)
	m := connector.Incoming{
		Platform: platform.Mock,
		Author:   author,
		FromBot:  p.isBot(author),
		Message:  eventtype.Message{ID: messageID, Text: text},
	}
	if err := p.receiver.Message(ctx, m); err != nil {
		return sent, fmt.Errorf("say: %w", err)
	}
	return sent, nil
}

// Join simulates that the user with the login name joins the chat without
// writing (events.md, B14); a login name the platform does not know
// becomes a new user without roles.
func (p *Platform) Join(ctx context.Context, login string) error {
	if err := p.connected(); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	who, err := p.account(login)
	if err != nil {
		return fmt.Errorf("join: %w", err)
	}
	p.setPresent(who.Login, true)
	if err := p.receiver.Join(ctx, platform.Mock, who); err != nil {
		return fmt.Errorf("join: %w", err)
	}
	return nil
}

// Event is an event to simulate, with users named by their login names. A
// login name the platform does not know becomes a new user without roles.
type Event struct {
	// Type is a platform-neutral type of the catalog, e.g.
	// "channel.follow".
	Type event.Type
	// User and Target are the login names of the user and of the target
	// user (events.md, B6, B9); empty if the event has none.
	User   string
	Target string
	// Recipients are the login names of the users who got a subscription
	// with a mass gift; empty for the other types.
	Recipients []string
	// Details are the values of the event (B7, B9).
	Details eventtype.Details
}

// Simulate simulates an event with a new ID and hands it to the receiver,
// which checks that its data fit its type. It returns ErrNotSimulated for a
// type that is not simulated this way: one of another platform, the
// application events, a chat message (Say), a join (Join), the start and
// end of the stream (GoLive, GoOffline), and the types the core derives
// itself, such as chat.user.entrance.
func (p *Platform) Simulate(ctx context.Context, e Event) (Sent, error) {
	return p.SimulateWithID(ctx, e, p.newID())
}

// SimulateWithID simulates an event like Simulate, with the platform's ID
// eventID, e.g. to simulate a repeat (events.md, B22).
func (p *Platform) SimulateWithID(ctx context.Context, e Event, eventID string) (Sent, error) {
	if eventID == "" {
		return Sent{}, errors.New("simulate: empty event ID")
	}
	if err := simulated(e.Type); err != nil {
		return Sent{}, fmt.Errorf("simulate: %w", err)
	}
	if err := p.connected(); err != nil {
		return Sent{}, fmt.Errorf("simulate %s: %w", e.Type, err)
	}
	in := connector.Event{Platform: platform.Mock, Type: e.Type, Details: e.Details}
	var err error
	if in.User, err = p.optionalAccount(e.User); err != nil {
		return Sent{}, fmt.Errorf("simulate %s: user: %w", e.Type, err)
	}
	if in.Target, err = p.optionalAccount(e.Target); err != nil {
		return Sent{}, fmt.Errorf("simulate %s: target: %w", e.Type, err)
	}
	for _, login := range e.Recipients {
		ident, err := p.account(login)
		if err != nil {
			return Sent{}, fmt.Errorf("simulate %s: recipient: %w", e.Type, err)
		}
		in.Recipients = append(in.Recipients, ident)
	}
	sent := Sent{ID: eventID, Delivery: Duplicate}
	if !p.dedup.First(eventID) {
		p.logger.InfoContext(ctx, "mock event dropped as a repeat", "id", eventID, "type", e.Type)
		return sent, nil
	}
	sent.Delivery = Delivered
	if e.Type == eventtype.ChatUserLeave && in.User != nil {
		p.setPresent(in.User.Login, false)
	}
	if err := p.receiver.Event(ctx, in); err != nil {
		return sent, fmt.Errorf("simulate %s: %w", e.Type, err)
	}
	return sent, nil
}

// simulated checks that Simulate simulates events of type t.
func simulated(t event.Type) error {
	d, ok := eventtype.Lookup(t)
	if !ok {
		return fmt.Errorf("%w %q", event.ErrUnknownType, t)
	}
	if d.Platform != "" {
		return fmt.Errorf("%w: %s is a type of %s", ErrNotSimulated, t, d.Platform.DisplayName())
	}
	switch t {
	case eventtype.AppStarted, eventtype.AppStopping:
		return fmt.Errorf("%w: %s is an event of the core", ErrNotSimulated, t)
	case eventtype.ChatMessage:
		return fmt.Errorf("%w: a chat message is written with Say", ErrNotSimulated)
	case eventtype.ChatUserJoin:
		return fmt.Errorf("%w: a join is simulated with Join", ErrNotSimulated)
	case eventtype.ChannelStreamStart, eventtype.ChannelStreamStop:
		return fmt.Errorf("%w: the stream starts and stops with GoLive and GoOffline", ErrNotSimulated)
	case eventtype.ChatUserNew, eventtype.ChatUserEntrance, eventtype.ChatUserFirstMessage:
		return fmt.Errorf("%w: the core derives %s from chat messages and joins", ErrNotSimulated, t)
	default:
		return nil
	}
}

// GoLive starts the stream with the title and category. While the platform
// runs, it tells the receiver (events.md, B11); otherwise Run does after
// connecting. A stream that is live already keeps its start and only gets
// the new title and category.
func (p *Platform) GoLive(ctx context.Context, title, game string) error {
	p.mu.Lock()
	wasLive := p.channel.Live
	p.channel.Title, p.channel.Game = title, game
	if !wasLive {
		p.channel.Live, p.channel.StartedAt = true, time.Now().UTC()
	}
	running := p.running
	p.mu.Unlock()
	if wasLive || !running {
		return nil
	}
	p.logger.InfoContext(ctx, "mock stream online", "title", title, "game", game)
	if err := p.receiver.Stream(ctx, platform.Mock, true); err != nil {
		return fmt.Errorf("go live: %w", err)
	}
	return nil
}

// GoOffline ends the stream. While the platform runs, it tells the
// receiver (events.md, B11). A stream that is offline stays so.
func (p *Platform) GoOffline(ctx context.Context) error {
	p.mu.Lock()
	wasLive := p.channel.Live
	p.channel.Live, p.channel.StartedAt = false, time.Time{}
	running := p.running
	p.mu.Unlock()
	if !wasLive || !running {
		return nil
	}
	p.logger.InfoContext(ctx, "mock stream offline")
	if err := p.receiver.Stream(ctx, platform.Mock, false); err != nil {
		return fmt.Errorf("go offline: %w", err)
	}
	return nil
}

// connected returns an error wrapping connector.ErrNotConnected unless Run
// runs.
func (p *Platform) connected() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return fmt.Errorf("mock platform: %w", connector.ErrNotConnected)
	}
	return nil
}

// optionalAccount returns the account with the login name, or nil for an
// empty name.
func (p *Platform) optionalAccount(login string) (*user.Identity, error) {
	if login == "" {
		return nil, nil
	}
	ident, err := p.account(login)
	if err != nil {
		return nil, err
	}
	return &ident, nil
}

// isBot reports whether ident is the bot account.
func (p *Platform) isBot(ident user.Identity) bool {
	return p.hasBot && key(ident.Login) == p.bot
}
