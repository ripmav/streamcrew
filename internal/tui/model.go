// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
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

// model is the tea.Model of the TUI.
type model struct {
	server string
	token  string

	width  int
	height int

	dark bool
	view int
	help bool

	status *v1alpha1.StatusResponse
	err    error
	cmd    tea.Cmd
}

// views is the order of the views, as on the key row.
//
//nolint:gochecknoglobals // static table of views
var views = []string{"dashboard", "chat", "events", "queue", "commands", "users", "logs"}

// newModel returns the initial model.
func newModel(opts Options) model {
	dark := opts.Theme != "light"
	m := model{
		server: server(opts.DataDir, opts.Server),
		token:  opts.Token,
		dark:   dark,
	}
	m.cmd = m.fetch()
	return m
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd {
	return m.cmd
}

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case pollMsg:
		return m, m.fetch()

	case statusMsg:
		m.status = msg.status
		m.err = nil
		return m, tea.Every(pollInterval, func(t time.Time) tea.Msg { return pollMsg(t) })

	case statusErr:
		m.err = msg.err
		return m, tea.Every(retryInterval, func(t time.Time) tea.Msg { return pollMsg(t) })

	case tea.KeyMsg:
		return m.updateKey(msg.Key())
	}
	return m, nil
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
		return m, nil
	case '1', '2', '3', '4', '5', '6', '7':
		if len(key.Text) == 1 {
			m.view = int(key.Code - '1')
		}
		return m, nil
	}
	return m, nil
}

// fetch returns the command that requests the status of the core.
func (m model) fetch() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resp, err := systemClient(m.server, m.token).Status(ctx, connect.NewRequest(&v1alpha1.StatusRequest{}))
		if err != nil {
			return statusErr{err: err}
		}
		return statusMsg{status: resp.Msg}
	}
}

// viewName is the name of the current view.
func (m model) viewName() string { return views[m.view] }
