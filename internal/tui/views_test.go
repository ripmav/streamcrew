// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/mock"
	"github.com/ripmav/streamcrew/internal/profile"
)

// testToken is the admin token the in-process test cores start with
// (bootstrap token, ADR-0013 §5); the test clients send it as bearer.
const testToken = "tui-test-token"

// startMockCore starts an in-process core with the mock platform and
// returns its base URL.
func startMockCore(t *testing.T) string {
	return startMockCoreWithConfigFile(t, "", config.ModeDaemon)
}

// startMockCoreServer starts an in-process core in the server mode with a
// configuration file and returns its base URL (roadmap 6.5.3).
func startMockCoreServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(path, []byte("log_level: info\n"), 0o600))
	return startMockCoreWithConfigFile(t, path, config.ModeServer)
}

func startMockCoreWithConfigFile(t *testing.T, cfgFile string, mode config.Mode) string {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:         dir,
		Mode:            mode,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		BootstrapToken:  testToken,
		Stream:          config.StreamConfig{Buffer: 1024, CatchUp: 256},
		Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1, MaxFiles: 1},
	}
	var opts []app.Option
	opts = append(opts, app.WithConsole(io.Discard), app.WithKeyring(nil))
	if f := cfgFile; f != "" {
		fr := config.NewFileResolver(f)
		require.NoError(t, fr.Load(f))
		opts = append(opts, app.WithConfigFile(fr))
	}
	a, err := startCoreOpts(t, cfg, opts...)
	require.NoError(t, err)
	return "http://" + mustServerAddr(t, a)
}

// startCore starts the core with the platform and waits for it to be
// ready.
func startCoreOpts(t *testing.T, cfg config.Config, opts ...app.Option) (*app.App, error) {
	t.Helper()
	ctx := t.Context()
	if cfg.Profile == "" {
		mgr := profile.NewManager(cfg.DataDir)
		_, err := mgr.Create(ctx, "Demo", "demo")
		require.NoError(t, err)
		cfg.Profile = "demo"
	}
	var mp *mock.Platform
	var mpErr error
	// The logger is passed so that the "mock output" lines of the mock
	// platform reach the log sink and the live stream: the TUI tests use
	// them to prove that an API call reached the core.
	opts = append(opts, app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
		var err error
		mp, err = mock.New(b.Receiver, mock.WithPublisher(b.Publisher), mock.WithLogger(b.Logger))
		mpErr = err
		return mp, err
	}))
	a, err := app.New(ctx, cfg, opts...)
	if err != nil {
		return nil, err
	}
	if mpErr != nil {
		return nil, mpErr
	}
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond, "the core to be ready")
	require.Eventually(t, func() bool { return mp != nil && mp.Status().Connected() }, 5*time.Second, 10*time.Millisecond, "the mock platform to connect")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return a, nil
}

// mockConsoleExampleCommands are the example commands as code for the views.
const mockConsoleExampleCommands = "apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hello}\nspec:\n  triggers: [hello]\n  actions:\n    - {type: chat, kind: message, message: \"Hello $userdisplayname!\"}\n---\napiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Bye}\nspec:\n  triggers: [bye]\n  actions:\n    - {type: chat, kind: message, message: \"Bye $userdisplayname!\"}\n"

