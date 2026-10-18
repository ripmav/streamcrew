// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// pollInterval is how often the dashboard polls the core status.
const pollInterval = 2 * time.Second

// retryInterval is the retry delay when the core is not reachable.
const retryInterval = 5 * time.Second

// Options configures the TUI.
type Options struct {
	// Server is the address of the core; empty resolves the runtime file
	// of the data directory and then the local default.
	Server string
	// DataDir is the data directory the runtime file is read from.
	DataDir string
	// Token is the API token of the core.
	Token string
	// Theme is "auto", "light" or "dark"; auto starts dark.
	Theme string
}

// statusMsg carries the status of the core.
type statusMsg struct {
	status *v1alpha1.StatusResponse
}

// statusErr carries the error of a failed status request.
type statusErr struct {
	err error
}

// pollMsg is the tick that triggers the next status request.
type pollMsg time.Time

// streamMsg carries one event of the live stream.
type streamMsg struct {
	event *v1alpha1.Event
}

// clients bundles the API clients of the TUI.
type clients struct {
	system  streamcrewv1alpha1connect.SystemServiceClient
	stream  streamcrewv1alpha1connect.StreamServiceClient
	chat    streamcrewv1alpha1connect.ChatServiceClient
	command streamcrewv1alpha1connect.CommandServiceClient
	user    streamcrewv1alpha1connect.UserServiceClient
	config  streamcrewv1alpha1connect.ConfigServiceClient
}

// newClients builds the clients for the core at base.
func newClients(httpClient *http.Client, base, token string) *clients {
	o := bearerOption(token)
	return &clients{
		system:  streamcrewv1alpha1connect.NewSystemServiceClient(httpClient, base, o...),
		stream:  streamcrewv1alpha1connect.NewStreamServiceClient(httpClient, base, o...),
		chat:    streamcrewv1alpha1connect.NewChatServiceClient(httpClient, base, o...),
		command: streamcrewv1alpha1connect.NewCommandServiceClient(httpClient, base, o...),
		user:    streamcrewv1alpha1connect.NewUserServiceClient(httpClient, base, o...),
		config:  streamcrewv1alpha1connect.NewConfigServiceClient(httpClient, base, o...),
	}
}

// commandsLoadedMsg carries the result of a command list fetch.
type commandsLoadedMsg struct {
	list []*v1alpha1.CommandSummary
	err  error
}

// usersLoadedMsg carries the result of a user list fetch.
type usersLoadedMsg struct {
	list []*v1alpha1.UserView
	err  error
}

// queueLoadedMsg carries the result of a queue fetch.
type queueLoadedMsg struct {
	list []*v1alpha1.CommandInstanceView
	err  error
}

// actionDoneMsg confirms a queue action (pause, cancel, retry).
type actionDoneMsg struct {
	err error
}

// model is the tea.Model of the TUI.
type model struct {
	server string
	token  string
	// ctx is canceled when the program quits, ending the stream.
	ctx context.Context

	width  int
	height int

	dark bool
	view int
	help bool

	status *v1alpha1.StatusResponse
	err    error

	cl       *clients
	streamCh chan streamMsg

	chat     chatView
	feed     feedView
	logs     feedView
	logMin   slog.Level // the lowest level the logs view shows (key l)
	queue    queueView
	commands commandsView
	users    usersView
	settings settingsView
}

// views is the order of the views, as on the key row.
//
//nolint:gochecknoglobals // static table of views
var views = []string{"dashboard", "chat", "events", "queue", "commands", "users", "logs", "settings"}

