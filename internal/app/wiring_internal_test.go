// SPDX-License-Identifier: MIT

package app

import (
	"bytes"
	"context"
	"log/slog"
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
	"github.com/ripmav/streamcrew/internal/template"
)

// nopReceiver takes what the mock platform hands over without doing anything.
type nopReceiver struct{}

func (nopReceiver) Message(context.Context, connector.Incoming) error { return nil }
func (nopReceiver) Join(context.Context, platform.Name, connector.Channel, user.Identity) error {
	return nil
}
func (nopReceiver) Event(context.Context, connector.Event) error      { return nil }
func (nopReceiver) Stream(context.Context, platform.Name, bool) error { return nil }

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
