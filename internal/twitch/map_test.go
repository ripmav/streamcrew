// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// fakeReceiver records what the mapper handed over.
type fakeReceiver struct {
	mu       sync.Mutex
	messages []connector.Incoming
	streams  []bool
	events   []connector.Event
}

func (r *fakeReceiver) Message(_ context.Context, m connector.Incoming) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, m)
	return nil
}

// received is a copy of the received messages.
func (r *fakeReceiver) received() []connector.Incoming {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]connector.Incoming(nil), r.messages...)
}

// eventList is a copy of the received events.
func (r *fakeReceiver) eventList() []connector.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]connector.Event(nil), r.events...)
}

// streamStates is a copy of the reported stream states, in order.
func (r *fakeReceiver) streamStates() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.streams...)
}

func (r *fakeReceiver) Join(_ context.Context, _ platform.Name, _ connector.Channel, _ user.Identity) error {
	return nil
}

func (r *fakeReceiver) Event(_ context.Context, e connector.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *fakeReceiver) Stream(_ context.Context, _ platform.Name, live bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streams = append(r.streams, live)
	return nil
}

func (r *fakeReceiver) lastMessage(t *testing.T) connector.Incoming {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.messages)
	return r.messages[len(r.messages)-1]
}

func (r *fakeReceiver) lastEvent(t *testing.T) connector.Event {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	require.NotEmpty(t, r.events)
	return r.events[len(r.events)-1]
}

// mapperFor builds a Mapper with the receiver, the bot ID and one event
// of the given type and payload.
func mapperFor(t *testing.T, r *fakeReceiver, botID string) *twitch.Mapper {
	t.Helper()
	dedup, err := connector.NewDedup(time.Minute)
	require.NoError(t, err)
	return twitch.NewMapper(twitch.MapperOptions{
		Receiver: r,
		Dedup:    dedup,
		BotID:    botID,
	})
}

func handle(t *testing.T, m *twitch.Mapper, eventType, payload string) {
	t.Helper()
	var e twitch.Event
	raw, err := json.Marshal(map[string]any{
		"subscription": map[string]any{
			"type":    eventType,
			"version": "1",
		},
		"event": json.RawMessage(payload),
	})
	require.NoError(t, err)
	require.NoError(t, jsonv2.Unmarshal(raw, &e))
	// The adapter sets the dedup key from the payload content (B22).
	e.ID = string(raw)
	m.OnMessage(t.Context(), e)
}

func TestMapChatMessage(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "9")
	handle(t, m, "channel.chat.message", `{
		"message_id": "m-1",
		"message": {
			"text": "hi :Kappa: there",
			"fragments": [
				{"type": "text", "text": "hi "},
				{"type": "emote", "text": ":Kappa:", "emote": {"id": "25", "emote_set_id": "1"}},
				{"type": "text", "text": " there"}
			]
		},
		"badges": [{"set_id": "moderator/1"}, {"set_id": "premium/1"}],
		"chatter_user_id": "8",
		"chatter_user_login": "alice",
		"chatter_user_name": "Alice"
	}`)

	msg := r.lastMessage(t)
	assert.Equal(t, "m-1", msg.Message.ID)
	assert.Equal(t, "hi :Kappa: there", msg.Message.Text)
	assert.Equal(t, []string{":Kappa:"}, msg.Message.Emotes)
	assert.Equal(t, "8", msg.Author.PlatformUserID)
	assert.Equal(t, "alice", msg.Author.Login)
	assert.Equal(t, "Alice", msg.Author.DisplayName)
	assert.True(t, msg.Author.Roles.Has(role.Moderator))
	assert.False(t, msg.Author.Roles.Has(role.Streamer))
	assert.False(t, msg.FromBot)
}

func TestMapChatMessageResolvesEmotes(t *testing.T) {
	r := &fakeReceiver{}
	catalog := emote.New([]emote.Source{fixedSource{emotes: []emote.Emote{{ID: "25", Code: "Kappa", Image: "img"}}}}, time.Hour, nil)
	catalog.Refresh(t.Context(), "chan")
	dedup, err := connector.NewDedup(time.Minute)
	require.NoError(t, err)
	m := twitch.NewMapper(twitch.MapperOptions{Receiver: r, Dedup: dedup, BotID: "9", Emotes: catalog, EmoteChannel: "chan"})
	handle(t, m, "channel.chat.message", `{
		"message_id": "m-1",
		"message": {
			"text": "hi ? there",
			"fragments": [
				{"type": "text", "text": "hi "},
				{"type": "emote", "emote": {"id": "25", "emote_set_id": "1"}},
				{"type": "text", "text": " there"}
			]
		},
		"badges": [{"set_id": "moderator/1"}],
		"chatter_user_id": "8",
		"chatter_user_login": "alice",
		"chatter_user_name": "Alice"
	}`)

	msg := r.lastMessage(t)
	// The fragment marks no code, so the catalog resolves the ID to its
	// canonical code.
	assert.Equal(t, []string{"Kappa"}, msg.Message.Emotes)
}

