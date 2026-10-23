// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/prompt"
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

// TestConvertEventCarriesTheProtocolType covers the protocol contract
// (stream.proto): every converted event carries its type name, the stream
// clients (TUI routing, kind filters) switch on it.
func TestConvertEventCarriesTheProtocolType(t *testing.T) {
	t.Parallel()
	src := event.Source{Kind: event.SourceSystem, Name: "test"}
	tests := []struct {
		name     string
		envelope event.Envelope
		want     string // empty: the type is not delivered by the stream
	}{
		{
			name: "chat",
			envelope: event.New(src, eventtype.ChatMessage, eventtype.Payload{
				Platform: platform.Mock, Channel: "crew",
				Details: eventtype.Details{Message: &eventtype.Message{ID: "m1", Text: "hi"}},
			}),
			want: "chat.message",
		},
		{
			name: "feed",
			envelope: event.New(src, eventtype.ChannelFollow, eventtype.Payload{
				Platform: platform.Mock, Channel: "crew",
			}),
			want: "feed",
		},
		{
			name: "instance",
			envelope: event.New(src, engine.TypeInstanceStarted, engine.Instance{
				ID: id.New(), CommandID: id.New(), CommandName: "Hello", State: engine.StateRunning,
			}),
			want: "command.instance",
		},
		{
			name:     "prompt",
			envelope: event.New(src, prompt.Type, prompt.Prompt{ID: "p1", Kind: "information", Text: "info"}),
			want:     "prompt",
		},
		{
			name:     "log",
			envelope: event.New(src, eventtype.Log, LogPayload{Level: "info", Component: "engine", Message: "started"}),
			want:     "log",
		},
		{
			name:     "undelivered",
			envelope: event.New(src, eventtype.ChatUserJoin, eventtype.Payload{Platform: platform.Mock}),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := convertEvent(tc.envelope)
			if tc.want == "" {
				assert.Nil(t, ev)
				return
			}
			require.NotNil(t, ev)
			assert.Equal(t, tc.want, ev.GetType())
		})
	}
}