// newModel returns the initial model.
func newModel(opts Options) model {
	base := server(opts.DataDir, opts.Server)
	httpClient, base := httpClientFor(base)
	return model{
		server:   base,
		token:    opts.Token,
		ctx:      context.Background(),
		dark:     opts.Theme != "light",
		cl:       newClients(httpClient, base, opts.Token),
		streamCh: make(chan streamMsg, 64),
		logMin:   slog.LevelDebug, // "all": nothing is logged below debug
	}
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd {
	return m.boot()
}

// boot starts the stream subscription and the first status poll.
func (m model) boot() tea.Cmd {
	return func() tea.Msg {
		go m.subscribeLoop()
		return m.fetchStatus()
	}
}

// fetchStatus performs one status request and returns the message.
func (m model) fetchStatus() tea.Msg {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()
	resp, err := m.cl.system.Status(ctx, connect.NewRequest(&v1alpha1.StatusRequest{}))
	if err != nil {
		return statusErr{err: err}
	}
	return statusMsg{status: resp.Msg}
}

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case pollMsg:
		return m, m.fetchStatus

	case statusMsg:
		m.status = msg.status
		m.err = nil
		return m, tea.Batch(m.poll(), m.nextStream())

	case statusErr:
		m.err = msg.err
		return m, tea.Every(retryInterval, func(t time.Time) tea.Msg { return pollMsg(t) })

	case streamMsg:
		m.handleEvent(msg.event)
		return m, m.nextStream()

	case commandsLoadedMsg:
		if msg.list != nil {
			m.commands.list = msg.list
		}
		return m, nil

	case usersLoadedMsg:
		if msg.list != nil {
			m.users.list = msg.list
		}
		return m, nil

	case queueLoadedMsg:
		if msg.list != nil {
			m.queue.instances = msg.list
			if m.queue.selected >= len(m.queue.instances) {
				m.queue.selected = 0
			}
		}
		return m, nil

	case configLoadedMsg:
		m.settings.loaded = true
		m.settings.err = msg.err
		if msg.resp != nil {
			m.settings.document = msg.resp.GetDocument()
			m.settings.hot = msg.resp.GetHot()
			m.settings.fixed = msg.resp.GetFixed()
			m.settings.restart = msg.resp.GetRestart()
			m.settings.live = msg.resp.GetLive()
		}
		return m, nil

	case configUpdatedMsg:
		m.settings.err = msg.err
		if msg.err == nil {
			m.settings.document = msg.document
			m.settings.result = "applied: " + strings.Join(msg.applied, ", ")
			if len(msg.restart) > 0 {
				m.settings.result += " · restart: " + strings.Join(msg.restart, ", ")
			}
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.err = msg.err
		}
		return m, nil

	case tea.KeyMsg:
		return m.updateKey(msg.Key())
	}
	return m, nil
}

// poll schedules the next status request.
func (m model) poll() tea.Cmd {
	return tea.Every(pollInterval, func(t time.Time) tea.Msg { return pollMsg(t) })
}

// nextStream waits for the next stream event.
func (m model) nextStream() tea.Cmd {
	return func() tea.Msg {
		s := <-m.streamCh
		return s
	}
}

// subscribeLoop forwards the live stream into the model until the context
// ends or the stream breaks.
func (m model) subscribeLoop() {
	stream, err := m.cl.stream.Subscribe(m.ctx, connect.NewRequest(&v1alpha1.SubscribeRequest{}))
	if err != nil {
		return
	}
	defer stream.Close()
	for {
		if !stream.Receive() {
			return
		}
		select {
		case m.streamCh <- streamMsg{event: stream.Msg()}:
		default: // the UI is too slow; drop the event
		}
	}
}

// handleEvent routes one stream event into the view buffers.
func (m *model) handleEvent(e *v1alpha1.Event) {
	switch e.GetType() {
	case "chat.message":
		if e.GetChat() != nil {
			m.chat.appendChat(e.GetChat(), e.GetPlatform())
		}
	case "log":
		if l := e.GetLog(); l != nil {
			level, err := parseLevel(l.GetLevel())
			if err == nil && level >= m.logMin {
				m.logs.append(l.GetLevel(), "["+l.GetComponent()+"] "+l.GetMessage())
			}
		}
	case "feed":
		if f := e.GetFeed(); f != nil {
			text := f.GetLogin()
			if f.GetText() != "" {
				text += ": " + f.GetText()
			}
			m.feed.append(f.GetType(), text)
		}
	case "command.instance":
		if c := e.GetCommand(); c != nil {
			m.feed.append("command", c.GetCommandName()+" → "+c.GetState())
			m.queue.instances = append(m.queue.instances, cToView(c))
			if len(m.queue.instances) > maxLines {
				m.queue.instances = m.queue.instances[len(m.queue.instances)-maxLines:]
			}
		}
	case "platform.status":
		if p := e.GetPlatformStatus(); p != nil {
			state := "offline"
			if p.GetConnected() {
				state = "online"
			}
			m.feed.append("platform", p.GetPlatform()+" "+state)
		}
	case "prompt":
		if p := e.GetPrompt(); p != nil {
			m.feed.append("prompt", p.GetText())
		}
	}
}

