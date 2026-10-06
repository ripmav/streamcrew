// SPDX-License-Identifier: Apache-2.0

package mock_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/polydoc"
)

// The mock platform implements the port, its optional capabilities and
// those of its chat.
var (
	_ connector.Platform   = (*mock.Platform)(nil)
	_ connector.Identities = (*mock.Platform)(nil)
	_ connector.Chatters   = (*mock.Platform)(nil)
	_ connector.Replier    = (*mock.Platform)(nil).Chat().(connector.Replier)
	_ connector.Whisperer  = (*mock.Platform)(nil).Chat().(connector.Whisperer)
)

// receiver records what the platform hands over and fails with err.
type receiver struct {
	mu       sync.Mutex
	messages []connector.Incoming
	joins    []user.Identity
	events   []connector.Event
	streams  []bool
	err      error
}

func (r *receiver) Message(_ context.Context, m connector.Incoming) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, m)
	return r.err
}

func (r *receiver) Join(_ context.Context, p platform.Name, who user.Identity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p != platform.Mock {
		return errors.New("wrong platform")
	}
	r.joins = append(r.joins, who)
	return r.err
}

func (r *receiver) Event(_ context.Context, e connector.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return r.err
}

func (r *receiver) Stream(_ context.Context, p platform.Name, live bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p != platform.Mock {
		return errors.New("wrong platform")
	}
	r.streams = append(r.streams, live)
	return r.err
}

// publisher records the outputs.
type publisher struct {
	mu      sync.Mutex
	outputs []mock.Output
}

func (p *publisher) Publish(_ context.Context, e event.Envelope) error {
	o, ok := event.Payload[mock.Output](e)
	if !ok || e.Type != mock.TypeOutput || e.Source.Name != string(platform.Mock) {
		return errors.New("unexpected event")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outputs = append(p.outputs, o)
	return nil
}

func (p *publisher) all() []mock.Output {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]mock.Output(nil), p.outputs...)
}

// start runs a platform with the options until the test ends; it must run
// in a synctest bubble.
func start(t *testing.T, opts ...mock.Option) (*mock.Platform, *receiver, *publisher) {
	t.Helper()
	r, pub := &receiver{}, &publisher{}
	p, err := mock.New(r, append([]mock.Option{mock.WithPublisher(pub)}, opts...)...)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})
	synctest.Wait()
	require.True(t, p.Status().Streamer)
	return p, r, pub
}

func TestNewOptions(t *testing.T) {
	t.Parallel()
	r := &receiver{}
	_, err := mock.New(nil)
	require.ErrorIs(t, err, mock.ErrInvalidOption)
	for name, opt := range map[string]mock.Option{
		"streamer":  mock.WithStreamer("no spaces"),
		"bot":       mock.WithBot(""),
		"logger":    mock.WithLogger(nil),
		"publisher": mock.WithPublisher(nil),
	} {
		_, err := mock.New(r, opt)
		assert.ErrorIs(t, err, mock.ErrInvalidOption, name)
	}
	_, err = mock.New(r, mock.WithStreamer("Chan"), mock.WithBot("chan"))
	require.ErrorIs(t, err, mock.ErrInvalidOption, "the bot is the streamer")

	p, err := mock.New(r)
	require.NoError(t, err)
	assert.Equal(t, platform.Mock, p.Name())
	assert.Equal(t, mock.DefaultStreamer, p.Streamer().Login)
	assert.True(t, p.Streamer().Roles.Has(role.Streamer))
	_, ok := p.Bot()
	assert.False(t, ok)
	assert.Equal(t, connector.Status{}, p.Status(), "not connected before Run")
}

// TestRun covers B11 of events.md: after connecting, the platform tells
// whether the stream is live, and afterwards every change.
func TestRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := &receiver{}
		p, err := mock.New(r, mock.WithBot("Bot"))
		require.NoError(t, err)
		require.NoError(t, p.GoLive(t.Context(), "Title", "Game"), "before Run")
		assert.Empty(t, r.streams)

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- p.Run(ctx) }()
		synctest.Wait()
		assert.Equal(t, connector.Status{Streamer: true, Bot: true}, p.Status())
		assert.Equal(t, []bool{true}, r.streams, "the state after connecting")
		require.ErrorIs(t, p.Run(ctx), mock.ErrAlreadyRunning)

		require.NoError(t, p.GoLive(t.Context(), "Other", "Game 2"))
		info, err := p.Channel(t.Context())
		require.NoError(t, err)
		assert.True(t, info.Live)
		assert.Equal(t, "Other", info.Title)
		assert.Equal(t, "Game 2", info.Game)
		assert.Equal(t, time.Now().UTC(), info.StartedAt, "the start stays")

		require.NoError(t, p.GoOffline(t.Context()))
		require.NoError(t, p.GoOffline(t.Context()))
		require.NoError(t, p.GoLive(t.Context(), "", ""))
		assert.Equal(t, []bool{true, false, true}, r.streams, "only changes")

		cancel()
		require.NoError(t, <-done)
		assert.Equal(t, connector.Status{}, p.Status())
	})
}

