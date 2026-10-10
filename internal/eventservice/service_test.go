// SPDX-License-Identifier: MIT

package eventservice_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/decimal"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/eventservice"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/template"
)

// on returns an event command for type t, named after it.
func on(t event.Type) command.Command {
	return command.Command{ID: id.New(), Name: string(t), Kind: command.KindEvent, Event: t, Enabled: true, ErrorPolicy: command.ErrorContinue}
}

// chat returns an enabled chat command with the trigger.
func chat(name, trigger string) command.Command {
	return command.Command{
		ID: id.New(), Name: name, Kind: command.KindChat, Enabled: true,
		TriggerMode: command.TriggerExclamation, Triggers: []string{trigger}, ErrorPolicy: command.ErrorContinue,
	}
}

// account returns an account on platform p.
func account(p platform.Name, login string, roles ...role.Role) user.Identity {
	return user.Identity{Platform: p, PlatformUserID: login, Login: login, DisplayName: login, Roles: role.NewSet(roles...)}
}

// message returns a chat message of the account.
func message(author user.Identity, messageID, text string) connector.Incoming {
	return connector.Incoming{Platform: author.Platform, Author: author, Message: eventtype.Message{ID: messageID, Text: text}}
}

// allChatEvents returns event commands for every chat event of B13.
func allChatEvents() []command.Command {
	return []command.Command{
		on(eventtype.ChatUserNew), on(eventtype.ChatUserJoin), on(eventtype.ChatMessage),
		on(eventtype.ChatUserFirstMessage), on(eventtype.ChatUserEntrance),
	}
}

// TestMessageOrder covers B13 and B12 of events.md: the events and
// commands of a chat message in their order, with the data of the
// message.
func TestMessageOrder(t *testing.T) {
	t.Parallel()
	welcome := chat("welcome", "welcome")
	f := newFixture(t, append(allChatEvents(), chat("hug", "hug"), welcome)...)
	ctx := t.Context()
	ada := account(platform.Mock, "ada", role.Moderator)
	f.store.setEntrance(t, ada, welcome.ID)
	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	f.engine.runs()
	f.pub.types()

	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "!hug bob")))
	assert.Equal(t, []event.Type{
		eventtype.ChatUserNew, eventtype.ChatUserJoin, eventtype.ChatMessage,
		eventtype.ChatUserFirstMessage, eventtype.ChatUserEntrance,
	}, f.pub.types())
	reqs := f.engine.take()
	names := make([]string, len(reqs))
	for i, r := range reqs {
		names[i] = r.Command.Name
	}
	assert.Equal(t, []string{
		"chat.user.new", "chat.user.join", "chat.message", "hug", "chat.user.first_message", "chat.user.entrance", "welcome",
	}, names)

	hug := reqs[3]
	assert.Equal(t, engine.SourceChat, hug.Source)
	assert.Equal(t, platform.Mock, hug.Params.Platform)
	assert.Equal(t, "ada", hug.Params.User.Identities[0].Login)
	assert.True(t, hug.Params.User.Roles(platform.Mock).Has(role.Moderator), "the roles of the message")
	assert.Equal(t, []string{"bob"}, hug.Params.Args)
	assert.Equal(t, "bob", hug.Params.ArgsText)
	assert.Equal(t, "!hug bob", hug.Params.Message)
	assert.Equal(t, "m1", hug.Params.MessageID)

	msg := reqs[2]
	assert.Equal(t, engine.SourceEvent, msg.Source)
	assert.Equal(t, eventtype.ChatMessage, msg.Event)
	assert.Equal(t, "m1", msg.Params.MessageID, "B12: the message is the triggering one")
	assert.Equal(t, template.TextValue("!hug bob"), msg.Params.Values[template.EventMessage])

	entrance := reqs[6]
	assert.True(t, entrance.Entrance)
	assert.Equal(t, engine.SourceChat, entrance.Source)
	assert.Equal(t, "m1", entrance.Params.MessageID)

	require.NoError(t, f.service.Message(ctx, message(ada, "m2", "hello")))
	assert.Equal(t, []event.Type{eventtype.ChatMessage}, f.pub.types(), "the second message fires only itself")
	assert.Equal(t, []string{"chat.message"}, f.engine.runs())
}

