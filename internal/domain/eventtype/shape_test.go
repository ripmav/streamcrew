// SPDX-License-Identifier: MIT

package eventtype_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
)

// input is the data of an event for Shape.Check.
type input struct {
	user, target bool
	details      eventtype.Details
}

// fill returns an input with every part the shape requires or allows.
func fill(sh eventtype.Shape) input {
	has := func(p eventtype.Presence) bool { return p != eventtype.Absent }
	in := input{user: has(sh.User), target: has(sh.Target)}
	if has(sh.Message) {
		in.details.Message = &eventtype.Message{Text: "hi"}
	}
	if has(sh.Raid) {
		in.details.Raid = &eventtype.Raid{Viewers: 3}
	}
	if has(sh.Subscription) {
		in.details.Subscription = &eventtype.Subscription{Plan: "1000"}
	}
	if has(sh.Gift) {
		in.details.Gift = &eventtype.Gift{Count: 1}
	}
	return in
}

// TestShapes covers B9 of events.md: the table as data. Each
// platform-neutral type of a platform has a shape; data with all parts it
// has pass; without a required part or with a part it does not have, they
// fail.
func TestShapes(t *testing.T) {
	t.Parallel()
	want := map[event.Type]string{
		eventtype.ChannelStreamStart:          "",
		eventtype.ChannelStreamStop:           "",
		eventtype.ChannelFollow:               "user",
		eventtype.ChannelRaid:                 "user raid",
		eventtype.ChannelSubscribe:            "user subscription",
		eventtype.ChannelResubscribe:          "user subscription (message)",
		eventtype.ChannelSubscriptionGift:     "(user) target subscription gift",
		eventtype.ChannelSubscriptionMassGift: "(user) subscription gift",
		eventtype.ChatMessage:                 "user message",
		eventtype.ChatMessageDelete:           "user message",
		eventtype.ChatWhisper:                 "user message",
		eventtype.ChatUserJoin:                "user",
		eventtype.ChatUserLeave:               "user",
		eventtype.ChatUserNew:                 "user",
		eventtype.ChatUserEntrance:            "user message",
		eventtype.ChatUserFirstMessage:        "user message",
		eventtype.ChatUserTimeout:             "user",
		eventtype.ChatUserBan:                 "user",
	}
	for _, d := range eventtype.All() {
		sh, ok := eventtype.ShapeOf(d.Type)
		switch {
		case d.Platform == "" && d.Type != eventtype.AppStarted && d.Type != eventtype.AppStopping:
			require.True(t, ok, d.Type)
			assert.Equal(t, want[d.Type], describe(sh), d.Type)
		case d.Neutral != "":
			neutral, _ := eventtype.ShapeOf(d.Neutral)
			assert.Equal(t, neutral, sh, "%s has the shape of %s", d.Type, d.Neutral)
		default:
			assert.False(t, ok, d.Type)
		}
		if !ok {
			continue
		}
		full := fill(sh)
		if full.details.Gift != nil && sh.User == eventtype.Optional {
			full.details.Gift.Anonymous = false
		}
		require.NoError(t, sh.Check(d.Type, full.user, full.target, full.details), d.Type)
	}

	sh, _ := eventtype.ShapeOf(eventtype.ChannelRaid)
	assert.ErrorIs(t, sh.Check(eventtype.ChannelRaid, false, false, eventtype.Details{Raid: &eventtype.Raid{}}), eventtype.ErrShape, "no user")
	assert.ErrorIs(t, sh.Check(eventtype.ChannelRaid, true, true, eventtype.Details{Raid: &eventtype.Raid{}}), eventtype.ErrShape, "a target")
	assert.ErrorIs(t, sh.Check(eventtype.ChannelRaid, true, false, eventtype.Details{Raid: &eventtype.Raid{Viewers: -1}}), eventtype.ErrShape)
	assert.ErrorIs(t, sh.Check(eventtype.ChannelRaid, true, false, eventtype.Details{Raid: &eventtype.Raid{}, Message: &eventtype.Message{Text: "x"}}), eventtype.ErrShape)
}

