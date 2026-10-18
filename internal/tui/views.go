// SPDX-License-Identifier: MIT

package tui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// maxLines caps the on-screen buffers of the streaming views.
const maxLines = 200

// feedLine is one line of the event feed or the logs.
type feedLine struct {
	label string
	text  string
}

// chatLine is one line of the chat view.
type chatLine struct {
	platform string
	login    string
	bot      bool
	text     string
}

// chatView is the state of the chat view.
type chatView struct {
	lines []chatLine
	input string
}

// appendChat adds a chat message and keeps the buffer capped.
func (c *chatView) appendChat(p *v1alpha1.ChatMessage, platform string) {
	c.lines = append(c.lines, chatLine{platform: platform, login: p.GetLogin(), bot: p.GetBot(), text: p.GetText()})
	if len(c.lines) > maxLines {
		c.lines = c.lines[len(c.lines)-maxLines:]
	}
}

// render draws the chat lines for a width.
func (c chatView) render(t *Theme, width int) string {
	var lines []string
	for _, l := range c.lines {
		who := l.login
		if l.bot {
			who = "bot"
		}
		lines = append(lines, "["+l.platform+"] "+who+": "+l.text)
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	return t.border.Width(width).Render(t.title.Render("Chat") + "\n" + body)
}

// feedView is the state of the event feed view.
type feedView struct {
	lines []feedLine
}

// append adds a feed line and keeps the buffer capped.
func (f *feedView) append(label, text string) {
	f.lines = append(f.lines, feedLine{label: label, text: text})
	if len(f.lines) > maxLines {
		f.lines = f.lines[len(f.lines)-maxLines:]
	}
}

// render draws the feed lines for a width.
func (f feedView) render(t *Theme, title string, width int) string {
	var lines []string
	for _, l := range f.lines {
		lines = append(lines, l.label+"  "+l.text)
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	return t.border.Width(width).Render(t.title.Render(title) + "\n" + body)
}

// queueView is the state of the command queue view.
type queueView struct {
	instances []*v1alpha1.CommandInstanceView
	selected  int
}

// render draws the queue lines for a width.
func (q queueView) render(t *Theme, width int) string {
	var lines []string
	for i, inst := range q.instances {
		mark := "  "
		if i == q.selected {
			mark = "→ "
		}
		sym := "·"
		switch inst.GetState() {
		case "running":
			sym = "▶"
		case "queued":
			sym = "‖"
		case "failed":
			sym = "✗"
		}
		line := mark + sym + " " + inst.GetCommandName() + " (" + inst.GetSource() + ")  " + inst.GetState()
		if inst.GetError() != "" {
			line += "  " + t.err.Render(inst.GetError())
		}
		lines = append(lines, line)
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	help := t.dim.Render("↑↓ select · p pause queue · x cancel · r retry")
	return t.border.Width(width).Render(t.title.Render("Command Queue") + "\n" + body + "\n" + help)
}

// commandsView is the state of the commands view.
type commandsView struct {
	list      []*v1alpha1.CommandSummary
	filter    string
	filtering bool
	selected  int
	editing   bool // the document is open in $EDITOR
}

// render draws the command list for a width.
func (c commandsView) render(t *Theme, width int) string {
	var lines []string
	for i, cmd := range c.list {
		mark := "  "
		if i == c.selected {
			mark = "→ "
		}
		state := "off"
		if cmd.GetEnabled() {
			state = "on"
		}
		lines = append(lines, mark+cmd.GetName()+"  ["+cmd.GetKind()+"]  "+state)
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	help := t.dim.Render("/ filter · enter run · e enable/disable · E edit in $EDITOR")
	return t.border.Width(width).Render(t.title.Render("Commands") + "\n" + body + "\n" + help)
}

// usersView is the state of the users view.
type usersView struct {
	list      []*v1alpha1.UserView
	filter    string
	filtering bool
	selected  int
}

// render draws the user list for a width.
func (u usersView) render(t *Theme, width int) string {
	var lines []string
	for i, usr := range u.list {
		mark := "  "
		if i == u.selected {
			mark = "→ "
		}
		login := "-"
		if len(usr.GetIdentities()) > 0 {
			login = usr.GetIdentities()[0].GetLogin()
		}
		excl := ""
		if usr.GetExcluded() {
			excl = "  excluded"
		}
		lines = append(lines, mark+login+"  ["+usr.GetRole()+"]  "+usr.GetTitle()+"  "+
			strconv.FormatInt(usr.GetMessages(), 10)+" msg, "+strconv.FormatInt(usr.GetCommandsRun(), 10)+" cmds"+excl)
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	help := t.dim.Render("/ filter · r re-sort by stats")
	return t.border.Width(width).Render(t.title.Render("Users") + "\n" + body + "\n" + help)
}

// settingsView is the state of the settings view (roadmap 6.5.3): the
// configuration document with an annotation per setting — hot (applied
// without a restart), restart (applied after a restart) and fixed (set by
// a flag or environment variable, not changeable from the file).
type settingsView struct {
	loaded   bool
	err      error
	document string
	hot      []string
	fixed    []string
	restart  []string
	live     bool
	result   string
	editing  bool
}

// annotation returns the mark for a configuration file key: hot, restart
// or fixed.
func (s settingsView) annotation(key string) string {
	flag := strings.ReplaceAll(key, "_", "-")
	for _, f := range s.fixed {
		if f == key || f == flag {
			return "✋"
		}
	}
	if slices.Contains(s.hot, flag) {
		return "⚡"
	}
	return "⟳"
}

// render draws the configuration document for a width.
func (s settingsView) render(t *Theme, width int) string {
	var lines []string
	switch {
	case !s.loaded:
		lines = append(lines, "loading …")
	case s.err != nil:
		lines = append(lines, t.err.Render(s.err.Error()))
	default:
		state := "read-only (daemon or desktop mode)"
		if s.live {
			state = "live"
		}
		lines = append(lines, t.dim.Render("live: "+state))
		if s.editing {
			lines = append(lines, t.dim.Render("editing in $EDITOR …"))
		}
		for raw := range strings.SplitSeq(strings.TrimRight(s.document, "\n"), "\n") {
			key, _, ok := strings.Cut(raw, ":")
			mark := "  "
			if ok {
				mark = s.annotation(strings.TrimSpace(key)) + " "
			}
			lines = append(lines, mark+raw)
		}
		if s.result != "" {
			lines = append(lines, t.ok.Render(s.result))
		}
	}
	body := ""
	if len(lines) > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, lines...)
	}
	help := t.dim.Render("e edit in $EDITOR · r reload")
	if !s.live {
		help = t.dim.Render("read-only · r reload")
	}
	return t.border.Width(width).Render(t.title.Render("Settings") + "\n" + body + "\n" + help)
}