// importExampleCommands imports the example commands into the core.
func importExampleCommands(t *testing.T, base string) {
	t.Helper()
	client, base := httpClientFor(base)
	cmdClient := streamcrewv1alpha1connect.NewCommandServiceClient(connect.NewClient(connecthttp.NewTransport(client, base, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := cmdClient.Import(ctx, &v1alpha1.ImportCommandsRequest{
		Document: mockConsoleExampleCommands,
		Format:   "yaml",
	})
	require.NoError(t, err)
}

// TestStreamEventsFillTheViews routes stream events into the view buffers.
func TestStreamEventsFillTheViews(t *testing.T) {
	t.Parallel()
	m := newModel(Options{Server: "http://127.0.0.1:1"})

	m.handleEvent(&v1alpha1.Event{
		Type:     "chat.message",
		Platform: "mock",
		Payload:  &v1alpha1.Event_Chat{Chat: &v1alpha1.ChatMessage{Login: "alice", Text: "hi"}},
	})
	m.handleEvent(&v1alpha1.Event{
		Type:    "feed",
		Payload: &v1alpha1.Event_Feed{Feed: &v1alpha1.FeedItem{Type: "channel.follow", Login: "dave"}},
	})
	m.handleEvent(&v1alpha1.Event{
		Type:    "log",
		Payload: &v1alpha1.Event_Log{Log: &v1alpha1.LogEntry{Level: "info", Component: "engine", Message: "started"}},
	})
	m.handleEvent(&v1alpha1.Event{
		Type: "command.instance",
		Payload: &v1alpha1.Event_Command{Command: &v1alpha1.CommandInstance{
			InstanceId:  "i1",
			CommandName: "Hello",
			State:       "running",
			Source:      "chat",
		}},
	})
	m.handleEvent(&v1alpha1.Event{
		Type:    "platform.status",
		Payload: &v1alpha1.Event_PlatformStatus{PlatformStatus: &v1alpha1.PlatformStatus{Platform: "mock", Connected: true}},
	})
	m.handleEvent(&v1alpha1.Event{
		Type:    "prompt",
		Payload: &v1alpha1.Event_Prompt{Prompt: &v1alpha1.Prompt{Kind: "information", Text: "info"}},
	})

	require.Len(t, m.chat.lines, 1)
	assert.Equal(t, "alice", m.chat.lines[0].login)
	require.Len(t, m.feed.lines, 4)
	assert.Equal(t, "channel.follow", m.feed.lines[0].label)
	require.Len(t, m.logs.lines, 1)
	assert.Contains(t, m.logs.lines[0].text, "engine")
	require.Len(t, m.queue.instances, 1)
	assert.Equal(t, "Hello", m.queue.instances[0].GetCommandName())

	// the rendering shows the content
	t2 := newTheme(true)
	m.width = 100
	assert.Contains(t, m.chat.render(t2, 90), "alice: hi")
	assert.Contains(t, m.feed.render(t2, "Events", 90), "dave")
	assert.Contains(t, m.logs.render(t2, "Logs", 90), "started")
	assert.Contains(t, m.queue.render(t2, 90), "Hello")
}

// TestChatInputAndSend types a message and sends it through the API: the
// mock core logs the operation, the log event arrives on the stream.
func TestChatInputAndSend(t *testing.T) {
	t.Parallel()
	tm, base := newTestModelWithCore(t)
	sub := subscribeStream(t, base)

	tm.Type("2") // chat view
	tm.Type("hallox")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace}) // the wrong letter is undone
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	waitForLogEvent(t, sub, "mock output") // the core received the send
	m := quitModel(t, tm)
	assert.Equal(t, "", m.chat.input, "the input is cleared after Enter")
	assert.NoError(t, m.err)
}

// TestChatDeleteAndClear covers the moderation keys of the chat view.
func TestChatDeleteAndClear(t *testing.T) {
	t.Parallel()
	t.Run("clear", func(t *testing.T) {
		t.Parallel()
		tm, base := newTestModelWithCore(t)
		sub := subscribeStream(t, base)
		tm.Type("2") // chat view
		tm.Type("c")
		waitForLogEvent(t, sub, "mock output") // the core received the clear
		m := quitModel(t, tm)
		assert.NoError(t, m.err)
	})
	t.Run("delete", func(t *testing.T) {
		// The TUI sends the delete request without a platform and the core
		// rejects it. This pins the current behavior; sending the platform
		// is a separate issue. The error is transient (a successful status
		// poll clears it), so press d a few times: at least one error frame
		// is rendered between the polls.
		tm, _ := newTestModelWithCore(t)
		tm.Type("2") // chat view
		for range 3 {
			tm.Type("d")
			time.Sleep(100 * time.Millisecond)
		}
		teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
			return strings.Contains(string(b), "platform is required")
		}, teatest.WithDuration(10*time.Second))
		quitModel(t, tm)
	})
}

