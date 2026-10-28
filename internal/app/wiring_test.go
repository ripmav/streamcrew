// SPDX-License-Identifier: MIT

package app_test

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
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/obs"
)

// exampleCommands are the example commands as code (roadmap 3.6, M1): a
// chat command and an event command for "app.started".
const exampleCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hello}
spec:
  triggers: [hello]
  actions:
    - {type: chat, kind: message, message: "Hello $userdisplayname!"}
---
apiVersion: streamcrew/v1alpha1
kind: EventCommand
metadata: {name: Greet}
spec:
  event: app.started
  actions:
    - {type: chat, kind: message, message: "Core started"}
`

// instanceNamed reports whether e is the completed instance of the command
// named.
func instanceNamed(e event.Envelope, named string) bool {
	if e.Type != engine.TypeInstanceCompleted {
		return false
	}
	ins, ok := event.Payload[engine.Instance](e)
	return ok && ins.CommandName == named
}

// mockSent reports whether e is the mock output op with the text.
func mockSent(e event.Envelope, text string) bool {
	if e.Type != mock.TypeOutput {
		return false
	}
	o, ok := event.Payload[mock.Output](e)
	return ok && o.Op == mock.OpSend && o.Text == text
}

// TestChatCommandRunsOnMockPlatform covers the wiring of roadmap 3.6 (M1):
// the example commands imported as code react on the mock platform: the
// event command for "app.started" runs when the engine takes instances, and
// the chat command runs when a simulated user sends its trigger, and its
// chat action reaches the platform again as a mock.output.
func TestChatCommandRunsOnMockPlatform(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)

	// Create the profile and find its path.
	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	// Import the example commands as code.
	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "hello.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(exampleCommands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	// The core on the profile with the mock platform.
	var mp *mock.Platform
	a, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver, mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher))
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

	// The event command for "app.started" runs when the engine takes
	// instances, and its chat action reaches the platform.
	waitFor("the app.started event command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "Greet") })
	waitFor("the chat output of the app.started event command",
		func(e event.Envelope) bool { return mockSent(e, "Core started") })

	// A simulated user triggers the chat command, which runs, and its chat
	// action reaches the platform with the user's name rendered.
	sent, err := mp.Say(ctx, "alice", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the chat command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	waitFor("the chat output of the chat command",
		func(e event.Envelope) bool { return mockSent(e, "Hello alice!") })

	cancel()
	require.NoError(t, <-errc)
}

// soundCommands is a chat command that plays a file of the media
// directory (roadmap 7.3, B40).
const soundCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: PlaySound}
spec:
  triggers: [playsound]
  actions:
    - {type: sound, file: "intro.wav", sink: "overlay"}
`

// TestSoundActionRunsAgainstTheBuiltServices covers the wiring of
// roadmap 7.3 (Review 345-1): the sound action is wired before the media
// and tts services are built, so the ports resolve them at the call; the
// action completes instead of panicking on a nil service.
func TestSoundActionRunsAgainstTheBuiltServices(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)

	// Create the profile, find its path, and put the file in the media
	// directory the core creates next to the profile database.
	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	p := first.Profile()
	path := p.Path
	require.NoError(t, first.Close())
	mediaDir := filepath.Join(filepath.Dir(path), p.ID, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(mediaDir, "intro.wav"), []byte("RIFF"), 0o600))

	// Import the command as code.
	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "sound.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(soundCommands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	// The core on the profile with the mock platform.
	var mp *mock.Platform
	a, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver, mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher))
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
	require.Eventually(t, func() bool { return mp.Status().Connected() },
		5*time.Second, 10*time.Millisecond, "the mock platform to connect")

	// The simulated user triggers the sound command; the action plays
	// through the built media service and the instance completes.
	sent, err := mp.Say(ctx, "alice", "!playsound")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the sound command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "PlaySound") })

	// Before the fix the nil service panicked inside the engine; the
	// command's default error policy continues on a failed action, so
	// the instance completes either way — the error lands in its action
	// errors.
	var completed engine.Instance
	found := false
	mu.Lock()
	for _, e := range seen {
		if instanceNamed(e, "PlaySound") {
			completed, found = event.Payload[engine.Instance](e)
			break
		}
	}
	mu.Unlock()
	require.True(t, found, "the completed instance of the sound command")
	assert.Empty(t, completed.Errors, "the sound action must not fail")

	cancel()
	require.NoError(t, <-errc)
}

// streamingCommands is a chat command that switches the OBS scene
// (roadmap 7.4, B42).
const streamingCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: SwitchScene}
spec:
  triggers: [switchscene]
  actions:
    - {type: streaming_software, kind: "set_scene", scene: "Main"}
`

// TestStreamingActionRunsAgainstTheBuiltService covers the wiring of
// roadmap 7.4 (Review 346-N1): the OBS service is built before the
// action wiring, so the port carries the service, not a nil pointer;
// without a running OBS the action answers the clear error of B55
// instead of panicking.
func TestStreamingActionRunsAgainstTheBuiltService(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cfg := testConfig(t)

	// Create the profile and find its path.
	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	// Import the command as code.
	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "streaming.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(streamingCommands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	// The core on the profile with the mock platform; no OBS runs.
	var mp *mock.Platform
	a, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver, mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher))
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
	require.Eventually(t, func() bool { return mp.Status().Connected() },
		5*time.Second, 10*time.Millisecond, "the mock platform to connect")

	// The simulated user triggers the streaming command; the action runs
	// on the built service and answers the clear error of B55.
	sent, err := mp.Say(ctx, "alice", "!switchscene")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the streaming command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "SwitchScene") })

	// Before the fix the nil service panicked inside the engine (caught,
	// "action panicked"); the action error must be the not-connected
	// error of the obs service.
	var completed engine.Instance
	found := false
	mu.Lock()
	for _, e := range seen {
		if instanceNamed(e, "SwitchScene") {
			completed, found = event.Payload[engine.Instance](e)
			break
		}
	}
	mu.Unlock()
	require.True(t, found, "the completed instance of the streaming command")
	require.Len(t, completed.Errors, 1, "the action must fail once")
	assert.Contains(t, completed.Errors[0].Message, obs.ErrNotConnected.Error())

	cancel()
	require.NoError(t, <-errc)
}
