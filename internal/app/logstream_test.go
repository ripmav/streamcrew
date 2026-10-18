// SPDX-License-Identifier: MIT

package app_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
)

// TestRunPublishesLogEvents covers the log stream (roadmap 6.6): the core's
// log records appear on the bus as "log.line" events with the lower-case
// level, the component and the message. The test provokes a warn line of
// the configuration watcher (an invalid file) and expects it on the bus.
func TestRunPublishesLogEvents(t *testing.T) {
	a, path := hotCore(t, &outPub{})

	sub := a.Bus().Subscribe(t.Context(), event.WithFilter(func(e event.Envelope) bool {
		return e.Type == eventtype.Log
	}))
	defer sub.Close()

	rewriteFile(t, path, "not: [valid yaml\n")
	var p app.LogPayload
	found := false
	deadline := time.Now().Add(10 * time.Second)
	for !found && time.Now().Before(deadline) {
		e, ok := <-sub.C()
		if !ok {
			t.Fatal("the subscription ended")
		}
		if q, ok := event.Payload[app.LogPayload](e); ok {
			p, found = q, true
		}
	}
	require.True(t, found, "the warned configuration problem to reach the bus")
	assert.Equal(t, "warn", p.Level)
	assert.Equal(t, "config", p.Component)
	assert.Contains(t, p.Message, "configuration file not reloaded")
	rewriteFile(t, path, "log_level: debug\n")
}
