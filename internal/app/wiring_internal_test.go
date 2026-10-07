// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
	"github.com/ripmav/streamcrew/internal/template"
)

// nopReceiver takes what the mock platform hands over without doing anything.
type nopReceiver struct{}

func (nopReceiver) Message(context.Context, connector.Incoming) error        { return nil }
func (nopReceiver) Join(context.Context, platform.Name, user.Identity) error { return nil }
func (nopReceiver) Event(context.Context, connector.Event) error             { return nil }
func (nopReceiver) Stream(context.Context, platform.Name, bool) error        { return nil }

// openStore returns a store in a temporary directory, closed with the test.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "profile.db"),
		store.WithLogger(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return st
}

// runPlatform runs the mock platform until the test ends and waits for the
// streamer to connect.
func runPlatform(t *testing.T, mp *mock.Platform) {
	t.Helper()
	runCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		if err := mp.Run(runCtx); err != nil {
			t.Errorf("mock platform: %v", err)
		}
	}()
	require.Eventually(t, func() bool { return mp.Status().Streamer }, 2*time.Second, time.Millisecond)
}

// TestCatalog covers the event types the bus knows: the application
// events, the auth events of roadmap 4.1, and the types of the services.
func TestCatalog(t *testing.T) {
	t.Parallel()
	c, err := newCatalog()
	require.NoError(t, err)
	types := c.Types()
	assert.Contains(t, types, eventtype.AppStarted)
	assert.Contains(t, types, eventtype.AppStopping)
	assert.Contains(t, types, eventtype.AuthActionRequired)
	assert.Contains(t, types, eventtype.AuthLoginCompleted)
	assert.Contains(t, types, eventtype.AuthLoginFailed)
	assert.Contains(t, types, TypeSupervisorStatus)
}

// TestUserLookup covers the lookup the core uses until the user service
// replaces it (roadmap 5.2): the users by login name, the accounts of the
// platforms, stored when seen, and the state of the stream.
func TestUserLookup(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	st := openStore(t)
	set := newPlatformSet()
	l := &userLookup{store: st, set: set}

	// Without platforms nothing is found, and the streamer is unknown.
	_, ok, err := l.UserByName(ctx, platform.Mock, "alice")
	assert.NoError(t, err)
	assert.False(t, ok)
	_, ok, err = l.Account(ctx, platform.Mock, template.StreamerAccount)
	assert.NoError(t, err)
	assert.False(t, ok)
	_, err = l.StreamerUser(ctx, platform.Mock)
	assert.Error(t, err)
	_, err = l.Chatters(ctx, platform.Mock)
	assert.Error(t, err)
	_, ok, err = l.UserByPlatformID(ctx, platform.Mock, "42")
	assert.NoError(t, err)
	assert.False(t, ok)

	// With the mock platform the accounts are found and stored when seen.
	mp, err := mock.New(nopReceiver{}, mock.WithStreamer("streamy"), mock.WithBot("botbot"))
	require.NoError(t, err)
	require.NoError(t, set.fill(mp))

	u, ok, err := l.UserByName(ctx, platform.Mock, "streamy")
	require.NoError(t, err)
	require.True(t, ok)
	streamIdent, ok := u.Identity(platform.Mock)
	require.True(t, ok)
	assert.Equal(t, "streamy", streamIdent.Login)
	_, ok, err = l.UserByName(ctx, platform.Mock, "BOTBOT")
	assert.NoError(t, err)
	assert.True(t, ok)
	_, ok, err = l.UserByName(ctx, platform.Mock, "alice")
	assert.NoError(t, err)
	assert.False(t, ok)

	_, ok, err = l.Account(ctx, platform.Mock, template.StreamerAccount)
	assert.NoError(t, err)
	assert.True(t, ok)
	_, ok, err = l.Account(ctx, platform.Mock, template.BotAccount)
	assert.NoError(t, err)
	assert.True(t, ok)

	sid, err := l.StreamerUser(ctx, platform.Mock)
	assert.NoError(t, err)
	assert.Equal(t, u.ID, sid)

	// The chat of the platform: a simulated user joins and is a chatter,
	// stored like a newly seen one.
	runPlatform(t, mp)
	ident, err := mp.AddUser(mock.UserSpec{Login: "alice"})
	require.NoError(t, err)
	require.NoError(t, mp.Join(ctx, "alice"))
	chat, err := l.Chatters(ctx, platform.Mock)
	require.NoError(t, err)
	require.Len(t, chat, 1)
	chatIdent, ok := chat[0].Identity(platform.Mock)
	require.True(t, ok)
	assert.Equal(t, "alice", chatIdent.Login)
	found, ok, err := l.UserByPlatformID(ctx, platform.Mock, ident.PlatformUserID)
	assert.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, chat[0].ID, found.ID)
	again, created, err := l.UpsertIdentity(ctx, ident)
	assert.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, chat[0].ID, again.ID)
}

