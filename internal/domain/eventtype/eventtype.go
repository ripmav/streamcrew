// SPDX-License-Identifier: MIT

// Package eventtype is the catalog of domain event types (spec events.md):
// the stable names that event commands react to, together with the rules of
// the spec that are data rather than code.
//
// A type enters the event bus catalog (internal/event) with its payload when
// its source exists: the chat in roadmap phase 5, Twitch in phase 4. Names
// are never renamed once published (B1).
package eventtype

import (
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/event"
)

// Once says how often an event of a type fires (B3, B4).
type Once string

// Frequencies of events.
const (
	// Always fires every time (B4).
	Always Once = "always"
	// PerSession fires at most once per stream session (B3).
	PerSession Once = "per_session"
	// PerUserSession fires at most once per user and stream session; for a
	// raid the user is the raiding channel (B3).
	PerUserSession Once = "per_user_session"
	// PerUser fires at most once per user, ever.
	PerUser Once = "per_user"
)

// Descriptor describes an event type.
type Descriptor struct {
	// Type is the stable name.
	Type event.Type
	// Platform is the platform of a platform-specific type; empty for a
	// platform-neutral one.
	Platform platform.Name
	// Neutral is the platform-neutral type that is published after this
	// one, with the platform as its source (B2); empty if there is none.
	Neutral event.Type
	// Once is how often the event fires.
	Once Once
}

// Application.
const (
	AppStarted  event.Type = "app.started"
	AppStopping event.Type = "app.stopping"
)

// Authentication, app events for the frontends (roadmap 4.1, ADR-0014): the
// auth service publishes them directly on the bus, and they never trigger a
// command, so they are not in the spec catalog All().
const (
	AuthActionRequired event.Type = "auth.action_required"
	AuthLoginCompleted event.Type = "auth.login_completed"
	AuthLoginFailed    event.Type = "auth.login_failed"
)

// Channel, platform-neutral.
const (
	ChannelStreamStart          event.Type = "channel.stream.start"
	ChannelStreamStop           event.Type = "channel.stream.stop"
	ChannelFollow               event.Type = "channel.follow"
	ChannelRaid                 event.Type = "channel.raid"
	ChannelSubscribe            event.Type = "channel.subscribe"
	ChannelResubscribe          event.Type = "channel.resubscribe"
	ChannelSubscriptionGift     event.Type = "channel.subscription.gift"
	ChannelSubscriptionMassGift event.Type = "channel.subscription.mass_gift"
)

// Chat, platform-neutral.
const (
	ChatMessage          event.Type = "chat.message"
	ChatMessageDelete    event.Type = "chat.message.delete"
	ChatWhisper          event.Type = "chat.whisper"
	ChatUserJoin         event.Type = "chat.user.join"
	ChatUserLeave        event.Type = "chat.user.leave"
	ChatUserEntrance     event.Type = "chat.user.entrance"
	ChatUserNew          event.Type = "chat.user.new"
	ChatUserFirstMessage event.Type = "chat.user.first_message"
	ChatUserTimeout      event.Type = "chat.user.timeout"
	ChatUserBan          event.Type = "chat.user.ban"
)

// Twitch (roadmap phase 4).
const (
	TwitchStreamStart             event.Type = "twitch.stream.start"
	TwitchStreamStop              event.Type = "twitch.stream.stop"
	TwitchChannelUpdate           event.Type = "twitch.channel.update"
	TwitchChannelFollow           event.Type = "twitch.channel.follow"
	TwitchChannelRaid             event.Type = "twitch.channel.raid"
	TwitchRaidOutgoing            event.Type = "twitch.raid.outgoing"
	TwitchChannelSubscribe        event.Type = "twitch.channel.subscribe"
	TwitchChannelResubscribe      event.Type = "twitch.channel.resubscribe"
	TwitchSubscriptionGift        event.Type = "twitch.subscription.gift"
	TwitchSubscriptionMassGift    event.Type = "twitch.subscription.mass_gift"
	TwitchChatWatchStreak         event.Type = "twitch.chat.watch_streak"
	TwitchChatModiversary         event.Type = "twitch.chat.modiversary"
	TwitchChatHighlightedMessage  event.Type = "twitch.chat.highlighted_message"
	TwitchChatUserIntro           event.Type = "twitch.chat.user_intro"
	TwitchPowerUpMessageEffect    event.Type = "twitch.power_up.message_effect"
	TwitchPowerUpGigantifiedEmote event.Type = "twitch.power_up.gigantified_emote"
	TwitchPowerUpCelebration      event.Type = "twitch.power_up.celebration"
	TwitchCustomPowerUpRedeem     event.Type = "twitch.custom_power_up.redeem"
	TwitchChannelPointsRedeem     event.Type = "twitch.channel_points.redeem"
	TwitchBitsCheer               event.Type = "twitch.bits.cheer"
	TwitchAdUpcoming              event.Type = "twitch.ad.upcoming"
	TwitchAdStart                 event.Type = "twitch.ad.start"
	TwitchAdEnd                   event.Type = "twitch.ad.end"
	TwitchCharityDonation         event.Type = "twitch.charity.donation"
	TwitchHypeTrainStart          event.Type = "twitch.hype_train.start"
	TwitchHypeTrainProgress       event.Type = "twitch.hype_train.progress"
	TwitchHypeTrainLevelUp        event.Type = "twitch.hype_train.level_up"
	TwitchHypeTrainEnd            event.Type = "twitch.hype_train.end"
	TwitchModerationUserWarn      event.Type = "twitch.moderation.user_warn"
	TwitchShoutoutReceive         event.Type = "twitch.shoutout.receive"
	TwitchSuspiciousUserMessage   event.Type = "twitch.suspicious_user.message"
	TwitchSuspiciousUserUpdate    event.Type = "twitch.suspicious_user.update"
	TwitchShieldModeStart         event.Type = "twitch.shield_mode.start"
	TwitchShieldModeEnd           event.Type = "twitch.shield_mode.end"
	TwitchUnbanRequestCreate      event.Type = "twitch.unban_request.create"
	TwitchUnbanRequestResolve     event.Type = "twitch.unban_request.resolve"
	TwitchGoalStart               event.Type = "twitch.goal.start"
	TwitchGoalProgress            event.Type = "twitch.goal.progress"
	TwitchGoalEnd                 event.Type = "twitch.goal.end"
)

