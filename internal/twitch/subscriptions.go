// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// reconcileInterval is how often the manager re-aligns the
// subscriptions (roadmap 4.3).
const reconcileInterval = 10 * time.Minute

// Spec is the desired state of one EventSub subscription: the event
// type with a fixed version and the condition naming the object the
// events are about. The event names, versions and conditions follow
// the EventSub Reference (plan 2026-10-22-eventsub-reference-fix.md,
// Findings F1–F7): the names are the current ones (e.g.
// channel.hype_train.begin, channel.ad_break.begin, channel.goal.end,
// channel.charity_campaign.progress, channel.shoutout.receive).
type Spec struct {
	// EventType is the EventSub event type, e.g. "channel.follow".
	EventType string
	// Version is the fixed event version.
	Version string
	// User is true for user.whisper.message (user_id condition: the
	// account as whisper receiver); all the other events are channel
	// and stream events (broadcaster_user_id).
	User bool
	// HomeOnly is true for the events whose condition the token owner
	// limits: the stream state events (spec twitch-multi-channel.md,
	// B10) and the goal events, whose broadcaster_user_id must match
	// the user_id of the access token (EventSub Reference "Goals
	// Condition").
	HomeOnly bool
	// ChatAsUser is true for the chat events (channel.chat.message,
	// channel.chat.message_delete, channel.chat.notification): their
	// condition carries the user_id of the token owner ("The User ID
	// to read chat as", EventSub Reference "Conditions").
	ChatAsUser bool
	// Moderator is true for the events whose condition carries the
	// moderator_user_id of the token owner (channel.follow v2,
	// channel.moderate v2; the broadcaster user ID when the token is
	// the broadcaster's, EventSub Reference "Channel Follow
	// Condition").
	Moderator bool
	// RaidTo is true for channel.raid: the condition names the raided
	// channel as to_broadcaster_user_id (the condition must name
	// from_ or to_, not both, EventSub Reference "Channel Raid
	// Condition").
	RaidTo bool
}

// desired is the table of the subscriptions of this adapter: exactly
// the event types that the mapping (map.go) handles, nothing else
// (plan 2026-10-10-eventsub-websocket.md, open point 3: the manager
// must not subscribe to events without a handler). The names, versions
// and conditions are the current ones of the EventSub Reference
// (plan 2026-10-22-eventsub-reference-fix.md, Findings F1–F7).
//
//nolint:gochecknoglobals // the static desired state; it carries the account ID nowhere
var desired = []Spec{
	{EventType: "stream.online", Version: "1", HomeOnly: true},
	{EventType: "stream.offline", Version: "1", HomeOnly: true},
	{EventType: "channel.follow", Version: "2", Moderator: true},
	{EventType: "channel.raid", Version: "1", RaidTo: true},
	{EventType: "channel.chat.message", Version: "1", ChatAsUser: true},
	{EventType: "channel.chat.message_delete", Version: "1", ChatAsUser: true},
	{EventType: "channel.chat.notification", Version: "1", ChatAsUser: true},
	{EventType: "channel.cheer", Version: "1"},
	{EventType: "channel.moderate", Version: "2", Moderator: true},
	{EventType: "channel.shared_chat.begin", Version: "1"},
	{EventType: "channel.shared_chat.update", Version: "1"},
	{EventType: "channel.shared_chat.end", Version: "1"},
	{EventType: "channel.hype_train.begin", Version: "2"},
	{EventType: "channel.hype_train.progress", Version: "2"},
	{EventType: "channel.hype_train.end", Version: "2"},
	{EventType: "channel.ad_break.begin", Version: "1"},
	{EventType: "channel.shoutout.receive", Version: "1"},
	{EventType: "channel.goal.begin", Version: "1", HomeOnly: true},
	{EventType: "channel.goal.progress", Version: "1", HomeOnly: true},
	{EventType: "channel.goal.end", Version: "1", HomeOnly: true},
	{EventType: "channel.charity_campaign.progress", Version: "1"},
	{EventType: "channel.charity_campaign.stop", Version: "1"},
	{EventType: "channel.channel_points_automatic_reward_redemption.add", Version: "1"},
	{EventType: "channel.channel_points_custom_reward_redemption.add", Version: "1"},
	{EventType: "user.whisper.message", Version: "1", User: true},
}

