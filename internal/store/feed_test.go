// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/feed"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/stream"
	"github.com/ripmav/streamcrew/internal/event"
)

// TestRecordFeedEvent covers roadmap 5.3: feed events are kept and read back
// oldest first, and, while the platform has a session, they count to that
// session's statistics.
func TestRecordFeedEvent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	bob, _, err := s.UpsertIdentity(ctx, twitchIdentity("1002", "bob"))
	require.NoError(t, err)
	p := platform.Twitch
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	// No session yet: the event is kept in the feed, but there are no
	// session statistics.
	_, ok, err := s.CurrentSessionStats(ctx, p)
	require.NoError(t, err)
	assert.False(t, ok, "no session yet")

	ev := func(at time.Time) feed.Event { return feed.Event{Platform: p, Time: at} }
	follow := ev(base)
	follow.Type, follow.UserID, follow.Login, follow.DisplayName = eventtype.ChannelFollow, ada.ID, "ada", "Ada"
	require.NoError(t, s.RecordFeedEvent(ctx, follow, time.Time{}))

	_, ok, err = s.CurrentSessionStats(ctx, p)
	require.NoError(t, err)
	assert.False(t, ok, "still no session")

	// Start the session; the following events count to it.
	start := base.Add(time.Minute)
	live := stream.Session{Platform: p, StartedAt: start, State: stream.StateLive, Since: start, SeenLive: start}
	require.NoError(t, s.PutStreamSession(ctx, live))

	subscribe := ev(start.Add(time.Minute))
	subscribe.Type, subscribe.UserID, subscribe.Login, subscribe.Tier = eventtype.ChannelSubscribe, ada.ID, "ada", 2
	require.NoError(t, s.RecordFeedEvent(ctx, subscribe, start))

	cheer := ev(start.Add(2 * time.Minute))
	cheer.Type, cheer.UserID, cheer.Login, cheer.Amount = eventtype.TwitchBitsCheer, ada.ID, "ada", 200
	require.NoError(t, s.RecordFeedEvent(ctx, cheer, start))

	raid := ev(start.Add(3 * time.Minute))
	raid.Type, raid.UserID, raid.Login, raid.Amount = eventtype.ChannelRaid, bob.ID, "bob", 42
	require.NoError(t, s.RecordFeedEvent(ctx, raid, start))

	events, err := s.LatestFeed(ctx, 10)
	require.NoError(t, err)
	require.Len(t, events, 4)
	assert.Equal(t, []event.Type{
		eventtype.ChannelFollow, eventtype.ChannelSubscribe, eventtype.TwitchBitsCheer, eventtype.ChannelRaid,
	}, []event.Type{events[0].Type, events[1].Type, events[2].Type, events[3].Type})
	assert.Equal(t, ada.ID, events[0].UserID)
	assert.Equal(t, 2, events[1].Tier)
	assert.Equal(t, int64(200), events[2].Amount)
	assert.Equal(t, bob.ID, events[3].UserID)
	assert.Equal(t, "bob", events[3].Login)

	// The follow was recorded before the session started, so it is in the
	// feed but not in the session statistics; the rest count.
	want := feed.SessionStats{
		Platform: p, StartedAt: start.Truncate(time.Millisecond),
		Followers: 0, Subscribers: 1, Cheers: 1, BitsTotal: 200, Raids: 1,
	}
	st, ok, err := s.CurrentSessionStats(ctx, p)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, want, st)

	// The same by the session's start.
	byStart, ok, err := s.SessionStats(ctx, p, start)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, want, byStart)

	// A session without feed events returns zero statistics.
	none, ok, err := s.SessionStats(ctx, p, start.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, ok)
	assert.True(t, none.StartedAt.IsZero())

	recent, err := s.RecentSessions(ctx, p, 10)
	require.NoError(t, err)
	assert.Equal(t, []feed.SessionStats{want}, recent)
}

// TestFeedPruning keeps only the latest events of the feed.
func TestFeedPruning(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	p := platform.Twitch
	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	for i := range 205 {
		ev := feed.Event{
			Platform: p, Type: eventtype.ChannelFollow, UserID: ada.ID, Login: "ada",
			Time: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, s.RecordFeedEvent(ctx, ev, time.Time{}))
	}
	events, err := s.LatestFeed(ctx, 200)
	require.NoError(t, err)
	require.Len(t, events, 200)
	// The first five (i = 0..4) are pruned; the oldest kept is i = 5.
	assert.Equal(t, base.Add(5*time.Second), events[0].Time)
	assert.Equal(t, base.Add(204*time.Second), events[199].Time)
}