// describe writes the parts of a shape, optional ones in parentheses.
func describe(sh eventtype.Shape) string {
	out := ""
	for _, part := range []struct {
		name string
		p    eventtype.Presence
	}{
		{"user", sh.User}, {"target", sh.Target}, {"raid", sh.Raid},
		{"subscription", sh.Subscription}, {"gift", sh.Gift}, {"message", sh.Message},
	} {
		word := ""
		switch part.p {
		case eventtype.Required:
			word = part.name
		case eventtype.Optional:
			word = "(" + part.name + ")"
		case eventtype.Absent:
			continue
		}
		if out != "" {
			out += " "
		}
		out += word
	}
	return out
}

// TestShapeDetails covers the checks of the values (B9, B29).
func TestShapeDetails(t *testing.T) {
	t.Parallel()
	gift, _ := eventtype.ShapeOf(eventtype.ChannelSubscriptionGift)
	sub := &eventtype.Subscription{Plan: "1000"}
	assert.NoError(t, gift.Check(eventtype.ChannelSubscriptionGift, false, true,
		eventtype.Details{Subscription: sub, Gift: &eventtype.Gift{Anonymous: true, Count: 1}}), "B29")
	assert.ErrorIs(t, gift.Check(eventtype.ChannelSubscriptionGift, true, true,
		eventtype.Details{Subscription: sub, Gift: &eventtype.Gift{Anonymous: true, Count: 1}}), eventtype.ErrShape, "anonymous with a user")
	assert.ErrorIs(t, gift.Check(eventtype.ChannelSubscriptionGift, false, true,
		eventtype.Details{Subscription: sub, Gift: &eventtype.Gift{Count: 1}}), eventtype.ErrShape, "not anonymous without a user")
	assert.ErrorIs(t, gift.Check(eventtype.ChannelSubscriptionGift, true, true,
		eventtype.Details{Subscription: sub, Gift: &eventtype.Gift{Count: 2}}), eventtype.ErrShape, "a single gift is one")
	assert.ErrorIs(t, gift.Check(eventtype.ChannelSubscriptionGift, true, true,
		eventtype.Details{Subscription: &eventtype.Subscription{}, Gift: &eventtype.Gift{Count: 1}}), eventtype.ErrShape, "no plan")

	mass, _ := eventtype.ShapeOf(eventtype.ChannelSubscriptionMassGift)
	assert.ErrorIs(t, mass.Check(eventtype.ChannelSubscriptionMassGift, true, false,
		eventtype.Details{Subscription: sub, Gift: &eventtype.Gift{}}), eventtype.ErrShape, "no gift")

	msg, _ := eventtype.ShapeOf(eventtype.ChatMessage)
	assert.ErrorIs(t, msg.Check(eventtype.ChatMessage, true, false, eventtype.Details{Message: &eventtype.Message{}}), eventtype.ErrShape, "no text")
	del, _ := eventtype.ShapeOf(eventtype.ChatMessageDelete)
	assert.NoError(t, del.Check(eventtype.ChatMessageDelete, true, false, eventtype.Details{Message: &eventtype.Message{ID: "m1"}}), "deleted text unknown")
}

func TestSingleGiftAndSpecific(t *testing.T) {
	t.Parallel()
	single, ok := eventtype.SingleGift(eventtype.TwitchSubscriptionMassGift)
	require.True(t, ok)
	assert.Equal(t, eventtype.TwitchSubscriptionGift, single)
	_, ok = eventtype.SingleGift(eventtype.ChannelSubscriptionGift)
	assert.False(t, ok)

	specific, ok := eventtype.Specific(platform.Twitch, eventtype.ChannelStreamStart)
	require.True(t, ok)
	assert.Equal(t, eventtype.TwitchStreamStart, specific)
	_, ok = eventtype.Specific(platform.Mock, eventtype.ChannelStreamStart)
	assert.False(t, ok)
}
