// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	jsonv2 "encoding/json/v2"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	emotecat "github.com/ripmav/streamcrew/internal/emote"
)

// Mapper is the mapping of the received EventSub events onto the event
// model (roadmap 4.3, task 4): it hands the events to the
// connector.Receiver, in the order received, and drops the repeats by
// the message ID before the mapping (B22), also after a
// session_reconnect.
type Mapper struct {
	rec       connector.Receiver
	dedup     *connector.Dedup
	botID     string
	onRevoked func(context.Context)
	log       *slog.Logger
	// emotes resolves the emote IDs of a chat message to their code
	// (roadmap 4.4, task 9); nil without a catalog, then the code is
	// read from the marked text.
	emotes       *emotecat.Catalog
	emoteChannel string
	// channel is the channel of the WS session (B5), stamped on every
	// inbound by the receiver wrapper; empty for the home channel.
	channel connector.Channel
}

// MapperOptions configures NewMapper.
type MapperOptions struct {
	// Receiver takes the mapped events (the event service).
	Receiver connector.Receiver
	// Dedup drops the repeats of the messages (B22).
	Dedup *connector.Dedup
	// BotID is the user ID of the bot account of the channel; its
	// messages are flagged FromBot (B13). Empty for none.
	BotID string
	// Logger logs the dropped events (Code-ADR-0003); if nil, the log
	// is discarded.
	Logger *slog.Logger
	// Emotes resolves the emote IDs of a chat message to their code
	// (roadmap 4.4, task 9); nil for the raw code from the text.
	Emotes *emotecat.Catalog
	// EmoteChannel is the login the emote catalog is resolved for.
	EmoteChannel string
	// Channel is the channel of the WS session this mapper belongs to
	// (spec twitch-multi-channel.md, B5); empty for the home channel. It
	// is stamped on every chat message and event, because the session —
	// one per channel — is what knows it, not the event body.
	Channel connector.Channel
}

// NewMapper creates a Mapper.
func NewMapper(o MapperOptions) *Mapper {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Mapper{
		rec:          channelReceiver{next: o.Receiver, channel: o.Channel},
		dedup:        o.Dedup,
		botID:        o.BotID,
		log:          log,
		emotes:       o.Emotes,
		emoteChannel: o.EmoteChannel,
		channel:      o.Channel,
	}
}

// channelReceiver stamps the mapper's channel on the chat messages and
// events before forwarding them: the channel is known to the WS session
// (one per channel, spec twitch-multi-channel.md, B5), not to the event
// body.
type channelReceiver struct {
	next    connector.Receiver
	channel connector.Channel
}

func (r channelReceiver) Message(ctx context.Context, m connector.Incoming) error {
	m.Channel = r.channel
	return r.next.Message(ctx, m)
}

func (r channelReceiver) Join(ctx context.Context, p platform.Name, ch connector.Channel, who user.Identity) error {
	return r.next.Join(ctx, p, ch, who)
}

func (r channelReceiver) Event(ctx context.Context, e connector.Event) error {
	e.Channel = r.channel
	return r.next.Event(ctx, e)
}

func (r channelReceiver) Stream(ctx context.Context, p platform.Name, live bool) error {
	return r.next.Stream(ctx, p, live)
}

// WithRevoked sets the revocation callback, called once when the
// server reports a revoked token; the composition root uses it to
// re-reconcile the subscriptions.
func (m *Mapper) WithRevoked(f func(context.Context)) {
	m.onRevoked = f
}

// OnRevoked is the Handler of the WebSocket client.
func (m *Mapper) OnRevoked(ctx context.Context) {
	if m.onRevoked != nil {
		m.onRevoked(ctx)
	}
}

// OnMessage is the Handler of the WebSocket client: it drops the
// repeats of the messages (B22) and maps the event.
func (m *Mapper) OnMessage(ctx context.Context, e Event) {
	if !m.dedup.First(e.ID) {
		m.log.DebugContext(ctx, "drop a repeated eventsub event", "eventtype", e.Subscription.Type, "id", e.ID)
		return
	}
	m.log.DebugContext(ctx, "the eventsub event", "eventtype", e.Subscription.Type, "channel", m.channel)
	if err := m.dispatch(ctx, e); err != nil {
		m.log.WarnContext(ctx, "drop an unmapped eventsub event", "eventtype", e.Subscription.Type, "error", err)
	}
}

