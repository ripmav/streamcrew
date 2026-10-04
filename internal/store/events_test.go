// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
)

// TestStreamSessions covers B11 and B21 of events.md: the session of each
// platform is stored, and a new one forgets the events of the old one.
func TestStreamSessions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	sessions, err := s.StreamSessions(ctx)
	require.NoError(t, err)
	assert.Empty(t, sessions)

	initial := stream.Initial(platform.Mock)
	require.NoError(t, s.PutStreamSession(ctx, initial))
	start := time.Date(2026, 10, 4, 18, 0, 0, 123_456_789, time.UTC)
	live := stream.Session{Platform: platform.Twitch, StartedAt: start, State: stream.StateLive, Since: start, SeenLive: start}
	require.NoError(t, s.PutStreamSession(ctx, live))

	sessions, err = s.StreamSessions(ctx)
	require.NoError(t, err)
	ms := start.Truncate(time.Millisecond)
	assert.Equal(t, []stream.Session{
		initial,
		{Platform: platform.Twitch, StartedAt: ms, State: stream.StateLive, Since: ms, SeenLive: ms},
	}, sessions)

	require.Error(t, s.PutStreamSession(ctx, stream.Session{Platform: platform.Twitch, State: stream.StateLive}), "live without start")
	require.Error(t, s.PutStreamSession(ctx, stream.Session{Platform: platform.Twitch, State: "paused"}))

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	first, err := s.FirstInSession(ctx, platform.Twitch, eventtype.ChannelFollow, ada.ID)
	require.NoError(t, err)
	assert.True(t, first)
	first, err = s.FirstInSession(ctx, platform.Twitch, eventtype.ChannelFollow, ada.ID)
	require.NoError(t, err)
	assert.False(t, first, "B20")
	first, err = s.FirstInSession(ctx, platform.Mock, eventtype.ChannelFollow, ada.ID)
	require.NoError(t, err)
	assert.True(t, first, "per platform")

	later := start.Add(time.Hour)
	require.NoError(t, s.StartStreamSession(ctx, stream.Session{
		Platform: platform.Twitch, StartedAt: later, State: stream.StateLive, Since: later, SeenLive: later,
	}))
	first, err = s.FirstInSession(ctx, platform.Twitch, eventtype.ChannelFollow, ada.ID)
	require.NoError(t, err)
	assert.True(t, first, "a new session forgets")
	first, err = s.FirstInSession(ctx, platform.Mock, eventtype.ChannelFollow, ada.ID)
	require.NoError(t, err)
	assert.False(t, first, "the session of another platform stays")
}

func TestFirstForUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)

	first, err := s.FirstForUser(ctx, ada.ID, eventtype.ChatUserFirstMessage)
	require.NoError(t, err)
	assert.True(t, first)
	first, err = s.FirstForUser(ctx, ada.ID, eventtype.ChatUserFirstMessage)
	require.NoError(t, err)
	assert.False(t, first)
	first, err = s.FirstForUser(ctx, ada.ID, eventtype.ChatUserNew)
	require.NoError(t, err)
	assert.True(t, first)

	require.NoError(t, s.DeleteUser(ctx, ada.ID))
	_, err = s.FirstForUser(ctx, ada.ID, eventtype.ChatUserNew)
	require.Error(t, err, "the user must exist")
}