// fixedSource is a Source that lists one fixed set of emotes.
type fixedSource struct {
	emotes []emote.Emote
}

func (fixedSource) Name() string { return "test" }

func (f fixedSource) Emotes(context.Context, string) ([]emote.Emote, error) { return f.emotes, nil }

func TestMapChatMessageFromBot(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "9")
	handle(t, m, "channel.chat.message", `{"message_id":"m-1","message":{"text":"hi"},"chatter_user_id":"9","chatter_user_login":"bot"}`)
	assert.True(t, r.lastMessage(t).FromBot)
}

func TestMapEmotesInTextOrder(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.chat.message", `{
		"message_id": "m-1",
		"message": {
			"text": "x",
			"fragments": [
				{"type": "emote", "text": "c", "emote": {"id": "c"}},
				{"type": "emote", "text": "b", "emote": {"id": "b"}},
				{"type": "emote", "text": "a", "emote": {"id": "a"}}
			]
		},
		"chatter_user_id": "8",
		"chatter_user_login": "a"
	}`)
	assert.Equal(t, []string{"c", "b", "a"}, r.lastMessage(t).Message.Emotes)
}

func TestMapStream(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "stream.online", `{"id": "1"}`)
	handle(t, m, "stream.offline", `{"id": "2"}`)
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Equal(t, []bool{true, false}, r.streams)
}

func TestMapFollow(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.follow", `{"user_id": "8", "user_login": "alice", "user_name": "Alice"}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelFollow, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
}

func TestMapRaid(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.raid", `{"viewers": 42, "from_broadcaster_user_id": "8", "from_broadcaster_user_login": "bob", "from_broadcaster_user_name": "Bob", "to_broadcaster_user_id": "7"}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelRaid, ev.Type)
	assert.Equal(t, int64(42), ev.Details.Raid.Viewers)
}