// dispatch is the table of the mapping; the error is for a payload
// that does not fit its type, and the event is dropped.
func (m *Mapper) dispatch(ctx context.Context, e Event) error {
	body := e.Body
	switch e.Subscription.Type {
	case "channel.chat.message":
		var p chatMessage
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Message(ctx, m.incoming(p))
	case "stream.online", "stream.offline":
		return m.rec.Stream(ctx, platform.Twitch, e.Subscription.Type == "stream.online")
	case "channel.follow":
		var p follow
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelFollow,
			User:     new(identity(p.user())),
		})
	case "channel.raid":
		var p raid
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelRaid,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{Raid: &eventtype.Raid{Viewers: p.Viewers}},
		})
	case "channel.chat.notification":
		var p chatNotification
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		ev, err := p.event()
		if err != nil {
			return err
		}
		return m.rec.Event(ctx, ev)
	case "channel.cheer":
		var p cheer
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchBitsCheer,
			User:     new(identity(p.user())),
			Details: eventtype.Details{
				Bits:    &eventtype.Bits{Amount: p.Bits},
				Message: &eventtype.Message{Text: p.Message},
			},
		})
	case "channel.moderate":
		var p moderate
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		// channel.moderate v2 names the target per action object
		// (EventSub Reference "Channel Moderate Event V2").
		target := p.Ban
		if target == nil {
			target = p.Timeout
		}
		if target == nil {
			return fmt.Errorf("eventsub: the moderate action %q has no target", p.Action)
		}
		typ := eventtype.ChatUserTimeout
		if p.Action == "ban" {
			typ = eventtype.ChatUserBan
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			User:     new(identity(target.user())),
		}
		if target.Reason != "" {
			ev.Details = eventtype.Details{Moderation: &eventtype.Moderation{Message: target.Reason}}
		}
		return m.rec.Event(ctx, ev)
	case "channel.chat.message_delete":
		var p messageDelete
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatMessageDelete,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{Message: &eventtype.Message{ID: p.MessageID}},
		})
	case "user.whisper.message":
		var p whisper
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.ChatWhisper,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{Message: &eventtype.Message{Text: p.Whisper.Text}},
		})
	case "channel.shared_chat.begin", "channel.shared_chat.update", "channel.shared_chat.end":
		var p sharedChat
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchSharedChatStart
		switch e.Subscription.Type {
		case "channel.shared_chat.update":
			typ = eventtype.TwitchSharedChatUpdate
		case "channel.shared_chat.end":
			typ = eventtype.TwitchSharedChatEnd
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{SharedChat: &eventtype.SharedChat{SessionID: p.SessionID}},
		})
	case "channel.hype_train.begin", "channel.hype_train.progress", "channel.hype_train.end":
		var p hypeTrain
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchHypeTrainStart
		switch e.Subscription.Type {
		case "channel.hype_train.progress":
			typ = eventtype.TwitchHypeTrainProgress
		case "channel.hype_train.end":
			typ = eventtype.TwitchHypeTrainEnd
		}
		// The v2 payload names no reward interval and no end reason
		// (EventSub Reference "Hype Train" events), so RewardLevel and
		// Outcome stay empty.
		train := &eventtype.HypeTrain{Level: p.Level, Progress: p.Progress, Goal: p.Goal}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{HypeTrain: train},
		})
	case "channel.ad_break.begin":
		var p adStarted
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchAdStart,
			Details:  eventtype.Details{AdBreak: &eventtype.AdBreak{Duration: p.DurationSeconds}},
		})
	case "channel.shoutout.receive":
		var p shoutout
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchShoutoutReceive,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{Shoutout: &eventtype.Shoutout{Viewers: p.ViewerCount}},
		})
	case "channel.goal.begin", "channel.goal.progress", "channel.goal.end":
		var p goal
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchGoalStart
		switch e.Subscription.Type {
		case "channel.goal.progress":
			typ = eventtype.TwitchGoalProgress
		case "channel.goal.end":
			typ = eventtype.TwitchGoalEnd
		}
		// The goal payload names no currency (EventSub Reference "Goals
		// Event"), so Currency stays empty.
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{Goal: &eventtype.Goal{Current: p.CurrentAmount, Target: p.TargetAmount}},
		})
	case "channel.charity_campaign.progress", "channel.charity_campaign.stop":
		var p charity
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCharityDonation,
			Details: eventtype.Details{Charity: &eventtype.Charity{
				Current:  p.CurrentAmount.Value,
				Target:   p.TargetAmount.Value,
				Currency: p.CurrentAmount.Currency,
			}},
		})
	case "channel.channel_points_automatic_reward_redemption.add":
		var p autoRedemption
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelPointsRedeem,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: p.Reward.Cost, Reward: p.Reward.Type}},
		})
	case "channel.channel_points_custom_reward_redemption.add":
		var p customRedemption
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCustomPowerUpRedeem,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{CustomPowerUp: &eventtype.CustomPowerUp{Reward: p.Reward.ID}},
		}
		if p.UserInput != "" {
			ev.Details.Message = &eventtype.Message{Text: p.UserInput}
		}
		return m.rec.Event(ctx, ev)
	default:
		return fmt.Errorf("eventsub: no mapping for %s", e.Subscription.Type)
	}
}

