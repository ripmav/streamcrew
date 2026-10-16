// SPDX-License-Identifier: MIT

// Package usercommand is the user-specific chat command action (roadmap 5.6,
// P1): it answers a user with their own stored answer for the command, or
// stores the arguments as the user's answer. This is what makes a chat
// command user-specific.
package usercommand

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypeUserCommand is the type ID of the user-specific chat command action.
const TypeUserCommand = "user_command"

// Kind is what a user-command action does.
type Kind string

// KindRemember stores the arguments as the user's answer, or answers with
// the stored answer (or the default) when there are none.
const KindRemember Kind = "remember"

// Kinds returns the kinds of the user-command action.
func Kinds() []Kind { return []Kind{KindRemember} }

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool { return k == KindRemember }

// Answers stores the per-user answers. *store.Store implements it.
type Answers interface {
	// SetUserAnswer stores the user's answer for the key.
	SetUserAnswer(ctx context.Context, userID id.ID, key, answer string) error
	// UserAnswer returns the user's stored answer for the key; ok is false
	// if there is none.
	UserAnswer(ctx context.Context, userID id.ID, key string) (string, bool, error)
}

// ChatSender delivers the answers. *chat.Service implements it.
type ChatSender interface {
	Send(ctx context.Context, req chatsvc.SendRequest) error
}

// Ports are what the user-command type needs.
type Ports struct {
	// Templates renders the text, the default and the confirmation.
	Templates *template.Engine
	// Chat delivers the answers.
	Chat ChatSender
	// Answers stores the per-user answers.
	Answers Answers
	// Logger records the changes.
	Logger *slog.Logger
}

// ports are the ports of the user-command type.
type ports struct {
	Ports
}

// Descriptors returns the user-command type with its ports.
func Descriptors(p Ports) ([]action.Descriptor, error) {
	if p.Templates == nil {
		return nil, errors.New("user-command action types: no templates")
	}
	if p.Chat == nil {
		return nil, errors.New("user-command action types: no chat")
	}
	if p.Answers == nil {
		return nil, errors.New("user-command action types: no answers")
	}
	if p.Logger == nil {
		p.Logger = slog.New(slog.DiscardHandler)
	}
	return descriptors(&ports{Ports: p}), nil
}

// Catalog returns the user-command type without ports, for the type catalog
// and commands as code (Code-ADR-0013, point 3).
func Catalog() []action.Descriptor { return descriptors(nil) }

// descriptors returns the user-command type with ports, which are nil in the
// catalog.
func descriptors(ports *ports) []action.Descriptor {
	return []action.Descriptor{
		action.Descriptor{
			Type:     TypeUserCommand,
			Version:  1,
			Category: action.CategoryUserCommand,
			Schema:   userCommandSchema(),
		}.WithKinds(KindRemember, func(k Kind) (UserCommand, bool) {
			if !k.Valid() {
				return UserCommand{}, false
			}
			return UserCommand{Common: action.On(), Kind: k, ports: ports}, true
		}),
	}
}

// userCommandSchema returns the schema of the user-command action.
func userCommandSchema() *schema.Schema {
	return schema.Kinds(
		[]schema.Property{
			{Name: "text", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
			{Name: "default", Schema: schema.Template()},
			{Name: "confirm", Schema: schema.Template()},
		},
	)
}

// UserCommand is the user-specific chat command action (roadmap 5.6, P1).
type UserCommand struct {
	action.Common `json:",embed"`
	Kind          Kind            `json:"kind"`
	Text          action.Template `json:"text"`
	Default       action.Template `json:"default,omitempty"`
	Confirm       action.Template `json:"confirm,omitempty"`
	ports         *ports
}

// DocType implements command.Action.
func (a UserCommand) DocType() string { return TypeUserCommand }

// Validate implements command.Action.
func (a UserCommand) Validate() error {
	if !a.Kind.Valid() {
		return fmt.Errorf("%w: unknown kind %q", action.ErrInvalid, a.Kind)
	}
	if a.Text == "" {
		return fmt.Errorf("%w: the text is required", action.ErrInvalid)
	}
	return nil
}

// Perform implements engine.Performer: with arguments it stores them as the
// user's answer and confirms; without it answers with the stored answer or
// the default.
func (a UserCommand) Perform(ctx context.Context, run *engine.Run) error {
	p := run.Params()
	if p.User == nil {
		return fmt.Errorf("%w: the command needs a user", action.ErrInvalid)
	}
	key := run.Command().Name
	ts := []template.Template{a.Text.Parse(), a.Default.Parse(), a.Confirm.Parse()}
	rendered, err := a.ports.Templates.RenderEach(ctx, ts, run.Scope())
	if err != nil {
		return err
	}
	answer := strings.TrimSpace(rendered[0].Text)
	var reply string
	if answer != "" {
		if err := a.ports.Answers.SetUserAnswer(ctx, p.User.ID, key, answer); err != nil {
			return fmt.Errorf("store the answer: %w", err)
		}
		a.ports.Logger.InfoContext(ctx, "user answer stored", "command", key, "user", p.User.ID)
		reply = rendered[2].Text
	} else {
		stored, found, err := a.ports.Answers.UserAnswer(ctx, p.User.ID, key)
		if err != nil {
			return fmt.Errorf("read the answer: %w", err)
		}
		if found {
			reply = stored
		} else {
			reply = rendered[1].Text
		}
	}
	if strings.TrimSpace(reply) == "" {
		return nil
	}
	return a.ports.Chat.Send(ctx, chatsvc.SendRequest{Text: reply, Channel: connector.Channel(p.Channel)})
}
