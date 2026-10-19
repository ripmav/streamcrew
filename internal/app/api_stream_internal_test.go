// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	"github.com/ripmav/streamcrew/internal/config"
)

// chatEventFor builds a chat event for the hub tests.
func chatEventFor(text string) *v1alpha1.Event {
	return &v1alpha1.Event{
		Type: "chat.message",
		Payload: &v1alpha1.Event_Chat{
			Chat: &v1alpha1.ChatMessage{Text: text},
		},
	}
}

// TestStreamHubCatchUpKeepsTheConfiguredSize covers the catch-up buffer of
// the stream hub (roadmap 6.2.6): the snapshot for a new subscriber holds
// the last configured events only.
func TestStreamHubCatchUpKeepsTheConfiguredSize(t *testing.T) {
	h := newStreamHub(nil, config.StreamConfig{Buffer: 1, CatchUp: 3})
	for i := range 5 {
		h.publish(chatEventFor("hello " + string(rune('a'+i))))
	}
	_, buffered := h.subscribe()
	require.Len(t, buffered, 3)
	assert.Equal(t, "hello c", buffered[0].GetChat().GetText()) // the last three
	assert.Equal(t, "hello e", buffered[2].GetChat().GetText())
}

// TestStreamHubTakesTheConfiguredBusBuffer covers the wiring of the bus
// subscription buffer (roadmap 6.2.6): the hub passes the configured size
// to the bus subscription.
func TestStreamHubTakesTheConfiguredBusBuffer(t *testing.T) {
	h := newStreamHub(nil, config.StreamConfig{Buffer: 17, CatchUp: 3})
	assert.Equal(t, 17, h.busBuffer)
}