// ChannelSpecs is the desired table without the home-only specs (the
// stream state events, spec twitch-multi-channel.md, B10): the desired
// state of an additional channel.
func ChannelSpecs() []Spec {
	specs := make([]Spec, 0, len(desired))
	for _, d := range desired {
		if d.HomeOnly {
			continue
		}
		specs = append(specs, d)
	}
	return specs
}

// Manager maintains the EventSub subscriptions of the channels it is
// given (roadmap 4.3, 4.6): it keeps the actual state (Helix
// GET/POST/DELETE /eventsub/subscriptions) in line with the desired
// state — every spec for every channel, plus the whisper subscription
// for its receiver.
type Manager struct {
	helix     *helix.Client
	channels  []string
	whisper   string
	moderator string
	specs     []Spec
	log       *slog.Logger
	interval  time.Duration
	sessionID func(ctx context.Context) (string, error)
	ready     func(ctx context.Context) (bool, error)
}

// ManagerOptions configures NewManager.
type ManagerOptions struct {
	// Helix is the Helix client of the account that owns the
	// subscriptions.
	Helix *helix.Client
	// Channels are the broadcaster user IDs the channel and stream
	// events are subscribed for; the home manager gets the home channel
	// only, the additional-channels manager the additional channels.
	Channels []string
	// Whisper is the user ID that receives user.whisper.message (the
	// user_id condition); empty to not subscribe to whispers.
	Whisper string
	// Moderator is the user ID of the token owner (the broadcaster for
	// the home channel, the bot for an additional channel): the
	// moderator_user_id condition of channel.follow and
	// channel.moderate v2 (the broadcaster user ID when the token is
	// the broadcaster's, EventSub Reference "Channel Follow
	// Condition") and the user_id condition of the chat events ("the
	// user ID to read chat as"); empty falls back to the broadcaster.
	Moderator string
	// Specs is the desired table; the caller leaves out the home-only
	// specs for the additional channels (stream state, spec
	// twitch-multi-channel.md, B10).
	Specs []Spec
	// Logger logs the reconciliations (Code-ADR-0003); if nil, the
	// log is discarded.
	Logger *slog.Logger
	// Interval is the re-align interval (tests shorten it).
	Interval time.Duration
	// SessionID returns the ID of the current EventSub session for
	// the websocket transport of the subscriptions; it blocks until
	// the session is established. Nil for a session-less transport
	// (tests).
	SessionID func(ctx context.Context) (string, error)
	// Ready reports whether the channels are operated (spec
	// twitch-multi-channel.md, B3): for the additional channels the mod
	// status of the bot, checked at the start and at every reconciliation.
	// While it reports not ready, the manager holds no subscriptions;
	// on an error it leaves the state as it is. Nil for the home channel.
	Ready func(ctx context.Context) (bool, error)
}

// NewManager creates a Manager.
func NewManager(o ManagerOptions) *Manager {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	interval := o.Interval
	if interval <= 0 {
		interval = reconcileInterval
	}
	specs := o.Specs
	if len(specs) == 0 {
		specs = desired
	}
	return &Manager{
		helix:     o.Helix,
		channels:  o.Channels,
		whisper:   o.Whisper,
		moderator: o.Moderator,
		specs:     specs,
		log:       log,
		interval:  interval,
		sessionID: o.SessionID,
		ready:     o.Ready,
	}
}

// Run reconciles at once and then on the interval, until the context
// is canceled.
func (m *Manager) Run(ctx context.Context) error {
	if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
		m.log.ErrorContext(ctx, "the eventsub reconciliation failed", "error", err)
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := m.Reconcile(ctx); err != nil && ctx.Err() == nil {
				m.log.ErrorContext(ctx, "the eventsub reconciliation failed", "error", err)
			}
		}
	}
}