// incoming is a chat message with its author, the roles from the
// badges and the FromBot flag (B13).
func (m *Mapper) incoming(p chatMessage) connector.Incoming {
	author := identity(p.user())
	return connector.Incoming{
		Platform: platform.Twitch,
		Author:   author,
		FromBot:  p.ChatterID != "" && p.ChatterID == m.botID,
		Message:  eventtype.Message{ID: p.MessageID, Text: p.Message.Text, Emotes: m.emoteCodes(p)},
	}
}

// chatUser is the user of a chat event, built by the payloads from
// their own field names.
type chatUser struct {
	ID          string
	Login       string
	DisplayName string
	Color       string
	Badges      []badge
}

// badge is a chat badge; the set ID is "<badge>/<version>".
type badge struct {
	SetID string `json:"set_id"`
}

// identity is the user.Identity of a chat user; the badges are mapped
// onto the roles of the core model (broadcaster, moderator, vip,
// subscriber and sub), the other badges are ignored, they are no roles
// of the model.
func identity(u chatUser) user.Identity {
	roles := role.NewSet()
	for _, b := range u.Badges {
		switch strings.SplitN(b.SetID, "/", 2)[0] {
		case "broadcaster":
			roles = roles.With(role.Streamer)
		case "moderator":
			roles = roles.With(role.Moderator)
		case "vip":
			roles = roles.With(role.TwitchVIP)
		case "subscriber", "sub":
			roles = roles.With(role.Subscriber)
		}
	}
	name := u.DisplayName
	if name == "" {
		name = u.Login
	}
	return user.Identity{
		Platform:       platform.Twitch,
		PlatformUserID: u.ID,
		Login:          u.Login,
		DisplayName:    name,
		Color:          u.Color,
		Roles:          roles,
	}
}

// chatMessage is the payload of channel.chat.message (EventSub
// Reference "Channel Chat Message Event"): the chatter under
// chatter_user_*, the message as an object with the text and the
// fragments (the emotes are the fragments of type "emote"; the platform
// names no profile image and no start and end marks any more).
type chatMessage struct {
	MessageID string  `json:"message_id"`
	Message   message `json:"message"`
	Color     string  `json:"color"`
	Badges    []badge `json:"badges"`

	ChatterID    string `json:"chatter_user_id"`
	ChatterLogin string `json:"chatter_user_login"`
	ChatterName  string `json:"chatter_user_name"`
}

func (p chatMessage) user() chatUser {
	return chatUser{
		ID:          p.ChatterID,
		Login:       p.ChatterLogin,
		DisplayName: p.ChatterName,
		Color:       p.Color,
		Badges:      p.Badges,
	}
}