// TestBotMessage covers B28: the bot's messages trigger nothing.
func TestBotMessage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, append(allChatEvents(), chat("hug", "hug"))...)
	bot := message(account(platform.Mock, "bot"), "m1", "!hug")
	bot.FromBot = true
	require.NoError(t, f.service.Message(t.Context(), bot))
	assert.Empty(t, f.pub.types())
	assert.Empty(t, f.engine.runs())
}

// TestGreetingOnlyLive covers command-engine.md B41: messages while the
// stream is offline do not greet and do not count as the first.
func TestGreetingOnlyLive(t *testing.T) {
	t.Parallel()
	f := newFixture(t, on(eventtype.ChatUserEntrance))
	ctx := t.Context()
	ada := account(platform.Mock, "ada")
	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "hi")))
	assert.Empty(t, f.engine.runs(), "offline")

	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	require.NoError(t, f.service.Message(ctx, message(ada, "m2", "hi again")))
	assert.Equal(t, []string{"chat.user.entrance"}, f.engine.runs(), "the first message while live")
	require.NoError(t, f.service.Message(ctx, message(ada, "m3", "and again")))
	assert.Empty(t, f.engine.runs())

	require.NoError(t, f.service.Stream(ctx, platform.Twitch, true))
	require.NoError(t, f.service.Message(ctx, message(account(platform.Twitch, "ada"), "t1", "hi")))
	assert.Equal(t, []string{"chat.user.entrance"}, f.engine.runs(), "B11: per platform and session")
}

// TestJoin covers B14: a join fires chat.user.new and chat.user.join, but
// greets nobody; the next message does not repeat them.
func TestJoin(t *testing.T) {
	t.Parallel()
	f := newFixture(t, allChatEvents()...)
	ctx := t.Context()
	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	ada := account(platform.Mock, "ada")
	require.NoError(t, f.service.Join(ctx, platform.Mock, ada))
	assert.Equal(t, []string{"chat.user.new", "chat.user.join"}, f.engine.runs())
	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "hi")))
	assert.Equal(t, []string{"chat.message", "chat.user.first_message", "chat.user.entrance"}, f.engine.runs())

	require.Error(t, f.service.Join(ctx, platform.Twitch, ada), "an account of another platform")
}

// TestOncePerSession covers B3, B11 and B20: a second follow of the same
// user in the session fires nothing, a new session or another platform
// fires again.
func TestOncePerSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, on(eventtype.ChannelFollow))
		f.settings.set(grace(0))
		ctx := t.Context()
		follow := func(p platform.Name, login string) connector.Event {
			who := account(p, login)
			return connector.Event{Platform: p, Type: eventtype.ChannelFollow, User: &who}
		}
		require.NoError(t, f.service.Event(ctx, follow(platform.Mock, "ada")))
		require.NoError(t, f.service.Event(ctx, follow(platform.Mock, "ada")))
		require.NoError(t, f.service.Event(ctx, follow(platform.Mock, "bob")))
		require.NoError(t, f.service.Event(ctx, follow(platform.Kick, "ada")))
		assert.Equal(t, []string{"channel.follow", "channel.follow", "channel.follow"}, f.engine.runs())

		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		require.NoError(t, f.service.Event(ctx, follow(platform.Mock, "ada")))
		assert.Equal(t, []string{"channel.follow"}, f.engine.runs(), "a new session")
	})
}

// TestSpecificAndNeutral covers B2 and B12: a platform-specific event fires
// with its neutral type after it, each with its event command.
func TestSpecificAndNeutral(t *testing.T) {
	t.Parallel()
	f := newFixture(t, on(eventtype.TwitchChannelFollow), on(eventtype.ChannelFollow))
	ada := account(platform.Twitch, "ada")
	require.NoError(t, f.service.Event(t.Context(), connector.Event{Platform: platform.Twitch, Type: eventtype.TwitchChannelFollow, User: &ada}))
	assert.Equal(t, []event.Type{eventtype.TwitchChannelFollow, eventtype.ChannelFollow}, f.pub.types())
	assert.Equal(t, []string{"twitch.channel.follow", "channel.follow"}, f.engine.runs())

	require.NoError(t, f.service.Event(t.Context(), connector.Event{Platform: platform.Twitch, Type: eventtype.ChannelFollow, User: &ada}))
	assert.Empty(t, f.engine.runs(), "the neutral type counts for the session too")
}