func TestMapNotification(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")

	// A subscription.
	handle(t, m, "channel.chat.notification", `{
		"notice_type": "sub",
		"chatter_user_id": "8", "chatter_user_login": "alice",
		"chatter_is_anonymous": false,
		"sub": {"sub_tier": "1000"}
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelSubscribe, ev.Type)
	assert.Equal(t, "1000", ev.Details.Subscription.Plan)
	assert.Empty(t, ev.Details.Subscription.PlanName)

	// A resubscription with a message.
	handle(t, m, "channel.chat.notification", `{
		"notice_type": "resub",
		"chatter_user_id": "8", "chatter_user_login": "alice",
		"chatter_is_anonymous": false,
		"resub": {"cumulative_months": 3, "streak_months": 2, "sub_tier": "2000"},
		"message": {"text": "thanks"}
	}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelResubscribe, ev.Type)
	require.NotNil(t, ev.Details.Message)
	assert.Equal(t, "thanks", ev.Details.Message.Text)

	// A gift.
	handle(t, m, "channel.chat.notification", `{
		"notice_type": "sub_gift",
		"chatter_user_id": "8", "chatter_user_login": "alice",
		"chatter_is_anonymous": false,
		"sub_gift": {"sub_tier": "1000", "recipient_user_id": "10", "recipient_user_login": "carol", "recipient_user_name": "Carol"}
	}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSubscriptionGift, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
	require.NotNil(t, ev.Target)
	assert.Equal(t, "carol", ev.Target.Login)
	assert.False(t, ev.Details.Gift.Anonymous)
	assert.Equal(t, 1, ev.Details.Gift.Count)
	assert.Empty(t, ev.Details.Gift.CommunityGiftID, "a gift outside of a mass gift")

	// A gift of a mass action carries the community gift ID, so the
	// core can drop it when the action fired (events.md B5, "never
	// both").
	handle(t, m, "channel.chat.notification", `{
		"notice_type": "sub_gift",
		"chatter_user_id": "8", "chatter_user_login": "alice",
		"chatter_is_anonymous": false,
		"sub_gift": {"sub_tier": "1000", "recipient_user_id": "11", "recipient_user_login": "dave", "recipient_user_name": "Dave", "community_gift_id": "cg-1"}
	}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSubscriptionGift, ev.Type)
	assert.Equal(t, "cg-1", ev.Details.Gift.CommunityGiftID)

	// An anonymous mass gift: no user, no recipient list (the payload
	// names the total only).
	handle(t, m, "channel.chat.notification", `{
		"notice_type": "community_sub_gift",
		"chatter_user_id": "8", "chatter_user_login": "bob",
		"chatter_is_anonymous": true,
		"community_sub_gift": {"id": "cg-1", "total": 2, "sub_tier": "1000"}
	}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSubscriptionMassGift, ev.Type)
	assert.Nil(t, ev.User)
	assert.True(t, ev.Details.Gift.Anonymous)
	assert.Equal(t, 2, ev.Details.Gift.Count)
	assert.Empty(t, ev.Recipients)
}

func TestMapCheer(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.cheer", `{
		"is_anonymous": false,
		"bits": 100, "message": "cheers",
		"user_id": "8", "user_login": "alice", "user_name": "Alice"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchBitsCheer, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "8", ev.User.PlatformUserID)
	assert.Equal(t, int64(100), ev.Details.Bits.Amount)
	assert.Equal(t, "cheers", ev.Details.Message.Text)

	// An anonymous cheer names no user (code review 2026-10-24, T10):
	// the event carries the flag, the user is nil.
	handle(t, m, "channel.cheer", `{"is_anonymous": true, "bits": 50, "message": "cheers"}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchBitsCheer, ev.Type)
	assert.Nil(t, ev.User)
	require.NotNil(t, ev.Details.Bits)
	assert.True(t, ev.Details.Bits.Anonymous)
	assert.Equal(t, int64(50), ev.Details.Bits.Amount)
}

func TestMapModerate(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.moderate", `{
		"action": "timeout",
		"timeout": {"user_id": "8", "user_login": "alice", "user_name": "Alice"}
	}`)
	assert.Equal(t, eventtype.ChatUserTimeout, r.lastEvent(t).Type)
	handle(t, m, "channel.moderate", `{
		"action": "ban",
		"ban": {"user_id": "8", "user_login": "alice", "user_name": "Alice"}
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatUserBan, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)

	// The neutral moderation types the platform delivers beside ban and
	// timeout (code review 2026-10-24, T8).
	for _, tc := range []struct {
		action string
		want   any
	}{
		{"unban", eventtype.ChatUserUnban},
		{"untimeout", eventtype.ChatUserUntimeout},
		{"mod", eventtype.ChatUserMod},
		{"unmod", eventtype.ChatUserUnmod},
		{"vip", eventtype.ChatUserVIP},
		{"unvip", eventtype.ChatUserUnVIP},
	} {
		handle(t, m, "channel.moderate",
			`{"action": "`+tc.action+`", "`+tc.action+`": {"user_id": "8", "user_login": "alice", "user_name": "Alice"}}`)
		ev = r.lastEvent(t)
		assert.Equal(t, tc.want, ev.Type, tc.action)
		require.NotNil(t, ev.User)
		assert.Equal(t, "alice", ev.User.Login)
	}

	// An action without a target (clear, slow, …) is dropped with a
	// warning, not mapped.
	handle(t, m, "channel.moderate", `{"action": "clear"}`)
	assert.Len(t, r.eventList(), 8, "the unmapped action fires nothing")
}

func TestMapMessageDelete(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.chat.message_delete", `{
		"message_id": "m-9",
		"target_user_id": "8", "target_user_login": "alice", "target_user_name": "Alice"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatMessageDelete, ev.Type)
	assert.Equal(t, "m-9", ev.Details.Message.ID)
	assert.Empty(t, ev.Details.Message.Text)
}

func TestMapWhisper(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "user.whisper.message", `{
		"whisper": {"text": "secret"},
		"whisper_id": "w-1",
		"from_user_id": "8", "from_user_login": "alice", "from_user_name": "Alice"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatWhisper, ev.Type)
	assert.Equal(t, "secret", ev.Details.Message.Text)
}

func TestMapSharedChat(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.shared_chat.begin", `{"session_id": "s1"}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSharedChatStart, ev.Type)
	assert.Equal(t, "s1", ev.Details.SharedChat.SessionID)
	handle(t, m, "channel.shared_chat.update", `{"session_id": "s1"}`)
	assert.Equal(t, eventtype.TwitchSharedChatUpdate, r.lastEvent(t).Type)
	handle(t, m, "channel.shared_chat.end", `{"session_id": "s1"}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSharedChatEnd, ev.Type)
}

func TestMapDropsTheRepeats(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	msg := `{"message_id": "m-1", "message": {"text": "hi"}, "chatter_user_id": "8", "chatter_user_login": "a"}`
	handle(t, m, "channel.chat.message", msg)
	// The repeat: the same message arrives twice (B22).
	handle(t, m, "channel.chat.message", msg)
	handle(t, m, "channel.chat.message", `{"message_id": "m-2", "message": {"text": "next"}, "chatter_user_id": "8", "chatter_user_login": "a"}`)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.messages, 2)
	assert.Equal(t, "hi", r.messages[0].Message.Text)
	assert.Equal(t, "next", r.messages[1].Message.Text)
}

func TestMapDropsTheUnknown(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.poll.begin", `{"id": "1"}`)
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Empty(t, r.messages)
	assert.Empty(t, r.events)
	assert.Empty(t, r.streams)
}

func TestMapHypeTrain(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.hype_train.begin", `{"level": 1, "progress": 0, "goal": 10000}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainStart, ev.Type)
	assert.Equal(t, &eventtype.HypeTrain{Level: 1, Progress: 0, Goal: 10000}, ev.Details.HypeTrain)

	handle(t, m, "channel.hype_train.progress", `{"level": 2, "progress": 6000, "goal": 10000}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainProgress, ev.Type)
	assert.Equal(t, int64(6000), ev.Details.HypeTrain.Progress)

	// The payload names no reward interval and no end reason any more
	// (hype_train v2); the model keeps the fields empty.
	handle(t, m, "channel.hype_train.end", `{"level": 3, "progress": 12000, "goal": 10000}`)
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainEnd, ev.Type)
	assert.Empty(t, ev.Details.HypeTrain.Outcome)
}

func TestMapAdStart(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.ad_break.begin", `{"duration_seconds": 30, "is_automatic": false}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchAdStart, ev.Type)
	assert.Equal(t, &eventtype.AdBreak{Duration: 30}, ev.Details.AdBreak)
}

