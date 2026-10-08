// SPDX-License-Identifier: MIT

package connector_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// The fakes implement the ports.
var (
	_ connector.Platform = (*connectortest.Platform)(nil)
	_ connector.Known    = (*connectortest.Known)(nil)
)

// TestFake covers what the tests of the consumers rely on: the optional
// capabilities follow the features, and a message from an account that is
// not connected fails as it does with an adapter.
func TestFake(t *testing.T) {
	t.Parallel()
	for _, f := range []connectortest.Features{{}, {Replies: true}, {Whispers: true}, {Replies: true, Whispers: true}} {
		chat := connectortest.New(platform.Twitch, f).Chat()
		_, replies := chat.(connector.Replier)
		_, whispers := chat.(connector.Whisperer)
		assert.Equal(t, f, connectortest.Features{Replies: replies, Whispers: whispers})
	}

	p := connectortest.New(platform.Twitch, connectortest.Features{})
	require.NoError(t, p.Chat().Send(t.Context(), connector.Message{Text: "a", From: connector.AccountStreamer}))
	err := p.Chat().Send(t.Context(), connector.Message{Text: "b", From: connector.AccountBot})
	require.ErrorIs(t, err, connector.ErrNotConnected)
	p.SetStatus(connector.Status{Streamer: true, Bot: true})
	require.NoError(t, p.Chat().Send(t.Context(), connector.Message{Text: "c", From: connector.AccountBot}))
	assert.Equal(t, []connectortest.Call{
		{Op: connectortest.OpSend, From: connector.AccountStreamer, Text: "a"},
		{Op: connectortest.OpSend, From: connector.AccountBot, Text: "b"},
		{Op: connectortest.OpSend, From: connector.AccountBot, Text: "c"},
	}, p.Calls())
}

func TestAccount(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []connector.Account{connector.AccountStreamer, connector.AccountBot}, connector.Accounts())
	for _, a := range connector.Accounts() {
		assert.True(t, a.Valid(), a)
	}
	assert.False(t, connector.Account("").Valid())
	assert.False(t, connector.Account("Bot").Valid())
}

// TestStatus covers actions.md B61 and B62: a platform is connected with
// its streamer account, and messages go from the bot if it is connected,
// unless they go as the streamer.
func TestStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		status     connector.Status
		connected  bool
		sender     connector.Account
		asStreamer connector.Account
	}{
		{"nothing", connector.Status{}, false, connector.AccountStreamer, connector.AccountStreamer},
		{"streamer", connector.Status{Streamer: true}, true, connector.AccountStreamer, connector.AccountStreamer},
		{"streamer and bot", connector.Status{Streamer: true, Bot: true}, true, connector.AccountBot, connector.AccountStreamer},
		{"bot only", connector.Status{Bot: true}, false, connector.AccountBot, connector.AccountStreamer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.connected, tc.status.Connected())
			assert.Equal(t, tc.sender, tc.status.Sender(false))
			assert.Equal(t, tc.asStreamer, tc.status.Sender(true))
		})
	}
}

func TestSet(t *testing.T) {
	t.Parallel()
	twitch := connectortest.New(platform.Twitch, connectortest.Features{})
	youtube := connectortest.New(platform.YouTube, connectortest.Features{})
	kick := connectortest.New(platform.Kick, connectortest.Features{})
	s, err := connector.NewSet(youtube, twitch, kick)
	require.NoError(t, err)

	p, ok := s.Platform(platform.Twitch)
	require.True(t, ok)
	assert.Same(t, twitch, p)
	_, ok = s.Platform("velora")
	assert.False(t, ok)

	t.Run("connected in the order of the set", func(t *testing.T) {
		assert.Equal(t, []connector.Platform{youtube, twitch, kick}, s.Connected())
	})
	t.Run("connected follows the status", func(t *testing.T) {
		youtube.SetStatus(connector.Status{Bot: true})
		kick.SetStatus(connector.Status{})
		assert.Equal(t, []connector.Platform{twitch}, s.Connected())
		twitch.SetStatus(connector.Status{})
		assert.Empty(t, s.Connected())
	})
	t.Run("zero value", func(t *testing.T) {
		var empty connector.Set
		assert.Empty(t, empty.Connected())
		_, ok := empty.Platform(platform.Twitch)
		assert.False(t, ok)
	})
}

func TestNewSetRejects(t *testing.T) {
	t.Parallel()
	twitch := connectortest.New(platform.Twitch, connectortest.Features{})
	for _, tc := range []struct {
		name string
		ps   []connector.Platform
		want string
	}{
		{"nil", []connector.Platform{twitch, nil}, "platform set: nil platform"},
		{"twice", []connector.Platform{twitch, connectortest.New(platform.Twitch, connectortest.Features{})}, "platform set: twitch twice"},
		{"invalid name", []connector.Platform{connectortest.New("Twitch", connectortest.Features{})}, `platform set: platform name "Twitch": only lowercase letters and digits are allowed`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := connector.NewSet(tc.ps...)
			require.EqualError(t, err, tc.want)
		})
	}
}