// message is the structured chat message of channel.chat.message and
// channel.chat.notification (EventSub Reference "Message").
type message struct {
	Text      string     `json:"text"`
	Fragments []fragment `json:"fragments"`
}

// fragment is one ordered piece of a structured chat message (EventSub
// Reference "Channel Chat Message Event"); the emote fragments carry
// the emote ID and the code as their text.
type fragment struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emote struct {
		ID         string `json:"id"`
		EmoteSetID string `json:"emote_set_id"`
	} `json:"emote"`
}

// emoteCodes are the emote codes in the order of the text: the
// fragments are ordered, so the emote fragments in their order give
// the codes; the code of a fragment is its text, resolved to the code
// with the catalog when the platform names none (roadmap 4.4, task 9).
// The cheermote fragments stay text, the amount of the bits is carried
// by the separate channel.cheer event.
func (m *Mapper) emoteCodes(p chatMessage) []string {
	codes := make([]string, 0, len(p.Message.Fragments))
	for _, f := range p.Message.Fragments {
		if f.Type != "emote" {
			continue
		}
		codes = append(codes, m.emoteCode(f))
	}
	return codes
}

// emoteCode is the code of one emote fragment: the text the platform
// marks, from the catalog if the text is empty.
func (m *Mapper) emoteCode(f fragment) string {
	if f.Text != "" {
		return f.Text
	}
	if m.emotes != nil && f.Emote.ID != "" {
		if got, ok := m.emotes.Resolve(m.emoteChannel, f.Emote.ID); ok && got.Code != "" {
			return got.Code
		}
	}
	return f.Emote.ID
}

// follow is the payload of channel.follow.
type follow struct {
	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
}

func (p follow) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserName}
}

// raid is the payload of channel.raid: the raiding and the raided
// broadcaster under from_ and to_broadcaster_*.
type raid struct {
	Viewers int64 `json:"viewers"`

	FromBroadcasterUserID    string `json:"from_broadcaster_user_id"`
	FromBroadcasterUserLogin string `json:"from_broadcaster_user_login"`
	FromBroadcasterUserName  string `json:"from_broadcaster_user_name"`
}

func (p raid) user() chatUser {
	return chatUser{
		ID:          p.FromBroadcasterUserID,
		Login:       p.FromBroadcasterUserLogin,
		DisplayName: p.FromBroadcasterUserName,
	}
}

// chatNotification is the payload of channel.chat.notification (EventSub
// Reference "Channel Chat Notification Event"): the notice_type names
// the notice, the chatter is the actor, the type objects carry the
// details (null for a notice that does not fill them). The
// subscriptions, the resubscriptions and the gifts all come from here
// (the separate subscribe subscriptions are not subscribed).
type chatNotification struct {
	NoticeType    string  `json:"notice_type"`
	SystemMessage string  `json:"system_message"`
	Message       message `json:"message"`

	ChatterID        string `json:"chatter_user_id"`
	ChatterLogin     string `json:"chatter_user_login"`
	ChatterName      string `json:"chatter_user_name"`
	ChatterAnonymous bool   `json:"chatter_is_anonymous"`

	Sub              *subNotice           `json:"sub"`
	Resub            *resubNotice         `json:"resub"`
	SubGift          *subGiftNotice       `json:"sub_gift"`
	CommunitySubGift *communityGiftNotice `json:"community_sub_gift"`
}

// subNotice is the sub object of a "sub" notice.
type subNotice struct {
	SubTier string `json:"sub_tier"`
}

// resubNotice is the resub object of a "resub" notice.
type resubNotice struct {
	CumulativeMonths int    `json:"cumulative_months"`
	StreakMonths     int    `json:"streak_months"`
	SubTier          string `json:"sub_tier"`
}

// subGiftNotice is the sub_gift object of a "sub_gift" notice.
type subGiftNotice struct {
	SubTier        string `json:"sub_tier"`
	RecipientID    string `json:"recipient_user_id"`
	RecipientLogin string `json:"recipient_user_login"`
	RecipientName  string `json:"recipient_user_name"`
}

