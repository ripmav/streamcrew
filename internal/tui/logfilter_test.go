// SPDX-License-Identifier: MIT

package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
// the filter, and the header shows it. The core is not touched; the events
// are sent directly into the program loop.
func TestLogsViewFiltersByLevel(t *testing.T) {
	t.Parallel()
	tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
	tm.Type("7") // logs view
	tm.Send(logEventMsg("debug", "engine", "debug line"))
	tm.Send(logEventMsg("info", "engine", "info line"))
	tm.Send(logEventMsg("warn", "config", "warn line"))
	tm.Send(logEventMsg("error", "http", "error line"))

	// the key l raises the filter: debug -> info; events below the filter
	// are dropped, earlier lines are kept (the buffer is a view of history)
	tm.Type("l")
	tm.Send(logEventMsg("debug", "engine", "another debug"))
	tm.Send(logEventMsg("info", "engine", "another info"))

	m := quitModel(t, tm)
	assert.Equal(t, logLevels[1], m.logMin)
	var shown []string
	for _, l := range m.logs.lines {
		shown = append(shown, l.text)
	}
	assert.Contains(t, shown, "[engine] info line")
	assert.Contains(t, shown, "[config] warn line")
	assert.Contains(t, shown, "[http] error line")
	assert.NotContains(t, shown, "[engine] another debug")
	assert.Contains(t, shown, "[engine] debug line")

	// the header shows the active filter
	assert.Contains(t, m.View().Content, "Logs ≥ info")
}

// TestLogsViewCyclesTheFilterLevel covers the wrap-around of the key l.
func TestLogsViewCyclesTheFilterLevel(t *testing.T) {
	t.Parallel()
	tm := newTestModel(t, Options{Server: "http://127.0.0.1:1"})
	tm.Type("7") // logs view
	for range logLevels {
		tm.Type("l")
	}
	m := quitModel(t, tm)
	assert.Equal(t, logLevels[0], m.logMin) // back to debug
}