// All returns the catalog in the order of the spec.
func All() []Descriptor {
	neutral := func(t event.Type, once Once) Descriptor { return Descriptor{Type: t, Once: once} }
	twitch := func(t, n event.Type, once Once) Descriptor {
		return Descriptor{Type: t, Platform: platform.Twitch, Neutral: n, Once: once}
	}
	return []Descriptor{
		neutral(AppStarted, Always),
		neutral(AppStopping, Always),

		neutral(ChannelStreamStart, PerSession),
		neutral(ChannelStreamStop, PerSession),
		neutral(ChannelFollow, PerUserSession),
		neutral(ChannelRaid, PerUserSession),
		neutral(ChannelSubscribe, PerUserSession),
		neutral(ChannelResubscribe, PerUserSession),
		neutral(ChannelSubscriptionGift, Always),
		neutral(ChannelSubscriptionMassGift, Always),

		neutral(ChatMessage, Always),
		neutral(ChatMessageDelete, Always),
		neutral(ChatWhisper, Always),
		neutral(ChatUserJoin, PerUserSession),
		neutral(ChatUserLeave, Always),
		neutral(ChatUserEntrance, PerUserSession),
		neutral(ChatUserNew, PerUser),
		neutral(ChatUserFirstMessage, PerUser),
		neutral(ChatUserTimeout, Always),
		neutral(ChatUserBan, Always),

		twitch(TwitchStreamStart, ChannelStreamStart, PerSession),
		twitch(TwitchStreamStop, ChannelStreamStop, PerSession),
		twitch(TwitchChannelUpdate, "", Always),
		twitch(TwitchChannelFollow, ChannelFollow, PerUserSession),
		twitch(TwitchChannelRaid, ChannelRaid, PerUserSession),
		twitch(TwitchRaidOutgoing, "", Always),
		twitch(TwitchChannelSubscribe, ChannelSubscribe, PerUserSession),
		twitch(TwitchChannelResubscribe, ChannelResubscribe, PerUserSession),
		twitch(TwitchSubscriptionGift, ChannelSubscriptionGift, Always),
		twitch(TwitchSubscriptionMassGift, ChannelSubscriptionMassGift, Always),
		twitch(TwitchChatWatchStreak, "", Always),
		twitch(TwitchChatModiversary, "", Always),
		twitch(TwitchChatHighlightedMessage, "", Always),
		twitch(TwitchChatUserIntro, "", Always),
		twitch(TwitchPowerUpMessageEffect, "", Always),
		twitch(TwitchPowerUpGigantifiedEmote, "", Always),
		twitch(TwitchPowerUpCelebration, "", Always),
		twitch(TwitchCustomPowerUpRedeem, "", Always),
		twitch(TwitchChannelPointsRedeem, "", Always),
		twitch(TwitchBitsCheer, "", Always),
		twitch(TwitchAdUpcoming, "", Always),
		twitch(TwitchAdStart, "", Always),
		twitch(TwitchAdEnd, "", Always),
		twitch(TwitchCharityDonation, "", Always),
		twitch(TwitchHypeTrainStart, "", Always),
		twitch(TwitchHypeTrainProgress, "", Always),
		twitch(TwitchHypeTrainLevelUp, "", Always),
		twitch(TwitchHypeTrainEnd, "", Always),
		twitch(TwitchModerationUserWarn, "", Always),
		twitch(TwitchShoutoutReceive, "", Always),
		twitch(TwitchSuspiciousUserMessage, "", Always),
		twitch(TwitchSuspiciousUserUpdate, "", Always),
		twitch(TwitchShieldModeStart, "", Always),
		twitch(TwitchShieldModeEnd, "", Always),
		twitch(TwitchUnbanRequestCreate, "", Always),
		twitch(TwitchUnbanRequestResolve, "", Always),
		twitch(TwitchGoalStart, "", Always),
		twitch(TwitchGoalProgress, "", Always),
		twitch(TwitchGoalEnd, "", Always),
	}
}

// Lookup returns the descriptor of a type.
func Lookup(t event.Type) (Descriptor, bool) {
	for _, d := range All() {
		if d.Type == t {
			return d, true
		}
	}
	return Descriptor{}, false
}
