// SPDX-License-Identifier: MIT

package twitch

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	jsonv2 "encoding/json/v2"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
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
}

// MapperOptions configures NewMapper.
type MapperOptions struct {
	// Receiver takes the mapped events (the event service).
	Receiver connector.Receiver
	// Dedup drops the repeats by the message ID (B22).
	Dedup *connector.Dedup
	// BotID is the user ID of the bot account of the channel; its
	// messages are flagged FromBot (B13). Empty for none.
	BotID string
	// Logger logs the dropped events (Code-ADR-0003); if nil, the log
	// is discarded.
	Logger *slog.Logger
}

// NewMapper creates a Mapper.
func NewMapper(o MapperOptions) *Mapper {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Mapper{rec: o.Receiver, dedup: o.Dedup, botID: o.BotID, log: log}
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
// repeats by the message ID and maps the event.
func (m *Mapper) OnMessage(ctx context.Context, e Event) {
	if !m.dedup.First(e.ID) {
		return
	}
	if err := m.dispatch(ctx, e); err != nil {
		m.log.WarnContext(ctx, "drop an unmapped eventsub event", "eventtype", e.Event.EventType, "error", err)
	}
}

// dispatch is the table of the mapping; the error is for a payload
// that does not fit its type, and the event is dropped.
func (m *Mapper) dispatch(ctx context.Context, e Event) error {
	body := e.Event.Event
	switch e.Event.EventType {
	case "channel.chat.message":
		var p chatMessage
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Message(ctx, m.incoming(p))
	case "stream.online", "stream.offline":
		return m.rec.Stream(ctx, platform.Twitch, e.Event.EventType == "stream.online")
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
			Details:  eventtype.Details{Raid: &eventtype.Raid{Viewers: p.ViewerCount}},
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
		typ := eventtype.ChatUserTimeout
		if p.Action == "ban" {
			typ = eventtype.ChatUserBan
		}
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			User:     new(identity(p.user())),
		}
		if p.Message != "" {
			ev.Details = eventtype.Details{Moderation: &eventtype.Moderation{Message: p.Message}}
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
			Details:  eventtype.Details{Message: &eventtype.Message{ID: p.ID}},
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
			Details:  eventtype.Details{Message: &eventtype.Message{Text: p.Message}},
		})
	case "channel.shared_chat.begin", "channel.shared_chat.update", "channel.shared_chat.end":
		var p sharedChat
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchSharedChatStart
		switch e.Event.EventType {
		case "channel.shared_chat.update":
			typ = eventtype.TwitchSharedChatUpdate
		case "channel.shared_chat.end":
			typ = eventtype.TwitchSharedChatEnd
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{SharedChat: &eventtype.SharedChat{SessionID: p.ChatSessionID, Title: p.Title}},
		})
	case "channel.hype_train.start", "channel.hype_train.progress", "channel.hype_train.end":
		var p hypeTrain
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchHypeTrainStart
		switch e.Event.EventType {
		case "channel.hype_train.progress":
			typ = eventtype.TwitchHypeTrainProgress
		case "channel.hype_train.end":
			typ = eventtype.TwitchHypeTrainEnd
		}
		train := &eventtype.HypeTrain{Level: p.CurrentLevel, Progress: p.Progress, Goal: p.Goal}
		if p.RewardInterval > 0 {
			train.RewardLevel = int(p.Progress / p.RewardInterval)
		}
		if e.Event.EventType == "channel.hype_train.end" {
			train.Outcome = p.EndReason
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{HypeTrain: train},
		})
	case "channel.ad.started":
		var p adStarted
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchAdStart,
			Details:  eventtype.Details{AdBreak: &eventtype.AdBreak{Duration: p.Length, Message: p.Message}},
		})
	case "channel.shoutout.received":
		var p shoutout
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchShoutoutReceive,
			User:     new(identity(p.user())),
			Details:  eventtype.Details{Shoutout: &eventtype.Shoutout{Viewers: p.FromUserViewers}},
		})
	case "channel.goal.start", "channel.goal.progress", "channel.goal.complete":
		var p goal
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		typ := eventtype.TwitchGoalStart
		switch e.Event.EventType {
		case "channel.goal.progress":
			typ = eventtype.TwitchGoalProgress
		case "channel.goal.complete":
			typ = eventtype.TwitchGoalEnd
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     typ,
			Details:  eventtype.Details{Goal: &eventtype.Goal{Current: p.CurrentAmount, Target: p.TargetAmount, Currency: p.Currency}},
		})
	case "channel.charity.progress", "channel.charity.complete":
		var p charity
		if err := jsonv2.Unmarshal(body, &p); err != nil {
			return err
		}
		return m.rec.Event(ctx, connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchCharityDonation,
			Details:  eventtype.Details{Charity: &eventtype.Charity{Current: p.CurrentAmount, Target: p.TargetAmount, Currency: p.Currency}},
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
			Details:  eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: p.Cost, Reward: p.RewardID}},
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
			Details:  eventtype.Details{CustomPowerUp: &eventtype.CustomPowerUp{Reward: p.RewardID}},
		}
		if p.UserInput != "" {
			ev.Details.Message = &eventtype.Message{Text: p.UserInput}
		}
		return m.rec.Event(ctx, ev)
	default:
		return fmt.Errorf("eventsub: no mapping for %s", e.Event.EventType)
	}
}