func TestSay(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, r, _ := start(t, mock.WithBot("bot"))
		alice, err := p.AddUser(mock.UserSpec{Login: "Alice", DisplayName: "Ali", Roles: role.NewSet(role.Moderator)})
		require.NoError(t, err)
		assert.Equal(t, user.Identity{
			Platform: platform.Mock, PlatformUserID: "alice", Login: "Alice", DisplayName: "Ali",
			Roles: role.NewSet(role.Moderator),
		}, alice)

		sent, err := p.Say(t.Context(), "alice", "hi")
		require.NoError(t, err)
		assert.Equal(t, mock.Delivered, sent.Delivery)
		_, err = p.Say(t.Context(), "Bob", "!hug alice")
		require.NoError(t, err)
		_, err = p.Say(t.Context(), "bot", "!hug")
		require.NoError(t, err)

		require.Len(t, r.messages, 3)
		assert.Equal(t, connector.Incoming{
			Platform: platform.Mock, Author: alice,
			Message: eventtype.Message{ID: sent.ID, Text: "hi"},
		}, r.messages[0])
		bob := r.messages[1].Author
		assert.Equal(t, "Bob", bob.Login, "a new user")
		assert.Equal(t, "Bob", bob.DisplayName)
		assert.Equal(t, role.NewSet(), bob.Roles)
		assert.False(t, r.messages[1].FromBot)
		assert.True(t, r.messages[2].FromBot, "B13")
		assert.NotEqual(t, r.messages[0].Message.ID, r.messages[1].Message.ID)

		_, err = p.Say(t.Context(), "alice", "")
		require.Error(t, err)
		_, err = p.Say(t.Context(), "not valid", "hi")
		require.ErrorIs(t, err, mock.ErrInvalidUser)
	})
}

// TestRepeat covers B22 of events.md: a message or event with an ID the
// platform got before is dropped.
func TestRepeat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, r, _ := start(t)
		sent, err := p.SayWithID(t.Context(), "alice", "hi", "m1")
		require.NoError(t, err)
		assert.Equal(t, mock.Sent{ID: "m1", Delivery: mock.Delivered}, sent)
		sent, err = p.SayWithID(t.Context(), "alice", "hi again", "m1")
		require.NoError(t, err)
		assert.Equal(t, mock.Sent{ID: "m1", Delivery: mock.Duplicate}, sent)
		assert.Len(t, r.messages, 1)

		follow := mock.Event{Type: eventtype.ChannelFollow, User: "alice"}
		_, err = p.SimulateWithID(t.Context(), follow, "e1")
		require.NoError(t, err)
		sent, err = p.SimulateWithID(t.Context(), follow, "e1")
		require.NoError(t, err)
		assert.Equal(t, mock.Duplicate, sent.Delivery)
		assert.Len(t, r.events, 1)

		time.Sleep(connector.DefaultDedupTTL)
		sent, err = p.SayWithID(t.Context(), "alice", "hi", "m1")
		require.NoError(t, err)
		assert.Equal(t, mock.Delivered, sent.Delivery, "after the time to live")
	})
}

func TestNotConnected(t *testing.T) {
	t.Parallel()
	p, err := mock.New(&receiver{})
	require.NoError(t, err)
	ctx := t.Context()
	_, err = p.Say(ctx, "alice", "hi")
	require.ErrorIs(t, err, connector.ErrNotConnected)
	require.ErrorIs(t, p.Join(ctx, "alice"), connector.ErrNotConnected)
	_, err = p.Simulate(ctx, mock.Event{Type: eventtype.ChannelFollow, User: "alice"})
	require.ErrorIs(t, err, connector.ErrNotConnected)
	err = p.Chat().Send(ctx, connector.Message{Text: "hi", From: connector.AccountStreamer})
	require.ErrorIs(t, err, connector.ErrNotConnected)
	require.ErrorIs(t, p.Moderation().ClearChat(ctx), connector.ErrNotConnected)
}

