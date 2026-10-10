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
	{EventType: "stream.online", Version: "1"},
	{EventType: "stream.offline", Version: "1"},
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

// Manager maintains the EventSub subscriptions of the streamer
// account (roadmap 4.3): it keeps the actual state (Helix
// GET/POST/DELETE /eventsub/subscriptions) in line with the desired
// state of the table above.
type Manager struct {
	helix     *helix.Client
	accountID string
	log       *slog.Logger
	interval  time.Duration
}

// ManagerOptions configures NewManager.
type ManagerOptions struct {
	// Helix is the Helix client of the streamer account.
	Helix *helix.Client
	// AccountID is the user ID of the streamer account (the
	// broadcaster of the conditions, and the whisper receiver).
	AccountID string
	// Logger logs the reconciliations (Code-ADR-0003); if nil, the
	// log is discarded.
	Logger *slog.Logger
	// Interval is the re-align interval (tests shorten it).
	Interval time.Duration
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
	return &Manager{
		helix:     o.Helix,
		accountID: o.AccountID,
		log:       log,
		interval:  interval,
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
// Run calls it at the start and on the interval; the composition root
// calls it once more right after a token revocation.
func (m *Manager) Reconcile(ctx context.Context) error {
	actual, err := m.helix.GetSubscriptions(ctx)
	if err != nil {
		return err
	}
	have := map[specKey]bool{}
	for _, s := range actual {
		have[specKeyOf(s.EventType, s.Version, s.Condition)] = true
	}

	want := map[specKey]bool{}
	for _, d := range desired {
		k := specKeyOf(d.EventType, d.Version, m.condition(d))
		want[k] = true
		if have[k] {
			continue
		}
		if _, err := m.helix.CreateSubscription(ctx, helix.CreateSubscriptionInput{
			EventType: d.EventType,
			Version:   d.Version,
			Condition: m.condition(d),
		}); err != nil {
			var se *httpclient.StatusError
			if errors.As(err, &se) && se.StatusCode == http.StatusBadRequest {
				m.log.WarnContext(ctx, "skip the eventsub subscription", "eventtype", d.EventType, "error", err)
				continue
			}
			return err
		}
		m.log.InfoContext(ctx, "eventsub subscription created", "eventtype", d.EventType)
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

// condition is the condition of the desired spec: the account as
// broadcaster, or as whisper receiver (user.whisper.message).
func (m *Manager) condition(d Spec) helix.SubscriptionCondition {
	if d.User {
		return helix.SubscriptionCondition{UserID: m.accountID}
	}
	return helix.SubscriptionCondition{BroadcasterUserID: m.accountID}
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
