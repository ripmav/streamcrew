// SPDX-License-Identifier: MIT

package preset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chataction "github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/action/commandadmin"
	streamaction "github.com/ripmav/streamcrew/internal/action/stream"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/role"
)

func TestCommands(t *testing.T) {
	t.Parallel()
	cmds := Commands()
	require.Len(t, cmds, 10)

	// the display presets answer with their identifier
	answer := map[string]string{
		"uptime":    "$streamuptimetotal",
		"followage": "$followage",
		"title":     "$streamtitle",
		"game":      "$streamgamename",
		"commands":  "$commands",
	}
	for _, c := range cmds {
		assert.Equal(t, command.KindChat, c.Kind, c.Name)
		assert.True(t, c.Enabled, c.Name)
		assert.Equal(t, command.TriggerExclamation, c.TriggerMode, c.Name)
		assert.Equal(t, []string{c.Name}, c.Triggers, c.Name)
		assert.NoError(t, c.Validate(), c.Name)
		require.Len(t, c.Actions, 1, c.Name)

		if msg, ok := answer[c.Name]; ok {
			act, isChat := c.Actions[0].(chataction.Chat)
			require.True(t, isChat, c.Name)
			assert.Equal(t, chataction.KindMessage, act.Kind, c.Name)
			require.NotNil(t, act.Chat, c.Name)
			assert.Equal(t, msg, string(act.Message), c.Name)
			continue
		}

		// the stream presets set title or category from the arguments
		if act, isStream := c.Actions[0].(streamaction.Stream); isStream {
			assert.Equal(t, "$allargs", string(act.Text), c.Name)
			switch c.Name {
			case "settitle":
				assert.Equal(t, streamaction.KindSetTitle, act.Kind, c.Name)
			case "setgame":
				assert.Equal(t, streamaction.KindSetGame, act.Kind, c.Name)
			default:
				t.Fatalf("unexpected stream preset %q", c.Name)
			}
			continue
		}

		// the admin presets manage commands and need the moderator role
		act, isAdmin := c.Actions[0].(commandadmin.CommandAdmin)
		require.True(t, isAdmin, c.Name)
		require.Len(t, c.Requirements, 1, c.Name)
		assert.Equal(t, command.RoleRequirement{Role: role.Moderator}, c.Requirements[0], c.Name)
		switch c.Name {
		case "addcmd":
			assert.Equal(t, commandadmin.KindAdd, act.Kind, c.Name)
		case "updatecmd":
			assert.Equal(t, commandadmin.KindUpdate, act.Kind, c.Name)
		case "disablecmd":
			assert.Equal(t, commandadmin.KindDisable, act.Kind, c.Name)
		default:
			t.Fatalf("unexpected admin preset %q", c.Name)
		}
	}
}
