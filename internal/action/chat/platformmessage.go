// SPDX-License-Identifier: MIT

package chat

import (
	"context"
	"fmt"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/schema"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/template"
)

// TypePlatformMessage is the type ID of the platform message action
// (Code-ADR-0013, point 1).
const TypePlatformMessage = "platform_message"

// platformMessageSchema returns the schema of the platform message action.
func platformMessageSchema() *schema.Schema {
	return schema.Document(
		schema.Property{Name: "platform", Schema: schema.Platform(), Required: true},
		schema.Property{Name: "message", Schema: schema.NonEmpty(schema.UITemplate), Required: true},
		schema.Property{Name: "asStreamer", Schema: schema.Switch()},
		schema.Property{Name: "reply", Schema: schema.Switch()},
	)
}

// PlatformMessage is the platform message action (actions.md B67): like a
// chat message, but on exactly one platform, and without whispers.
type PlatformMessage struct {
	action.Common `json:",embed"`
	// Platform is the platform the message goes to; a new action has none.
	// Saving takes names of platforms this version has no adapter for, so
	// that commands of a newer version survive; such a platform is never
	// connected.
	Platform platform.Name `json:"platform,omitzero"`
	// Message is the text; it is not empty. A new action has none.
	Message action.Template `json:"message,omitzero"`
	// AsStreamer sends from the streamer's account even if a bot is
	// connected (B61); a new action does not.
	AsStreamer bool `json:"asStreamer"`
	// Reply sends the message as a reply to the triggering message if the
	// run was triggered on Platform (B64); a new action does not.
	Reply bool `json:"reply"`
	ports *ports
}

// DocType implements command.Action.
func (PlatformMessage) DocType() string { return TypePlatformMessage }

// Validate implements command.Action.
func (m PlatformMessage) Validate() error {
	if err := m.Platform.Validate(); err != nil {
		return field("platform", fmt.Errorf("%w: %w", action.ErrInvalid, err))
	}
	if m.Message == "" {
		return field("message", fmt.Errorf("%w: empty message", action.ErrInvalid))
	}
	return nil
}

// Perform implements engine.Performer. If the platform is not connected,
// nothing happens, and that is no failure (B67); otherwise the action
// behaves like a chat message on that platform (B61, B64 to B66).
func (m PlatformMessage) Perform(ctx context.Context, run *engine.Run) error {
	target, ok := m.ports.Platforms.Platform(m.Platform)
	if !ok || !target.Status().Connected() {
		m.ports.Logger.InfoContext(ctx, "platform message not sent: platform not connected",
			"instance_id", run.InstanceID(), "platform", m.Platform)
		return nil
	}
	text, err := m.ports.Templates.Render(ctx, m.Message.Parse(), run.Scope(), template.Text)
	if err != nil {
		return err
	}
	if m.ports.blank(ctx, run, text) {
		return nil
	}
	return send(ctx, run.Params(), []connector.Platform{target}, connector.Message{Text: text},
		delivery{asStreamer: m.AsStreamer, reply: m.Reply})
}
