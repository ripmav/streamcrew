// SPDX-License-Identifier: Apache-2.0

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
func mockSent(e event.Envelope, op mock.Op, text string) bool {
	if e.Type != mock.TypeOutput {
		return false
	}
	o, ok := event.Payload[mock.Output](e)
	return ok && o.Op == op && o.Text == text
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

	// The event command for "app.started" runs when the engine takes
	// instances, and its chat action reaches the platform.
	waitFor("the app.started event command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "Greet") })
	waitFor("the chat output of the app.started event command",
		func(e event.Envelope) bool { return mockSent(e, mock.OpSend, "Core started") })

	// A simulated user triggers the chat command, which runs, and its chat
	// action reaches the platform with the user's name rendered.
	sent, err := mp.Say(ctx, "alice", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	waitFor("the chat command to complete",
		func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	waitFor("the chat output of the chat command",
		func(e event.Envelope) bool { return mockSent(e, mock.OpSend, "Hello alice!") })

	cancel()
	require.NoError(t, <-errc)
}
