// SPDX-License-Identifier: MIT

package feed_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
)

func userWith(login string) *user.User {
	return &user.User{
		ID:         id.New(),
		Identities: []user.Identity{{Platform: platform.Twitch, PlatformUserID: "u-1", Login: login, DisplayName: "Display"}},
	}
}

func TestFromPayload(t *testing.T) {
	t.Parallel()
	p := platform.Twitch

	cases := []struct {
		name    string
		typ     event.Type
		payload eventtype.Payload
		want    feed.Event
		ok      bool
	}{
		{
			name:    "follow",
			typ:     eventtype.ChannelFollow,
			payload: eventtype.Payload{Platform: p, User: userWith("ada")},
			want:    feed.Event{Platform: p, Type: eventtype.ChannelFollow, Login: "ada", DisplayName: "Display"},
			ok:      true,
		},
		{
			name: "subscribe tier 2 with message",
			typ:  eventtype.ChannelSubscribe,
			payload: eventtype.Payload{Platform: p, User: userWith("ada"),
				Details: eventtype.Details{
					Subscription: &eventtype.Subscription{Plan: "2000"},
					Message:      &eventtype.Message{Text: "cheers"},
				}},
			want: feed.Event{Platform: p, Type: eventtype.ChannelSubscribe, Login: "ada", DisplayName: "Display", Tier: 2, Text: "cheers"},
			ok:   true,
		},
		{
			name: "resubscribe prime has no tier",
			typ:  eventtype.ChannelResubscribe,
			payload: eventtype.Payload{Platform: p, User: userWith("ada"),
				Details: eventtype.Details{Subscription: &eventtype.Subscription{Plan: "Prime"}}},
			want: feed.Event{Platform: p, Type: eventtype.ChannelResubscribe, Login: "ada", DisplayName: "Display"},
			ok:   true,
		},
		{
			name: "single gift count 1",
			typ:  eventtype.ChannelSubscriptionGift,
			payload: eventtype.Payload{Platform: p, User: userWith("ada"),
				Details: eventtype.Details{Gift: &eventtype.Gift{Count: 1}}},
			want: feed.Event{Platform: p, Type: eventtype.ChannelSubscriptionGift, Login: "ada", DisplayName: "Display", Amount: 1},
			ok:   true,
		},
		{
			name: "mass gift count 5",
			typ:  eventtype.ChannelSubscriptionMassGift,
			payload: eventtype.Payload{Platform: p, User: userWith("ada"),
				Details: eventtype.Details{Gift: &eventtype.Gift{Count: 5}}},
			want: feed.Event{Platform: p, Type: eventtype.ChannelSubscriptionMassGift, Login: "ada", DisplayName: "Display", Amount: 5},
			ok:   true,
		},
		{
			name: "raid with viewers",
			typ:  eventtype.ChannelRaid,
			payload: eventtype.Payload{Platform: p, User: userWith("bob"),
				Details: eventtype.Details{Raid: &eventtype.Raid{Viewers: 42}}},
			want: feed.Event{Platform: p, Type: eventtype.ChannelRaid, Login: "bob", DisplayName: "Display", Amount: 42},
			ok:   true,
		},
		{
			name: "cheer with bits and text",
			typ:  eventtype.TwitchBitsCheer,
			payload: eventtype.Payload{Platform: p, User: userWith("ada"),
				Details: eventtype.Details{
					Bits:    &eventtype.Bits{Amount: 200},
					Message: &eventtype.Message{Text: "go go go"},
				}},
			want: feed.Event{Platform: p, Type: eventtype.TwitchBitsCheer, Login: "ada", DisplayName: "Display", Amount: 200, Text: "go go go"},
			ok:   true,
		},
		{
			name:    "anonymous gift has no user",
			typ:     eventtype.ChannelSubscriptionGift,
			payload: eventtype.Payload{Platform: p, Details: eventtype.Details{Gift: &eventtype.Gift{Anonymous: true, Count: 1}}},
			want:    feed.Event{Platform: p, Type: eventtype.ChannelSubscriptionGift, Amount: 1},
			ok:      true,
		},
		{
			name:    "chat message is not a feed event",
			typ:     eventtype.ChatMessage,
			payload: eventtype.Payload{Platform: p, User: userWith("ada")},
			ok:      false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, ok := feed.FromPayload(c.typ, c.payload)
			require.Equal(t, c.ok, ok)
			if !c.ok {
				return
			}
			// UserID is a generated ID; set it iff there is a user, then
			// compare the rest.
			if c.payload.User == nil {
				require.True(t, got.UserID.IsZero())
			} else {
				require.Equal(t, c.payload.User.ID, got.UserID)
			}
			got.UserID = id.ID{}
			c.want.UserID = id.ID{}
			require.Equal(t, c.want, got)
		})
	}
}

func TestStatsDelta(t *testing.T) {
	t.Parallel()
	p := platform.Twitch
	cases := []struct {
		name string
		ev   feed.Event
		want feed.StatsDelta
	}{
		{name: "follow", ev: feed.Event{Type: eventtype.ChannelFollow}, want: feed.StatsDelta{Followers: 1}},
		{name: "subscribe", ev: feed.Event{Type: eventtype.ChannelSubscribe}, want: feed.StatsDelta{Subscribers: 1}},
		{name: "resubscribe", ev: feed.Event{Type: eventtype.ChannelResubscribe}, want: feed.StatsDelta{Resubscribers: 1}},
		{name: "single gift", ev: feed.Event{Type: eventtype.ChannelSubscriptionGift, Amount: 1}, want: feed.StatsDelta{Gifts: 1}},
		{name: "mass gift", ev: feed.Event{Type: eventtype.ChannelSubscriptionMassGift, Amount: 5}, want: feed.StatsDelta{Gifts: 5}},
		{name: "gift without count defaults to 1", ev: feed.Event{Type: eventtype.ChannelSubscriptionGift}, want: feed.StatsDelta{Gifts: 1}},
		{name: "raid", ev: feed.Event{Type: eventtype.ChannelRaid, Amount: 42}, want: feed.StatsDelta{Raids: 1}},
		{name: "cheer", ev: feed.Event{Type: eventtype.TwitchBitsCheer, Amount: 200}, want: feed.StatsDelta{Cheers: 1, BitsTotal: 200}},
		{name: "not a feed event", ev: feed.Event{Platform: p, Type: eventtype.ChatMessage}, want: feed.StatsDelta{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, c.want, c.ev.StatsDelta())
		})
	}
}