// communityGiftNotice is the community_sub_gift object of a
// "community_sub_gift" notice (the mass gift).
type communityGiftNotice struct {
	ID      string `json:"id"`
	Total   int    `json:"total"`
	SubTier string `json:"sub_tier"`
}

// event is the mapped event of the notification; the error is for a
// notice type the platform does not send for the subscribed events.
func (p chatNotification) event() (connector.Event, error) {
	switch p.NoticeType {
	case "sub":
		if p.Sub == nil {
			return connector.Event{}, errors.New("eventsub: the sub notice has no sub object")
		}
		return connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelSubscribe,
			User:     new(identity(p.chatter())),
			Details:  eventtype.Details{Subscription: &eventtype.Subscription{Plan: p.Sub.SubTier}},
		}, nil
	case "resub":
		if p.Resub == nil {
			return connector.Event{}, errors.New("eventsub: the resub notice has no resub object")
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelResubscribe,
			User:     new(identity(p.chatter())),
			Details:  eventtype.Details{Subscription: &eventtype.Subscription{Plan: p.Resub.SubTier}},
		}
		if p.Message.Text != "" {
			ev.Details.Message = &eventtype.Message{Text: p.Message.Text}
		}
		return ev, nil
	case "sub_gift":
		if p.SubGift == nil {
			return connector.Event{}, errors.New("eventsub: the sub_gift notice has no sub_gift object")
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionGift,
			Target:   new(identity(chatUser{ID: p.SubGift.RecipientID, Login: p.SubGift.RecipientLogin, DisplayName: p.SubGift.RecipientName})),
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: p.SubGift.SubTier},
				Gift:         &eventtype.Gift{Anonymous: p.ChatterAnonymous, Count: 1},
			},
		}
		if !p.ChatterAnonymous {
			ev.User = new(identity(p.chatter()))
		}
		return ev, nil
	case "community_sub_gift":
		if p.CommunitySubGift == nil {
			return connector.Event{}, errors.New("eventsub: the community_sub_gift notice has no object")
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionMassGift,
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: p.CommunitySubGift.SubTier},
				Gift:         &eventtype.Gift{Anonymous: p.ChatterAnonymous, Count: p.CommunitySubGift.Total},
			},
		}
		if !p.ChatterAnonymous {
			ev.User = new(identity(p.chatter()))
		}
		return ev, nil
	default:
		return connector.Event{}, fmt.Errorf("eventsub: unknown chat notification %q", p.NoticeType)
	}
}

func (p chatNotification) chatter() chatUser {
	return chatUser{ID: p.ChatterID, Login: p.ChatterLogin, DisplayName: p.ChatterName}
}

// cheer is the payload of channel.cheer (the cheering user is under
// user_*; it is null when the cheer is anonymous).
type cheer struct {
	IsAnonymous bool   `json:"is_anonymous"`
	Bits        int64  `json:"bits"`
	Message     string `json:"message"`

	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
}

func (p cheer) user() chatUser {
	return chatUser{
		ID:          p.UserID,
		Login:       p.UserLogin,
		DisplayName: p.UserName,
	}
}

// modTarget is the per-action target object of channel.moderate v2
// (ban, timeout, …; the object is null for the actions without a
// target).
type modTarget struct {
	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
	Reason    string `json:"reason"`
}

func (t modTarget) user() chatUser {
	return chatUser{ID: t.UserID, Login: t.UserLogin, DisplayName: t.UserName}
}

// moderate is the payload of channel.moderate v2 (EventSub Reference
// "Channel Moderate Event V2"): the action under "action", the target
// in the per-action object.
type moderate struct {
	Action  string     `json:"action"`
	Ban     *modTarget `json:"ban"`
	Timeout *modTarget `json:"timeout"`
}

// messageDelete is the payload of channel.chat.message_delete: the
// deleted message's author under target_user_*.
type messageDelete struct {
	MessageID string `json:"message_id"`

	TargetUserID    string `json:"target_user_id"`
	TargetUserLogin string `json:"target_user_login"`
	TargetUserName  string `json:"target_user_name"`
}

