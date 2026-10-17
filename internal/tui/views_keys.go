// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// updateChatKey handles the keys of the chat view: the input line, sending,
// and the moderation shortcuts.
func (m model) updateChatKey(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.Code {
	case tea.KeyEnter:
		if m.chat.input == "" {
			return m, nil
		}
		text := m.chat.input
		m.chat.input = ""
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
			defer cancel()
			_, err := m.cl.chat.Send(ctx, connect.NewRequest(&v1alpha1.SendChatRequest{Text: text}))
			return actionDoneMsg{err: err}
		}
	case tea.KeyBackspace:
		if len(m.chat.input) > 0 {
			r := []rune(m.chat.input)
			m.chat.input = string(r[:len(r)-1])
		}
		return m, nil
	case 'd':
		// delete the last message of the platform
		return m, m.request(func(ctx context.Context, c *clients) error {
			_, err := c.chat.Delete(ctx, connect.NewRequest(&v1alpha1.DeleteChatRequest{}))
			return err
		})
	case 'c':
		// clear the chat
		return m, m.request(func(ctx context.Context, c *clients) error {
			_, err := c.chat.Clear(ctx, connect.NewRequest(&v1alpha1.ClearChatRequest{}))
			return err
		})
	}
	if len(key.Text) == 1 && (key.Text >= " " && key.Text <= "\x7f") {
		m.chat.input += key.Text
		return m, nil
	}
	return m, nil
}

// editorFromEnv returns the configured editor, preferring $EDITOR over
// $VISUAL.
//
//nolint:forbidigo // process environment, not core configuration
func editorFromEnv() string {
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if k == "EDITOR" && v != "" {
				return v
			}
		}
	}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if k == "VISUAL" {
				return v
			}
		}
	}
	return ""
}

// request runs an API call in a command and reports the error as message.
func (m model) request(fn func(context.Context, *clients) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		return actionDoneMsg{err: fn(ctx, m.cl)}
	}
}

// updateQueueKey handles the keys of the queue view.
func (m model) updateQueueKey(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.Code {
	case tea.KeyUp:
		if m.queue.selected > 0 {
			m.queue.selected--
		}
		return m, nil
	case tea.KeyDown:
		if m.queue.selected < len(m.queue.instances)-1 {
			m.queue.selected++
		}
		return m, nil
	case 'p':
		// pause or resume the whole queue
		return m, m.request(func(ctx context.Context, c *clients) error {
			_, err := c.command.PauseQueue(ctx, connect.NewRequest(&v1alpha1.PauseQueueRequest{}))
			return err
		})
	case 'x':
		if i := m.selectedInstance(); i != nil {
			return m, m.request(func(ctx context.Context, c *clients) error {
				_, err := c.command.CancelInstance(ctx, connect.NewRequest(&v1alpha1.CancelInstanceRequest{
					InstanceId: i.GetInstanceId(),
				}))
				return err
			})
		}
	case 'r':
		if i := m.selectedInstance(); i != nil {
			return m, m.request(func(ctx context.Context, c *clients) error {
				_, err := c.command.RetryInstance(ctx, connect.NewRequest(&v1alpha1.RetryInstanceRequest{
					InstanceId: i.GetInstanceId(),
				}))
				return err
			})
		}
	}
	return m, nil
}

// selectedInstance is the selected queue entry, or nil.
func (m model) selectedInstance() *v1alpha1.CommandInstanceView {
	if m.queue.selected < len(m.queue.instances) {
		return m.queue.instances[m.queue.selected]
	}
	return nil
}

// updateCommandsKey handles the keys of the commands view: filter, run,
// enable/disable, and the $EDITOR.
func (m model) updateCommandsKey(key tea.Key) (tea.Model, tea.Cmd) {
	if m.commands.filtering {
		switch key.Code {
		case tea.KeyBackspace:
			if len(m.commands.filter) > 0 {
				m.commands.filter = m.commands.filter[:len(m.commands.filter)-1]
				m.applyCommandFilter()
			}
			return m, nil
		case '/':
			m.commands.filtering = false
			return m, nil
		case tea.KeyEsc:
			m.commands.filtering = false
			m.commands.filter = ""
			m.applyCommandFilter()
			return m, nil
		}
		if len(key.Text) == 1 && (key.Text >= " " && key.Text <= "\x7f") {
			m.commands.filter += key.Text
			m.applyCommandFilter()
		}
		return m, nil
	}
	switch key.Code {
	case tea.KeyUp:
		if m.commands.selected > 0 {
			m.commands.selected--
		}
		return m, nil
	case tea.KeyDown:
		if m.commands.selected < len(m.commands.list)-1 {
			m.commands.selected++
		}
		return m, nil
	case '/':
		// enter the filter mode
		m.commands.filtering = true
		return m, nil
	case 'e':
		if c := m.selectedCommand(); c != nil {
			enabled := !c.GetEnabled()
			return m, m.request(func(ctx context.Context, c2 *clients) error {
				_, err := c2.command.SetEnabled(ctx, connect.NewRequest(&v1alpha1.SetEnabledCommandRequest{
					Id:      c.GetId(),
					Enabled: enabled,
				}))
				return err
			})
		}
	case 'E':
		if c := m.selectedCommand(); c != nil {
			return m, m.editCommand(c.GetId())
		}
	case tea.KeyEnter:
		if c := m.selectedCommand(); c != nil {
			return m, m.request(func(ctx context.Context, c2 *clients) error {
				_, err := c2.command.Run(ctx, connect.NewRequest(&v1alpha1.RunCommandRequest{
					Id: c.GetId(),
				}))
				return err
			})
		}
	case tea.KeyBackspace:
		if len(m.commands.filter) > 0 {
			m.commands.filter = m.commands.filter[:len(m.commands.filter)-1]
			m.applyCommandFilter()
		}
		return m, nil
	}
	if len(key.Text) == 1 && (key.Text >= " " && key.Text <= "\x7f") {
		m.commands.filter += key.Text
		m.applyCommandFilter()
		return m, nil
	}
	return m, nil
}

