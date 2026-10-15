// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/store"
)

func twitchIdentity(userID, login string) user.Identity {
	return user.Identity{Platform: platform.Twitch, PlatformUserID: userID, Login: login, DisplayName: login}
}

// TestUpsertIdentity covers B1 and B4: a known account keeps its user when
// its names change.
func TestUpsertIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ident := twitchIdentity("1001", "ada")
	ident.Color = "#1E90FF"
	ident.Roles = role.NewSet(role.Follower)
	u, created, err := s.UpsertIdentity(ctx, ident)
	require.NoError(t, err)
	assert.True(t, created)
	assert.False(t, u.ID.IsZero())
	require.Len(t, u.Identities, 1)
	assert.Equal(t, ident, u.Identities[0])
	assert.Equal(t, user.Stats{}, u.Stats)

	ident.Login, ident.DisplayName, ident.Color = "ada_l", "Ada L", "#FF0000"
	ident.Roles = role.NewSet(role.Moderator) // ignored: roles come through SetRoles
	renamed, created, err := s.UpsertIdentity(ctx, ident)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, u.ID, renamed.ID, "B1: the ID stays")
	got := renamed.Identities[0]
	assert.Equal(t, "ada_l", got.Login)
	assert.Equal(t, "Ada L", got.DisplayName)
	assert.Equal(t, "#FF0000", got.Color)
	assert.Equal(t, role.NewSet(role.Follower), got.Roles)

	byIdent, err := s.UserByIdentity(ctx, platform.Twitch, "1001")
	require.NoError(t, err)
	assert.Equal(t, renamed, byIdent)

	_, _, err = s.UpsertIdentity(ctx, user.Identity{Platform: platform.Twitch, Login: "x"})
	require.ErrorIs(t, err, user.ErrInvalid)
	_, err = s.UserByIdentity(ctx, platform.Twitch, "404")
	require.ErrorIs(t, err, store.ErrNotFound)
}

// TestAccountBelongsToOneUser covers B3 and B40: the database rejects a
// second user with the same platform account; the same name on another
// platform is another user.
func TestAccountBelongsToOneUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	grace, _, err := s.UpsertIdentity(ctx, user.Identity{Platform: platform.YouTube, PlatformUserID: "UC7", Login: "ada", DisplayName: "Ada"})
	require.NoError(t, err)
	assert.NotEqual(t, ada.ID, grace.ID, "B40: same name, other platform, other user")

	require.ErrorIs(t, s.AddIdentity(ctx, grace.ID, twitchIdentity("1001", "ada")), store.ErrConflict)
	require.ErrorIs(t, s.AddIdentity(ctx, id.New(), twitchIdentity("2002", "x")), store.ErrNotFound)
}

// TestRolesAcrossIdentities covers B24 and B42.
func TestRolesAcrossIdentities(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	u, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	require.NoError(t, s.AddIdentity(ctx, u.ID, user.Identity{Platform: platform.YouTube, PlatformUserID: "UC7", Login: "ada", DisplayName: "Ada"}))
	require.NoError(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.TwitchVIP, role.Subscriber)))
	require.NoError(t, s.SetRoles(ctx, platform.YouTube, "UC7", role.NewSet(role.Moderator)))
	u, err = s.UpdateUser(ctx, u.ID, func(u *user.User) error {
		u.Regular = true
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "user,regular,twitch_vip,subscriber", u.Roles(platform.Twitch).String())
	assert.Equal(t, "user,regular,moderator", u.Roles(platform.YouTube).String())

	// B42: the platform no longer reports VIP; regular stays.
	require.NoError(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.Subscriber)))
	u, err = s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "user,regular,subscriber", u.Roles(platform.Twitch).String())

	require.ErrorIs(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.Regular)), user.ErrInvalid)
	require.ErrorIs(t, s.SetRoles(ctx, platform.Twitch, "404", role.NewSet(role.TwitchVIP)), store.ErrNotFound)
}