func (p messageDelete) user() chatUser {
	return chatUser{ID: p.TargetUserID, Login: p.TargetUserLogin, DisplayName: p.TargetUserName}
}

// whisper is the payload of user.whisper.message (EventSub Reference
// "Whisper Received Event"): the sender under from_*, the text under
// whisper.text.
type whisper struct {
	Whisper struct {
		Text string `json:"text"`
	} `json:"whisper"`
	WhisperID string `json:"whisper_id"`

	FromUserID    string `json:"from_user_id"`
	FromUserLogin string `json:"from_user_login"`
	FromUserName  string `json:"from_user_name"`
}

func (p whisper) user() chatUser {
	return chatUser{ID: p.FromUserID, Login: p.FromUserLogin, DisplayName: p.FromUserName}
}

// sharedChat is the payload of the channel.shared_chat events (EventSub
// Reference "Channel Shared Chat Session" events); the session is named
// by session_id, the payload names no title.
type sharedChat struct {
	SessionID string `json:"session_id"`
}

// hypeTrain is the payload of the channel.hype_train v2 events (EventSub
// Reference "Hype Train" events): the level under "level"; the payload
// names no reward interval and no end reason.
type hypeTrain struct {
	Level    int   `json:"level"`
	Progress int64 `json:"progress"`
	Goal     int64 `json:"goal"`
	Total    int64 `json:"total"`
}

// adStarted is the payload of channel.ad_break.begin.
type adStarted struct {
	DurationSeconds int  `json:"duration_seconds"`
	IsAutomatic     bool `json:"is_automatic"`
}

// shoutout is the payload of channel.shoutout.receive: the shouting
// channel under from_broadcaster_*.
type shoutout struct {
	ViewerCount int64 `json:"viewer_count"`

	FromBroadcasterUserID    string `json:"from_broadcaster_user_id"`
	FromBroadcasterUserLogin string `json:"from_broadcaster_user_login"`
	FromBroadcasterUserName  string `json:"from_broadcaster_user_name"`
}

func (p shoutout) user() chatUser {
	return chatUser{
		ID:          p.FromBroadcasterUserID,
		Login:       p.FromBroadcasterUserLogin,
		DisplayName: p.FromBroadcasterUserName,
	}
}

// goal is the payload of the channel.goal events (EventSub Reference
// "Goals Event"); the payload names no currency.
type goal struct {
	CurrentAmount int64 `json:"current_amount"`
	TargetAmount  int64 `json:"target_amount"`
	IsAchieved    bool  `json:"is_achieved"`
}

// charity is the payload of the channel.charity_campaign events
// (EventSub Reference "Charity Campaign" events): the amounts are
// objects with the value in the currency's minor unit.
type charity struct {
	CurrentAmount charityAmount `json:"current_amount"`
	TargetAmount  charityAmount `json:"target_amount"`
	CharityName   string        `json:"charity_name"`
}

// charityAmount is the amount object of the charity events.
type charityAmount struct {
	Value         int64  `json:"value"`
	DecimalPlaces int    `json:"decimal_places"`
	Currency      string `json:"currency"`
}

// autoRedemption is the payload of
// channel.channel_points_automatic_reward_redemption.add: the reward
// (type and cost) is an object; the payload names no reward ID, so the
// reward type stands in for it (shortcut: type instead of ID, name the
// reward by the Helix endpoint when the commands need it).
type autoRedemption struct {
	Reward struct {
		Type string `json:"type"`
		Cost int64  `json:"cost"`
	} `json:"reward"`
	ID string `json:"id"`

	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
}

func (p autoRedemption) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserName}
}

// customRedemption is the payload of
// channel.channel_points_custom_reward_redemption.add: the reward (id,
// cost, title) is an object.
type customRedemption struct {
	Reward struct {
		ID   string `json:"id"`
		Cost int64  `json:"cost"`
	} `json:"reward"`
	ID        string `json:"id"`
	UserInput string `json:"user_input"`
	Status    string `json:"status"`

	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
}

func (p customRedemption) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserName}
}
