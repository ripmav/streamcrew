// SPDX-License-Identifier: MIT

package mock

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// TypeOutput is the type of the events that say what the core asked of the
// mock platform; its payload is Output. It is not in the domain catalog
// (internal/domain/eventtype), so event commands cannot react to it.
const TypeOutput event.Type = "mock.output"

// RegisterEvents adds the event types of the platform to c.
func RegisterEvents(c *event.Catalog) error {
	return event.Register[Output](c, TypeOutput)
}

// RegisterEvents implements app.eventRegistrar: the bus of the core must
// know the payload of mock.output.
func (p *Platform) RegisterEvents(c *event.Catalog) error {
	return RegisterEvents(c)
}

// Op is an operation the core asked of the mock platform.
type Op string

// The operations.
const (
	OpSend      Op = "send"
	OpReply     Op = "reply"
	OpWhisper   Op = "whisper"
	OpDelete    Op = "delete"
	OpTimeout   Op = "timeout"
	OpPurge     Op = "purge"
	OpClearChat Op = "clear_chat"
	OpBan       Op = "ban"
	OpUnban     Op = "unban"
	OpMod       Op = "mod"
	OpUnmod     Op = "unmod"
)

// Output is an operation the core asked of the mock platform, the payload
// of TypeOutput. Fields the operation does not have are empty.
type Output struct {
	Op Op `json:"op"`
	// From is the account that sends a message.
	From connector.Account `json:"from,omitempty"`
	// Text is the text of a message.
	Text string `json:"text,omitempty"`
	// MessageID is the message a reply answers or a delete deletes.
	MessageID string `json:"messageId,omitempty"`
	// Target is the login name of the recipient of a whisper or of the
	// target of a moderation.
	Target string `json:"target,omitempty"`
	// Duration is the duration of a timeout.
	Duration polydoc.Duration `json:"duration,omitzero"`
	// Reason is the reason of a timeout or ban; empty for none.
	Reason string `json:"reason,omitempty"`
}

// output logs o and publishes it.
func (p *Platform) output(ctx context.Context, o Output) {
	p.logger.InfoContext(ctx, "mock output", "op", o.Op, "from", o.From, "text", o.Text,
		"message_id", o.MessageID, "target", o.Target, "duration", o.Duration.Std(), "reason", o.Reason)
	e := event.New(event.Source{Kind: event.SourcePlatform, Name: string(platform.Mock)}, TypeOutput, o)
	if err := p.publisher.Publish(ctx, e); err != nil {
		p.logger.ErrorContext(ctx, "publishing an output failed", "error", err)
	}
}

// chat is the chat of the mock platform.
type chat struct {
	p *Platform
}

// Send implements connector.Chat.
func (c chat) Send(ctx context.Context, m connector.Message) error {
	if err := c.p.sendable(m); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	c.p.output(ctx, Output{Op: OpSend, From: m.From, Text: m.Text})
	return nil
}

// Delete implements connector.Chat.
func (c chat) Delete(ctx context.Context, messageID string) error {
	if messageID == "" {
		return errors.New("delete: empty message ID")
	}
	if err := c.p.connected(); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	c.p.output(ctx, Output{Op: OpDelete, MessageID: messageID})
	return nil
}

// Reply implements connector.Replier.
func (c chat) Reply(ctx context.Context, messageID string, m connector.Message) error {
	if messageID == "" {
		return errors.New("reply: empty message ID")
	}
	if err := c.p.sendable(m); err != nil {
		return fmt.Errorf("reply: %w", err)
	}
	c.p.output(ctx, Output{Op: OpReply, From: m.From, Text: m.Text, MessageID: messageID})
	return nil
}

// Whisper implements connector.Whisperer.
func (c chat) Whisper(ctx context.Context, to user.Identity, m connector.Message) error {
	if err := c.p.sendable(m); err != nil {
		return fmt.Errorf("whisper: %w", err)
	}
	c.p.output(ctx, Output{Op: OpWhisper, From: m.From, Text: m.Text, Target: to.Login})
	return nil
}

