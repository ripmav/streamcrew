// SPDX-License-Identifier: MIT

// Package chat has the action types that write to the chat (spec
// actions.md, B60 to B67): chat sends a message or a whisper on every
// connected platform, platform_message a message on one platform. They
// belong to the category "chat" (Code-ADR-0013).
//
// The actions reach the platforms through the ports of internal/connector;
// adapters split long messages and keep to the rate limits (B65).
package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeChat is the type ID of the chat action (Code-ADR-0013, point 1).
const TypeChat = "chat"

// Recipient is the recipient of a new whisper: the user of the run
// (actions.md B60).
const Recipient = "$username"

// ErrNoWhisper is the error of a whisper that is possible on none of the
// connected platforms (actions.md B63).
var ErrNoWhisper = errors.New("no connected platform can whisper to the recipient")

// Kind says where a chat action sends its message (actions.md B60).
type Kind string

// The kinds of the chat action.
const (
	// KindMessage sends the message to the chat, optionally as a reply
	// (B61, B62, B64). New chat actions do this.
	KindMessage Kind = "message"
	// KindWhisper sends the message privately to a recipient, the option
	// "whisper" of B60 (B63).
	KindWhisper Kind = "whisper"
)

// Kinds returns the kinds of the chat action, in the order editors show
// them.
func Kinds() []Kind {
	return []Kind{KindMessage, KindWhisper}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// Platforms are the platforms of the profile; *connector.Set implements it.
type Platforms interface {
	// Connected returns the platforms that are connected now (B62).
	Connected() []connector.Platform
	// Platform returns the platform name; ok is false if the profile has
	// none (B67).
	Platform(name platform.Name) (p connector.Platform, ok bool)
}

// Ports are what the chat types need.
type Ports struct {
	// Templates renders the message and the recipient.
	Templates *template.Engine
	// Platforms are the platforms messages go to.
	Platforms Platforms
	// Logger records messages that are not sent and the platforms a
	// whisper skips (B62, B63).
	Logger *slog.Logger
}

// ports are the ports of the chat types.
type ports struct {
	Ports
}

// Descriptors returns the chat types with their ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	switch {
	case p.Templates == nil:
		return nil, errors.New("chat action types: no template engine")
	case p.Platforms == nil:
		return nil, errors.New("chat action types: no platforms")
	case p.Logger == nil:
		return nil, errors.New("chat action types: no logger")
	}
	ports := &ports{Ports: p}
	return descriptors(ports), nil
}

// Catalog returns the chat types without ports, for the type catalog and
// commands as code (Code-ADR-0013, point 3): their actions decode,
// validate and encode, but must not run.
func Catalog() []action.Descriptor {
	return descriptors(nil)
}

// descriptors returns the chat types with ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeChat,
			Version:  1,
			Category: action.CategoryChat,
			Schema:   chatSchema(),
		}.WithKinds(KindMessage, func(k Kind) (Chat, bool) {
			c := Chat{Common: action.On(), Kind: k, ports: ports}
			switch k {
			case KindMessage:
				c.Chat = &MessageOptions{}
			case KindWhisper:
				c.Whisper = &WhisperOptions{Recipient: Recipient}
			default:
				return Chat{}, false
			}
			return c, true
		}),
		action.Descriptor{
			Type:     TypePlatformMessage,
			Version:  1,
			Category: action.CategoryChat,
			Schema:   platformMessageSchema(),
		}.WithNew(func() PlatformMessage {
			return PlatformMessage{Common: action.On(), ports: ports}
		}),
	}
}

// chatSchema returns the schema of the chat action: a message has the
// option reply, a whisper a recipient.
func chatSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{
			{Name: "message", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "asStreamer", Schema: schema.Switch()},
		},
		schema.Variant{Kind: string(KindMessage), Props: []schema.Property{
			{Name: "reply", Schema: schema.Switch()},
		}},
		schema.Variant{Kind: string(KindWhisper), Props: []schema.Property{
			{Name: "recipient", Schema: schema.NonEmpty(schema.UIUser)},
		}},
	)
}

// Chat is the chat action (actions.md B60 to B66). Which members it has
// depends on its kind: Chat for a message, Whisper for a whisper.
type Chat struct {
	action.Common `json:",embed"`
	Kind          Kind `json:"kind"`
	// Message is the text; it is not empty. A new action has none.
	Message action.Template `json:"message,omitzero"`
	// AsStreamer sends from the streamer's account even if a bot is
	// connected (B61); a new action does not.
	AsStreamer bool `json:"asStreamer"`
	// Chat are the options of a message; nil for a whisper.
	Chat *MessageOptions `json:",embed"`
	// Whisper are the options of a whisper; nil for a message.
	Whisper *WhisperOptions `json:",embed"`
	ports   *ports
}

// MessageOptions are the options of the kind message.
type MessageOptions struct {
	// Reply sends the message on the platform of the run as a reply to the
	// triggering message (B64); a new action does not.
	Reply bool `json:"reply"`
}

// WhisperOptions are the options of the kind whisper.
type WhisperOptions struct {
	// Recipient is the user name, with or without "@", as a template; a
	// new action whispers to the user of the run (Recipient).
	Recipient action.Template `json:"recipient"`
}

// DocType implements command.Action.
func (Chat) DocType() string { return TypeChat }

