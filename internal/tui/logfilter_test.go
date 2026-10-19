// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// logEventMsg builds a stream event carrying one log line.
func logEventMsg(level, component, message string) streamMsg {
	return streamMsg{event: &v1alpha1.Event{
		Type: "log",
		Payload: &v1alpha1.Event_Log{Log: &v1alpha1.LogEntry{
			Level: level, Component: component, Message: message,
		}},
	}}
}

// TestLogsViewFiltersByLevel covers the display level of the logs view
// (roadmap 6.6.2): events below the filter are dropped, the key l cycles
// the filter, and the header shows it. The core is not touched.
func TestLogsViewFiltersByLevel(t *testing.T) {
	m := newModel(Options{Server: "http://127.0.0.1:1"})
	m.view = 6 // logs

	m.handleEvent(logEventMsg("debug", "engine", "debug line").event)
	m.handleEvent(logEventMsg("info", "engine", "info line").event)
	m.handleEvent(logEventMsg("warn", "config", "warn line").event)
	m.handleEvent(logEventMsg("error", "http", "error line").event)
	require.Len(t, m.logs.lines, 4) // the default debug shows everything

	// the key l raises the filter: debug -> info
	next, cmd := m.Update(&keyMsg{code: 'l', text: "l"})
	m = next.(model)
	require.Nil(t, cmd)
	assert.Equal(t, logLevels[1], m.logMin)

	m.handleEvent(logEventMsg("debug", "engine", "another debug").event)
	m.handleEvent(logEventMsg("info", "engine", "another info").event)
	var shown []string
	for _, l := range m.logs.lines {
		shown = append(shown, l.text)
	}
	assert.Contains(t, shown, "[engine] info line")
	assert.Contains(t, shown, "[config] warn line")
	assert.Contains(t, shown, "[http] error line")
	assert.NotContains(t, shown, "[engine] another debug")
	assert.Contains(t, shown, "[engine] debug line") // the pre-filter line is kept: it is a view of history

	// the header shows the active filter
	t2 := newTheme(true)
	out := m.logs.render(t2, "Logs ≥ "+strings.ToLower(m.logMin.String()), 90)
	assert.Contains(t, out, "Logs ≥ info")
}

// TestLogsViewCyclesTheFilterLevel covers the wrap-around of the key l.
func TestLogsViewCyclesTheFilterLevel(t *testing.T) {
	m := newModel(Options{Server: "http://127.0.0.1:1"})
	m.view = 6 // logs
	for range logLevels {
		next, _ := m.Update(&keyMsg{code: 'l', text: "l"})
		m = next.(model)
	}
	assert.Equal(t, logLevels[0], m.logMin) // back to debug
}
