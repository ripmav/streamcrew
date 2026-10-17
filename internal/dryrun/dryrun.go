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
	"sync/atomic"
	"time"

	twitchaction "github.com/ripmav/streamcrew/internal/action/twitch"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
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

// AsInner returns the inner platform of the wrapper; ok is false if the
// platform was not wrapped by this package (roadmap 6.6).
func AsInner(p connector.Platform) (connector.Platform, bool) {
	w, ok := p.(interface {
		innerPlatform() connector.Platform
	})
	if !ok {
		return nil, false
	}
	return w.innerPlatform(), true
}

// Platform wraps a connector.Platform in dry run: every read is
// forwarded, and every write — chat, moderation — is recorded and returns
// success. The optional capabilities of the inner platform (runnable,
// event registrar, identities, chatters, mock simulation) are kept.
type Platform struct {
	inner connector.Platform
	run   supervisor.Runnable
	reg   EventRegistrar
	log   *slog.Logger
	// on switches the writes on and off at runtime; nil means always on
	// (roadmap 6.6).
	on *atomic.Bool
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

// NewSwitchablePlatform wraps p for dry run, switched by on (roadmap 6.6):
// with on set, the writes are recorded; with it unset, they go to the inner
// platform. The reads always go to the inner platform.
func NewSwitchablePlatform(p connector.Platform, log *slog.Logger, on *atomic.Bool) Platform {
	w := NewPlatform(p, log)
	w.on = on
	return w
}

// switched reports whether the dry run is on.
func (w Platform) switched() bool { return w.on == nil || w.on.Load() }

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

// innerPlatform returns the wrapped platform (roadmap 6.6).
func (w Platform) innerPlatform() connector.Platform { return w.inner }

// Chat implements connector.Platform.
func (w Platform) Chat() connector.Chat {
	if !w.switched() {
		return w.inner.Chat()
	}
	return chat{log: w.log}
}

// Moderation implements connector.Platform.
func (w Platform) Moderation() connector.Moderation {
	if !w.switched() {
		return w.inner.Moderation()
	}
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

// UserPlatformData implements connector.PlatformData of the inner
// platform, so the dry-run wrapper keeps the platform data capability for
// the user service (roadmap 6.6).
func (w Platform) UserPlatformData(ctx context.Context, platformUserID string) (user.PlatformData, error) {
	d, ok := w.inner.(connector.PlatformData)
	if !ok {
		return user.PlatformData{}, connector.ErrUnknownUser
	}
	return d.UserPlatformData(ctx, platformUserID)
}

// Say implements mock.Say of the inner platform, so the dry-run wrapper
// stays a mock platform for the API (roadmap 6.3) and the platform set
// (roadmap 6.6).
func (w Platform) Say(ctx context.Context, login, text string) (mock.Sent, error) {
	m, ok := w.inner.(interface {
		Say(ctx context.Context, login, text string) (mock.Sent, error)
	})
	if !ok {
		return mock.Sent{}, errors.New("dryrun: the inner platform cannot simulate")
	}
	return m.Say(ctx, login, text)
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

// SwitchOps switches between the real twitch operations and their dry-run
// recording at runtime (roadmap 6.6): with on set, the operations are
// recorded; with it unset, they run on the streamer channel.
type SwitchOps struct {
	realOps, dry twitchaction.Ops
	on           *atomic.Bool
}

// NewSwitchOps returns the switchable implementation of the twitch
// operations.
func NewSwitchOps(realOps, dry twitchaction.Ops, on *atomic.Bool) SwitchOps {
	return SwitchOps{realOps: realOps, dry: dry, on: on}
}

// switched reports whether the dry run is on.
func (o SwitchOps) switched() bool { return o.on == nil || o.on.Load() }

// Clip implements twitchaction.Ops.
func (o SwitchOps) Clip(ctx context.Context) (string, error) {
	if o.switched() {
		return o.dry.Clip(ctx)
	}
	return o.realOps.Clip(ctx)
}

// Marker implements twitchaction.Ops.
func (o SwitchOps) Marker(ctx context.Context, description string) (string, error) {
	if o.switched() {
		return o.dry.Marker(ctx, description)
	}
	return o.realOps.Marker(ctx, description)
}

// StartAd implements twitchaction.Ops.
func (o SwitchOps) StartAd(ctx context.Context, lengthSeconds int) error {
	if o.switched() {
		return o.dry.StartAd(ctx, lengthSeconds)
	}
	return o.realOps.StartAd(ctx, lengthSeconds)
}

// Raid implements twitchaction.Ops.
func (o SwitchOps) Raid(ctx context.Context, toLogin string) error {
	if o.switched() {
		return o.dry.Raid(ctx, toLogin)
	}
	return o.realOps.Raid(ctx, toLogin)
}

// Shoutout implements twitchaction.Ops.
func (o SwitchOps) Shoutout(ctx context.Context, toLogin string) error {
	if o.switched() {
		return o.dry.Shoutout(ctx, toLogin)
	}
	return o.realOps.Shoutout(ctx, toLogin)
}

// CreatePoll implements twitchaction.Ops.
func (o SwitchOps) CreatePoll(ctx context.Context, in twitchaction.PollInput) (string, error) {
	if o.switched() {
		return o.dry.CreatePoll(ctx, in)
	}
	return o.realOps.CreatePoll(ctx, in)
}

// PollStatus implements twitchaction.Ops.
func (o SwitchOps) PollStatus(ctx context.Context, pollID string) (twitchaction.PollStatus, error) {
	if o.switched() {
		return o.dry.PollStatus(ctx, pollID)
	}
	return o.realOps.PollStatus(ctx, pollID)
}

// CreatePrediction implements twitchaction.Ops.
func (o SwitchOps) CreatePrediction(ctx context.Context, in twitchaction.PredictionInput) (string, error) {
	if o.switched() {
		return o.dry.CreatePrediction(ctx, in)
	}
	return o.realOps.CreatePrediction(ctx, in)
}

// PredictionStatus implements twitchaction.Ops.
func (o SwitchOps) PredictionStatus(ctx context.Context, pollID string) (twitchaction.PredictionStatus, error) {
	if o.switched() {
		return o.dry.PredictionStatus(ctx, pollID)
	}
	return o.realOps.PredictionStatus(ctx, pollID)
}

// CreateReward implements twitchaction.Ops.
func (o SwitchOps) CreateReward(ctx context.Context, title string, cost int) (string, error) {
	if o.switched() {
		return o.dry.CreateReward(ctx, title, cost)
	}
	return o.realOps.CreateReward(ctx, title, cost)
}

// SetReward implements twitchaction.Ops.
func (o SwitchOps) SetReward(ctx context.Context, title string, enabled bool) error {
	if o.switched() {
		return o.dry.SetReward(ctx, title, enabled)
	}
	return o.realOps.SetReward(ctx, title, enabled)
}

// DeleteReward implements twitchaction.Ops.
func (o SwitchOps) DeleteReward(ctx context.Context, title string) error {
	if o.switched() {
		return o.dry.DeleteReward(ctx, title)
	}
	return o.realOps.DeleteReward(ctx, title)
}