// TestMassGift covers B5, B26 and B29: from the threshold on only the mass
// gift fires, below it only the single gifts.
func TestMassGift(t *testing.T) {
	t.Parallel()
	f := newFixture(t, on(eventtype.ChannelSubscriptionGift), on(eventtype.ChannelSubscriptionMassGift))
	ctx := t.Context()
	gift := func(anonymous bool, gifter string, recipients ...string) connector.Event {
		e := connector.Event{
			Platform: platform.Mock, Type: eventtype.ChannelSubscriptionMassGift,
			Details: eventtype.Details{
				Subscription: &eventtype.Subscription{Plan: "1000", PlanName: "Tier 1"},
				Gift:         &eventtype.Gift{Anonymous: anonymous, Count: len(recipients)},
			},
		}
		if gifter != "" {
			who := account(platform.Mock, gifter)
			e.User = &who
		}
		for _, r := range recipients {
			e.Recipients = append(e.Recipients, account(platform.Mock, r))
		}
		return e
	}

	require.NoError(t, f.service.Event(ctx, gift(false, "ada", "bob")))
	reqs := f.engine.take()
	require.Len(t, reqs, 1, "B26: one gift, threshold 2")
	assert.Equal(t, eventtype.ChannelSubscriptionGift, reqs[0].Event)
	assert.Equal(t, "bob", reqs[0].Params.Target.Identities[0].Login)
	assert.Equal(t, template.TextValue("false"), reqs[0].Params.Values[template.EventAnonymous])
	assert.NotContains(t, reqs[0].Params.Values, template.EventGiftedSubs)

	require.NoError(t, f.service.Event(ctx, gift(false, "ada", "bob", "carol", "dave")))
	reqs = f.engine.take()
	require.Len(t, reqs, 1)
	assert.Equal(t, eventtype.ChannelSubscriptionMassGift, reqs[0].Event)
	assert.Nil(t, reqs[0].Params.Target)
	assert.Equal(t, template.NumberValue(decimal.New(3)), reqs[0].Params.Values[template.EventGiftedSubs])
	assert.Equal(t, template.TextValue("1000"), reqs[0].Params.Values[template.EventSubPlan])
	assert.Equal(t, template.TextValue("Tier 1"), reqs[0].Params.Values[template.EventSubPlanName])

	f.settings.set(func(e *settings.Events) { e.MassGiftThreshold = 5 })
	require.NoError(t, f.service.Event(ctx, gift(true, "", "bob", "carol", "dave")))
	reqs = f.engine.take()
	require.Len(t, reqs, 3, "below the new threshold")
	for _, r := range reqs {
		assert.Equal(t, eventtype.ChannelSubscriptionGift, r.Event)
		assert.Nil(t, r.Params.User, "B29: anonymous")
		assert.Equal(t, template.TextValue("true"), r.Params.Values[template.EventAnonymous])
	}

	bad := gift(false, "ada", "bob")
	bad.Details.Gift.Count = 2
	require.ErrorIs(t, f.service.Event(ctx, bad), eventtype.ErrShape, "recipients and count differ")
}

