// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// reconcileInterval is how often the manager re-aligns the
// subscriptions (roadmap 4.3).
const reconcileInterval = 10 * time.Minute

// Spec is the desired state of one EventSub subscription: the event
// type with a fixed version and the condition naming the object the
// events are about.
type Spec struct {
	// EventType is the EventSub event type, e.g. "channel.follow".
	EventType string
	// Version is the fixed event version.
	Version string
	// User is true for user.whisper.message (user_id condition: the
	// account as whisper receiver); all the other events are channel
	// and stream events (broadcaster_user_id).
	User bool
	// HomeOnly is true for the stream state events: they report the
	// stream of the home channel only (spec twitch-multi-channel.md,
	// B10), so an additional channel does not subscribe to them.
	HomeOnly bool
}

// desired is the table of the subscriptions of this adapter: exactly
// the event types that the mapping (map.go) handles, nothing else
// (plan 2026-10-10-eventsub-websocket.md, open point 3: the manager
// must not subscribe to events without a handler). The reference is
// plan appendix A.2 (4.3) and the specification twitch-events.md
// (4.4, B1, B15).
//
//nolint:gochecknoglobals // the static desired state; it carries the account ID nowhere
var desired = []Spec{
	{EventType: "stream.online", Version: "1", HomeOnly: true},
	{EventType: "stream.offline", Version: "1", HomeOnly: true},
	{EventType: "channel.follow", Version: "2"},
	{EventType: "channel.raid", Version: "1"},
	{EventType: "channel.chat.message", Version: "2"},
	{EventType: "channel.chat.message_delete", Version: "2"},
	{EventType: "channel.chat.notification", Version: "2"},
	{EventType: "channel.cheer", Version: "1"},
	{EventType: "channel.moderate", Version: "2"},
	{EventType: "channel.shared_chat.begin", Version: "1"},
	{EventType: "channel.shared_chat.update", Version: "1"},
	{EventType: "channel.shared_chat.end", Version: "1"},
	{EventType: "channel.hype_train.start", Version: "1"},
	{EventType: "channel.hype_train.progress", Version: "1"},
	{EventType: "channel.hype_train.end", Version: "1"},
	{EventType: "channel.ad.started", Version: "1"},
	{EventType: "channel.shoutout.received", Version: "1"},
	{EventType: "channel.goal.start", Version: "1"},
	{EventType: "channel.goal.progress", Version: "1"},
	{EventType: "channel.goal.complete", Version: "1"},
	{EventType: "channel.charity.progress", Version: "1"},
	{EventType: "channel.charity.complete", Version: "1"},
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
	specs     []Spec
	log       *slog.Logger
	interval  time.Duration
	sessionID func(ctx context.Context) (string, error)
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
		specs:     specs,
		log:       log,
		interval:  interval,
		sessionID: o.SessionID,
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
	for _, d := range m.specs {
		if d.User {
			if m.whisper != "" {
				wants = append(wants, wantSpec{d, helix.SubscriptionCondition{UserID: m.whisper}})
			}
			continue
		}
		for _, b := range m.channels {
			wants = append(wants, wantSpec{d, helix.SubscriptionCondition{BroadcasterUserID: b}})
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
	// (e.g. left over of an older version of the app).
	for _, s := range actual {
		if want[specKeyOf(s.EventType, s.Version, s.Condition)] {
			continue
		}
		if err := m.helix.RevokeSubscription(ctx, s.ID); err != nil {
			return err
		}
		m.log.InfoContext(ctx, "eventsub subscription revoked", "eventtype", s.EventType)
	}
	return nil
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
