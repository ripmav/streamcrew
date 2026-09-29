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
	require.NoError(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.VIP, role.Subscriber)))
	require.NoError(t, s.SetRoles(ctx, platform.YouTube, "UC7", role.NewSet(role.Moderator)))
	u, err = s.UpdateUser(ctx, u.ID, func(u *user.User) error {
		u.Regular = true
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "user,regular,vip,subscriber", u.Roles(platform.Twitch).String())
	assert.Equal(t, "user,regular,moderator", u.Roles(platform.YouTube).String())

	// B42: the platform no longer reports VIP; regular stays.
	require.NoError(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.Subscriber)))
	u, err = s.User(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "user,regular,subscriber", u.Roles(platform.Twitch).String())

	require.ErrorIs(t, s.SetRoles(ctx, platform.Twitch, "1001", role.NewSet(role.Regular)), user.ErrInvalid)
	require.ErrorIs(t, s.SetRoles(ctx, platform.Twitch, "404", role.NewSet(role.VIP)), store.ErrNotFound)
}

// TestUserDataAndStats covers B5 to B10: title, notes, exclusion, entrance
// command, statistics and platform data are stored and read.
func TestUserDataAndStats(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	codec, err := command.NewCodec()
	require.NoError(t, err)
	entrance, err := command.NewService(s, codec).Save(ctx, command.Command{Name: "welcome", Kind: command.KindActionGroup})
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