// TestEventData covers B7 and B9: values of the events, and data that do
// not fit the type.
func TestEventData(t *testing.T) {
	t.Parallel()
	f := newFixture(t, on(eventtype.ChannelRaid), on(eventtype.ChannelResubscribe))
	ctx := t.Context()
	ada := account(platform.Mock, "ada")
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Mock, Type: eventtype.ChannelRaid, User: &ada,
		Details: eventtype.Details{Raid: &eventtype.Raid{Viewers: 42}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Mock, Type: eventtype.ChannelResubscribe, User: &ada,
		Details: eventtype.Details{Subscription: &eventtype.Subscription{Plan: "2000"}, Message: &eventtype.Message{Text: "two years!"}},
	}))
	reqs := f.engine.take()
	require.Len(t, reqs, 2)
	assert.Equal(t, template.NumberValue(decimal.New(42)), reqs[0].Params.Values[template.EventRaidViewerCount])
	assert.Equal(t, "ada", reqs[0].Params.User.Identities[0].Login)
	assert.Equal(t, template.TextValue("two years!"), reqs[1].Params.Values[template.EventMessage])
	assert.NotContains(t, reqs[1].Params.Values, template.EventSubPlanName, "the platform named none")
	assert.Empty(t, reqs[1].Params.Message, "only chat events have a triggering message")

	for name, e := range map[string]connector.Event{
		"raid without viewers":   {Platform: platform.Mock, Type: eventtype.ChannelRaid, User: &ada},
		"follow without user":    {Platform: platform.Mock, Type: eventtype.ChannelFollow},
		"follow with a target":   {Platform: platform.Mock, Type: eventtype.ChannelFollow, User: &ada, Target: &ada},
		"named anonymous gifter": {Platform: platform.Mock, Type: eventtype.ChannelSubscriptionGift, User: &ada, Target: &ada, Details: eventtype.Details{Subscription: &eventtype.Subscription{Plan: "1"}, Gift: &eventtype.Gift{Anonymous: true, Count: 1}}},
		"recipients of a follow": {Platform: platform.Mock, Type: eventtype.ChannelFollow, User: &ada, Recipients: []user.Identity{ada}},
	} {
		assert.ErrorIs(t, f.service.Event(ctx, e), eventtype.ErrShape, name)
	}
	for name, e := range map[string]connector.Event{
		"chat message":        {Platform: platform.Mock, Type: eventtype.ChatMessage, User: &ada},
		"stream start":        {Platform: platform.Mock, Type: eventtype.ChannelStreamStart},
		"derived":             {Platform: platform.Mock, Type: eventtype.ChatUserEntrance, User: &ada},
		"type of Twitch":      {Platform: platform.Mock, Type: eventtype.TwitchChannelFollow, User: &ada},
		"application":         {Platform: platform.Mock, Type: eventtype.AppStarted},
		"account of Twitch":   {Platform: platform.Mock, Type: eventtype.ChannelFollow, User: new(account(platform.Twitch, "ada"))},
		"unknown type":        {Platform: platform.Mock, Type: "channel.nothing"},
		"invalid platform ID": {Platform: "Mock!", Type: eventtype.ChannelFollow, User: &ada},
	} {
		assert.Error(t, f.service.Event(ctx, e), name)
	}
	assert.Empty(t, f.engine.runs())
}


// TestChannelPointsCommand covers roadmap 4.4: a redemption runs the
// command the reward is mapped to, with the values of the redemption;
// a reward without a mapping runs nothing.
func TestChannelPointsCommand(t *testing.T) {
	t.Parallel()
	cmd := command.Command{ID: id.New(), Name: "reward-command", Kind: command.KindChat, Enabled: true,
		TriggerMode: command.TriggerExclamation, Triggers: []string{"rc"}, ErrorPolicy: command.ErrorContinue}
	f := newFixture(t, cmd)
	f.points.cfg = settings.ChannelPoints{Rewards: []settings.ChannelPointReward{{RewardID: "r1", Command: "reward-command"}}}
	ctx := t.Context()
	ada := account(platform.Twitch, "8")
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchChannelPointsRedeem, User: &ada,
		Details: eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: 100, Reward: "r1"}},
	}))
	reqs := f.engine.take()
	require.Len(t, reqs, 1)
	assert.Equal(t, cmd.ID, reqs[0].Command.ID)
	assert.Equal(t, eventtype.TwitchChannelPointsRedeem, reqs[0].Event)
	assert.Equal(t, template.NumberValue(decimal.New(100)), reqs[0].Params.Values[template.EventChannelPoints])
	assert.Equal(t, template.TextValue("r1"), reqs[0].Params.Values[template.EventChannelPointsID])
	assert.Equal(t, "8", reqs[0].Params.User.Identities[0].PlatformUserID)

	// a reward without a mapping: published, but nothing runs
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchChannelPointsRedeem, User: &ada,
		Details: eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: 100, Reward: "r2"}},
	}))
	assert.Empty(t, f.engine.take())
	assert.Contains(t, f.pub.types(), eventtype.TwitchChannelPointsRedeem)
}

// TestStreamSession covers B8, B24 and B25 of events.md and B41 of