func TestJoin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, r, _ := start(t)
		require.NoError(t, p.Join(t.Context(), "carol"))
		require.Len(t, r.joins, 1)
		assert.Equal(t, "carol", r.joins[0].Login)
	})
}

func TestSimulate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, r, _ := start(t)
		gift := eventtype.Details{
			Subscription: &eventtype.Subscription{Plan: "1000"},
			Gift:         &eventtype.Gift{Count: 2},
		}
		_, err := p.Simulate(t.Context(), mock.Event{
			Type: eventtype.ChannelSubscriptionMassGift, User: "alice",
			Recipients: []string{"bob", "carol"}, Details: gift,
		})
		require.NoError(t, err)
		_, err = p.Simulate(t.Context(), mock.Event{
			Type: eventtype.ChannelRaid, User: "dave", Details: eventtype.Details{Raid: &eventtype.Raid{Viewers: 12}},
		})
		require.NoError(t, err)

		require.Len(t, r.events, 2)
		mass := r.events[0]
		assert.Equal(t, platform.Mock, mass.Platform)
		assert.Equal(t, eventtype.ChannelSubscriptionMassGift, mass.Type)
		require.NotNil(t, mass.User)
		assert.Equal(t, "alice", mass.User.Login)
		assert.Nil(t, mass.Target)
		require.Len(t, mass.Recipients, 2)
		assert.Equal(t, "carol", mass.Recipients[1].Login)
		assert.Equal(t, gift, mass.Details)
		assert.Equal(t, int64(12), r.events[1].Details.Raid.Viewers)
	})
}

func TestSimulateRejects(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, r, _ := start(t)
		for _, typ := range []event.Type{
			eventtype.AppStarted, eventtype.ChatMessage, eventtype.ChatUserJoin,
			eventtype.ChannelStreamStart, eventtype.ChannelStreamStop,
			eventtype.ChatUserNew, eventtype.ChatUserEntrance, eventtype.ChatUserFirstMessage,
			eventtype.TwitchChannelFollow,
		} {
			_, err := p.Simulate(t.Context(), mock.Event{Type: typ, User: "alice"})
			assert.ErrorIs(t, err, mock.ErrNotSimulated, typ)
		}
		_, err := p.Simulate(t.Context(), mock.Event{Type: "channel.nothing"})
		require.ErrorIs(t, err, event.ErrUnknownType)
		assert.Empty(t, r.events)

		r.err = errors.New("does not fit")
		_, err = p.Simulate(t.Context(), mock.Event{Type: eventtype.ChannelFollow})
		require.ErrorIs(t, err, r.err, "the receiver checks the data")
	})
}

func TestChat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, _, pub := start(t)
		ctx := t.Context()
		chat := p.Chat()
		require.NoError(t, chat.Send(ctx, connector.Message{Text: "hi", From: connector.AccountStreamer}))
		err := chat.Send(ctx, connector.Message{Text: "hi", From: connector.AccountBot})
		require.ErrorIs(t, err, connector.ErrNotConnected, "no bot")
		require.Error(t, chat.Send(ctx, connector.Message{From: connector.AccountStreamer}), "empty")
		require.NoError(t, chat.(connector.Replier).Reply(ctx, "m1", connector.Message{Text: "re", From: connector.AccountStreamer}))
		alice, err := p.AddUser(mock.UserSpec{Login: "alice"})
		require.NoError(t, err)
		require.NoError(t, chat.(connector.Whisperer).Whisper(ctx, alice, connector.Message{Text: "psst", From: connector.AccountStreamer}))
		require.NoError(t, chat.Delete(ctx, "m1"))

		assert.Equal(t, []mock.Output{
			{Op: mock.OpSend, From: connector.AccountStreamer, Text: "hi"},
			{Op: mock.OpReply, From: connector.AccountStreamer, Text: "re", MessageID: "m1"},
			{Op: mock.OpWhisper, From: connector.AccountStreamer, Text: "psst", Target: "alice"},
			{Op: mock.OpDelete, MessageID: "m1"},
		}, pub.all())
	})
}

