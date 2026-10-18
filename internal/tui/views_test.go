// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

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

// startMockCore starts an in-process core with the mock platform and
// returns its base URL.
func startMockCore(t *testing.T) string {
	t.Helper()
	ctx := t.Context()
	dir := t.TempDir()
	cfg := config.Config{
		DataDir:         dir,
		Mode:            config.ModeDaemon,
		Listen:          "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
		Log:             config.LogConfig{Level: "info", Format: "text", MaxSize: 1, MaxFiles: 1},
	}
	require.NoError(t, cfg.Resolve())
	mgr := profile.NewManager(dir)
	_, err := mgr.Create(ctx, "Demo", "demo")
	require.NoError(t, err)
	cfg.Profile = "demo"

	var mp *mock.Platform
	a, err := app.New(ctx, cfg,
		app.WithConsole(io.Discard), app.WithKeyring(nil),
		app.WithPlatform(func(_ context.Context, b app.PlatformBuilder) (connector.Platform, error) {
			mp, err = mock.New(b.Receiver, mock.WithPublisher(b.Publisher))
			return mp, err
		}),
	)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	errc := make(chan error, 1)
	go func() { errc <- a.Run(runCtx) }()
	require.Eventually(t, a.Ready, 10*time.Second, 10*time.Millisecond, "the core to be ready")
	require.Eventually(t, func() bool { return mp.Status().Connected() }, 5*time.Second, 10*time.Millisecond, "the mock platform to connect")
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-errc)
	})
	return "http://" + mustServerAddr(t, a)
}

// mockConsoleExampleCommands are the example commands as code for the views.
const mockConsoleExampleCommands = "apiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Hello}\nspec:\n  triggers: [hello]\n  actions:\n    - {type: chat, kind: message, message: \"Hello $userdisplayname!\"}\n---\napiVersion: streamcrew/v1alpha1\nkind: ChatCommand\nmetadata: {name: Bye}\nspec:\n  triggers: [bye]\n  actions:\n    - {type: chat, kind: message, message: \"Bye $userdisplayname!\"}\n"

// importExampleCommands imports the example commands into the core.
func importExampleCommands(t *testing.T, base string) {
	t.Helper()
	client, base := httpClientFor(base)
	cmdClient := streamcrewv1alpha1connect.NewCommandServiceClient(client, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := cmdClient.Import(ctx, connect.NewRequest(&v1alpha1.ImportCommandsRequest{
		Document: mockConsoleExampleCommands,
		Format:   "yaml",
	}))
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

// TestChatInputAndSend types a message and sends it through the API.
func TestChatInputAndSend(t *testing.T) {
	t.Parallel()
	base := startMockCore(t)
	m := newModel(Options{Server: base})
	m.view = 1 // chat

	// type "hallo" with a wrong letter at the end
	var next tea.Model
	for _, k := range []string{"h", "a", "l", "l", "o", "x"} {
		next, _ = m.Update(&keyMsg{code: rune(k[0]), text: k})
		m = next.(model)
	}
	next, _ = m.Update(&keyMsg{code: tea.KeyBackspace})
	m = next.(model)
	assert.Equal(t, "hallo", m.chat.input)

	// Enter sends the message and clears the input
	next, cmd := m.Update(&keyMsg{code: tea.KeyEnter})
	m = next.(model)
	require.NotNil(t, cmd)
	require.IsType(t, actionDoneMsg{}, cmd())
	assert.NoError(t, cmd().(actionDoneMsg).err)
	assert.Equal(t, "", m.chat.input)
}

// TestQueueActions runs a command and checks the queue view.
func TestQueueActions(t *testing.T) {
	t.Parallel()
	base := startMockCore(t)
	importExampleCommands(t, base)
	m := newModel(Options{Server: base})

	// run the first command through the API
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, base := httpClientFor(base)
	cmdClient := streamcrewv1alpha1connect.NewCommandServiceClient(client, base)
	list, err := cmdClient.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{}))
	require.NoError(t, err)
	require.NotEmpty(t, list.Msg.GetCommands())
	_, err = cmdClient.Run(ctx, connect.NewRequest(&v1alpha1.RunCommandRequest{Id: list.Msg.GetCommands()[0].GetId()}))
	require.NoError(t, err)

	// the queue fills up from the stream or the fetch
	require.Eventually(t, func() bool {
		resp, err := cmdClient.ListInstances(ctx, connect.NewRequest(&v1alpha1.ListInstancesRequest{}))
		if err != nil {
			return false
		}
		m.queue.instances = resp.Msg.GetInstances()
		return len(m.queue.instances) > 0
	}, 5*time.Second, 50*time.Millisecond, "the queue to fill")

	// navigation and cancel do not panic and stay consistent
	next, _ := m.Update(&keyMsg{code: tea.KeyDown})
	m = next.(model)
	next, _ = m.Update(&keyMsg{code: 'x', text: "x"})
	m = next.(model)
	assert.LessOrEqual(t, m.queue.selected, len(m.queue.instances))
}