// TestTwitchEventValues covers B18 and B19 of twitch-events.md: the
// values of the twitch events the identifiers of the event commands
// can use.
func TestTwitchEventValues(t *testing.T) {
	t.Parallel()
	ada := account(platform.Twitch, "8")
	f := newFixture(t,
		on(eventtype.TwitchBitsCheer),
		on(eventtype.ChatUserBan),
		on(eventtype.TwitchHypeTrainEnd),
		on(eventtype.TwitchAdStart),
		on(eventtype.TwitchShoutoutReceive),
		on(eventtype.TwitchGoalEnd),
		on(eventtype.TwitchCharityDonation),
		on(eventtype.TwitchChannelPointsRedeem),
		on(eventtype.TwitchCustomPowerUpRedeem),
	)
	// the redemption runs through its own mapping (roadmap 4.4)
	f.points.cfg = settings.ChannelPoints{Rewards: []settings.ChannelPointReward{
		{RewardID: "r1", Command: "twitch.channel_points.redeem"},
	}}
	ctx := t.Context()
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchBitsCheer, User: &ada,
		Details: eventtype.Details{Bits: &eventtype.Bits{Amount: 100}, Message: &eventtype.Message{Text: "cheer"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.ChatUserBan, User: &ada,
		Details: eventtype.Details{Moderation: &eventtype.Moderation{Message: "no spam"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchHypeTrainEnd,
		Details: eventtype.Details{HypeTrain: &eventtype.HypeTrain{Level: 3, Progress: 12000, Goal: 10000, RewardLevel: 2, Outcome: "goal_reached"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchAdStart,
		Details: eventtype.Details{AdBreak: &eventtype.AdBreak{Duration: 30, Message: "break"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchShoutoutReceive, User: &ada,
		Details: eventtype.Details{Shoutout: &eventtype.Shoutout{Viewers: 7}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchGoalEnd,
		Details: eventtype.Details{Goal: &eventtype.Goal{Current: 100, Target: 100, Currency: "USD"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchCharityDonation,
		Details: eventtype.Details{Charity: &eventtype.Charity{Current: 50, Target: 200, Currency: "EUR"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchChannelPointsRedeem, User: &ada,
		Details: eventtype.Details{ChannelPoints: &eventtype.ChannelPoints{Amount: 100, Reward: "r1"}},
	}))
	require.NoError(t, f.service.Event(ctx, connector.Event{
		Platform: platform.Twitch, Type: eventtype.TwitchCustomPowerUpRedeem, User: &ada,
		Details: eventtype.Details{CustomPowerUp: &eventtype.CustomPowerUp{Reward: "p1"}, Message: &eventtype.Message{Text: "a hug"}},
	}))
	reqs := f.engine.take()
	require.Len(t, reqs, 9)
	v := reqs[0].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(100)), v[template.EventCheerBits])
	assert.Equal(t, template.TextValue("cheer"), v[template.EventMessage])
	v = reqs[1].Params.Values
	assert.Equal(t, template.TextValue("no spam"), v[template.EventModerationMessage])
	v = reqs[2].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(3)), v[template.EventHypeTrainLevel])
	assert.Equal(t, template.NumberValue(decimal.New(12000)), v[template.EventHypeTrainProgress])
	assert.Equal(t, template.NumberValue(decimal.New(10000)), v[template.EventHypeTrainGoal])
	assert.Equal(t, template.NumberValue(decimal.New(2)), v[template.EventHypeTrainReward])
	assert.Equal(t, template.TextValue("goal_reached"), v[template.EventHypeTrainOutcome])
	v = reqs[3].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(30)), v[template.EventAdBreakDuration])
	assert.Equal(t, template.TextValue("break"), v[template.EventAdBreakMessage])
	v = reqs[4].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(7)), v[template.EventShoutoutViewers])
	v = reqs[5].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(100)), v[template.EventGoalCurrentAmount])
	assert.Equal(t, template.NumberValue(decimal.New(100)), v[template.EventGoalTargetAmount])
	assert.Equal(t, template.TextValue("USD"), v[template.EventGoalCurrency])
	v = reqs[6].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(50)), v[template.EventDonationCurrent])
	assert.Equal(t, template.NumberValue(decimal.New(200)), v[template.EventDonationTarget])
	assert.Equal(t, template.TextValue("EUR"), v[template.EventDonationCurrency])
	v = reqs[7].Params.Values
	assert.Equal(t, template.NumberValue(decimal.New(100)), v[template.EventChannelPoints])
	assert.Equal(t, template.TextValue("r1"), v[template.EventChannelPointsID])
	v = reqs[8].Params.Values
	assert.Equal(t, template.TextValue("p1"), v[template.EventCustomPowerUp])
	assert.Equal(t, template.TextValue("a hug"), v[template.EventMessage])
}