// TestPlatformSet covers the set the composition root fills after the
// platforms are built.
func TestPlatformSet(t *testing.T) {
	t.Parallel()
	set := newPlatformSet()
	_, ok := set.Platform(platform.Mock)
	assert.False(t, ok)
	assert.Empty(t, set.Connected())

	mp, err := mock.New(nopReceiver{})
	require.NoError(t, err)
	require.NoError(t, set.fill(mp))
	p, ok := set.Platform(platform.Mock)
	assert.True(t, ok)
	assert.Same(t, any(mp), any(p))
	// Not connected yet: the connected list is empty.
	assert.Empty(t, set.Connected())

	runPlatform(t, mp)
	assert.Equal(t, []connector.Platform{mp}, set.Connected())
}

// TestStreamStates covers the state of the stream from the channel of the
// platform.
func TestStreamStates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	set := newPlatformSet()
	s := streamStates{set: set}
	_, err := s.StreamState(ctx, platform.Mock)
	assert.Error(t, err)

	mp, err := mock.New(nopReceiver{})
	require.NoError(t, err)
	require.NoError(t, set.fill(mp))
	st, err := s.StreamState(ctx, platform.Mock)
	require.NoError(t, err)
	assert.False(t, st.Live)

	runPlatform(t, mp)
	require.NoError(t, mp.GoLive(ctx, "Evening", "Just Chatting"))
	st, err = s.StreamState(ctx, platform.Mock)
	require.NoError(t, err)
	assert.True(t, st.Live)
	assert.Equal(t, "Evening", st.Title)
	assert.Equal(t, "Just Chatting", st.Game)
}

// TestChatMute covers the muted chat, which is not stored.
func TestChatMute(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	m := newChatMute(slog.New(slog.DiscardHandler))
	assert.False(t, m.Muted())
	m.Mute(ctx)
	assert.True(t, m.Muted())
	m.Mute(ctx)
	assert.True(t, m.Muted())
	m.Unmute(ctx)
	assert.False(t, m.Muted())
	m.Unmute(ctx)
	assert.False(t, m.Muted())
}

// TestLateSwitches covers the port of the command action before the command
// service is set.
func TestLateSwitches(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	l := &lateSwitches{}
	_, err := l.SwitchCommand(ctx, id.New(), command.SwitchOff)
	assert.Error(t, err)
	assert.Error(t, l.SwitchGroup(ctx, id.New(), command.SwitchOff))
}

// TestFormatLocale_B41: a name of the section is used as is; "system" is
// the locale of the environment, with a warning for an environment locale
// outside the list.
func TestFormatLocale_B41(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	logs := &bytes.Buffer{}
	a := &App{logger: slog.New(slog.NewTextHandler(logs, nil)), systemLocale: "de_DE"}

	assert.Equal(t, template.LocaleDEGerman, a.formatLocale(ctx, "de-DE"))
	assert.Equal(t, template.LocaleDEGerman, a.formatLocale(ctx, "de-de"), "regardless of case")
	assert.Equal(t, template.LocaleDEGerman, a.formatLocale(ctx, settings.LocaleSystem))
	assert.Empty(t, logs.String(), "a locale of the list needs no warning")

	a.systemLocale = "de"
	assert.Equal(t, template.LocaleDEGerman, a.formatLocale(ctx, settings.LocaleSystem), "the first locale of the language")
	assert.Contains(t, logs.String(), "outside the list")

	logs.Reset()
	a.systemLocale = "fr-FR"
	assert.Equal(t, template.LocaleUSEnglish, a.formatLocale(ctx, settings.LocaleSystem), "no language in the list")
	assert.Contains(t, logs.String(), "outside the list")

	logs.Reset()
	a.systemLocale = ""
	assert.Equal(t, template.LocaleUSEnglish, a.formatLocale(ctx, settings.LocaleSystem), "without an environment locale")
	assert.Empty(t, logs.String(), "nothing to warn about")
}
