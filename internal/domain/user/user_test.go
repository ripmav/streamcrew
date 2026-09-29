// SPDX-License-Identifier: Apache-2.0

package user_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// TestRolesPerPlatform covers B21, B24 and B26: the roles of the identity on
// the platform the user acts on, plus regular and user.
func TestRolesPerPlatform(t *testing.T) {
	t.Parallel()
	u := user.User{
		Regular: true,
		Identities: []user.Identity{
			{Platform: platform.Twitch, PlatformUserID: "1", Login: "ada", Roles: role.NewSet(role.Subscriber, role.Follower)},
			{Platform: platform.YouTube, PlatformUserID: "UC1", Login: "ada", Roles: role.NewSet(role.Moderator)},
		},
	}
	assert.Equal(t, "user,follower,regular,subscriber", u.Roles(platform.Twitch).String())
	assert.Equal(t, role.Subscriber, u.Roles(platform.Twitch).Primary())
	assert.Equal(t, "user,regular,moderator", u.Roles(platform.YouTube).String())
	assert.Equal(t, "user,regular", u.Roles(platform.Kick).String())

	assert.Equal(t, role.User, user.User{}.Roles(platform.Twitch).Primary(), "B21")

	yt, ok := u.Identity(platform.YouTube)
	require.True(t, ok)
	assert.Equal(t, "UC1", yt.PlatformUserID)
	_, ok = u.Identity(platform.Kick)
	assert.False(t, ok)
}

func TestIdentityValidate(t *testing.T) {
	t.Parallel()
	valid := user.Identity{Platform: platform.Twitch, PlatformUserID: "42", Login: "ada", DisplayName: "Ada"}
	require.NoError(t, valid.Validate())

	tests := map[string]func(*user.Identity){
		"invalid platform":    func(i *user.Identity) { i.Platform = "Twitch" },
		"empty platform ID":   func(i *user.Identity) { i.PlatformUserID = "" },
		"empty login":         func(i *user.Identity) { i.Login = "" },
		"control character":   func(i *user.Identity) { i.DisplayName = "Ada\n" },
		"regular on platform": func(i *user.Identity) { i.Roles = role.NewSet(role.Regular) },
		"negative tier":       func(i *user.Identity) { i.Data.SubTier = -1 },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			i := valid
			change(&i)
			require.ErrorIs(t, i.Validate(), user.ErrInvalid)
		})
	}
}