// command-engine.md: a short break keeps the session, a long one ends it
// after the grace period, and greetings stop when the stream goes offline.
func TestStreamSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, on(eventtype.ChannelStreamStart), on(eventtype.ChannelStreamStop))
		stop := run(t, f.service)
		defer stop()
		ctx := t.Context()

		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		assert.Equal(t, []string{"channel.stream.start"}, f.engine.runs())
		started := f.store.mockSession().StartedAt

		require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
		assert.Equal(t, 1, f.engine.cancels(), "greetings are canceled at once")
		time.Sleep(2 * time.Minute)
		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		time.Sleep(time.Hour)
		assert.Empty(t, f.engine.runs(), "B24: neither stop nor start")
		assert.Equal(t, started, f.store.mockSession().StartedAt, "the same session")

		require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
		time.Sleep(10*time.Minute - time.Second)
		assert.Empty(t, f.engine.runs())
		time.Sleep(time.Second)
		synctest.Wait()
		assert.Equal(t, []string{"channel.stream.stop"}, f.engine.runs(), "B25: after the grace period")
		assert.Equal(t, stream.StateOffline, f.store.mockSession().State)
		require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
		assert.Empty(t, f.engine.runs(), "offline stays offline")

		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		assert.Equal(t, []string{"channel.stream.start"}, f.engine.runs(), "a new session")

		f.settings.set(grace(0))
		require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
		assert.Equal(t, []string{"channel.stream.stop"}, f.engine.runs(), "without grace period at once")
	})
}

// TestGreetingsWithAnotherLiveStream covers the canceling of greetings:
// only when no stream is live any more.
func TestGreetingsWithAnotherLiveStream(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	require.NoError(t, f.service.Stream(ctx, platform.Twitch, true))
	require.NoError(t, f.service.Stream(ctx, platform.Twitch, false))
	assert.Zero(t, f.engine.cancels(), "the mock stream is still live")
	require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
	assert.Equal(t, 1, f.engine.cancels())
}

// TestRestart covers B21 and B27: a restart during the stream keeps the
// session; a grace period that ended while the core did not run ends the
// session without a stream stop.
func TestRestart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, on(eventtype.ChannelStreamStart), on(eventtype.ChannelStreamStop), on(eventtype.ChannelFollow))
		ctx := t.Context()
		ada := account(platform.Mock, "ada")
		followed := connector.Event{Platform: platform.Mock, Type: eventtype.ChannelFollow, User: &ada}

		stop := run(t, f.service)
		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		require.NoError(t, f.service.Event(ctx, followed))
		time.Sleep(time.Hour)
		stop()
		assert.Equal(t, time.Now(), f.store.mockSession().SeenLive, "remembered at the stop")
		f.engine.runs()

		time.Sleep(time.Minute)
		svc := f.restart(t)
		stop = run(t, svc)
		require.NoError(t, svc.Stream(ctx, platform.Mock, true))
		require.NoError(t, svc.Event(ctx, followed))
		assert.Empty(t, f.engine.runs(), "B21: no second start, the follow fired before")

		stop()
		time.Sleep(11 * time.Minute)
		svc = f.restart(t)
		stop = run(t, svc)
		require.NoError(t, svc.Stream(ctx, platform.Mock, false))
		synctest.Wait()
		assert.Empty(t, f.engine.runs(), "B27: offline for longer than the grace period, no late stop")
		assert.Equal(t, stream.StateOffline, f.store.mockSession().State)
		require.NoError(t, svc.Stream(ctx, platform.Mock, true))
		assert.Equal(t, []string{"channel.stream.start"}, f.engine.runs(), "the next start begins a new session")
		stop()
	})
}

