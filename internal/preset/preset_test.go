// SPDX-License-Identifier: MIT

package preset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/domain/command"
)

func TestCommands(t *testing.T) {
	t.Parallel()
	cmds := Commands()
	require.Len(t, cmds, 4)

	want := map[string]string{
		"uptime":    "$streamuptimetotal",
		"followage": "$followage",
		"title":     "$streamtitle",
		"game":      "$streamgamename",
	}
	for _, c := range cmds {
		// a chat command, enabled, triggered by its own name
		assert.Equal(t, command.KindChat, c.Kind, c.Name)
		assert.True(t, c.Enabled, c.Name)
		assert.Equal(t, command.TriggerExclamation, c.TriggerMode, c.Name)
		assert.Equal(t, []string{c.Name}, c.Triggers, c.Name)
		assert.NoError(t, c.Validate(), c.Name)

		// one message action with the identifier
		require.Len(t, c.Actions, 1, c.Name)
		act, ok := c.Actions[0].(chataction.Chat)
		require.True(t, ok, c.Name)
		assert.Equal(t, chataction.KindMessage, act.Kind, c.Name)
		require.NotNil(t, act.Chat, c.Name)
		msg, ok := want[c.Name]
		require.True(t, ok, "unexpected preset %q", c.Name)
		assert.Equal(t, msg, string(act.Message), c.Name)
	}
}