// TestQueueActions drives the queue view through the program loop: p pauses
// the queue, enter (in the commands view) queues a command that stays held
// while paused, x cancels the selected instance and r retries it.
func TestQueueActions(t *testing.T) {
	t.Parallel()
	tm, base := newTestModelWithCore(t)
	importExampleCommands(t, base)
	cmds := commandClient(t, base)
	ctx := t.Context()
	list := func() *v1alpha1.ListInstancesResponse {
		resp, err := cmds.ListInstances(ctx, &v1alpha1.ListInstancesRequest{})
		require.NoError(t, err)
		return resp
	}

	// pause the queue first, so the instance we run next waits in the queue
	// (a stable, cancellable state)
	tm.Type("4") // queue view
	waitOutput(t, tm, "Command Queue")
	tm.Type("p")
	require.Eventually(t, func() bool { return list().GetPaused() }, 10*time.Second, 50*time.Millisecond, "the queue to be paused")

	// run a command: it stays pending while the queue is paused
	tm.Type("5")               // commands view
	waitOutput(t, tm, "Hello") // the list is loaded
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Eventually(t, func() bool {
		insts := list().GetInstances()
		return len(insts) == 1 && insts[0].GetState() == "pending"
	}, 10*time.Second, 50*time.Millisecond, "the instance to wait in the paused queue")

	// the queue view shows the pending instance; x cancels the selected one
	tm.Type("4")
	waitOutput(t, tm, "pending")
	tm.Type("x")
	require.Eventually(t, func() bool {
		insts := list().GetInstances()
		return len(insts) == 1 && insts[0].GetState() == "canceled"
	}, 10*time.Second, 50*time.Millisecond, "the instance to be canceled")

	// resume and retry: the instance runs and completes
	tm.Type("p")
	require.Eventually(t, func() bool { return !list().GetPaused() }, 10*time.Second, 50*time.Millisecond, "the queue to be resumed")
	tm.Type("r")
	require.Eventually(t, func() bool {
		for _, i := range list().GetInstances() {
			if i.GetState() == "completed" {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "the retried instance to complete")

	m := quitModel(t, tm)
	assert.NoError(t, m.err)
}

// TestCommandsViewFetchesFiltersAndEdits covers the commands view. The
// parent test does not run in parallel: the edit subtest sets EDITOR.
func TestCommandsViewFetchesFiltersAndEdits(t *testing.T) {
	t.Run("filter", func(t *testing.T) {
		t.Parallel()
		tm, base := newTestModelWithCore(t)
		importExampleCommands(t, base)
		tm.Type("5")               // commands view
		waitOutput(t, tm, "Hello") // the list is loaded
		tm.Type("/")
		tm.Type("he")
		tm.Send(tea.KeyPressMsg{Code: tea.KeyBackspace}) // the filter is "h"
		m := quitModel(t, tm)
		assert.True(t, m.commands.filtering)
		assert.Equal(t, "h", m.commands.filter)
		// the list is narrowed to the commands matching "h"
		assert.Contains(t, commandNames(m.commands.list), "Hello")
		assert.NotContains(t, commandNames(m.commands.list), "Bye")
	})
	t.Run("esc", func(t *testing.T) {
		t.Parallel()
		tm, base := newTestModelWithCore(t)
		importExampleCommands(t, base)
		tm.Type("5")               // commands view
		waitOutput(t, tm, "Hello") // the list is loaded
		tm.Type("/")
		tm.Type("he")
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc})
		m := quitModel(t, tm)
		assert.False(t, m.commands.filtering)
		assert.Equal(t, "", m.commands.filter)
		// the full list is restored
		assert.Contains(t, commandNames(m.commands.list), "Hello")
		assert.Contains(t, commandNames(m.commands.list), "Bye")
	})
	t.Run("edit", func(t *testing.T) {
		// no t.Parallel(): the test sets the EDITOR environment variable.
		// The editor script flips the enabled flag of the command it opens,
		// so the flag in the API is the completion signal: it is set only
		// after the editor ran and the model wrote the file back. (A
		// trailing YAML comment is not a usable signal — the document
		// round-trips through the command-file converter, which drops
		// comments.)
		script := filepath.Join(t.TempDir(), "editor.sh")
		require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nsed -i '0,/enabled: true/s//enabled: false/' \"$1\"\n"), 0o755)) //nolint:gosec // test-only editor script
		t.Setenv("EDITOR", script)
		tm, base := newTestModelWithCore(t)
		importExampleCommands(t, base)

		tm.Type("5")               // commands view
		waitOutput(t, tm, "Hello") // the list is loaded
		before, _ := filepath.Glob(filepath.Join(os.TempDir(), "streamcrew-command-*.yaml"))
		tm.Send(tea.KeyPressMsg{Code: 'E'})
		require.Eventually(t, func() bool {
			files, _ := filepath.Glob(filepath.Join(os.TempDir(), "streamcrew-command-*.yaml"))
			for _, f := range files {
				if !slices.Contains(before, f) {
					return true
				}
			}
			return false
		}, 10*time.Second, 50*time.Millisecond, "the editor to open the fetched document")
		// Wait until the model wrote the edited file back: one of the
		// imported commands is disabled in the API.
		cc := commandClient(t, base)
		var disabled string
		require.Eventually(t, func() bool {
			list, err := cc.List(t.Context(), &v1alpha1.ListCommandsRequest{})
			if err != nil {
				return false
			}
			for _, c := range list.GetCommands() {
				if (c.GetName() == "Hello" || c.GetName() == "Bye") && !c.GetEnabled() {
					disabled = c.GetName()
					return true
				}
			}
			return false
		}, 10*time.Second, 50*time.Millisecond, "the edited command to be stored disabled")
		// The key presses are queued behind the actionDoneMsg of the edit
		// (the one that clears the editing flag), so once the program
		// caught up with them the final model carries the result of the
		// edit. (tm.Send is an async program.Send; a single key press does
		// not order the quit against the message of the edit.)
		for range 8 {
			tm.Send(tea.KeyPressMsg{Code: tea.KeyUp})
			time.Sleep(25 * time.Millisecond)
			tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
			time.Sleep(25 * time.Millisecond)
		}
		m := quitModel(t, tm)
		assert.NoError(t, m.err)
		assert.NotEmpty(t, disabled, "an imported command to be the edited one")
		// the actionDoneMsg cleared the editing flag again
		assert.False(t, m.commands.editing)
	})
	t.Run("edit status", func(t *testing.T) {
		// no t.Parallel(): the test sets the EDITOR environment variable.
		// The editor "yes" blocks until the 5s editor context kills it, so
		// while it is open the view shows the editing status; the flag is
		// still set when the program quits, which is the state the test
		// asserts.
		t.Setenv("EDITOR", "yes")
		tm, base := newTestModelWithCore(t)
		importExampleCommands(t, base)

		tm.Type("5")               // commands view
		waitOutput(t, tm, "Hello") // the list is loaded
		tm.Send(tea.KeyPressMsg{Code: 'E'})
		waitOutput(t, tm, "editing in $EDITOR") // the flag renders while editing
		m := quitModel(t, tm)
		assert.True(t, m.commands.editing)
	})
	t.Run("enable", func(t *testing.T) {
		t.Parallel()
		tm, base := newTestModelWithCore(t)
		importExampleCommands(t, base)
		cmds := commandClient(t, base)
		resp, err := cmds.List(t.Context(), &v1alpha1.ListCommandsRequest{})
		require.NoError(t, err)
		require.NotEmpty(t, resp.GetCommands())
		id := resp.GetCommands()[0].GetId()
		enabled := resp.GetCommands()[0].GetEnabled()

		tm.Type("5")               // commands view
		waitOutput(t, tm, "Hello") // the list is loaded
		tm.Type("e")
		require.Eventually(t, func() bool {
			resp, err := cmds.List(t.Context(), &v1alpha1.ListCommandsRequest{})
			if err != nil {
				return false
			}
			for _, c := range resp.GetCommands() {
				if c.GetId() == id {
					return c.GetEnabled() != enabled
				}
			}
			return false
		}, 10*time.Second, 50*time.Millisecond, "the command to be toggled")
		m := quitModel(t, tm)
		assert.NoError(t, m.err)
	})
}