// TestUserDataAndStats covers B5 to B10: title, notes, exclusion, entrance
// command, statistics and platform data are stored and read.
func TestUserDataAndStats(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	codec, err := command.NewCodec()
	require.NoError(t, err)
	entrance, err := newCommandService(t, s, codec).Save(ctx, command.Command{Name: "welcome", Kind: command.KindActionGroup, ErrorPolicy: command.ErrorContinue})
	require.NoError(t, err)

	u, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	first := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	stats := user.Stats{
		WatchMinutes: 600, Messages: 42, CommandsRun: 7, Mentions: 3, StreamsWatched: 5,
		FirstSeen: first, LastSeen: first.Add(28 * 24 * time.Hour), DonatedCents: 2500, Strikes: 1,
	}
	updated, err := s.UpdateUser(ctx, u.ID, func(u *user.User) error {
		u.Title = "Night Owl"
		u.Notes = "Joined in the first stream."
		u.Excluded = true
		u.EntranceCommand = entrance.ID
		u.Stats = stats
		u.Identities = nil // ignored
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "Night Owl", updated.Title)
	assert.Equal(t, "Joined in the first stream.", updated.Notes)
	assert.True(t, updated.Excluded)
	assert.Equal(t, entrance.ID, updated.EntranceCommand)
	assert.Equal(t, stats, updated.Stats)
	assert.Len(t, updated.Identities, 1)

	data := user.PlatformData{
		FollowedAt: first, SubscribedAt: first.Add(time.Hour), SubTier: 2,
		AccountCreatedAt: time.Date(2015, 3, 4, 0, 0, 0, 0, time.UTC), UpdatedAt: first.Add(2 * time.Hour),
	}
	require.NoError(t, s.SetPlatformData(ctx, platform.Twitch, "1001", data))
	loaded, err := s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, data, loaded.Identities[0].Data)
	assert.Equal(t, updated.Stats, loaded.Stats)
	require.ErrorIs(t, s.SetPlatformData(ctx, platform.Twitch, "1001", user.PlatformData{SubTier: -1}), user.ErrInvalid)
	require.ErrorIs(t, s.SetPlatformData(ctx, platform.Twitch, "404", data), store.ErrNotFound)

	// Deleting the entrance command clears the reference.
	require.NoError(t, s.DeleteCommand(ctx, entrance.ID))
	loaded, err = s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, loaded.EntranceCommand.IsZero())

	_, err = s.UpdateUser(ctx, u.ID, func(u *user.User) error {
		u.EntranceCommand = id.New()
		return nil
	})
	require.ErrorIs(t, err, store.ErrConflict, "the command must exist")
}

func TestUpdateUserRollsBack(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	u, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)

	_, err = s.UpdateUser(ctx, u.ID, func(u *user.User) error {
		u.Title = "changed"
		return assert.AnError
	})
	require.ErrorIs(t, err, assert.AnError)
	loaded, err := s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, loaded.Title)

	_, err = s.UpdateUser(ctx, id.New(), func(*user.User) error { return nil })
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	u, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)

	require.NoError(t, s.DeleteUser(ctx, u.ID))
	_, err = s.User(ctx, u.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = s.UserByIdentity(ctx, platform.Twitch, "1001")
	require.ErrorIs(t, err, store.ErrNotFound, "the identity went with the user")
	require.ErrorIs(t, s.DeleteUser(ctx, u.ID), store.ErrNotFound)

	again, created, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, u.ID, again.ID)
}

// TestUserByLogin finds users by login name regardless of case, the
// account changed last among several with the name.
func TestUserByLogin(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "Ada"))
	require.NoError(t, err)

	got, err := s.UserByLogin(ctx, platform.Twitch, "ada")
	require.NoError(t, err)
	assert.Equal(t, ada.ID, got.ID)
	_, err = s.UserByLogin(ctx, platform.YouTube, "ada")
	require.ErrorIs(t, err, store.ErrNotFound, "another platform")
	_, err = s.UserByLogin(ctx, platform.Twitch, "bob")
	require.ErrorIs(t, err, store.ErrNotFound)

	time.Sleep(2 * time.Millisecond)
	renamed, _, err := s.UpsertIdentity(ctx, twitchIdentity("1002", "ADA"))
	require.NoError(t, err)
	got, err = s.UserByLogin(ctx, platform.Twitch, "Ada")
	require.NoError(t, err)
	assert.Equal(t, renamed.ID, got.ID, "the account changed last")
}

func TestResetStrikes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	_, err = s.UpdateUser(ctx, ada.ID, func(u *user.User) error {
		u.Stats.Strikes = 3
		return nil
	})
	require.NoError(t, err)
	require.NoError(t, s.ResetStrikes(ctx))
	got, err := s.User(ctx, ada.ID)
	require.NoError(t, err)
	assert.Zero(t, got.Stats.Strikes)
}

// TestAddStats covers the batch update of the user statistics
// (users-and-roles.md, B9): counters add up, FirstSeen is set only once,
// LastSeen follows the latest contact, and an empty batch changes nothing.
func TestAddStats(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ident := twitchIdentity("1001", "ada")
	u, _, err := s.UpsertIdentity(ctx, ident)
	require.NoError(t, err)

	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	require.NoError(t, s.AddStats(ctx, nil), "an empty batch is a no-op")

	require.NoError(t, s.AddStats(ctx, []user.StatsDelta{
		{UserID: u.ID, Messages: 3, StreamsWatched: 1, Seen: t0},
		{UserID: u.ID, Mentions: 2, WatchMinutes: 5},
	}))
	loaded, err := s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(3), loaded.Stats.Messages)
	assert.Equal(t, int64(2), loaded.Stats.Mentions)
	assert.Equal(t, int64(1), loaded.Stats.StreamsWatched)
	assert.Equal(t, int64(5), loaded.Stats.WatchMinutes)
	assert.Equal(t, t0, loaded.Stats.FirstSeen)
	assert.Equal(t, t0, loaded.Stats.LastSeen)

	// An older Seen does not move LastSeen; FirstSeen stays.
	t1 := t0.Add(10 * time.Minute)
	tOld := t0.Add(time.Minute)
	require.NoError(t, s.AddStats(ctx, []user.StatsDelta{
		{UserID: u.ID, CommandsRun: 1, Seen: tOld},
		{UserID: u.ID, Seen: t1},
	}))
	loaded, err = s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), loaded.Stats.CommandsRun)
	assert.Equal(t, t0, loaded.Stats.FirstSeen)
	assert.Equal(t, t1, loaded.Stats.LastSeen)
}