// selectedCommand is the selected command, or nil.
func (m model) selectedCommand() *v1alpha1.CommandSummary {
	if m.commands.selected < len(m.commands.list) {
		return m.commands.list[m.commands.selected]
	}
	return nil
}

// applyCommandFilter filters the command list by the current filter.
func (m model) applyCommandFilter() {
	f := strings.ToLower(m.commands.filter)
	if f == "" {
		return
	}
	var kept []*v1alpha1.CommandSummary
	for _, c := range m.commands.list {
		if strings.Contains(strings.ToLower(c.GetName()), f) {
			kept = append(kept, c)
		}
	}
	m.commands.list = kept
	if m.commands.selected >= len(m.commands.list) {
		m.commands.selected = 0
	}
}

// editCommand opens the document of the command in $EDITOR.
func (m model) editCommand(id string) tea.Cmd {
	// the editor comes from the environment (no core config is involved)
	editor := editorFromEnv()
	if editor == "" {
		return func() tea.Msg { return actionDoneMsg{err: os.ErrNotExist} }
	}
	m.commands.editing = true
	// get the document first, then start the editor
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		resp, err := m.cl.command.Get(ctx, connect.NewRequest(&v1alpha1.GetCommandRequest{Id: id}))
		if err != nil {
			return actionDoneMsg{err: err}
		}
		file, err := os.CreateTemp("", "streamcrew-command-*.yaml")
		if err != nil {
			return actionDoneMsg{err: err}
		}
		name := file.Name()
		if _, err := file.WriteString(resp.Msg.GetDocument()); err != nil {
			_ = file.Close()
			return actionDoneMsg{err: err}
		}
		if err := file.Close(); err != nil {
			return actionDoneMsg{err: err}
		}
		// run the editor blocking; the file name is the only argument, so no
		// shell interpretation happens
		if err := exec.CommandContext(ctx, editor, name).Run(); err != nil {
			return actionDoneMsg{err: err}
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return actionDoneMsg{err: err}
		}
		_, err = m.cl.command.Update(ctx, connect.NewRequest(&v1alpha1.UpdateCommandRequest{
			Id:       id,
			Document: string(data),
		}))
		return actionDoneMsg{err: err}
	}
}

// updateUsersKey handles the keys of the users view: navigation and filter.
func (m model) updateUsersKey(key tea.Key) (tea.Model, tea.Cmd) {
	if m.users.filtering {
		switch key.Code {
		case tea.KeyBackspace:
			if len(m.users.filter) > 0 {
				m.users.filter = m.users.filter[:len(m.users.filter)-1]
				m.applyUserFilter()
			}
			return m, nil
		case '/':
			m.users.filtering = false
			return m, nil
		case tea.KeyEsc:
			m.users.filtering = false
			m.users.filter = ""
			m.applyUserFilter()
			return m, nil
		}
		if len(key.Text) == 1 && (key.Text >= " " && key.Text <= "\x7f") {
			m.users.filter += key.Text
			m.applyUserFilter()
		}
		return m, nil
	}
	switch key.Code {
	case tea.KeyUp:
		if m.users.selected > 0 {
			m.users.selected--
		}
		return m, nil
	case tea.KeyDown:
		if m.users.selected < len(m.users.list)-1 {
			m.users.selected++
		}
		return m, nil
	case tea.KeyBackspace:
		if len(m.users.filter) > 0 {
			m.users.filter = m.users.filter[:len(m.users.filter)-1]
			m.applyUserFilter()
		}
		return m, nil
	case 'r':
		// re-sort by messages
		return m, m.fetchUsers()
	case '/':
		// enter the filter mode
		m.users.filtering = true
		return m, nil
	}
	return m, nil
}

// applyUserFilter filters the user list by the current filter.
func (m model) applyUserFilter() {
	f := strings.ToLower(m.users.filter)
	if f == "" {
		return
	}
	var kept []*v1alpha1.UserView
	for _, u := range m.users.list {
		for _, i := range u.GetIdentities() {
			if strings.Contains(strings.ToLower(i.GetLogin()), f) {
				kept = append(kept, u)
				break
			}
		}
	}
	m.users.list = kept
	if m.users.selected >= len(m.users.list) {
		m.users.selected = 0
	}
}
