// SPDX-License-Identifier: Apache-2.0

package template_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/template"
)

// fakeUsers is the port of the user family with fixed users.
type fakeUsers struct {
	streamer, bot *user.User
	byName        map[string]user.User // login in lower case → user
	chatters      []user.User
	err           error
	chatterCalls  *atomic.Int64
}

func (f fakeUsers) Account(_ context.Context, p platform.Name, a template.Account) (user.User, bool, error) {
	u := f.streamer
	if a == template.BotAccount {
		u = f.bot
	}
	if u == nil || p != platform.Twitch {
		return user.User{}, false, f.err
	}
	return *u, true, f.err
}

func (f fakeUsers) UserByName(_ context.Context, _ platform.Name, name string) (user.User, bool, error) {
	u, ok := f.byName[strings.ToLower(name)]
	return u, ok, f.err
}

func (f fakeUsers) Chatters(context.Context, platform.Name) ([]user.User, error) {
	f.chatterCalls.Add(1)
	return f.chatters, f.err
}

// twitchUser returns a user with a Twitch identity.
func twitchUser(uid, login, display string, roles ...role.Role) user.User {
	return user.User{
		ID: id.MustParse(uid),
		Identities: []user.Identity{{
			Platform:       platform.Twitch,
			PlatformUserID: strings.ToLower(login) + "-id",
			Login:          login,
			DisplayName:    display,
			Roles:          role.NewSet(roles...),
		}},
	}
}

// testUsers returns the users of testdata/user.txt: Alice, Bob, the
// streamer and the bot, and the port with Carol and Dave in the chat.
func testUsers() (alice, bob user.User, users fakeUsers) {
	alice = twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d", "alice_99", "Ålice", role.Moderator, role.Subscriber, role.Follower)
	alice.Regular = true
	alice.Notes = "likes chess"
	alice.Stats = user.Stats{
		WatchMinutes: 132, Messages: 100, CommandsRun: 12, Mentions: 3, StreamsWatched: 10,
		LastSeen: time.Date(2009, time.June, 13, 2, 1, 0, 0, time.UTC), DonatedCents: 1234, Strikes: 2,
	}
	ident := &alice.Identities[0]
	ident.Color = "#1E90FF"
	ident.AvatarURL = "https://static.example/alice.png"
	ident.Data = user.PlatformData{
		AccountCreatedAt: time.Date(2005, time.June, 15, 8, 0, 0, 0, time.UTC),
		FollowedAt:       time.Date(2008, time.February, 3, 12, 0, 0, 0, time.UTC),
		SubscribedAt:     time.Date(2009, time.February, 15, 12, 0, 0, 0, time.UTC),
		SubTier:          1,
	}

	bob = twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b2e", "bob", "Bob")
	bob.Title = "Chess Master"
	streamer := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b3f", "chessqueen", "ChessQueen", role.Streamer)
	bot := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b40", "chessbot", "ChessBot")
	carol := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b51", "carol", "Carol", role.Follower)
	carol.Excluded = true
	dave := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b62", "dave", "Dave", role.Subscriber, role.Follower)

	users = fakeUsers{
		streamer:     &streamer,
		bot:          &bot,
		byName:       map[string]user.User{"bob": bob, "chessqueen": streamer},
		chatters:     []user.User{carol, dave},
		chatterCalls: new(atomic.Int64),
	}
	return alice, bob, users
}

// userRegistry returns a registry with the user family and the arguments.
func userRegistry(t testing.TB, users template.Users) *template.Registry {
	t.Helper()
	r, err := template.NewRegistry(template.UserFamily(users), template.ArgumentFamily())
	require.NoError(t, err)
	return r
}

func TestUserFamily_Golden_B42_B60(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sleepUntil(time.Date(2009, time.June, 15, 17, 45, 20, 0, time.UTC))
		alice, bob, users := testUsers()
		s := template.Scope{
			Platform: platform.Twitch,
			User:     &alice,
			Target:   &bob,
			Args:     []string{"@Bob", "chessqueen", "nobody"},
			Location: profileZone(),
		}
		renderGolden(t, template.New(userRegistry(t, users)), &s, "user")
	})
}

// TestUserFamily_RandomUserOncePerRender_B22 checks that all properties of a
// random user in a render belong to the same user.
func TestUserFamily_RandomUserOncePerRender_B22(t *testing.T) {
	t.Parallel()
	_, _, users := testUsers()
	users.chatters = nil
	for i := range 20 {
		name := "viewer" + string(rune('a'+i))
		users.chatters = append(users.chatters, twitchUser(id.New().String(), name, strings.ToUpper(name)))
	}
	e := template.New(userRegistry(t, users))
	s := template.Scope{Platform: platform.Twitch}
	seen := map[string]bool{}
	for range 40 {
		users.chatterCalls.Store(0)
		out := render(t, e, "$randomusername $randomuserdisplayname $RandomUserName", &s)
		parts := strings.Fields(out)
		require.Len(t, parts, 3)
		assert.Equal(t, strings.ToUpper(parts[0]), parts[1])
		assert.Equal(t, parts[0], parts[2])
		assert.Equal(t, int64(1), users.chatterCalls.Load(), "the chat is asked once per render")
		seen[parts[0]] = true
	}
	assert.Greater(t, len(seen), 1, "different renders pick different users")
}