// sendable checks that m has a text and its account is connected.
func (p *Platform) sendable(m connector.Message) error {
	if m.Text == "" {
		return errors.New("empty message")
	}
	if !m.From.Valid() {
		return fmt.Errorf("unknown account %q", m.From)
	}
	st := p.Status()
	if !st.Streamer || (m.From == connector.AccountBot && !st.Bot) {
		return fmt.Errorf("account %s: %w", m.From, connector.ErrNotConnected)
	}
	return nil
}

// moderation is the moderation of the mock platform. Like a real platform,
// it refuses to moderate the streamer (actions.md, B86).
type moderation struct {
	p *Platform
}

// Timeout implements connector.Moderation.
func (m moderation) Timeout(ctx context.Context, target user.Identity, d time.Duration, reason string) error {
	if d <= 0 {
		return fmt.Errorf("timeout: duration %s is not positive", d)
	}
	return m.do(ctx, Output{Op: OpTimeout, Target: target.Login, Duration: polydoc.Duration(d), Reason: reason})
}

// Purge implements connector.Moderation.
func (m moderation) Purge(ctx context.Context, target user.Identity) error {
	return m.do(ctx, Output{Op: OpPurge, Target: target.Login})
}

// ClearChat implements connector.Moderation.
func (m moderation) ClearChat(ctx context.Context) error {
	return m.do(ctx, Output{Op: OpClearChat})
}

// Ban implements connector.Moderation.
func (m moderation) Ban(ctx context.Context, target user.Identity, reason string) error {
	return m.do(ctx, Output{Op: OpBan, Target: target.Login, Reason: reason})
}

// Unban implements connector.Moderation.
func (m moderation) Unban(ctx context.Context, target user.Identity) error {
	return m.do(ctx, Output{Op: OpUnban, Target: target.Login})
}

// Mod implements connector.Moderation; a simulated user gets the
// moderator role.
func (m moderation) Mod(ctx context.Context, target user.Identity) error {
	if err := m.do(ctx, Output{Op: OpMod, Target: target.Login}); err != nil {
		return err
	}
	m.p.changeRoles(target.Login, func(s role.Set) role.Set { return s.With(role.Moderator) })
	return nil
}

// Unmod implements connector.Moderation; a simulated user loses the
// moderator role.
func (m moderation) Unmod(ctx context.Context, target user.Identity) error {
	if err := m.do(ctx, Output{Op: OpUnmod, Target: target.Login}); err != nil {
		return err
	}
	m.p.changeRoles(target.Login, func(s role.Set) role.Set { return s.Without(role.Moderator) })
	return nil
}

// do checks and records a moderation o.
func (m moderation) do(ctx context.Context, o Output) error {
	if err := m.p.connected(); err != nil {
		return fmt.Errorf("%s: %w", o.Op, err)
	}
	if o.Op != OpClearChat && key(o.Target) == m.p.streamer {
		return fmt.Errorf("%s %s: %w: the streamer cannot be moderated", o.Op, o.Target, connector.ErrRefused)
	}
	m.p.output(ctx, o)
	return nil
}

// changeRoles changes the roles of the simulated user with the login name,
// if the platform knows it.
func (p *Platform) changeRoles(login string, change func(role.Set) role.Set) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ident, ok := p.users[key(login)]; ok {
		ident.Roles = change(ident.Roles)
		p.users[key(login)] = ident
	}
}

// users finds the simulated accounts.
type users struct {
	p *Platform
}

// UserByLogin implements connector.Users.
func (u users) UserByLogin(_ context.Context, login string) (user.Identity, error) {
	if ident, ok := u.p.User(login); ok {
		return ident, nil
	}
	return user.Identity{}, fmt.Errorf("user %q: %w", login, connector.ErrUnknownUser)
}

// UserByID implements connector.Users; the ID of a simulated user is its
// login name in lowercase.
func (u users) UserByID(_ context.Context, platformUserID string) (user.Identity, error) {
	u.p.mu.Lock()
	defer u.p.mu.Unlock()
	if ident, ok := u.p.users[platformUserID]; ok {
		return ident, nil
	}
	return user.Identity{}, fmt.Errorf("user ID %q: %w", platformUserID, connector.ErrUnknownUser)
}
