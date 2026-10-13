// SPDX-License-Identifier: MIT

// Package chat has the action types that write to the chat (spec
// actions.md, B60 to B67): chat sends a message or a whisper on every
// connected platform, platform_message a message on one platform. They
// belong to the category "chat" (Code-ADR-0013).
//
// The actions delegate the delivery to the chat service (spec
// chat-service.md, B4); adapters split long messages and keep to the rate
// limits (B65).
package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeChat is the type ID of the chat action (Code-ADR-0013, point 1).
const TypeChat = "chat"

// Recipient is the recipient of a new whisper: the user of the run
// (actions.md B60).
const Recipient = "$username"

// ErrNoWhisper is the error of a whisper that is possible on none of the
// connected platforms (actions.md B63); the chat service returns it.
var ErrNoWhisper = chatsvc.ErrNoWhisper

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

// Ports are what the chat types need.
type Ports struct {
	// Templates renders the message and the recipient.
	Templates *template.Engine
	// Chat delivers the messages (spec chat-service.md, B4).
	Chat *chatsvc.Service
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
	case p.Chat == nil:
		return nil, errors.New("chat action types: no chat service")
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
// rendering (B62, B65). The delivery goes through the chat service (spec
// chat-service.md, B4), which applies B61 to B66.
func (c Chat) Perform(ctx context.Context, run *engine.Run) error {
	p := run.Params()
	ts := []template.Template{c.Message.Parse()}
	if c.Whisper != nil {
		ts = append(ts, c.Whisper.Recipient.Parse())
	}
	rendered, err := c.ports.Templates.RenderEach(ctx, ts, run.Scope()) // one render (B3)
	if err != nil {
		return err
	}
	text := rendered[0].Text
	var req chatsvc.SendRequest
	if c.Kind == KindMessage && c.Chat != nil && c.Chat.Reply {
		// The reply answers the triggering message on the platform of the
		// run, where it can (B64).
		req.ReplyPlatform, req.ReplyMessageID = p.Platform, p.MessageID
	}
	switch c.Kind {
	case KindMessage:
		req.Text, req.AsStreamer, req.Channel = text, c.AsStreamer, connector.Channel(p.Channel)
		return c.ports.Chat.Send(ctx, req)
	case KindWhisper:
		err := c.ports.Chat.Whisper(ctx, chatsvc.WhisperRequest{
			Text:       text,
			AsStreamer: c.AsStreamer,
			Channel:    connector.Channel(p.Channel),
			Recipient:  rendered[1].Text,
			Known:      run,
		})
		if errors.Is(err, chatsvc.ErrNoWhisper) {
			return field("recipient", err)
		}
		return err
	default:
		return field("kind", fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, c.Kind))
	}
}

// field names the field of an error (actions.md B6); nil stays nil.
func field(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", name, err)
}
