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

func (r *fakeReceiver) Join(_ context.Context, _ platform.Name, _ user.Identity) error {
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

func handle(t *testing.T, m *twitch.Mapper, eventType, payload, id string) {
	t.Helper()
	var e twitch.Event
	raw, err := json.Marshal(map[string]any{
		"id": id,
		"event": map[string]any{
			"event_type": eventType,
			"version":    "1",
			"event":      json.RawMessage(payload),
		},
	})
	require.NoError(t, err)
	require.NoError(t, jsonv2.Unmarshal(raw, &e))
	m.OnMessage(t.Context(), e)
}

func TestMapChatMessage(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "9")
	handle(t, m, "channel.chat.message", `{
		"id": "m-1",
		"message": "hi :Kappa: there",
		"emotes": [{"id": "25", "start": 3, "end": 9}],
		"user_id": "8",
		"user_login": "alice",
		"user_display_name": "Alice",
		"badges": [{"set_id": "moderator/1"}, {"set_id": "premium/1"}]
	}`, "id-1")

	msg := r.lastMessage(t)
	assert.Equal(t, "m-1", msg.Message.ID)
	assert.Equal(t, "hi :Kappa: there", msg.Message.Text)
	assert.Equal(t, []string{"25"}, msg.Message.Emotes)
	assert.Equal(t, "8", msg.Author.PlatformUserID)
	assert.Equal(t, "alice", msg.Author.Login)
	assert.Equal(t, "Alice", msg.Author.DisplayName)
	assert.True(t, msg.Author.Roles.Has(role.Moderator))
	assert.False(t, msg.Author.Roles.Has(role.Streamer))
	assert.False(t, msg.FromBot)
}

func TestMapChatMessageFromBot(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "9")
	handle(t, m, "channel.chat.message", `{"id":"m-1","message":"hi","user_id":"9","user_login":"bot"}`, "id-1")
	assert.True(t, r.lastMessage(t).FromBot)
}

func TestMapEmotesInTextOrder(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.chat.message", `{
		"id": "m-1",
		"message": "x",
		"emotes": [{"id": "a", "start": 10}, {"id": "b", "start": 2}],
		"emoji":  [{"id": "c", "start": 0}]
	}`, "id-1")
	assert.Equal(t, []string{"c", "b", "a"}, r.lastMessage(t).Message.Emotes)
}

func TestMapStream(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "stream.online", `{"id": "1"}`, "id-1")
	handle(t, m, "stream.offline", `{"id": "2"}`, "id-2")
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Equal(t, []bool{true, false}, r.streams)
}

func TestMapFollow(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.follow", `{"user_id": "8", "user_login": "alice"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelFollow, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
}

func TestMapRaid(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.raid", `{"from_user_id": "8", "from_user_login": "bob", "viewer_count": 42}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelRaid, ev.Type)
	assert.Equal(t, int64(42), ev.Details.Raid.Viewers)
}

