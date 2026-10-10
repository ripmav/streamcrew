// SPDX-License-Identifier: MIT

package eventtype

// Details are the values of a platform event besides its users (spec
// events.md, B7, B9). A field is nil if the event has no such value; which
// event has which says the table of B9.
type Details struct {
	// Message is the chat message of chat.message, chat.whisper,
	// chat.message.delete, chat.user.entrance and chat.user.first_message,
	// or the message a user shares with channel.resubscribe.
	Message *Message `json:"message,omitempty"`
	// Raid is the raid of channel.raid.
	Raid *Raid `json:"raid,omitempty"`
	// Subscription is the subscription of channel.subscribe,
	// channel.resubscribe and of the gifts.
	Subscription *Subscription `json:"subscription,omitempty"`
	// Gift is the gift of channel.subscription.gift and
	// channel.subscription.mass_gift.
	Gift *Gift `json:"gift,omitempty"`
	// Bits is the bits of twitch.bits.cheer.
	Bits *Bits `json:"bits,omitempty"`
	// SharedChat is the shared chat session of the twitch.shared_chat
	// events.
	SharedChat *SharedChat `json:"shared_chat,omitempty"`
	// Moderation is the moderation of chat.user.timeout and
	// chat.user.ban (the message the platform shows with the action).
	Moderation *Moderation `json:"moderation,omitempty"`
	// HypeTrain is the hype train of the twitch.hype_train events.
	HypeTrain *HypeTrain `json:"hype_train,omitempty"`
	// AdBreak is the ad break of twitch.ad.start.
	AdBreak *AdBreak `json:"ad_break,omitempty"`
	// Shoutout is the shoutout of twitch.shoutout.receive.
	Shoutout *Shoutout `json:"shoutout,omitempty"`
	// Goal is the goal of the twitch.goal events.
	Goal *Goal `json:"goal,omitempty"`
	// Charity is the charity collection of the twitch.charity events.
	Charity *Charity `json:"charity,omitempty"`
	// ChannelPoints is the redemption of twitch.channel_points.redeem.
	ChannelPoints *ChannelPoints `json:"channel_points,omitempty"`
	// CustomPowerUp is the redemption of
	// twitch.custom_power_up.redeem.
	CustomPowerUp *CustomPowerUp `json:"custom_power_up,omitempty"`
}

// Bits is the bits of a cheer (twitch.bits.cheer).
type Bits struct {
	// Amount is the number of bits cheered; not negative.
	Amount int64 `json:"amount"`
}

// SharedChat is the session of a shared chat (twitch.shared_chat.*).
type SharedChat struct {
	// SessionID is the chat session (chat_session_id); not empty.
	SessionID string `json:"session_id"`
	// Title is the title of the session; empty for the end.
	Title string `json:"title,omitempty"`
}

// Moderation is the moderation of chat.user.timeout and chat.user.ban;
// the platform shows the message with the action, e.g. the reason of
// the ban or the text of the timeout.
type Moderation struct {
	// Message is the message the platform shows; empty if the platform
	// names none.
	Message string `json:"message,omitempty"`
}

// HypeTrain is the hype train of the twitch.hype_train events.
type HypeTrain struct {
	// Level is the current level of the train.
	Level int `json:"level"`
	// Progress is the channel points raised so far; Goal the points the
	// level wants to reach.
	Progress int64 `json:"progress"`
	Goal     int64 `json:"goal"`
	// RewardLevel is the number of rewards reached so far.
	RewardLevel int `json:"reward_level"`
	// Outcome is the end reason of twitch.hype_train.end, e.g.
	// "goal_reached" or "time_reached"; empty while the train runs.
	Outcome string `json:"outcome,omitempty"`
}

// AdBreak is the ad break of twitch.ad.start.
type AdBreak struct {
	// Duration is the length of the break, in seconds (30 or 60).
	Duration int `json:"duration"`
	// Message is the message of the break; empty if the platform names
	// none.
	Message string `json:"message,omitempty"`
}

// Shoutout is the shoutout of twitch.shoutout.receive.
type Shoutout struct {
	// Viewers is the number of viewers of the shouting channel.
	Viewers int64 `json:"viewers"`
}

// Goal is the goal of the twitch.goal events.
type Goal struct {
	// Current is the amount reached so far; Target the wanted amount, in
	// Currency.
	Current  int64  `json:"current"`
	Target   int64  `json:"target"`
	Currency string `json:"currency"`
}

// Charity is the charity collection of the twitch.charity events.
type Charity struct {
	// Current is the amount raised so far; Target the wanted amount, in
	// Currency.
	Current  int64  `json:"current"`
	Target   int64  `json:"target"`
	Currency string `json:"currency"`
}

// ChannelPoints is the redemption of twitch.channel_points.redeem.
type ChannelPoints struct {
	// Amount is the channel points the redemption costs.
	Amount int64 `json:"amount"`
	// Reward is the ID of the redeemed reward.
	Reward string `json:"reward"`
}

// CustomPowerUp is the redemption of twitch.custom_power_up.redeem.
type CustomPowerUp struct {
	// Reward is the ID of the redeemed power up.
	Reward string `json:"reward"`
}

// Message is a chat message, or the message of a subscription.
type Message struct {
	// ID is the platform's ID of a chat message, e.g. to reply to it or to
	// delete it; empty for a message without one, such as that of a
	// subscription.
	ID string `json:"id,omitempty"`
	// Text is the text as written. Only the message of chat.message.delete
	// may have none, when the platform does not name the deleted text.
	Text string `json:"text,omitempty"`
	// Emotes are the emote codes in Text as the platform marks them.
	Emotes []string `json:"emotes,omitempty"`
}

// Raid is the raid of another channel (channel.raid).
type Raid struct {
	// Viewers is the number of viewers who came with it; not negative.
	Viewers int64 `json:"viewers"`
}

// Subscription is the plan of a subscription as the platform names it
// (events.md B9); for Twitch, twitch-events.md says which (phase 4).
type Subscription struct {
	// Plan is the tier, e.g. "1000" or "Prime"; it is not empty.
	Plan string `json:"plan"`
	// PlanName is the name of the plan; empty if the platform names none.
	PlanName string `json:"planName,omitempty"`
}

// Gift is the gift of subscriptions to other users (events.md B5).
type Gift struct {
	// Anonymous reports whether the gifter stays unknown; the event then
	// has no user (B29).
	Anonymous bool `json:"anonymous"`
	// Count is the number of subscriptions gifted with the event: 1 for a
	// gift to one user, at least 1 for a mass gift.
	Count int `json:"count"`
}