// incoming is a chat message with its author, the roles from the
// badges and the FromBot flag (B13).
func (m *Mapper) incoming(p chatMessage) connector.Incoming {
	author := identity(p.user())
	return connector.Incoming{
		Platform: platform.Twitch,
		Author:   author,
		FromBot:  p.UserID != "" && p.UserID == m.botID,
		Message:  eventtype.Message{ID: p.ID, Text: p.Message, Emotes: p.emoteCodes()},
	}
}

// chatUser is the user of a chat event, built by the payloads from
// their own field names.
type chatUser struct {
	ID          string
	Login       string
	DisplayName string
	Color       string
	Avatar      string
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
		AvatarURL:      u.Avatar,
		Roles:          roles,
	}
}

// emote is a marked emote in a chat message.
type emote struct {
	ID    string `json:"id"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

// emoteCodes is the emote IDs in the order of the text; the cheer
// motes stay text, the amount of the bits is carried by the separate
// channel.cheer event.
func emoteCodes(emotes, emoji []emote) []string {
	all := append(append([]emote{}, emotes...), emoji...)
	sort.Slice(all, func(i, j int) bool { return all[i].Start < all[j].Start })
	codes := make([]string, 0, len(all))
	for _, e := range all {
		codes = append(codes, e.ID)
	}
	return codes
}

// chatMessage is the payload of channel.chat.message.
type chatMessage struct {
	ID      string  `json:"id"`
	Message string  `json:"message"`
	Emotes  []emote `json:"emotes"`
	Emoji   []emote `json:"emoji"`

	UserID          string  `json:"user_id"`
	UserLogin       string  `json:"user_login"`
	UserDisplayName string  `json:"user_display_name"`
	Color           string  `json:"color"`
	Avatar          string  `json:"profile_image_url"`
	Badges          []badge `json:"badges"`
}

func (p chatMessage) user() chatUser {
	return chatUser{
		ID:          p.UserID,
		Login:       p.UserLogin,
		DisplayName: p.UserDisplayName,
		Color:       p.Color,
		Avatar:      p.Avatar,
		Badges:      p.Badges,
	}
}

func (p chatMessage) emoteCodes() []string {
	return emoteCodes(p.Emotes, p.Emoji)
}

// follow is the payload of channel.follow.
type follow struct {
	UserID          string `json:"user_id"`
	UserLogin       string `json:"user_login"`
	UserDisplayName string `json:"user_display_name"`
}

func (p follow) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisplayName}
}

// raid is the payload of channel.raid.
type raid struct {
	ViewerCount int64 `json:"viewer_count"`

	FromUserID          string `json:"from_user_id"`
	FromUserLogin       string `json:"from_user_login"`
	FromUserDisplayName string `json:"from_user_display_name"`
}

func (p raid) user() chatUser {
	return chatUser{ID: p.FromUserID, Login: p.FromUserLogin, DisplayName: p.FromUserDisplayName}
}

// chatNotification is the payload of channel.chat.notification: the
// subscriptions, the resubscriptions and the gifts of the channel
// (plan appendix A.2: the separate subscribe subscriptions are
// commented out in the original, so all of them come from here).
type chatNotification struct {
	Type      string `json:"type"`
	Anonymous bool   `json:"anonymous"`
	Total     int    `json:"total"`

	UserID          string   `json:"user_id"`
	UserLogin       string   `json:"user_login"`
	UserDisplayName string   `json:"user_display_name"`
	ToUserID        string   `json:"to_user_id"`
	ToUserLogin     string   `json:"to_user_login"`
	ToUserLogins    []string `json:"to_user_logins"`
	PlanName        string   `json:"plan_name"`
	Months          int      `json:"cumulative_month_count"`
	GiftMessage     string   `json:"gift_message"`
}

// event is the mapped event of the notification; the error is for a
// type the platform does not send.
func (p chatNotification) event() (connector.Event, error) {
	sub := &eventtype.Subscription{Plan: p.PlanName}
	switch p.Type {
	case "subscription":
		return connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelSubscribe,
			User:     new(identity(p.giver())),
			Details:  eventtype.Details{Subscription: sub},
		}, nil
	case "resubscription":
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchChannelResubscribe,
			User:     new(identity(p.giver())),
			Details:  eventtype.Details{Subscription: sub},
		}
		if p.GiftMessage != "" {
			ev.Details.Message = &eventtype.Message{Text: p.GiftMessage}
		}
		return ev, nil
	case "gift":
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionGift,
			Target:   new(identity(chatUser{ID: p.ToUserID, Login: p.ToUserLogin, DisplayName: p.ToUserLogin})),
			Details: eventtype.Details{
				Subscription: sub,
				Gift:         &eventtype.Gift{Anonymous: p.Anonymous, Count: 1},
			},
		}
		if !p.Anonymous {
			ev.User = new(identity(p.giver()))
		}
		return ev, nil
	case "mass_gift":
		ev := connector.Event{
			Platform: platform.Twitch,
			Type:     eventtype.TwitchSubscriptionMassGift,
			Details: eventtype.Details{
				Subscription: sub,
				Gift:         &eventtype.Gift{Anonymous: p.Anonymous, Count: p.Total},
			},
		}
		if !p.Anonymous {
			ev.User = new(identity(p.giver()))
		}
		// The platform names the recipients by login only; the login
		// stands in for the platform user ID until the user service
		// resolves it (shortcut: no recipient ID, resolve by the Helix
		// users endpoint when the mass gifts prove it necessary).
		for _, login := range p.ToUserLogins {
			ev.Recipients = append(ev.Recipients,
				user.Identity{Platform: platform.Twitch, PlatformUserID: login, Login: login, DisplayName: login})
		}
		return ev, nil
	default:
		return connector.Event{}, fmt.Errorf("eventsub: unknown chat notification %q", p.Type)
	}
}

func (p chatNotification) giver() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisplayName}
}

// cheer is the payload of channel.cheer.
type cheer struct {
	Bits    int64  `json:"bits"`
	Message string `json:"message"`

	UserID          string  `json:"user_id"`
	UserLogin       string  `json:"user_login"`
	UserDisplayName string  `json:"user_display_name"`
	Color           string  `json:"color"`
	Avatar          string  `json:"profile_image_url"`
	Badges          []badge `json:"badges"`
}

func (p cheer) user() chatUser {
	return chatUser{
		ID:          p.UserID,
		Login:       p.UserLogin,
		DisplayName: p.UserDisplayName,
		Color:       p.Color,
		Avatar:      p.Avatar,
		Badges:      p.Badges,
	}
}

// moderate is the payload of channel.moderate.
type moderate struct {
	Action  string `json:"moderation_action"`
	Message string `json:"moderation_message"`

	TargetUserID          string `json:"target_user_id"`
	TargetUserLogin       string `json:"target_user_login"`
	TargetUserDisplayName string `json:"target_user_display_name"`
}

func (p moderate) user() chatUser {
	return chatUser{
		ID:          p.TargetUserID,
		Login:       p.TargetUserLogin,
		DisplayName: p.TargetUserDisplayName,
	}
}

// messageDelete is the payload of channel.chat.message_delete.
type messageDelete struct {
	ID string `json:"id"`

	UserID          string `json:"user_id"`
	UserLogin       string `json:"user_login"`
	UserDisplayName string `json:"user_display_name"`
}

func (p messageDelete) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisplayName}
}

// whisper is the payload of user.whisper.message.
type whisper struct {
	Message string `json:"message"`

	UserID          string `json:"user_id"`
	UserLogin       string `json:"user_login"`
	UserDisplayName string `json:"user_display_name"`
}

func (p whisper) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisplayName}
}

// sharedChat is the payload of the channel.shared_chat events; the
// payload is formalized by the 4.4 specification twitch-events.md.
type sharedChat struct {
	ChatSessionID string `json:"chat_session_id"`
	Title         string `json:"title"`
}

// hypeTrain is the payload of the channel.hype_train events.
type hypeTrain struct {
	CurrentLevel   int    `json:"current_level"`
	Progress       int64  `json:"progress"`
	Goal           int64  `json:"goal"`
	RewardInterval int64  `json:"reward_interval"`
	EndReason      string `json:"end_reason"`
}

// adStarted is the payload of channel.ad.started.
type adStarted struct {
	Length  int    `json:"length"`
	Message string `json:"message"`
}

// shoutout is the payload of channel.shoutout.received.
type shoutout struct {
	FromUserID          string `json:"from_user_id"`
	FromUserLogin       string `json:"from_user_login"`
	FromUserDisplayName string `json:"from_user_display_name"`
	FromUserViewers     int64  `json:"from_user_viewers"`
}

func (p shoutout) user() chatUser {
	return chatUser{ID: p.FromUserID, Login: p.FromUserLogin, DisplayName: p.FromUserDisplayName}
}

// goal is the payload of the channel.goal events.
type goal struct {
	CurrentAmount int64  `json:"current_amount"`
	TargetAmount  int64  `json:"target_amount"`
	Currency      string `json:"currency"`
}

// charity is the payload of the channel.charity events.
type charity struct {
	CurrentAmount int64  `json:"current_amount"`
	TargetAmount  int64  `json:"target_amount"`
	Currency      string `json:"currency"`
}

// autoRedemption is the payload of
// channel.channel_points_automatic_reward_redemption.add.
type autoRedemption struct {
	Cost        int64  `json:"automatic_reward_cost"`
	RewardID    string `json:"automatic_reward_id"`
	UserID      string `json:"user_id"`
	UserLogin   string `json:"user_login"`
	UserDisplay string `json:"user_name"`
}

func (p autoRedemption) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisplay}
}

// customRedemption is the payload of
// channel.channel_points_custom_reward_redemption.add.
type customRedemption struct {
	Cost      int64  `json:"custom_reward_cost"`
	RewardID  string `json:"custom_reward_id"`
	UserInput string `json:"custom_reward_user_input"`
	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserDisp  string `json:"user_display_name"`
}

func (p customRedemption) user() chatUser {
	return chatUser{ID: p.UserID, Login: p.UserLogin, DisplayName: p.UserDisp}
}