func TestLogin(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"alice":      "alice",
		"@Alice":     "Alice",
		"  @alice  ": "alice",
		"@@alice":    "@alice",
		"@":          "",
		"":           "",
		"al ice":     "al ice",
	} {
		assert.Equal(t, want, connector.Login(in), in)
	}
}

// TestFindAccount covers actions.md B63 and B81: the account the core
// knows wins; otherwise the platform looks it up.
func TestFindAccount(t *testing.T) {
	t.Parallel()
	newFixture := func() (*connectortest.Known, *connectortest.Platform) {
		twitch := connectortest.New(platform.Twitch, connectortest.Features{})
		known := &connectortest.Known{}
		alice := twitch.Account("alice")
		alice.DisplayName = "Alice (known)"
		known.Add(user.User{Identities: []user.Identity{
			{Platform: platform.YouTube, PlatformUserID: "yt-alice", Login: "alice", DisplayName: "Alice"},
			twitch.Account("alice2"),
			alice,
		}})
		twitch.AddAccounts(twitch.Account("alice"), twitch.Account("bob"))
		return known, twitch
	}

	t.Run("known", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		for _, name := range []string{"alice", "@ALICE", " @Alice "} {
			got, err := connector.FindAccount(t.Context(), known, twitch, name)
			require.NoError(t, err)
			assert.Equal(t, "Alice (known)", got.DisplayName, name)
		}
		assert.Empty(t, twitch.Calls(), "the platform is not asked")
	})
	t.Run("from the platform", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		got, err := connector.FindAccount(t.Context(), known, twitch, "@Bob")
		require.NoError(t, err)
		assert.Equal(t, twitch.Account("bob"), got)
		assert.Equal(t, []connectortest.Call{{Op: connectortest.OpUserByLogin, Target: "Bob"}}, twitch.Calls())
	})
	t.Run("known on another platform only", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		youtube := connectortest.New(platform.YouTube, connectortest.Features{})
		_, err := connector.FindAccount(t.Context(), known, youtube, "alice2")
		require.ErrorIs(t, err, connector.ErrUnknownUser)
		assert.Equal(t, []connectortest.Op{connectortest.OpUserByLogin}, youtube.Ops())
		assert.Empty(t, twitch.Calls())
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		_, err := connector.FindAccount(t.Context(), known, twitch, "carol")
		require.ErrorIs(t, err, connector.ErrUnknownUser)
		assert.EqualError(t, err, `find account "carol" on twitch: twitch "carol": unknown user`)
	})
	t.Run("empty name", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		for _, name := range []string{"", " ", "@"} {
			_, err := connector.FindAccount(t.Context(), known, twitch, name)
			require.ErrorIs(t, err, connector.ErrUnknownUser, name)
		}
		assert.Zero(t, known.Asked())
		assert.Empty(t, twitch.Calls())
	})
	t.Run("the core fails", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		boom := errors.New("boom")
		known.Fail(boom)
		_, err := connector.FindAccount(t.Context(), known, twitch, "alice")
		require.ErrorIs(t, err, boom)
		assert.Empty(t, twitch.Calls())
	})
	t.Run("the platform fails", func(t *testing.T) {
		t.Parallel()
		known, twitch := newFixture()
		boom := errors.New("boom")
		twitch.Fail(connectortest.OpUserByLogin, boom)
		_, err := connector.FindAccount(t.Context(), known, twitch, "bob")
		require.ErrorIs(t, err, boom)
		require.NotErrorIs(t, err, connector.ErrUnknownUser)
	})
}

// TestJoinErrors covers actions.md B66 and B86: the error names the
// platforms an operation failed on, and each platform's error stays
// visible.
func TestJoinErrors(t *testing.T) {
	t.Parallel()
	twitch := connectortest.New(platform.Twitch, connectortest.Features{})
	youtube := connectortest.New(platform.YouTube, connectortest.Features{})
	kick := connectortest.New(platform.Kick, connectortest.Features{})
	ps := []connector.Platform{twitch, youtube, kick}

	require.NoError(t, connector.JoinErrors("not sent", ps, make([]error, 3)))
	require.NoError(t, connector.JoinErrors("not sent", nil, nil))

	boom := errors.New("boom")
	err := connector.JoinErrors("failed", ps, []error{boom, nil, connector.ErrRefused})
	require.EqualError(t, err, "failed on twitch, kick: twitch: boom; kick: refused by the platform")
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, err, connector.ErrRefused)
	opErr, ok := errors.AsType[*connector.OpError](err)
	require.True(t, ok)
	assert.Equal(t, []connector.Failure{
		{Platform: platform.Twitch, Err: boom},
		{Platform: platform.Kick, Err: connector.ErrRefused},
	}, opErr.Failures)
}