func TestUserFamily_NoUsers(t *testing.T) {
	t.Parallel()
	alice, _, users := testUsers()
	const text = "$username $targetusername $streamerusername $botusername $arg1username $randomusername"

	e := template.New(userRegistry(t, nil))
	assert.Equal(t, text, render(t, e, text, nil), "no run data and no port")

	s := template.Scope{Platform: platform.Twitch, User: &alice, Args: []string{"bob"}}
	assert.Equal(t, "alice_99 alice_99 $streamerusername $botusername $arg1username $randomusername", render(t, e, text, &s),
		"without a target the target is the triggering user")

	e = template.New(userRegistry(t, users))
	assert.Equal(t, "$arg1username", render(t, e, "$arg1username", &template.Scope{Platform: platform.Twitch, Args: []string{"@"}}))

	users.chatters = []user.User{users.chatters[0]} // only the excluded Carol
	e = template.New(userRegistry(t, users))
	assert.Equal(t, "$randomusername $arg1user", render(t, e, "$randomusername $arg1user", &template.Scope{Platform: platform.Kick}))
	assert.Equal(t, "$streamerusername", render(t, e, "$streamerusername", &template.Scope{Platform: platform.Kick}))
}

func TestUserFamily_Error_B23(t *testing.T) {
	t.Parallel()
	_, _, users := testUsers()
	users.err = errors.New("user service unavailable")
	logger, logs := logBuffer()
	e := template.New(userRegistry(t, users), template.WithLogger(logger))
	s := template.Scope{Platform: platform.Twitch, Args: []string{"bob"}}
	assert.Equal(t, "$randomusername $streamerusername $arg1username", render(t, e, "$randomusername $streamerusername $arg1username", &s))
	assert.Equal(t, 3, strings.Count(logs.String(), "user service unavailable"), logs.String())
}

// TestUserFamily_Properties covers properties with edge values on 1 March
// 2025, 12:00 UTC.
func TestUserFamily_Properties(t *testing.T) {
	t.Parallel()
	u := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d", "Zed", "zed", role.Banned, role.VIP)
	u.Stats = user.Stats{WatchMinutes: 61, DonatedCents: -5}
	u.Identities[0].Data.FollowedAt = time.Date(2025, time.January, 31, 12, 0, 0, 0, time.UTC)
	u.Identities[0].Data.SubscribedAt = time.Date(2025, time.March, 1, 11, 0, 0, 0, time.UTC)
	u.Identities[0].Data.AccountCreatedAt = time.Date(2025, time.March, 2, 0, 0, 0, 0, time.UTC)
	noIdentity := user.User{Title: "Guest"}
	everything := twitchUser("0192f0c4-8f7e-7c3a-9b1d-2f4e6a8c0b1d", "all", "All", role.All()...)
	tests := []struct {
		name  string
		scope template.Scope
		text  string
		want  string
	}{
		{"singular units", template.Scope{User: &u}, "$usertime", "1 Hour & 1 Min"},
		{"negative amount", template.Scope{User: &u}, "$usertotalamountdonated", "-0.05"},
		{"same name in other case", template.Scope{User: &u}, "$userfulldisplayname", "zed"},
		{"banned is the primary role", template.Scope{User: &u}, "$userprimaryrole|$usertitle|$userroles", "Banned|Banned|VIP, Banned"},
		{"end of a shorter month", template.Scope{User: &u}, "$userfollowage|$userfollowdays|$userfollowmonths", "1 Month, 1 Day|29|1"},
		{"today", template.Scope{User: &u}, "$usersubage|$usersubdays", "0 Days|0"},
		{"date in the future", template.Scope{User: &u}, "$useraccountage|$useraccountdays", "0 Days|0"},
		{"identity of another platform", template.Scope{User: &u, Platform: platform.Kick}, "$username|$userurl", "Zed|https://www.twitch.tv/Zed"},
		{"no identity", template.Scope{User: &noIdentity}, "$username|$userroles|$usertitle|$userid|$usersubtier", "$username|User|Guest|$userid|$usersubtier"},
		{"never seen", template.Scope{User: &noIdentity}, "$userlastseendate|$userlastseenage", "$userlastseendate|$userlastseenage"},
		{"all roles", template.Scope{User: &everything}, "$userroles", "Streamer, Editor, Moderator, Platform Staff, Subscriber, VIP, Regular, Follower, Creator, Banned"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				sleepUntil(time.Date(2025, time.March, 1, 12, 0, 0, 0, time.UTC))
				assert.Equal(t, tc.want, render(t, template.New(userRegistry(t, nil)), tc.text, &tc.scope))
			})
		})
	}
}

func TestUserFamily_Reserved_B12(t *testing.T) {
	t.Parallel()
	r := userRegistry(t, nil)
	for name, want := range map[string]string{
		"users":      "<subject><property>",
		"bot":        "<subject><property>",
		"random":     "<subject><property>",
		"botdeaths":  "",
		"userdeaths": "<subject><property>",
		"wins":       "",
	} {
		with, reserved := r.Reserved(name)
		assert.Equal(t, want != "", reserved, name)
		assert.Equal(t, want, with, name)
	}
}
