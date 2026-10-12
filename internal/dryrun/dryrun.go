// SPDX-License-Identifier: MIT

// Package dryrun records the side effects the core would cause on a
// platform instead of causing them (plan 4.5, task 2): the platforms read
// the channel as usual, and every write — chat, moderation, twitch
// actions — is logged at debug level with all of its parameters.
package dryrun

import (
	"context"
	"errors"
	"log/slog"
	"time"

	twitchaction "github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

// ID is the synthetic identifier the dry-run operations return for created
// entities, so the flows can continue as if they had run.
const ID = "dry-run"

// EventRegistrar is what a platform that registers the event types it
// publishes implements.
type EventRegistrar interface {
	// RegisterEvents registers the event types of the platform in the
	// catalog.
	RegisterEvents(c *event.Catalog) error
}

// Platform wraps a connector.Platform in dry run: every read is
// forwarded, and every write — chat, moderation — is recorded and returns
// success. The optional capabilities of the inner platform (runnable,
// event registrar, identities, chatters) are kept.
type Platform struct {
	inner connector.Platform
	run   supervisor.Runnable
	reg   EventRegistrar
	log   *slog.Logger
}

// NewPlatform wraps p for dry run.
func NewPlatform(p connector.Platform, log *slog.Logger) Platform {
	w := Platform{inner: p, log: log}
	if r, ok := any(p).(supervisor.Runnable); ok {
		w.run = r
	}
	if rg, ok := any(p).(EventRegistrar); ok {
		w.reg = rg
	}
	return w
}

// Name implements connector.Platform.
func (w Platform) Name() platform.Name { return w.inner.Name() }

// Status implements connector.Platform.
func (w Platform) Status() connector.Status { return w.inner.Status() }

// Users implements connector.Platform.
func (w Platform) Users() connector.Users { return w.inner.Users() }

// Channel implements connector.Platform.
func (w Platform) Channel(ctx context.Context) (connector.ChannelInfo, error) {
	return w.inner.Channel(ctx)
}

// Chat implements connector.Platform.
func (w Platform) Chat() connector.Chat { return chat{log: w.log} }

// Moderation implements connector.Platform.
func (w Platform) Moderation() connector.Moderation {
	return moderation{log: w.log}
}

// Run implements supervisor.Runnable of the inner platform; the app only
// wraps platforms it runs.
func (w Platform) Run(ctx context.Context) error {
	if w.run == nil {
		return errors.New("dryrun: the inner platform is not a runnable")
	}
	return w.run.Run(ctx)
}

// RegisterEvents implements EventRegistrar of the inner platform.
func (w Platform) RegisterEvents(c *event.Catalog) error {
	if w.reg == nil {
		return nil
	}
	return w.reg.RegisterEvents(c)
}

// Identity implements connector.Identities of the inner platform.
func (w Platform) Identity(a connector.Account) (user.Identity, bool) {
	id, ok := w.inner.(connector.Identities)
	if !ok {
		return user.Identity{}, false
	}
	return id.Identity(a)
}

// Chatters implements connector.Chatters of the inner platform.
func (w Platform) Chatters(ctx context.Context) ([]user.Identity, error) {
	c, ok := w.inner.(connector.Chatters)
	if !ok {
		return nil, errors.New("dryrun: the inner platform has no chatters")
	}
	return c.Chatters(ctx)
}

// chat records the writes of the chat and returns success.
type chat struct {
	log *slog.Logger
}

var _ connector.Chat = chat{}

// Send implements connector.Chat.
func (c chat) Send(ctx context.Context, m connector.Message) error {
	c.log.DebugContext(ctx, "dry run: chat send", "text", m.Text, "from", m.From)
	return nil
}

// Delete implements connector.Chat.
func (c chat) Delete(ctx context.Context, ch connector.Channel, messageID string) error {
	c.log.DebugContext(ctx, "dry run: chat delete", "channel", ch, "message_id", messageID)
	return nil
}

// moderation records the moderation actions and returns success.
type moderation struct {
	log *slog.Logger
}

var _ connector.Moderation = moderation{}

// Timeout implements connector.Moderation.
func (m moderation) Timeout(ctx context.Context, ch connector.Channel, target user.Identity, d time.Duration, reason string) error {
	m.log.DebugContext(ctx, "dry run: timeout", "channel", ch, "user", target, "duration", d, "reason", reason)
	return nil
}

// Purge implements connector.Moderation.
func (m moderation) Purge(ctx context.Context, ch connector.Channel, target user.Identity) error {
	m.log.DebugContext(ctx, "dry run: purge", "channel", ch, "user", target)
	return nil
}

// ClearChat implements connector.Moderation.
func (m moderation) ClearChat(ctx context.Context, ch connector.Channel) error {
	m.log.DebugContext(ctx, "dry run: clear chat", "channel", ch)
	return nil
}

// Ban implements connector.Moderation.
func (m moderation) Ban(ctx context.Context, ch connector.Channel, target user.Identity, reason string) error {
	m.log.DebugContext(ctx, "dry run: ban", "channel", ch, "user", target, "reason", reason)
	return nil
}

// Unban implements connector.Moderation.
func (m moderation) Unban(ctx context.Context, ch connector.Channel, target user.Identity) error {
	m.log.DebugContext(ctx, "dry run: unban", "channel", ch, "user", target)
	return nil
}

// Mod implements connector.Moderation.
func (m moderation) Mod(ctx context.Context, ch connector.Channel, target user.Identity) error {
	m.log.DebugContext(ctx, "dry run: mod", "channel", ch, "user", target)
	return nil
}

// Unmod implements connector.Moderation.
func (m moderation) Unmod(ctx context.Context, ch connector.Channel, target user.Identity) error {
	m.log.DebugContext(ctx, "dry run: unmod", "channel", ch, "user", target)
	return nil
}

// Ops records the twitch operations and returns synthetic results.
type Ops struct {
	log *slog.Logger
}

var _ twitchaction.Ops = Ops{}

// NewOps returns the dry-run implementation of the twitch operations.
func NewOps(log *slog.Logger) Ops {
	return Ops{log: log}
}

// Clip implements twitchaction.Ops.
func (o Ops) Clip(ctx context.Context) (string, error) {
	o.log.DebugContext(ctx, "dry run: clip")
	return ID, nil
}

// Marker implements twitchaction.Ops.
func (o Ops) Marker(ctx context.Context, description string) (string, error) {
	o.log.DebugContext(ctx, "dry run: marker", "description", description)
	return ID, nil
}

// StartAd implements twitchaction.Ops.
func (o Ops) StartAd(ctx context.Context, lengthSeconds int) error {
	o.log.DebugContext(ctx, "dry run: start ad", "length_seconds", lengthSeconds)
	return nil
}

// Raid implements twitchaction.Ops.
func (o Ops) Raid(ctx context.Context, toLogin string) error {
	o.log.DebugContext(ctx, "dry run: raid", "to", toLogin)
	return nil
}

// Shoutout implements twitchaction.Ops.
func (o Ops) Shoutout(ctx context.Context, toLogin string) error {
	o.log.DebugContext(ctx, "dry run: shoutout", "to", toLogin)
	return nil
}

// CreatePoll implements twitchaction.Ops.
func (o Ops) CreatePoll(ctx context.Context, in twitchaction.PollInput) (string, error) {
	o.log.DebugContext(ctx, "dry run: create poll", "title", in.Title,
		"choices", in.Choices, "duration", in.Duration)
	return ID, nil
}

// PollStatus implements twitchaction.Ops: a dry-run poll is ended, so the
// flow continues.
func (o Ops) PollStatus(ctx context.Context, pollID string) (twitchaction.PollStatus, error) {
	o.log.DebugContext(ctx, "dry run: poll status (ended)", "poll_id", pollID)
	return twitchaction.PollStatus{Ended: true}, nil
}

// CreatePrediction implements twitchaction.Ops.
func (o Ops) CreatePrediction(ctx context.Context, in twitchaction.PredictionInput) (string, error) {
	o.log.DebugContext(ctx, "dry run: create prediction",
		"outcomes", []string{in.OutcomeA, in.OutcomeB}, "duration", in.Duration)
	return ID, nil
}

// PredictionStatus implements twitchaction.Ops: a dry-run prediction is
// ended, so the flow continues.
func (o Ops) PredictionStatus(ctx context.Context, predictionID string) (twitchaction.PredictionStatus, error) {
	o.log.DebugContext(ctx, "dry run: prediction status (ended)", "prediction_id", predictionID)
	return twitchaction.PredictionStatus{Ended: true}, nil
}

// CreateReward implements twitchaction.Ops.
func (o Ops) CreateReward(ctx context.Context, title string, cost int) (string, error) {
	o.log.DebugContext(ctx, "dry run: create reward", "title", title, "cost", cost)
	return ID, nil
}

// SetReward implements twitchaction.Ops.
func (o Ops) SetReward(ctx context.Context, title string, enabled bool) error {
	o.log.DebugContext(ctx, "dry run: set reward", "title", title, "enabled", enabled)
	return nil
}

// DeleteReward implements twitchaction.Ops.
func (o Ops) DeleteReward(ctx context.Context, title string) error {
	o.log.DebugContext(ctx, "dry run: delete reward", "title", title)
	return nil
}