// TestListUsersRegular covers the sweep rows of the regular role (B26):
// every user with its watch minutes, in ID order.
func TestListUsersRegular(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	bob, _, err := s.UpsertIdentity(ctx, twitchIdentity("1002", "bob"))
	require.NoError(t, err)
	require.NoError(t, s.AddStats(ctx, []user.StatsDelta{
		{UserID: ada.ID, WatchMinutes: 60},
		{UserID: bob.ID, WatchMinutes: 125},
	}))
	_, err = s.UpdateUser(ctx, ada.ID, func(u *user.User) error {
		u.Regular = true
		return nil
	})
	require.NoError(t, err)

	rows, err := s.ListUsersRegular(ctx)
	require.NoError(t, err)
	assert.Equal(t, []user.RegularSweep{
		{ID: ada.ID, Regular: true, WatchMinutes: 60},
		{ID: bob.ID, Regular: false, WatchMinutes: 125},
	}, rows)
}

// TestTopUsers covers the top lists (roadmap 5.2.5): the order is by value
// down, ties by ID, excluded users (B7) and zero values never appear, and
// the list has at most n entries.
func TestTopUsers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	bob, _, err := s.UpsertIdentity(ctx, twitchIdentity("1002", "bob"))
	require.NoError(t, err)
	carol, _, err := s.UpsertIdentity(ctx, twitchIdentity("1003", "carol"))
	require.NoError(t, err)
	excl, _, err := s.UpsertIdentity(ctx, twitchIdentity("1004", "excl"))
	require.NoError(t, err)
	require.NoError(t, s.AddStats(ctx, []user.StatsDelta{
		{UserID: ada.ID, WatchMinutes: 125, Messages: 3},
		{UserID: bob.ID, WatchMinutes: 125, Messages: 30},
		{UserID: carol.ID, WatchMinutes: 60, Messages: 30}, // 0 watch tie below
		{UserID: excl.ID, WatchMinutes: 999, Messages: 30},
	}))
	// carol has zero messages below via a second user; make dan zero.
	dan, _, err := s.UpsertIdentity(ctx, twitchIdentity("1005", "dan"))
	require.NoError(t, err)
	require.NoError(t, s.AddStats(ctx, []user.StatsDelta{{UserID: dan.ID}}))
	// excl is exempt from rankings (B7).
	_, err = s.UpdateUser(ctx, excl.ID, func(u *user.User) error {
		u.Excluded = true
		return nil
	})
	require.NoError(t, err)

	// Top 3 by watch minutes: bob and ada (125, tie by ID), then carol (60);
	// excl (999, but B7) and dan (0) never appear.
	top, err := s.TopUsers(ctx, user.StatWatchMinutes, 3)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{ada.ID, bob.ID, carol.ID}, ids(top))
	assert.Equal(t, []int64{125, 125, 60}, vals(top))

	// Limit 2: the two 125s.
	top, err = s.TopUsers(ctx, user.StatWatchMinutes, 2)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{ada.ID, bob.ID}, ids(top))

	// Top 2 by messages: bob and carol (30, tie by ID); ada (3) third, dan 0.
	top, err = s.TopUsers(ctx, user.StatMessages, 2)
	require.NoError(t, err)
	assert.Equal(t, []id.ID{bob.ID, carol.ID}, ids(top))

	// A statistic nobody has: empty, not an error.
	top, err = s.TopUsers(ctx, user.StatCommandsRun, 5)
	require.NoError(t, err)
	assert.Empty(t, top)

	// Invalid arguments.
	_, err = s.TopUsers(ctx, user.Stat("donated_cents"), 1)
	require.ErrorIs(t, err, user.ErrInvalid)
	_, err = s.TopUsers(ctx, user.StatWatchMinutes, 0)
	require.ErrorIs(t, err, user.ErrInvalid)
}

func ids(top []user.Ranked) []id.ID {
	out := make([]id.ID, 0, len(top))
	for _, r := range top {
		out = append(out, r.User.ID)
	}
	return out
}

func vals(top []user.Ranked) []int64 {
	out := make([]int64, 0, len(top))
	for _, r := range top {
		out = append(out, r.Value)
	}
	return out
}