func TestMapNotification(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")

	// A subscription.
	handle(t, m, "channel.chat.notification", `{
		"type": "subscription",
		"user_id": "8", "user_login": "alice",
		"plan_name": "1000", "cumulative_month_count": 3
	}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelSubscribe, ev.Type)
	assert.Equal(t, "1000", ev.Details.Subscription.Plan)
	assert.Empty(t, ev.Details.Subscription.PlanName)

	// A resubscription with a message.
	handle(t, m, "channel.chat.notification", `{
		"type": "resubscription",
		"user_id": "8", "user_login": "alice",
		"plan_name": "2000", "gift_message": "thanks"
	}`, "id-2")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelResubscribe, ev.Type)
	require.NotNil(t, ev.Details.Message)
	assert.Equal(t, "thanks", ev.Details.Message.Text)

	// A gift.
	handle(t, m, "channel.chat.notification", `{
		"type": "gift",
		"user_id": "8", "user_login": "alice",
		"to_user_id": "10", "to_user_login": "carol",
		"plan_name": "1000"
	}`, "id-3")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSubscriptionGift, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
	require.NotNil(t, ev.Target)
	assert.Equal(t, "carol", ev.Target.Login)
	assert.False(t, ev.Details.Gift.Anonymous)
	assert.Equal(t, 1, ev.Details.Gift.Count)

	// An anonymous mass gift: no user, the recipients by login.
	handle(t, m, "channel.chat.notification", `{
		"type": "mass_gift",
		"anonymous": true,
		"total": 2,
		"to_user_logins": ["carol", "dave"],
		"plan_name": "1000"
	}`, "id-4")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSubscriptionMassGift, ev.Type)
	assert.Nil(t, ev.User)
	assert.True(t, ev.Details.Gift.Anonymous)
	assert.Equal(t, 2, ev.Details.Gift.Count)
	require.Len(t, ev.Recipients, 2)
	assert.Equal(t, "carol", ev.Recipients[0].Login)
	assert.Equal(t, "dave", ev.Recipients[1].Login)
}

func TestMapCheer(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.cheer", `{
		"user_id": "8", "user_login": "alice",
		"bits": 100, "message": "cheers"
	}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchBitsCheer, ev.Type)
	assert.Equal(t, int64(100), ev.Details.Bits.Amount)
	assert.Equal(t, "cheers", ev.Details.Message.Text)
}

func TestMapModerate(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.moderate", `{
		"moderation_action": "timeout",
		"target_user_id": "8", "target_user_login": "alice"
	}`, "id-1")
	assert.Equal(t, eventtype.ChatUserTimeout, r.lastEvent(t).Type)
	handle(t, m, "channel.moderate", `{
		"moderation_action": "ban",
		"target_user_id": "8", "target_user_login": "alice"
	}`, "id-2")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatUserBan, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
}

func TestMapMessageDelete(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.chat.message_delete", `{
		"id": "m-9",
		"user_id": "8", "user_login": "alice"
	}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatMessageDelete, ev.Type)
	assert.Equal(t, "m-9", ev.Details.Message.ID)
	assert.Empty(t, ev.Details.Message.Text)
}

func TestMapWhisper(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "user.whisper.message", `{
		"user_id": "8", "user_login": "alice",
		"message": "secret"
	}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatWhisper, ev.Type)
	assert.Equal(t, "secret", ev.Details.Message.Text)
}

func TestMapSharedChat(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.shared_chat.begin", `{"chat_session_id": "s1", "title": "t"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSharedChatStart, ev.Type)
	assert.Equal(t, "s1", ev.Details.SharedChat.SessionID)
	handle(t, m, "channel.shared_chat.update", `{"chat_session_id": "s1", "title": "t2"}`, "id-2")
	assert.Equal(t, eventtype.TwitchSharedChatUpdate, r.lastEvent(t).Type)
	handle(t, m, "channel.shared_chat.end", `{"chat_session_id": "s1"}`, "id-3")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchSharedChatEnd, ev.Type)
	assert.Empty(t, ev.Details.SharedChat.Title)
}

func TestMapDropsTheRepeats(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.chat.message", `{"id": "m-1", "message": "hi", "user_id": "8", "user_login": "a"}`, "id-1")
	handle(t, m, "channel.chat.message", `{"id": "m-1", "message": "hi again", "user_id": "8", "user_login": "a"}`, "id-1")
	handle(t, m, "channel.chat.message", `{"id": "m-2", "message": "next", "user_id": "8", "user_login": "a"}`, "id-2")
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.messages, 2)
	assert.Equal(t, "hi", r.messages[0].Message.Text)
	assert.Equal(t, "next", r.messages[1].Message.Text)
}

func TestMapDropsTheUnknown(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.poll.begin", `{"id": "1"}`, "id-1")
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Empty(t, r.messages)
	assert.Empty(t, r.events)
	assert.Empty(t, r.streams)
}

