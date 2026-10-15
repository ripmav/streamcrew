// SPDX-License-Identifier: MIT

package userservice_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/template"
)

// known adds a user the core already knows on the mock platform to the
// fixture's store, and returns it.
func known(f *fixture, login string) user.User {
	ident := user.Identity{Platform: platform.Mock, PlatformUserID: login, Login: login, DisplayName: login}
	u, _, err := f.store.UpsertIdentity(context.Background(), ident)
	if err != nil {
		panic(err)
	}
	return u
}

// TestUserByName_B91 covers the lookup order of the user service
// (actions.md, B91): a known login name, then the channel's accounts, then
// the known platform ID, then the platform by login name and by ID; an
// account found over the platform is stored, and a miss is not an error.
func TestUserByName_B91(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	_, _, err := f.store.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "ada", Login: "ada", DisplayName: "Ada"})
	require.NoError(t, err)
	// carol is known by platform ID 42, with a different login.
	_, _, err = f.store.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "42", Login: "carol", DisplayName: "Carol"})
	require.NoError(t, err)

	streamer := user.Identity{Platform: platform.Mock, PlatformUserID: "s1", Login: "streamy", DisplayName: "streamy"}
	bot := user.Identity{Platform: platform.Mock, PlatformUserID: "b1", Login: "botbot", DisplayName: "botbot"}
	f.platforms.connect(streamer, bot)
	// zed is on the platform only; the service must find and store it.
	f.platforms.user(user.Identity{Platform: platform.Mock, PlatformUserID: "zed", Login: "zed", DisplayName: "zed"})

	// A known login name, regardless of case.
	u, ok, err := f.svc.UserByName(ctx, platform.Mock, "ADA")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "ada", u.Identities[0].Login)

	// The channel's accounts, found by the login name and stored.
	u, ok, err = f.svc.UserByName(ctx, platform.Mock, "STREAMY")
	require.NoError(t, err)
	require.True(t, ok)
	_, ok = u.Identity(platform.Mock)
	assert.True(t, ok)
	stored, ok, err := f.store.UserByPlatformID(ctx, platform.Mock, "s1")
	require.NoError(t, err)
	require.True(t, ok, "the streamer is stored when seen")
	assert.Equal(t, u.ID, stored.ID)

	// A known platform ID, with a different login.
	u, ok, err = f.svc.UserByName(ctx, platform.Mock, "42")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "carol", u.Identities[0].Login)

	// The platform, by login name; the account is stored.
	u, ok, err = f.svc.UserByName(ctx, platform.Mock, "zed")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "zed", u.Identities[0].Login)
	stored, ok, err = f.store.UserByPlatformID(ctx, platform.Mock, "zed")
	require.NoError(t, err)
	require.True(t, ok, "the platform account is stored when seen")
	assert.Equal(t, u.ID, stored.ID)

	// No such account anywhere: a miss, not an error.
	_, ok, err = f.svc.UserByName(ctx, platform.Mock, "nobody")
	assert.NoError(t, err)
	assert.False(t, ok)
}

// TestUserByName_B92 covers the limit of one lookup over a platform per
// minute, across all lookups (actions.md, B92): the first reaches the
// platform, the second within the interval does not, and neither is an
// error.
func TestUserByName_B92(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	f.platforms.connect(
		user.Identity{Platform: platform.Mock, PlatformUserID: "s1", Login: "streamy", DisplayName: "streamy"},
		user.Identity{Platform: platform.Mock, PlatformUserID: "b1", Login: "botbot", DisplayName: "botbot"},
	)
	f.platforms.user(user.Identity{Platform: platform.Mock, PlatformUserID: "zed", Login: "zed", DisplayName: "zed"})
	f.platforms.user(user.Identity{Platform: platform.Mock, PlatformUserID: "yara", Login: "yara", DisplayName: "yara"})

	// The first platform lookup reaches the platform.
	_, ok, err := f.svc.UserByName(ctx, platform.Mock, "zed")
	require.NoError(t, err)
	assert.True(t, ok)

	// The second within the interval does not, but is not an error.
	_, ok, err = f.svc.UserByName(ctx, platform.Mock, "yara")
	assert.NoError(t, err)
	assert.False(t, ok)
}