// TestRestartDuringGrace covers B8 and B27 across a restart: a grace period
// that has time left runs on; one that ended meanwhile ends the session at
// the start without a stream stop.
func TestRestartDuringGrace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, on(eventtype.ChannelStreamStop))
		ctx := t.Context()
		require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
		require.NoError(t, f.service.Stream(ctx, platform.Mock, false))
		stop := run(t, f.service)
		stop()

		time.Sleep(4 * time.Minute)
		svc := f.restart(t)
		stop = run(t, svc)
		time.Sleep(6 * time.Minute)
		synctest.Wait()
		assert.Equal(t, []string{"channel.stream.stop"}, f.engine.runs(), "the rest of the grace period")
		stop()

		require.NoError(t, f.store.PutStreamSession(ctx, stream.Session{
			Platform: platform.Mock, StartedAt: time.Now(), State: stream.StateGrace, Since: time.Now(), SeenLive: time.Now(),
		}))
		time.Sleep(time.Hour)
		f.restart(t)
		assert.Empty(t, f.engine.runs(), "B27")
		assert.Equal(t, stream.StateOffline, f.store.mockSession().State)
	})
}

func TestApplication(t *testing.T) {
	t.Parallel()
	f := newFixture(t, on(eventtype.AppStarted))
	require.NoError(t, f.service.Application(t.Context(), eventtype.AppStarted, "payload"))
	assert.Equal(t, []event.Type{eventtype.AppStarted}, f.pub.types())
	reqs := f.engine.take()
	require.Len(t, reqs, 1)
	assert.Equal(t, engine.SourceEvent, reqs[0].Source)
	assert.Equal(t, eventtype.AppStarted, reqs[0].Event)
	require.ErrorIs(t, f.service.Application(t.Context(), eventtype.ChannelFollow, "x"), eventservice.ErrNotReceived)
}

func TestStopped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	stop := run(t, f.service)
	stop()
	ctx := t.Context()
	ada := account(platform.Mock, "ada")
	require.ErrorIs(t, f.service.Stream(ctx, platform.Mock, true), eventservice.ErrStopped)
	require.ErrorIs(t, f.service.Message(ctx, message(ada, "m1", "hi")), eventservice.ErrStopped)
	require.ErrorIs(t, f.service.Join(ctx, platform.Mock, ada), eventservice.ErrStopped)
	require.ErrorIs(t, f.service.Event(ctx, connector.Event{Platform: platform.Mock, Type: eventtype.ChannelFollow, User: &ada}), eventservice.ErrStopped)
	require.ErrorIs(t, f.service.Run(ctx), eventservice.ErrAlreadyRunning)
}

func TestNewChecksPorts(t *testing.T) {
	t.Parallel()
	_, err := eventservice.New(t.Context(), eventservice.Ports{})
	require.ErrorIs(t, err, eventservice.ErrInvalidOption)
	f := newFixture(t)
	_, err = eventservice.New(t.Context(), eventservice.Ports{
		Store: f.store, Commands: f.cmds, Engine: f.engine, Publisher: f.pub, Settings: f.settings.get,
	}, eventservice.WithLogger(nil))
	require.ErrorIs(t, err, eventservice.ErrInvalidOption)
}

func TestInvalidSettings(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx := t.Context()
	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	f.settings.set(grace(polydoc.Duration(-time.Second)))
	require.Error(t, f.service.Stream(ctx, platform.Mock, false), "the grace period cannot start")
	assert.Equal(t, stream.StateLive, f.store.mockSession().State, "nothing changed")
}

// TestAmbiguousAndMissing covers B16 of commands.md and a deleted entrance
// command: an ambiguous message runs no chat command, a missing entrance
// command is skipped.
func TestAmbiguousAndMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, chat("upper", "Hallo"), chat("lower", "hallo"))
	ctx := t.Context()
	ada := account(platform.Mock, "ada")
	f.store.setEntrance(t, ada, id.New())
	require.NoError(t, f.service.Stream(ctx, platform.Mock, true))
	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "!HALLO")))
	assert.Empty(t, f.engine.runs())
	require.NoError(t, f.service.Message(ctx, message(ada, "m2", "!hallo")))
	assert.Equal(t, []string{"lower"}, f.engine.runs())
}