// TestCommandsViewFetchesFiltersAndEdits covers the commands view.
func TestCommandsViewFetchesFiltersAndEdits(t *testing.T) {
	// no t.Parallel(): the test sets the EDITOR environment variable
	base := startMockCore(t)
	importExampleCommands(t, base)
	m := newModel(Options{Server: base})
	m.view = 4 // commands

	// fetch the list
	fetched := m.fetchCommands()()
	require.IsType(t, commandsLoadedMsg{}, fetched)
	var next tea.Model
	next, _ = m.Update(fetched)
	m = next.(model)
	names := map[string]bool{}
	for _, c := range m.commands.list {
		names[c.GetName()] = true
	}
	assert.True(t, names["Hello"] && names["Bye"], "the imported commands are listed")

	// the filter mode narrows the list
	next, _ = m.Update(&keyMsg{code: '/', text: "/"})
	m = next.(model)
	require.True(t, m.commands.filtering)
	for _, k := range []string{"h", "e"} {
		next, _ = m.Update(&keyMsg{code: rune(k[0]), text: k})
		m = next.(model)
	}
	next, _ = m.Update(&keyMsg{code: tea.KeyBackspace})
	m = next.(model)
	assert.Equal(t, "h", m.commands.filter)
	require.NotEmpty(t, m.commands.list)
	next, _ = m.Update(&keyMsg{code: tea.KeyEsc})
	m = next.(model)
	assert.False(t, m.commands.filtering)
	assert.Equal(t, "", m.commands.filter)

	// edit with a no-op editor leaves the command unchanged
	t.Setenv("EDITOR", "true")
	m.commands.filter = ""
	m.applyCommandFilter()
	id := m.commands.list[0].GetId()
	done := m.editCommand(id)()
	require.IsType(t, actionDoneMsg{}, done)
	assert.NoError(t, done.(actionDoneMsg).err)

	// the enable switch flips
	next, cmd := m.Update(&keyMsg{code: 'e', text: "e"})
	m = next.(model)
	require.NotNil(t, cmd)
	done = cmd()
	require.IsType(t, actionDoneMsg{}, done)
	assert.NoError(t, done.(actionDoneMsg).err)
}

// TestUsersViewRendersTheList covers the users view with the stream.
func TestUsersViewRendersTheList(t *testing.T) {
	t.Parallel()
	base := startMockCore(t)
	m := newModel(Options{Server: base})

	// some chat activity creates users
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, base := httpClientFor(base)
	stream := streamcrewv1alpha1connect.NewStreamServiceClient(client, base)
	_, err := stream.Simulate(ctx, connect.NewRequest(&v1alpha1.SimulateEventRequest{
		EventType: "chat.message",
		User:      "alice",
		Text:      "hallo",
	}))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		fetched := m.fetchUsers()()
		require.IsType(t, usersLoadedMsg{}, fetched)
		var next tea.Model
		next, _ = m.Update(fetched)
		m = next.(model)
		for _, u := range m.users.list {
			for _, i := range u.GetIdentities() {
				if i.GetLogin() == "alice" {
					return true
				}
			}
		}
		return false
	}, 5*time.Second, 100*time.Millisecond, "the simulated user to be listed")
	assert.Contains(t, m.users.render(newTheme(true), 90), "alice")
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
