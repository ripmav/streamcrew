// SPDX-License-Identifier: MIT

package eventtype_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
)

// TestCatalog covers B1: every type follows the naming rule of the event
// bus and appears once; and B2: every platform-specific type with a neutral
// counterpart points to a neutral type of the catalog.
func TestCatalog(t *testing.T) {
	t.Parallel()
	all := eventtype.All()
	require.NotEmpty(t, all)
	c := event.NewCatalog()
	for _, d := range all {
		// Register checks the naming rule and rejects duplicates.
		require.NoError(t, event.Register[struct{}](c, d.Type), d.Type)

		got, ok := eventtype.Lookup(d.Type)
		require.True(t, ok)
		assert.Equal(t, d, got)

		switch d.Once {
		case eventtype.Always, eventtype.PerSession, eventtype.PerUserSession, eventtype.PerUser:
		default:
			t.Errorf("%s: unknown frequency %q", d.Type, d.Once)
		}

		if d.Platform == "" {
			assert.Empty(t, d.Neutral, "%s: a neutral type has no neutral counterpart", d.Type)
			continue
		}
		assert.NoError(t, d.Platform.Validate())
		assert.True(t, strings.HasPrefix(string(d.Type), string(d.Platform)+"."), "%s: prefixed with its platform", d.Type)
		if d.Neutral != "" {
			n, ok := eventtype.Lookup(d.Neutral)
			require.True(t, ok, "%s: neutral type %s is in the catalog", d.Type, d.Neutral)
			assert.Empty(t, n.Platform, "%s: %s is platform-neutral", d.Type, d.Neutral)
			assert.Equal(t, n.Once, d.Once, "%s: same frequency as %s", d.Type, d.Neutral)
		}
	}

	_, ok := eventtype.Lookup("twitch.hype_chat")
	assert.False(t, ok, "B23: no type for Twitch Hype Chat")
}

// TestFrequencies covers B3 and B4 for the types the spec names.
func TestFrequencies(t *testing.T) {
	t.Parallel()
	tests := map[event.Type]eventtype.Once{
		eventtype.ChannelStreamStart:          eventtype.PerSession,
		eventtype.ChannelStreamStop:           eventtype.PerSession,
		eventtype.ChannelFollow:               eventtype.PerUserSession,
		eventtype.ChannelSubscribe:            eventtype.PerUserSession,
		eventtype.ChannelResubscribe:          eventtype.PerUserSession,
		eventtype.ChannelRaid:                 eventtype.PerUserSession,
		eventtype.ChatMessage:                 eventtype.Always,
		eventtype.ChannelSubscriptionGift:     eventtype.Always,
		eventtype.ChannelSubscriptionMassGift: eventtype.Always,
		eventtype.TwitchBitsCheer:             eventtype.Always,
	}
	for typ, want := range tests {
		d, ok := eventtype.Lookup(typ)
		require.True(t, ok, typ)
		assert.Equal(t, want, d.Once, typ)
	}
}