// Reconcile aligns the actual state with the desired state once: it
// creates the missing subscriptions and revokes the surplus ones. A
// 400 answer (limit reached, condition occupied) is skipped with a
// warning, not an error (roadmap 4.3); every other error is returned.
// The subscriptions are delivered to the current EventSub session
// (websocket transport); Run calls it at the start and on the
// interval; the composition root calls it once more right after a
// token revocation.
func (m *Manager) Reconcile(ctx context.Context) error {
	// B3: while the channels are not operated, the manager holds no
	// subscriptions: the own surplus is revoked below, nothing is created.
	operated := true
	if m.ready != nil {
		var err error
		if operated, err = m.ready(ctx); err != nil {
			m.log.WarnContext(ctx, "the readiness of the channels is unknown", "error", err)
			return nil
		}
	}
	sessionID := ""
	if m.sessionID != nil {
		var err error
		sessionID, err = m.sessionID(ctx)
		if err != nil {
			return err
		}
	}
	actual, err := m.helix.GetSubscriptions(ctx)
	if err != nil {
		return err
	}
	have := map[specKey]bool{}
	for _, s := range actual {
		have[specKeyOf(s.EventType, s.Version, s.Condition)] = true
	}

	// The desired (spec, condition) pairs: every channel and stream spec
	// for every channel, plus the whisper spec for its receiver.
	type wantSpec struct {
		d Spec
		c helix.SubscriptionCondition
	}
	var wants []wantSpec
	if operated {
		for _, d := range m.specs {
			if d.User {
				if m.whisper != "" {
					wants = append(wants, wantSpec{d, helix.SubscriptionCondition{UserID: m.whisper}})
				}
				continue
			}
			for _, b := range m.channels {
				wants = append(wants, wantSpec{d, m.conditionOf(d, b)})
			}
		}
	}

	want := map[specKey]bool{}
	for _, w := range wants {
		k := specKeyOf(w.d.EventType, w.d.Version, w.c)
		want[k] = true
		if have[k] {
			continue
		}
		if _, err := m.helix.CreateSubscription(ctx, helix.CreateSubscriptionInput{
			EventType: w.d.EventType,
			Version:   w.d.Version,
			Condition: w.c,
			Transport: helix.EventSubTransport{Method: "websocket", SessionID: sessionID},
		}); err != nil {
			var se *httpclient.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				m.log.WarnContext(ctx, "skip the eventsub subscription", "eventtype", w.d.EventType, "error", err)
				continue
			}
			return err
		}
		m.log.InfoContext(ctx, "eventsub subscription created", "eventtype", w.d.EventType)
	}

	// The surplus: actual subscriptions outside the desired state
	// (e.g. left over of an older version of the app, e.g. the old
	// event names of the pre-2026-10-22 table). The managers
	// revoke their own subscriptions only: the additional channels
	// share the bot token, whose listing also carries the subscriptions
	// of the other channels (spec twitch-multi-channel.md, B4).
	// The revocation runs before the creation, so a re-created
	// subscription (same type and version, new condition or session)
	// does not collide with the one it replaces (the twitch-cli mock
	// rejects the duplicate per type and version; production per
	// condition).
	for _, s := range actual {
		if want[specKeyOf(s.EventType, s.Version, s.Condition)] {
			continue
		}
		if !m.owns(s) {
			continue
		}
		if err := m.helix.RevokeSubscription(ctx, s.ID); err != nil {
			return err
		}
		m.log.InfoContext(ctx, "eventsub subscription revoked", "eventtype", s.EventType)
	}
	return nil
}

// conditionOf is the EventSub condition of one spec for one channel
// (EventSub Reference "Conditions", plan
// 2026-10-22-eventsub-reference-fix.md, F3–F7): most events name the
// broadcaster only; the chat events read as the token owner, follow and
// moderate v2 carry the token owner as moderator, and the raid names
// the raided channel as to_broadcaster_user_id.
func (m *Manager) conditionOf(d Spec, broadcaster string) helix.SubscriptionCondition {
	moderator := m.moderator
	if moderator == "" {
		moderator = broadcaster
	}
	switch {
	case d.RaidTo:
		return helix.SubscriptionCondition{ToBroadcasterUserID: broadcaster}
	case d.ChatAsUser:
		return helix.SubscriptionCondition{BroadcasterUserID: broadcaster, UserID: moderator}
	case d.Moderator:
		return helix.SubscriptionCondition{BroadcasterUserID: broadcaster, ModeratorUserID: moderator}
	default:
		return helix.SubscriptionCondition{BroadcasterUserID: broadcaster}
	}
}

// owns reports whether the subscription belongs to the manager: its
// broadcaster is one of the manager's channels, or the receiver is the
// manager's whisper.
func (m *Manager) owns(s helix.EventSubSubscription) bool {
	if slices.Contains(m.channels, s.Condition.BroadcasterUserID) {
		return true
	}
	return m.whisper != "" && s.Condition.UserID == m.whisper
}

// specKey names a subscription without its ID.
type specKey struct {
	eventType string
	version   string
	condition helix.SubscriptionCondition
}

func specKeyOf(eventType, version string, c helix.SubscriptionCondition) specKey {
	return specKey{eventType: eventType, version: version, condition: c}
}