func TestMapShoutout(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.shoutout.receive", `{"viewer_count": 7, "from_broadcaster_user_id": "5", "from_broadcaster_user_login": "shouter", "from_broadcaster_user_name": "Shouter", "to_broadcaster_user_id": "7"}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchShoutoutReceive, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "5", ev.User.PlatformUserID)
	assert.Equal(t, &eventtype.Shoutout{Viewers: 7}, ev.Details.Shoutout)
}

func TestMapGoal(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.goal.begin", `{"current_amount": 0, "target_amount": 100}`)
	assert.Equal(t, eventtype.TwitchGoalStart, r.lastEvent(t).Type)
	handle(t, m, "channel.goal.end", `{"current_amount": 100, "target_amount": 100, "is_achieved": true}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchGoalEnd, ev.Type)
	// The payload names no currency any more; the model field stays
	// empty.
	assert.Equal(t, &eventtype.Goal{Current: 100, Target: 100}, ev.Details.Goal)
}

func TestMapCharity(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.charity_campaign.progress", `{
		"current_amount": {"value": 5000, "decimal_places": 2, "currency": "EUR"},
		"target_amount":  {"value": 20000, "decimal_places": 2, "currency": "EUR"},
		"charity_name": "Anonymous Charity"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchCharityDonation, ev.Type)
	assert.Equal(t, &eventtype.Charity{Current: 50, Target: 200, Currency: "EUR"}, ev.Details.Charity)
}

func TestMapChannelPointsRedemption(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.channel_points_automatic_reward_redemption.add", `{
		"id": "red-1",
		"reward": {"type": "send_message_to_stream_chat", "cost": 100},
		"user_id": "8", "user_login": "alice", "user_name": "Alice"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelPointsRedeem, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
	assert.Equal(t, &eventtype.ChannelPoints{Amount: 100, Reward: "send_message_to_stream_chat"}, ev.Details.ChannelPoints)
}

func TestMapCustomPowerUpRedemption(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.channel_points_custom_reward_redemption.add", `{
		"id": "red-2",
		"reward": {"id": "p1", "cost": 50},
		"user_input": "a hug",
		"status": "unfulfilled",
		"user_id": "8", "user_login": "alice", "user_name": "Alice"
	}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchCustomPowerUpRedeem, ev.Type)
	assert.Equal(t, &eventtype.CustomPowerUp{Reward: "p1"}, ev.Details.CustomPowerUp)
	assert.Equal(t, "a hug", ev.Details.Message.Text)
}

func TestMapModerationMessage(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.moderate", `{"action": "ban", "ban": {"user_id": "8", "user_login": "alice", "user_name": "Alice", "reason": "no spam"}}`)
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatUserBan, ev.Type)
	assert.Equal(t, &eventtype.Moderation{Message: "no spam"}, ev.Details.Moderation)
}
