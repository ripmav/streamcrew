// SPDX-License-Identifier: MIT

package app_test

// The tests cover the channel routing of roadmap 4.6 (spec
// twitch-multi-channel.md, B5, B6) at the app level against the mock
// platform: a message of an additional channel runs the command in that
// channel, and the message of the home channel stays in the home channel.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// channelsCommands is the example command as code, reacting on a chat
// trigger with a message.
const channelsCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hello}
spec:
  triggers: [hello]
  actions:
    - {type: chat, kind: message, message: "Hello $userdisplayname!"}
`

// outputChannel is the channel of the mock output of e; empty if e is no
// mock output.
func outputChannel(e event.Envelope) string {
	if o, ok := event.Payload[mock.Output](e); ok {
		return o.Channel
	}
	return ""
}

// scopeCommands are the examples of the channel scope of roadmap 4.7
// (commands.md, B9): a command without a scope runs in every operated
// channel, and a scoped command runs only in its channel. The triggers
// differ, because triggers are unique in the profile (B14).
const scopeCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hello}
spec:
  triggers: [hello]
  actions:
    - {type: chat, kind: message, message: "Hello $userdisplayname!"}
---
apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Party}
spec:
  triggers: [party]
  channel: other
  actions:
    - {type: chat, kind: message, message: "It's party time!"}
`

// TestChannelScopeRunsOnlyInItsChannel covers commands.md, B9 and
// command-engine.md, B18 at the app level against the mock platform on two
// channels: a message of the home channel runs the command without a scope
// and not the scoped one, and a message of the additional channel runs
// both, with the reply of the scoped command in that channel. The target
// channel of the group timer (commands.md, B31) is checked at the trigger
// level (internal/engine); the scheduler of the interval itself follows
// with roadmap 5.4.
func TestChannelScopeRunsOnlyInItsChannel(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)

	// Create the profile and find its path.
	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	// Operate the additional channel "other" in the settings, so that the
	// scoped command passes the check of the save (B9).
	st, err := store.Open(ctx, path)
	require.NoError(t, err)
	svc, err := settings.New(st)
	require.NoError(t, err)
	chs, err := settings.Load(ctx, svc, settings.DefaultChannels())
	require.NoError(t, err)
	chs.Additional = append(chs.Additional, "other")
	require.NoError(t, settings.Save(ctx, svc, chs))
	require.NoError(t, st.Close())

	// Import the commands, one of them scoped to "other".
	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "scope.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(scopeCommands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	// The core on the profile with the mock platform and the additional
	// channel "other".
	var mp *mock.Platform
	a, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver,
				mock.WithLogger(b.Logger),
				mock.WithPublisher(b.Publisher),
				mock.WithChannels("other"))
			return mp, err
		}))
	require.NoError(t, err)

	var (
		mu   sync.Mutex
		seen []event.Envelope
	)
	sub := a.Bus().Subscribe(ctx, event.WithPrefixes("command.instance.", "mock."), event.WithBuffer(256))
	go func() {
		for e := range sub.C() {
			mu.Lock()
			seen = append(seen, e)
			mu.Unlock()
		}
	}()
	count := func(fn func(event.Envelope) bool) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, e := range seen {
			if fn(e) {
				n++
			}
		}
		return n
	}
	waitFor := func(what string, n int, fn func(event.Envelope) bool) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			if count(fn) >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no event: %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond)
	// Ready can be true while the mock platform still rejects chat
	// messages ("account not connected"); wait for the connection.
	require.Eventually(t, func() bool { return mp.Status().Connected() },
		5*time.Second, 10*time.Millisecond, "the mock platform to connect")

	// The message of the home channel runs the command without a scope.
	sent, err := mp.Say(ctx, "alice", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the chat command to complete", 1,
		func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	waitFor("the chat output of the chat command in the home channel", 1,
		func(e event.Envelope) bool {
			return mockSent(e, "Hello alice!") && outputChannel(e) == ""
		})

	// The message of the additional channel runs the command without a
	// scope again, and the reply lands in that channel.
	sent, err = mp.SayOn(ctx, connector.Channel("other"), "bob", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the chat command to complete in the additional channel", 2,
		func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	waitFor("the chat output in the additional channel", 1,
		func(e event.Envelope) bool {
			return mockSent(e, "Hello bob!") && outputChannel(e) == "other"
		})

	// The trigger of the scoped command runs it in its channel, with the
	// reply in that channel.
	sent, err = mp.SayOn(ctx, connector.Channel("other"), "carol", "!party")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the scoped command to complete in its channel", 1,
		func(e event.Envelope) bool { return instanceNamed(e, "Party") })
	waitFor("the chat output of the scoped command in the additional channel", 1,
		func(e event.Envelope) bool {
			return mockSent(e, "It's party time!") && outputChannel(e) == "other"
		})

	// The same trigger in the home channel does not run the scoped
	// command, because it is out of scope there.
	sent, err = mp.Say(ctx, "dave", "!party")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, count(func(e event.Envelope) bool { return instanceNamed(e, "Party") }),
		"B18: the scoped command ran only in its channel")

	cancel()
	require.NoError(t, <-errc)
}

// TestChatCommandRunsInTheChannelOfTheMessage covers B6 of the spec at the
// app level: the channel of the message is the channel of the run, so the
// chat action of the command lands in the channel the message came from —
// and the home message stays in the home channel.
func TestChatCommandRunsInTheChannelOfTheMessage(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)

	// Create the profile and find its path.
	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	// Import the example command as code.
	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "hello.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(channelsCommands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	// The core on the profile with the mock platform and the additional
	// channel "other".
	var mp *mock.Platform
	a, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver,
				mock.WithLogger(b.Logger),
				mock.WithPublisher(b.Publisher),
				mock.WithChannels("other"))
			return mp, err
		}))
	require.NoError(t, err)

	var (
		mu   sync.Mutex
		seen []event.Envelope
	)
	sub := a.Bus().Subscribe(ctx, event.WithPrefixes("command.instance.", "mock."), event.WithBuffer(256))
	go func() {
		for e := range sub.C() {
			mu.Lock()
			seen = append(seen, e)
			mu.Unlock()
		}
	}()
	waitFor := func(what string, fn func(event.Envelope) bool) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			mu.Lock()
			found := slices.ContainsFunc(seen, fn)
			mu.Unlock()
			if found {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("no event: %s", what)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond)
	// Ready can be true while the mock platform still rejects chat
	// messages ("account not connected"); wait for the connection.
	require.Eventually(t, func() bool { return mp.Status().Connected() },
		5*time.Second, 10*time.Millisecond, "the mock platform to connect")

	// The message of the additional channel runs the command in that
	// channel: the reply lands in the channel of the message.
	sent, err := mp.SayOn(ctx, connector.Channel("other"), "alice", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the chat command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	waitFor("the reply in the additional channel",
		func(e event.Envelope) bool {
			return mockSent(e, "Hello alice!") && outputChannel(e) == "other"
		})

	// The home message stays in the home channel.
	sent, err = mp.Say(ctx, "bob", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the reply in the home channel",
		func(e event.Envelope) bool {
			return mockSent(e, "Hello bob!") && outputChannel(e) == ""
		})

	cancel()
	require.NoError(t, <-errc)
}