// Validate implements command.Action.
func (c Chat) Validate() error {
	switch {
	case !c.Kind.Valid():
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	case c.Message == "":
		return field("message", fmt.Errorf("%w: empty message", action.ErrInvalid))
	case c.Kind == KindMessage && c.Chat == nil:
		return field("kind", fmt.Errorf("%w: a message needs its options", action.ErrInvalid))
	case c.Kind != KindMessage && c.Chat != nil:
		return field("reply", fmt.Errorf("%w: only a message has reply", action.ErrInvalid))
	case c.Kind == KindWhisper && c.Whisper == nil:
		return field("kind", fmt.Errorf("%w: a whisper needs its options", action.ErrInvalid))
	case c.Kind != KindWhisper && c.Whisper != nil:
		return field("recipient", fmt.Errorf("%w: only a whisper has a recipient", action.ErrInvalid))
	case c.Whisper != nil && c.Whisper.Recipient == "":
		return field("recipient", fmt.Errorf("%w: empty recipient", action.ErrInvalid))
	default:
		return nil
	}
}

// Perform implements engine.Performer. The action is done when every
// platform has taken the message (B2). It sends nothing, and does not fail,
// if no platform is connected or the message is empty or white space after
// rendering (B62, B65).
func (c Chat) Perform(ctx context.Context, run *engine.Run) error {
	targets := c.ports.Platforms.Connected()
	if len(targets) == 0 {
		c.ports.Logger.InfoContext(ctx, "chat message not sent: no platform connected",
			"instance_id", run.InstanceID(), "kind", c.Kind)
		return nil
	}
	ts := []template.Template{c.Message.Parse()}
	if c.Whisper != nil {
		ts = append(ts, c.Whisper.Recipient.Parse())
	}
	rendered, err := c.ports.Templates.RenderEach(ctx, ts, run.Scope()) // one render (B3)
	if err != nil {
		return err
	}
	text := rendered[0].Text
	if c.ports.blank(ctx, run, text) {
		return nil
	}
	switch c.Kind {
	case KindMessage:
		return send(ctx, run.Params(), targets, connector.Message{Text: text}, delivery{
			asStreamer: c.AsStreamer, reply: c.Chat.Reply})
	case KindWhisper:
		return c.whisper(ctx, run, targets, text, rendered[1].Text)
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
}

// blank reports whether text is empty or white space after rendering; such
// a message is not sent, and that is no failure (B65).
func (p *ports) blank(ctx context.Context, run *engine.Run, text string) bool {
	if strings.TrimSpace(text) != "" {
		return false
	}
	p.Logger.DebugContext(ctx, "chat message not sent: empty after rendering", "instance_id", run.InstanceID())
	return true
}

// delivery says how send delivers a message.
type delivery struct {
	// asStreamer sends from the streamer's account (B61).
	asStreamer bool
	// reply answers the triggering message where it can (B64).
	reply bool
}

// send sends m to the chat of each target at the same time, from the
// account B61 says (B62, B64, B67); it fails if a target fails, and the
// others keep the message (B66).
func send(ctx context.Context, p engine.Params, targets []connector.Platform, m connector.Message, d delivery) error {
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		msg := m
		msg.From = target.Status().Sender(d.asStreamer)
		chat := target.Chat()
		wg.Go(func() {
			if r, ok := chat.(connector.Replier); ok && d.replies(target.Name(), p) {
				errs[i] = r.Reply(ctx, p.MessageID, msg)
				return
			}
			errs[i] = chat.Send(ctx, msg)
		})
	}
	wg.Wait()
	return connector.JoinErrors("not sent", targets, errs)
}

// replies reports whether the message answers the triggering message on
// platform name (B64): the option is on, the run was triggered there by a
// message, and the platform gave its ID.
func (d delivery) replies(name platform.Name, p engine.Params) bool {
	return d.reply && name == p.Platform && p.MessageID != ""
}

// whisper sends text privately to the recipient on each target that can
// whisper and has an account of that name, at the same time (B63): a user
// the run knows, found through its lookup (spec command-engine.md, B17),
// and otherwise the account the platform reports. It fails if a target
// fails, and if no target whispered.
func (c Chat) whisper(ctx context.Context, run *engine.Run, targets []connector.Platform, text, recipient string) error {
	errs := make([]error, len(targets))
	sent := make([]bool, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		w, ok := target.Chat().(connector.Whisperer)
		if !ok {
			c.ports.Logger.InfoContext(ctx, "whisper skipped: the platform has no whispers",
				"platform", target.Name())
			continue
		}
		m := connector.Message{Text: text, From: target.Status().Sender(c.AsStreamer)}
		wg.Go(func() {
			to, err := connector.FindAccount(ctx, run, target, recipient)
			if errors.Is(err, connector.ErrUnknownUser) {
				c.ports.Logger.InfoContext(ctx, "whisper skipped: unknown recipient",
					"platform", target.Name(), "recipient", recipient)
				return
			}
			if err == nil {
				err = w.Whisper(ctx, to, m)
			}
			errs[i], sent[i] = err, err == nil
		})
	}
	wg.Wait()
	if err := connector.JoinErrors("not sent", targets, errs); err != nil {
		return err
	}
	if !slices.Contains(sent, true) {
		return field("recipient", fmt.Errorf("%w %q", ErrNoWhisper, connector.Login(recipient)))
	}
	return nil
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
