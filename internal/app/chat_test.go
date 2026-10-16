// SPDX-License-Identifier: MIT

package app_test

// The tests cover the chat service of roadmap 5.1 (spec chat-service.md)
// at the app level against the mock platform: a chat message fires its
// events, runs the command and lands in the protocol file (B1, B13, B14,
// B17); the muted chat deletes incoming messages except those of the
// streamer and the bot (B21, B22, B31); and the chat action sends and
// whispers through the service (B4, B10).

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// chatCommands is the example command as code, reacting on a chat trigger
// with a message, and a command that mutes the chat.
const chatCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Hello}
spec:
  triggers: [hello]
  actions:
    - {type: chat, kind: message, message: "Hello $userdisplayname!"}
---
apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: MuteAll}
spec:
  triggers: [muteall]
  actions:
    - {type: moderation, kind: disable_chat}
`

// protocolEntry is one line of the protocol file (spec chat-service.md,
// B13, B17).
type protocolEntry struct {
	Platform  string `json:"platform"`
	Name      string `json:"name"`
	Text      string `json:"text"`
	FromBot   bool   `json:"fromBot"`
	MessageID string `json:"messageId"`
}

// protocolEntries reads and decodes the protocol file of platform p below
// dir.
func protocolEntries(t *testing.T, dir, p string) []protocolEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "chat", p+".jsonl"))
	require.NoError(t, err)
	var out []protocolEntry
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "" {
			continue
		}
		var e protocolEntry
		require.NoError(t, json.Unmarshal([]byte(line), &e), "protocol line %q", line)
		out = append(out, e)
	}
	return out
}

// eventCollector is the events the core published, for assertions.
type eventCollector struct {
	mu   sync.Mutex
	seen []event.Envelope
}

func (c *eventCollector) add(e event.Envelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, e)
}

// count returns the number of events fn selects.
func (c *eventCollector) count(fn func(event.Envelope) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, e := range c.seen {
		if fn(e) {
			n++
		}
	}
	return n
}

// waitFor waits until fn selects at least one event, or the deadline runs
// out.
func (c *eventCollector) waitFor(t *testing.T, what string, fn func(event.Envelope) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if c.count(fn) >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no event: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mockOp reports whether e is the mock output op op with the message ID.
func mockOp(e event.Envelope, op mock.Op, messageID string) bool {
	if e.Type != mock.TypeOutput {
		return false
	}
	o, ok := event.Payload[mock.Output](e)
	return ok && o.Op == op && o.MessageID == messageID
}

// startedCore returns the core on a fresh profile running the mock
// platform, with the settings chat section and the commands imported, and
// a collector for the command instances and the mock outputs. The platform
// runs until the test ends.
func startedCore(t *testing.T, cfg config.Config, commands string, ch settings.Chat, opts ...mock.Option) (*mock.Platform, *eventCollector) {
	t.Helper()
	path := newProfileWithSettings(t, cfg, func(svc *settings.Service) {
		require.NoError(t, settings.Save(t.Context(), svc, ch))
	})
	return runCore(t, cfg, path, commands, nil, opts...)
}

// startedCoreModeration is startedCore with the moderation section saved too.
func startedCoreModeration(t *testing.T, cfg config.Config, commands string, ch settings.Chat, mods settings.Moderation, opts ...mock.Option) (*mock.Platform, *eventCollector) {
	t.Helper()
	path := newProfileWithSettings(t, cfg, func(svc *settings.Service) {
		require.NoError(t, settings.Save(t.Context(), svc, ch))
		require.NoError(t, settings.Save(t.Context(), svc, mods))
	})
	return runCore(t, cfg, path, commands, nil, opts...)
}

// startedCoreWatch is startedCore with a shorter watch time tick, for the
// watch time end-to-end test.
func startedCoreWatch(t *testing.T, cfg config.Config, commands string, ch settings.Chat, watchTick time.Duration, opts ...mock.Option) (*mock.Platform, *eventCollector) {
	t.Helper()
	path := newProfileWithSettings(t, cfg, func(svc *settings.Service) {
		require.NoError(t, settings.Save(t.Context(), svc, ch))
	})
	return runCore(t, cfg, path, commands, []app.Option{app.WithWatchTick(watchTick)}, opts...)
}

// newProfileWithSettings returns a fresh profile path and applies save to its
// settings before the profile is closed.
func newProfileWithSettings(t *testing.T, cfg config.Config, save func(svc *settings.Service)) string {
	t.Helper()
	ctx := t.Context()

	first, err := app.New(ctx, cfg, app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil))
	require.NoError(t, err)
	path := first.Profile().Path
	require.NoError(t, first.Close())

	st, err := store.Open(ctx, path)
	require.NoError(t, err)
	svc, err := settings.New(st)
	require.NoError(t, err)
	save(svc)
	require.NoError(t, st.Close())
	return path
}

// runCore runs the profile at path with the mock platform and the imported
// commands, and returns the platform and a collector for the command
// instances and the mock outputs.
func runCore(t *testing.T, cfg config.Config, path, commands string, appOpts []app.Option, opts ...mock.Option) (*mock.Platform, *eventCollector) {
	t.Helper()
	ctx := t.Context()

	rights, err := cfg.Rights()
	require.NoError(t, err)
	file := filepath.Join(t.TempDir(), "chat.v1alpha1.yaml")
	require.NoError(t, os.WriteFile(file, []byte(commands), 0o600))
	report, err := app.ImportCommandFiles(ctx, path, nil, rights, []string{file})
	require.NoError(t, err)
	require.True(t, report.Imported, "%+v", report)

	var mp *mock.Platform
	coreOpts := append([]app.Option{app.WithConsole(&bytes.Buffer{}), app.WithKeyring(nil)}, appOpts...)
	coreOpts = append(coreOpts, app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
		o := append([]mock.Option{mock.WithLogger(b.Logger), mock.WithPublisher(b.Publisher)}, opts...)
		mp, err = mock.New(b.Receiver, o...)
		return mp, err
	}))
	a, err := app.New(ctx, cfg, coreOpts...)
	require.NoError(t, err)

	coll := &eventCollector{}
	sub := a.Bus().Subscribe(ctx, event.WithPrefixes("command.instance.", "mock."), event.WithBuffer(256))
	go func() {
		for e := range sub.C() {
			coll.add(e)
		}
	}()
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond)
	// The supervisor reports StateRunning before the platform's Run sets its
	// connection, so Ready can be true while the platform still rejects chat
	// messages ("account not connected"); wait for the platform to connect.
	require.Eventually(t, func() bool { return mp.Status().Connected() },
		5*time.Second, 10*time.Millisecond, "the mock platform to connect")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return mp, coll
}

// TestChatHistoryAndProtocolE2E covers spec chat-service.md, B1, B13, B14
// and B17: a chat message fires its events, runs the command, and — with
// the protocol on — lands in the protocol file with the data of B13; the
// message the command sends is not recorded, only the incoming ones.
func TestChatHistoryAndProtocolE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	mp, coll := startedCore(t, cfg, chatCommands, settings.Chat{HistorySize: 200, Protocol: true})
	ctx := t.Context()

	sent, err := mp.Say(ctx, "alice", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	coll.waitFor(t, "the chat command to complete", func(e event.Envelope) bool { return instanceNamed(e, "Hello") })
	coll.waitFor(t, "the reply in the home channel", func(e event.Envelope) bool {
		return mockSent(e, "Hello alice!")
	})

	// The protocol is written as the pipeline takes the message, before
	// the command runs, so it is on disk by now; wait for it anyway.
	var entries []protocolEntry
	require.Eventually(t, func() bool {
		entries = protocolEntries(t, cfg.DataDir, "mock")
		return len(entries) == 1
	}, 5*time.Second, 10*time.Millisecond, "one protocol entry for the incoming message")

	// B13: the data of the message; the reply the command sent is not
	// recorded, only the incoming message (B14).
	assert.Equal(t, protocolEntry{
		Platform:  "mock",
		Name:      "alice",
		Text:      "!hello",
		MessageID: sent.ID,
	}, entries[0])
}

// TestMutedChatE2E covers spec chat-service.md, B21 to B23 and B31: while
// the muted chat is on, the core deletes the incoming messages and runs
// nothing for them; the messages of the streamer and of the bot run
// through, and nothing is deleted for them.
func TestMutedChatE2E(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t)
	mp, coll := startedCore(t, cfg, chatCommands, settings.DefaultChat(), mock.WithBot("bot"))
	ctx := t.Context()

	// mute the chat through the command
	sent, err := mp.Say(ctx, "alice", "!muteall")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	coll.waitFor(t, "the mute command to complete", func(e event.Envelope) bool { return instanceNamed(e, "MuteAll") })

	// a normal message is deleted and runs nothing
	sent, err = mp.Say(ctx, "bob", "!hello")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	coll.waitFor(t, "the delete of the muted message", func(e event.Envelope) bool { return mockOp(e, mock.OpDelete, sent.ID) })
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, coll.count(func(e event.Envelope) bool { return instanceNamed(e, "Hello") }),
		"B22: the muted message triggers nothing")

	// the streamer's message runs through, nothing is deleted for it (B31)
	sent, err = mp.Say(ctx, "streamer", "evening")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, coll.count(func(e event.Envelope) bool { return mockOp(e, mock.OpDelete, sent.ID) }),
		"B31: the streamer's message is not deleted")

	// the bot's message is not deleted either (B22)
	bot, ok := mp.Bot()
	require.True(t, ok, "the mock platform has a bot account")
	sent, err = mp.Say(ctx, bot.Login, "beep")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	time.Sleep(200 * time.Millisecond)
	assert.Zero(t, coll.count(func(e event.Envelope) bool { return mockOp(e, mock.OpDelete, sent.ID) }),
		"B22: the bot's message is not deleted")
}

// TestChatActionWhisperE2E covers spec chat-service.md, B4 and B10: the
// whisper of the chat action goes through the chat service to the
// recipient on the platform, found over the platform because the core does
// not know the user.
func TestChatActionWhisperE2E(t *testing.T) {
	t.Parallel()
	const whisperCommands = `apiVersion: streamcrew/v1alpha1
kind: ChatCommand
metadata: {name: Psst}
spec:
  triggers: [psst]
  actions:
    - {type: chat, kind: whisper, message: "psst", recipient: "bob"}
`
	cfg := testConfig(t)
	mp, coll := startedCore(t, cfg, whisperCommands, settings.DefaultChat())
	ctx := t.Context()
	_, err := mp.AddUser(mock.UserSpec{Login: "bob"})
	require.NoError(t, err)

	sent, err := mp.Say(ctx, "alice", "!psst")
	require.NoError(t, err)
	assert.Equal(t, mock.Delivered, sent.Delivery)
	coll.waitFor(t, "the whisper to reach the recipient", func(e event.Envelope) bool {
		if e.Type != mock.TypeOutput {
			return false
		}
		o, ok := event.Payload[mock.Output](e)
		return ok && o.Op == mock.OpWhisper && o.Target == "bob" && o.Text == "psst"
	})
}