// refreshQueue returns the command that refetches the command instances.
func (m model) refreshQueue() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		resp, err := m.cl.command.ListInstances(ctx, connect.NewRequest(&v1alpha1.ListInstancesRequest{}))
		var list []*v1alpha1.CommandInstanceView
		if err == nil {
			list = resp.Msg.GetInstances()
		}
		return queueLoadedMsg{list: list, err: err}
	}
}

// cToView maps the stream's CommandInstance to the queue view type.
func cToView(c *v1alpha1.CommandInstance) *v1alpha1.CommandInstanceView {
	return &v1alpha1.CommandInstanceView{
		InstanceId:  c.GetInstanceId(),
		CommandName: c.GetCommandName(),
		State:       c.GetState(),
		Source:      c.GetSource(),
		Channel:     c.GetChannel(),
		Error:       c.GetError(),
	}
}

// updateKey handles a key press.
func (m model) updateKey(key tea.Key) (tea.Model, tea.Cmd) {
	if m.help {
		if key.Code == 'q' || key.Code == '?' || key.Code == tea.KeyEsc {
			m.help = false
		}
		return m, nil
	}
	// the digit cases must stay in sync with views.
	switch key.Code {
	case 'q':
		return m, tea.Quit
	case '?':
		m.help = true
		return m, nil
	case 't':
		m.dark = !m.dark
		return m, nil
	case tea.KeyTab:
		m.view = (m.view + 1) % len(views)
		return m, m.onViewEnter()
	case '1', '2', '3', '4', '5', '6', '7', '8':
		if len(key.Text) == 1 {
			next := m.view
			m.view = int(key.Code - '1')
			if next != m.view {
				return m, m.onViewEnter()
			}
		}
		return m, nil
	}
	return m.updateViewKey(key)
}

// onViewEnter fetches the data of the newly active view.
func (m model) onViewEnter() tea.Cmd {
	switch views[m.view] {
	case "queue":
		return m.refreshQueue()
	case "commands":
		return m.fetchCommands()
	case "users":
		return m.fetchUsers()
	case "settings":
		return m.fetchConfig()
	}
	return nil
}

// updateViewKey passes the key to the active view.
func (m model) updateViewKey(key tea.Key) (tea.Model, tea.Cmd) {
	switch views[m.view] {
	case "chat":
		return m.updateChatKey(key)
	case "queue":
		return m.updateQueueKey(key)
	case "commands":
		return m.updateCommandsKey(key)
	case "users":
		return m.updateUsersKey(key)
	case "logs":
		return m.updateLogsKey(key)
	case "settings":
		return m.updateSettingsKey(key)
	}
	return m, nil
}

// configLoadedMsg carries the result of a configuration fetch.
type configLoadedMsg struct {
	resp *v1alpha1.GetConfigResponse
	err  error
}

// configUpdatedMsg carries the result of a configuration update.
type configUpdatedMsg struct {
	applied  []string
	restart  []string
	document string
	err      error
}

// fetchConfig returns the command that loads the configuration document.
func (m model) fetchConfig() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		resp, err := m.cl.config.Get(ctx, connect.NewRequest(&v1alpha1.GetConfigRequest{}))
		if err != nil {
			return configLoadedMsg{err: err}
		}
		return configLoadedMsg{resp: resp.Msg}
	}
}

// fetchCommands returns the command that loads the command list.
func (m model) fetchCommands() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		resp, err := m.cl.command.List(ctx, connect.NewRequest(&v1alpha1.ListCommandsRequest{}))
		var list []*v1alpha1.CommandSummary
		if err == nil {
			list = resp.Msg.GetCommands()
		}
		return commandsLoadedMsg{list: list, err: err}
	}
}

// fetchUsers returns the command that loads the user list.
func (m model) fetchUsers() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		resp, err := m.cl.user.List(ctx, connect.NewRequest(&v1alpha1.ListUsersRequest{
			Limit: 100,
		}))
		var list []*v1alpha1.UserView
		if err == nil {
			list = resp.Msg.GetUsers()
		}
		return usersLoadedMsg{list: list, err: err}
	}
}
