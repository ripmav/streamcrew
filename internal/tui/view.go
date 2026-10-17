// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View implements tea.Model.
func (m model) View() tea.View {
	t := newTheme(m.dark)
	if m.help {
		v := tea.NewView(t.helpView(m.width))
		v.AltScreen = true
		return v
	}
	lines := []string{m.header(t), "", m.mainArea(t), "", m.footer(t)}
	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, lines...))
	v.AltScreen = true
	return v
}

// header renders the status line.
func (m model) header(t *Theme) string {
	label := "offline"
	if m.status != nil {
		label = m.status.GetProfile()
	}
	if m.err != nil {
		return t.err.Render(fmt.Sprintf("streamcrew · %s · %s", m.server, m.err))
	}
	uptime := ""
	if m.status != nil && m.status.GetStartedAt() != nil {
		uptime = " · " + time.Since(m.status.GetStartedAt().AsTime()).Round(time.Second).String()
	}
	platforms := ""
	if m.status != nil {
		var names []string
		for _, p := range m.status.GetPlatforms() {
			state := "offline"
			if p.GetConnected() {
				state = "connected"
			}
			if p.GetLive() {
				state = "live"
			}
			names = append(names, fmt.Sprintf("%s:%s", p.GetPlatform(), state))
		}
		if len(names) > 0 {
			platforms = " · " + strings.Join(names, " ")
		}
	}
	return t.header.Render(fmt.Sprintf("streamcrew · %s · %s%s%s", m.server, label, platforms, uptime)) +
		t.dim.Render(" (v"+m.statusVersion()+")")
}

// statusVersion returns the version from the status, or a dash.
func (m model) statusVersion() string {
	if m.status == nil {
		return "-"
	}
	return m.status.GetVersion()
}

// mainArea renders the active view.
func (m model) mainArea(t *Theme) string {
	switch m.viewName() {
	case "dashboard":
		return m.dashboard(t)
	default:
		return t.dim.Render("not available yet — " + m.viewName())
	}
}

// footer renders the key row.
func (m model) footer(t *Theme) string {
	parts := []string{}
	for i, v := range views {
		k := t.key.Render(strconv.Itoa(i + 1))
		if m.view == i {
			parts = append(parts, k+t.dim.Render(v))
		} else {
			parts = append(parts, t.dim.Render(v))
		}
	}
	parts = append(parts, t.dim.Render("q quit · ? help · t theme"))
	return strings.Join(parts, "  ")
}

// dashboard renders the status of the core.
func (m model) dashboard(t *Theme) string {
	if m.status == nil {
		if m.err != nil {
			return t.borderErr.Padding(0, 1).Render(m.err.Error())
		}
		return t.dim.Render("connecting …")
	}
	s := m.status
	lines := []string{
		t.title.Render("Status"),
		"  version     " + s.GetVersion(),
		"  mode        " + s.GetMode(),
		"  profile     " + s.GetProfile(),
	}
	for _, p := range s.GetPlatforms() {
		dot := "○"
		if p.GetConnected() {
			dot = "●"
		}
		state := "offline"
		if p.GetConnected() {
			state = "connected"
		}
		if p.GetLive() {
			state = "live"
		}
		lines = append(lines, fmt.Sprintf("  platform    %s %s (%s)", dot, p.GetPlatform(), state))
	}
	if s.GetStartedAt() != nil {
		lines = append(lines, "  started     "+s.GetStartedAt().AsTime().Format("2006-01-02 15:04:05"))
	}
	return t.border.Padding(0, 1).Render(strings.Join(lines, "\n"))
}

// helpView renders the key help.
func (t *Theme) helpView(width int) string {
	lines := []string{
		t.title.Render("Help"),
		"",
		keyLine(t, "q", "quit"),
		keyLine(t, "1 … 7", "switch view"),
		keyLine(t, "tab", "next view"),
		keyLine(t, "t", "toggle theme"),
		keyLine(t, "?", "this help"),
	}
	body := strings.Join(lines, "\n")
	w := max(width, 40)
	return t.border.Padding(0, 1).Width(w - 2).Render(body)
}

// keyLine formats one key/label pair.
func keyLine(t *Theme, key, label string) string {
	return t.key.Render(key) + t.dim.Render("   ") + label
}
