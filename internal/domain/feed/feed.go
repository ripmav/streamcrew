// SPDX-License-Identifier: MIT

// Package feed is the event feed and the per-session statistics (roadmap
// 5.3): the recent events of the feed types that frontends read, and the
// counters of each stream session.
package feed

import (
	"time"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
)

// Event is one feed event (roadmap 5.3): a follow, a subscription, a raid,
// a cheer or a donation, as the core sees it. The user's login and display
// name are a snapshot at the time of the event.
type Event struct {
	Platform platform.Name
	Type     event.Type
	// UserID is the user that caused the event; zero if there is none
	// (e.g. an anonymous gift).
	UserID      id.ID
	Login       string
	DisplayName string
	Time        time.Time
	// Amount is the number of the type: bits of a cheer, the viewers of a
	// raid, the count of a gifted subscription; 0 if the type has none.
	Amount int64
	// Cents is the donation, in hundredths of the main currency unit; 0 if
	// the type has none.
	Cents int64
	// Tier is the subscription tier (1 to 3); 0 if the type has none or
	// the plan is not a tiered one (e.g. Prime).
	Tier int
	// Text is the shared message: the sub or the cheer text; empty if the
	// event has none.
	Text string
}

// SessionStats is the statistics of one stream session (roadmap 5.3).
type SessionStats struct {
	Platform      platform.Name
	StartedAt     time.Time
	Followers     int64
	Subscribers   int64
	Resubscribers int64
	Gifts         int64
	Raids         int64
	Cheers        int64
	Donations     int64
	BitsTotal     int64
	DonationCents int64
}

// StatsDelta is what one feed event adds to the session statistics.
type StatsDelta struct {
	Followers     int64
	Subscribers   int64
	Resubscribers int64
	Gifts         int64
	Raids         int64
	Cheers        int64
	Donations     int64
	BitsTotal     int64
	DonationCents int64
}

// StatsDelta reports what e adds to the session statistics.
func (e Event) StatsDelta() StatsDelta {
	var d StatsDelta
	switch e.Type {
	case eventtype.ChannelFollow:
		d.Followers = 1
	case eventtype.ChannelSubscribe:
		d.Subscribers = 1
	case eventtype.ChannelResubscribe:
		d.Resubscribers = 1
	case eventtype.ChannelSubscriptionGift, eventtype.ChannelSubscriptionMassGift:
		d.Gifts = max(e.Amount, 1)
	case eventtype.ChannelRaid:
		d.Raids = 1
	case eventtype.TwitchBitsCheer:
		d.Cheers = 1
		d.BitsTotal = e.Amount
	default:
		// not a feed event
	}
	return d
}

// FromPayload builds the feed event of type t with payload p; ok is false
// if t is not a feed type (roadmap 5.3). The platform-neutral type decides;
// the time is not set here.
func FromPayload(t event.Type, p eventtype.Payload) (Event, bool) {
	if d, ok := eventtype.Lookup(t); ok && d.Neutral != "" {
		t = d.Neutral
	}
	ev := Event{Platform: p.Platform, Type: t}
	if p.User != nil {
		ev.UserID = p.User.ID
		if ident, ok := p.User.Identity(p.Platform); ok {
			ev.Login, ev.DisplayName = ident.Login, ident.DisplayName
		}
	}
	switch t {
	case eventtype.ChannelFollow:
	case eventtype.ChannelSubscribe, eventtype.ChannelResubscribe:
		if sub := p.Details.Subscription; sub != nil {
			ev.Tier = tierFromPlan(sub.Plan)
		}
		if m := p.Details.Message; m != nil {
			ev.Text = m.Text
		}
	case eventtype.ChannelSubscriptionGift, eventtype.ChannelSubscriptionMassGift:
		if g := p.Details.Gift; g != nil {
			ev.Amount = int64(g.Count)
		}
		if sub := p.Details.Subscription; sub != nil {
			ev.Tier = tierFromPlan(sub.Plan)
		}
	case eventtype.ChannelRaid:
		if r := p.Details.Raid; r != nil {
			ev.Amount = r.Viewers
		}
	case eventtype.TwitchBitsCheer:
		if b := p.Details.Bits; b != nil {
			ev.Amount = b.Amount
		}
		if m := p.Details.Message; m != nil {
			ev.Text = m.Text
		}
	default:
		return Event{}, false
	}
	return ev, true
}

// tierFromPlan maps a subscription plan to a tier: 1000 to 1, 2000 to 2,
// 3000 to 3, otherwise 0 (e.g. Prime).
func tierFromPlan(plan string) int {
	switch plan {
	case "1000":
		return 1
	case "2000":
		return 2
	case "3000":
		return 3
	default:
		return 0
	}
}