// TestUsersViewRendersTheList covers the users view with the stream.
func TestUsersViewRendersTheList(t *testing.T) {
	t.Parallel()
	tm, base := newTestModelWithCore(t)

	// some chat activity creates users
	_, err := streamClient(t, base).Simulate(t.Context(), &v1alpha1.SimulateEventRequest{
		EventType: "chat.message",
		User:      "alice",
		Text:      "hallo",
	})
	require.NoError(t, err)
	// wait until the user service lists alice before the view fetches
	require.Eventually(t, func() bool {
		client, b := httpClientFor(base)
		users := streamcrewv1alpha1connect.NewUserServiceClient(connect.NewClient(connecthttp.NewTransport(client, b, connecthttp.WithReadMaxBytes(0)), bearerInterceptor(testToken)))
		resp, err := users.List(t.Context(), &v1alpha1.ListUsersRequest{Limit: 100})
		if err != nil {
			return false
		}
		for _, u := range resp.GetUsers() {
			for _, i := range u.GetIdentities() {
				if i.GetLogin() == "alice" {
					return true
				}
			}
		}
		return false
	}, 10*time.Second, 100*time.Millisecond, "the simulated user to be listed")

	tm.Type("6") // users view
	waitOutput(t, tm, "alice")
	m := quitModel(t, tm)
	found := false
	for _, u := range m.users.list {
		for _, i := range u.GetIdentities() {
			if i.GetLogin() == "alice" {
				found = true
			}
		}
	}
	assert.True(t, found, "alice is in the users list")
}

// mustServerAddr waits for the core's http server to listen and returns
// its address: the core is ready while the first listener is still coming
// up, so the address can lag readiness by a moment.
func mustServerAddr(t *testing.T, a *app.App) string {
	t.Helper()
	require.Eventually(t, func() bool {
		_, ok := a.HTTPServer().Addr()
		return ok
	}, 10*time.Second, 10*time.Millisecond, "the http server to listen")
	addr, _ := a.HTTPServer().Addr()
	return addr.String()
}
