// SPDX-License-Identifier: MIT

package eventservice_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
)

// TestChatHistory covers spec chat-service.md, B1 and B14: an accepted
// message lands in the history after the events and the trigger, and a
// message of the bot account is recorded without firing anything.
func TestChatHistory(t *testing.T) {
	t.Parallel()
	f := newFixture(t, chat("hug", "hug"))
	ctx := t.Context()
	ada := account(platform.Mock, "ada")
	u, _, err := f.store.UpsertIdentity(ctx, ada)
	require.NoError(t, err)

	// an ordinary message: the history is the last stage (B1)
	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "!hug bob")))
	entries := f.chat.taken()
	require.Len(t, entries, 1)
	assert.Equal(t, chatsvc.Entry{
		Platform:  platform.Mock,
		UserID:    u.ID,
		Name:      "ada",
		Text:      "!hug bob",
		MessageID: "m1",
	}, entries[0], "the time is filled in by the service")
	f.pub.types()
	f.engine.runs()

	// a message of the bot account: recorded, but it fires nothing (B14)
	bot := message(account(platform.Mock, "bot"), "m2", "!hug")
	bot.FromBot = true
	require.NoError(t, f.service.Message(ctx, bot))
	assert.Empty(t, f.pub.types())
	assert.Empty(t, f.engine.runs())
	entries = f.chat.taken()
	require.Len(t, entries, 1)
	assert.True(t, entries[0].FromBot)
	assert.Equal(t, "m2", entries[0].MessageID)
}

// TestMutedChat covers spec chat-service.md, B21 and B22: while the chat
// is muted the message is deleted, it fires nothing, runs no command and
// is not taken into the history; the state applies per message (B40).
func TestMutedChat(t *testing.T) {
	t.Parallel()
	f := newFixture(t, append(allChatEvents(), chat("hug", "hug"))...)
	ctx := t.Context()
	ada := account(platform.Mock, "ada")

	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "hi")))
	assert.Len(t, f.chat.taken(), 1)
	f.pub.types()
	f.engine.runs()

	f.chat.setMuted(true)
	require.NoError(t, f.service.Message(ctx, message(ada, "m2", "spammed")))
	assert.Empty(t, f.pub.types(), "no events")
	assert.Empty(t, f.engine.runs(), "no trigger")
	assert.Empty(t, f.chat.taken(), "not recorded")
	assert.Equal(t, []string{"mock//m2"}, f.chat.deletes(), "B21")

	// a new message is checked against the state at its time (B40)
	require.NoError(t, f.service.Message(ctx, message(ada, "m3", "more")))
	assert.Equal(t, []string{"mock//m3"}, f.chat.deletes())

	f.chat.setMuted(false)
	require.NoError(t, f.service.Message(ctx, message(ada, "m4", "back")))
	assert.Equal(t, []event.Type{eventtype.ChatMessage}, f.pub.types())
	assert.Len(t, f.chat.taken(), 1)
}

// TestMutedChatExceptions covers B31 and B22: messages of the streamer and
// of the bot account run through while the chat is muted, and a delete
// that the platform cannot do is a warning, not an error (B32).
func TestMutedChatExceptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t, allChatEvents()...)
	ctx := t.Context()
	f.chat.setMuted(true)

	// the streamer's message runs through (B31)
	streamer := account(platform.Mock, "streamer", role.Streamer)
	require.NoError(t, f.service.Message(ctx, message(streamer, "m1", "hello")))
	assert.Equal(t, []event.Type{
		eventtype.ChatUserNew, eventtype.ChatUserJoin, eventtype.ChatMessage, eventtype.ChatUserFirstMessage,
	}, f.pub.types())
	assert.Len(t, f.chat.taken(), 1)

	// a bot message is not deleted, but recorded (B22)
	bot := message(account(platform.Mock, "bot"), "m2", "beep")
	bot.FromBot = true
	require.NoError(t, f.service.Message(ctx, bot))
	assert.Empty(t, f.chat.deletes(), "nothing is deleted")
	assert.Len(t, f.chat.taken(), 1, "the bot's message is recorded")

	// a delete that fails is a warning; the message is discarded anyway
	// (B32)
	f.chat.setDeleteError(errors.New("the platform keeps it"))
	require.NoError(t, f.service.Message(ctx, message(account(platform.Mock, "eve"), "m3", "spam")))
	assert.Empty(t, f.pub.types())
	assert.Empty(t, f.chat.taken())
	assert.Equal(t, []string{"mock//m3"}, f.chat.deletes(), "the delete was asked for")
}

// TestWordFilterDiscards covers spec moderation.md, B1, B4 to B8 and B11:
// a message that violates a word filter is discarded at the moderation
// stage, like the muted chat, without events, trigger or history; a clean
// message runs through.
func TestWordFilterDiscards(t *testing.T) {
	t.Parallel()
	f := newFixture(t, allChatEvents()...)
	ctx := t.Context()
	ada := account(platform.Mock, "ada")

	require.NoError(t, f.service.Message(ctx, message(ada, "m1", "hello")))
	assert.Len(t, f.chat.taken(), 1)
	f.pub.types()
	f.engine.runs()

	f.mod.set(settings.Moderation{Filters: []settings.Filter{{Kind: settings.FilterWord, Word: "badword"}}})
	require.NoError(t, f.service.Message(ctx, message(ada, "m2", "hey badword")))
	assert.Empty(t, f.pub.types(), "no events")
	assert.Empty(t, f.engine.runs(), "no trigger")
	assert.Empty(t, f.chat.taken(), "not recorded")

	require.NoError(t, f.service.Message(ctx, message(ada, "m3", "hello")))
	assert.Equal(t, []event.Type{eventtype.ChatMessage}, f.pub.types())
	assert.Len(t, f.chat.taken(), 1)
}

// TestMutedChatNotJoins covers B25: the muted chat does not apply to
// joins without a message.
func TestMutedChatNotJoins(t *testing.T) {
	t.Parallel()
	f := newFixture(t, allChatEvents()...)
	ctx := t.Context()
	f.chat.setMuted(true)
	ada := account(platform.Mock, "ada")
	require.NoError(t, f.service.Join(ctx, platform.Mock, "", ada))
	assert.Equal(t, []string{"chat.user.new", "chat.user.join"}, f.engine.runs())
	assert.Empty(t, f.chat.deletes())
	assert.Empty(t, f.chat.taken())
}