// TestUserByName_Placeholder covers that a platform without a connection
// reports no account (not an error), and a rejected platform lookup is an
// error (command-engine.md, B17 repeats it).
func TestUserByName_Placeholder(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// Not connected: a miss, not an error, and no platform call.
	f := newFixture(t)
	f.platforms.user(user.Identity{Platform: platform.Mock, PlatformUserID: "zed", Login: "zed", DisplayName: "zed"})
	_, ok, err := f.svc.UserByName(ctx, platform.Mock, "zed")
	assert.NoError(t, err)
	assert.False(t, ok)

	// A rejected platform lookup is an error.
	f2 := newFixture(t)
	f2.platforms.connect(
		user.Identity{Platform: platform.Mock, PlatformUserID: "s1", Login: "streamy", DisplayName: "streamy"},
		user.Identity{},
	)
	f2.platforms.setLookupError(errors.New("helix down"))
	_, ok, err = f2.svc.UserByName(ctx, platform.Mock, "zed")
	assert.Error(t, err)
	assert.False(t, ok)
}

// TestAccounts covers the streamer's and the bot's accounts, the chatters
// and the streamer user (template.md, B22, B60; requirements.md, B4),
// stored like newly seen ones.
func TestAccounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	streamer := user.Identity{Platform: platform.Mock, PlatformUserID: "s1", Login: "streamy", DisplayName: "streamy"}
	bot := user.Identity{Platform: platform.Mock, PlatformUserID: "b1", Login: "botbot", DisplayName: "botbot"}
	f.platforms.connect(streamer, bot)
	f.platforms.setChatters(user.Identity{Platform: platform.Mock, PlatformUserID: "ada", Login: "ada", DisplayName: "ada"})

	// Without the capability, the accounts are not found (not an error).
	f2 := newFixture(t)
	_, ok, err := f2.svc.Account(ctx, platform.Mock, template.StreamerAccount)
	assert.NoError(t, err)
	assert.False(t, ok)

	// With the platform: both accounts, stored when seen.
	u, ok, err := f.svc.Account(ctx, platform.Mock, template.StreamerAccount)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "streamy", u.Identities[0].Login)
	b, ok, err := f.svc.Account(ctx, platform.Mock, template.BotAccount)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "botbot", b.Identities[0].Login)

	// The streamer user, for the requirements.
	sid, err := f.svc.StreamerUser(ctx, platform.Mock)
	require.NoError(t, err)
	assert.Equal(t, u.ID, sid)

	// The chatters, stored like newly seen ones.
	chat, err := f.svc.Chatters(ctx, platform.Mock)
	require.NoError(t, err)
	require.Len(t, chat, 1)
	assert.Equal(t, "ada", chat[0].Identities[0].Login)
	found, ok, err := f.store.UserByPlatformID(ctx, platform.Mock, "ada")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, chat[0].ID, found.ID)
}

// TestStrikes covers changing and resetting the strikes through the service
// (actions.md, B84), which delegates to the store.
func TestStrikes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	ada := known(f, "ada")
	bob := known(f, "bob")

	u, err := f.svc.UpdateUser(ctx, ada.ID, func(u *user.User) error {
		u.Stats.Strikes = 1
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), u.Stats.Strikes)

	require.NoError(t, f.svc.ResetStrikes(ctx))
	assert.Equal(t, int64(0), f.store.userOf(ada.ID).Stats.Strikes)
	assert.Equal(t, int64(0), f.store.userOf(bob.ID).Stats.Strikes)
}

// TestUserByPlatformID covers the lookup of a known user by the account on
// the platform (actions.md, B91); a miss is not an error.
func TestUserByPlatformID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	ada := known(f, "ada")
	u, ok, err := f.svc.UserByPlatformID(ctx, platform.Mock, "ada")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ada.ID, u.ID)
	_, ok, err = f.svc.UserByPlatformID(ctx, platform.Mock, "nobody")
	assert.NoError(t, err)
	assert.False(t, ok)
}

// TestUpsertIdentity covers storing an account found over a platform,
// updating the names of a known user (users-and-roles.md, B4).
func TestUpsertIdentity(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFixture(t)
	ada := known(f, "ada")

	// A new account is stored.
	_, created, err := f.svc.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "zed", Login: "zed", DisplayName: "zed"})
	require.NoError(t, err)
	assert.True(t, created)

	// A known account is updated, not recreated.
	u, created, err := f.svc.UpsertIdentity(ctx, user.Identity{Platform: platform.Mock, PlatformUserID: "ada", Login: "ada", DisplayName: "New Name"})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, ada.ID, u.ID)
	assert.Equal(t, "New Name", u.Identities[0].DisplayName)
}