func TestModeration(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, _, pub := start(t)
		ctx := t.Context()
		mod := p.Moderation()
		alice, err := p.AddUser(mock.UserSpec{Login: "alice"})
		require.NoError(t, err)

		require.NoError(t, mod.Timeout(ctx, alice, time.Minute, "spam"))
		require.NoError(t, mod.Mod(ctx, alice))
		got, _ := p.User("ALICE")
		assert.True(t, got.Roles.Has(role.Moderator))
		require.NoError(t, mod.Unmod(ctx, alice))
		got, _ = p.User("alice")
		assert.False(t, got.Roles.Has(role.Moderator))
		require.NoError(t, mod.Ban(ctx, alice, ""))
		require.NoError(t, mod.Unban(ctx, alice))
		require.NoError(t, mod.Purge(ctx, alice))
		require.NoError(t, mod.ClearChat(ctx))
		require.ErrorIs(t, mod.Timeout(ctx, p.Streamer(), time.Minute, ""), connector.ErrRefused)
		require.Error(t, mod.Timeout(ctx, alice, 0, ""))

		assert.Equal(t, []mock.Output{
			{Op: mock.OpTimeout, Target: "alice", Duration: polydoc.Duration(time.Minute), Reason: "spam"},
			{Op: mock.OpMod, Target: "alice"},
			{Op: mock.OpUnmod, Target: "alice"},
			{Op: mock.OpBan, Target: "alice"},
			{Op: mock.OpUnban, Target: "alice"},
			{Op: mock.OpPurge, Target: "alice"},
			{Op: mock.OpClearChat},
		}, pub.all())
	})
}

func TestUsers(t *testing.T) {
	t.Parallel()
	p, err := mock.New(&receiver{}, mock.WithStreamer("Chan"))
	require.NoError(t, err)
	alice, err := p.AddUser(mock.UserSpec{Login: "Alice"})
	require.NoError(t, err)
	ctx := t.Context()

	got, err := p.Users().UserByLogin(ctx, "aLiCe")
	require.NoError(t, err)
	assert.Equal(t, alice, got)
	got, err = p.Users().UserByID(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, alice, got)
	_, err = p.Users().UserByLogin(ctx, "bob")
	require.ErrorIs(t, err, connector.ErrUnknownUser)
	_, err = p.Users().UserByID(ctx, "Alice")
	require.ErrorIs(t, err, connector.ErrUnknownUser, "IDs are lowercase")

	streamer, err := p.AddUser(mock.UserSpec{Login: "chan", DisplayName: "Chan!"})
	require.NoError(t, err)
	assert.True(t, streamer.Roles.Has(role.Streamer), "the streamer keeps the role")
	assert.Equal(t, streamer, p.Streamer())
}

func TestRegisterEvents(t *testing.T) {
	t.Parallel()
	c := event.NewCatalog()
	require.NoError(t, mock.RegisterEvents(c))
	e := event.New(event.Source{Kind: event.SourcePlatform, Name: "mock"}, mock.TypeOutput, mock.Output{Op: mock.OpSend})
	require.NoError(t, c.Check(e))
}

// TestAccountsAndChatters covers the optional capabilities: the accounts
// of the channel and the users in the chat.
func TestAccountsAndChatters(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p, _, _ := start(t, mock.WithBot("Bot"))
		streamer, ok := p.Identity(connector.AccountStreamer)
		require.True(t, ok)
		assert.Equal(t, mock.DefaultStreamer, streamer.Login)
		bot, ok := p.Identity(connector.AccountBot)
		require.True(t, ok)
		assert.Equal(t, "Bot", bot.Login)

		ctx := t.Context()
		_, err := p.Say(ctx, "carol", "hi")
		require.NoError(t, err)
		require.NoError(t, p.Join(ctx, "ada"))
		chatters, err := p.Chatters(ctx)
		require.NoError(t, err)
		require.Len(t, chatters, 2)
		assert.Equal(t, "ada", chatters[0].Login)
		assert.Equal(t, "carol", chatters[1].Login)

		_, err = p.Simulate(ctx, mock.Event{Type: eventtype.ChatUserLeave, User: "ada"})
		require.NoError(t, err)
		chatters, err = p.Chatters(ctx)
		require.NoError(t, err)
		require.Len(t, chatters, 1, "ada left")
	})
	q, err := mock.New(&receiver{})
	require.NoError(t, err)
	_, ok := q.Identity(connector.AccountBot)
	assert.False(t, ok, "no bot")
}