func TestMapHypeTrain(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.hype_train.start", `{"current_level": 1, "progress": 0, "goal": 10000, "reward_interval": 5000}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainStart, ev.Type)
	assert.Equal(t, &eventtype.HypeTrain{Level: 1, Progress: 0, Goal: 10000}, ev.Details.HypeTrain)

	handle(t, m, "channel.hype_train.progress", `{"current_level": 2, "progress": 6000, "goal": 10000, "reward_interval": 5000}`, "id-2")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainProgress, ev.Type)
	assert.Equal(t, 1, ev.Details.HypeTrain.RewardLevel)

	handle(t, m, "channel.hype_train.end", `{"current_level": 3, "progress": 12000, "goal": 10000, "reward_interval": 5000, "end_reason": "goal_reached"}`, "id-3")
	ev = r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchHypeTrainEnd, ev.Type)
	assert.Equal(t, "goal_reached", ev.Details.HypeTrain.Outcome)
}

func TestMapAdStart(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.ad.started", `{"length": 30, "message": "take a break"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchAdStart, ev.Type)
	assert.Equal(t, &eventtype.AdBreak{Duration: 30, Message: "take a break"}, ev.Details.AdBreak)
}

func TestMapShoutout(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.shoutout.received", `{"from_user_id": "5", "from_user_login": "shouter", "from_user_display_name": "Shouter", "from_user_viewers": 7, "to_user_id": "7"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchShoutoutReceive, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "5", ev.User.PlatformUserID)
	assert.Equal(t, &eventtype.Shoutout{Viewers: 7}, ev.Details.Shoutout)
}

func TestMapGoal(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.goal.start", `{"current_amount": 0, "target_amount": 100, "currency": "USD"}`, "id-1")
	assert.Equal(t, eventtype.TwitchGoalStart, r.lastEvent(t).Type)
	handle(t, m, "channel.goal.complete", `{"current_amount": 100, "target_amount": 100, "currency": "USD"}`, "id-2")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchGoalEnd, ev.Type)
	assert.Equal(t, &eventtype.Goal{Current: 100, Target: 100, Currency: "USD"}, ev.Details.Goal)
}

func TestMapCharity(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.charity.progress", `{"current_amount": 50, "target_amount": 200, "currency": "EUR"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchCharityDonation, ev.Type)
	assert.Equal(t, &eventtype.Charity{Current: 50, Target: 200, Currency: "EUR"}, ev.Details.Charity)
}

func TestMapChannelPointsRedemption(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.channel_points_automatic_reward_redemption.add", `{"automatic_reward_id": "r1", "automatic_reward_cost": 100, "user_id": "8", "user_login": "alice"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchChannelPointsRedeem, ev.Type)
	require.NotNil(t, ev.User)
	assert.Equal(t, "alice", ev.User.Login)
	assert.Equal(t, &eventtype.ChannelPoints{Amount: 100, Reward: "r1"}, ev.Details.ChannelPoints)
}

func TestMapCustomPowerUpRedemption(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.channel_points_custom_reward_redemption.add", `{"custom_reward_id": "p1", "custom_reward_cost": 50, "custom_reward_user_input": "a hug", "user_id": "8", "user_login": "alice"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.TwitchCustomPowerUpRedeem, ev.Type)
	assert.Equal(t, &eventtype.CustomPowerUp{Reward: "p1"}, ev.Details.CustomPowerUp)
	assert.Equal(t, "a hug", ev.Details.Message.Text)
}

func TestMapModerationMessage(t *testing.T) {
	r := &fakeReceiver{}
	m := mapperFor(t, r, "")
	handle(t, m, "channel.moderate", `{"moderation_action": "ban", "moderation_message": "no spam", "target_user_id": "8", "target_user_login": "alice"}`, "id-1")
	ev := r.lastEvent(t)
	assert.Equal(t, eventtype.ChatUserBan, ev.Type)
	assert.Equal(t, &eventtype.Moderation{Message: "no spam"}, ev.Details.Moderation)
}
