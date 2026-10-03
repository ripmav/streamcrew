// SPDX-License-Identifier: Apache-2.0

package requirement_test

import (
	"errors"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
)

// deleting returns the setting that deletes the triggering message.
func deleting() command.SettingsRequirement {
	return command.SettingsRequirement{DeleteTriggerMessage: true}
}

// TestDeleteTriggerMessage covers B60 and B61: with the setting, the
// triggering message is deleted on the platform of the run; without it, or
// without a chat message, nothing is deleted; a message that cannot be
// deleted is an error. The settings never reject (B60, B62).
func TestDeleteTriggerMessage(t *testing.T) {
	t.Parallel()
	ada := person("ada", platform.Twitch)
	for name, tc := range map[string]struct {
		cmd     command.Command
		p       engine.Params
		deleted []connectortest.Call
	}{
		"deleted":         {cmd(deleting()), chat(ada), []connectortest.Call{{Op: connectortest.OpDelete, MessageID: "m1"}}},
		"setting off":     {cmd(command.SettingsRequirement{ShowInChatMenu: true}), chat(ada), nil},
		"no setting":      {cmd(), chat(ada), nil},
		"no chat message": {cmd(deleting()), engine.Params{Platform: platform.Twitch, User: ada}, nil},
	} {
		f := newFixture(t, language{lang: i18n.English})
		got, err := apply(t.Context(), f.service, tc.cmd, tc.p)
		require.NoError(t, err, name)
		assert.Equal(t, engine.VerdictMet, got.Verdict, "%s: the settings check nothing", name)
		require.NoError(t, f.service.Decided(t.Context(), tc.cmd, tc.p), name)
		assert.Equal(t, tc.deleted, f.twitch.Calls(), name)
	}
}

// TestDeleteTriggerMessageFails covers B61: a message without an ID, on a
// platform that is not connected or that refuses is an error.
func TestDeleteTriggerMessageFails(t *testing.T) {
	t.Parallel()
	ada := person("ada", platform.Twitch)
	c := cmd(deleting())

	f := newFixture(t, language{lang: i18n.English})
	noID := chat(ada)
	noID.MessageID = ""
	require.ErrorContains(t, f.service.Decided(t.Context(), c, noID), "no ID of the message")

	velora := chat(ada)
	velora.Platform = "velora"
	require.ErrorIs(t, f.service.Decided(t.Context(), c, velora), connector.ErrNotConnected)
	f.twitch.SetStatus(connector.Status{Bot: true})
	require.ErrorIs(t, f.service.Decided(t.Context(), c, chat(ada)), connector.ErrNotConnected)

	refused := newFixture(t, language{lang: i18n.English})
	refused.twitch.Fail(connectortest.OpDelete, errors.New("message too old"))
	require.ErrorContains(t, refused.service.Decided(t.Context(), c, chat(ada)), "message too old")
}

// TestDeleteWithEngine covers B61 with the engine: the message goes after a
// rejection, once the user was told, and after a run was queued.
func TestDeleteWithEngine(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, language{lang: i18n.English})
		h := actiontest.NewHarnessWith(t, noTypes{}, actiontest.NewCommands(), engine.WithRequirements(f.service))
		mods := h.Command("mods only")
		mods.Requirements = []command.Requirement{command.RoleRequirement{Role: role.Moderator}, deleting()}
		h.Put(mods)

		res, err := h.Engine().Trigger(t.Context(), engine.Request{Command: mods, Source: engine.SourceChat, Params: chat(person("ada", platform.Twitch))})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeRejected, res.Outcome)
		mo := chat(person("mo", platform.Twitch, role.Moderator))
		mo.MessageID = "m2"
		res, err = h.Engine().Trigger(t.Context(), engine.Request{Command: mods, Source: engine.SourceChat, Params: mo})
		require.NoError(t, err)
		assert.Equal(t, engine.OutcomeQueued, res.Outcome)
		synctest.Wait()

		calls := f.twitch.Calls()
		require.Len(t, calls, 3)
		assert.Equal(t, connectortest.OpReply, calls[0].Op, "told first")
		assert.Equal(t, connectortest.Call{Op: connectortest.OpDelete, MessageID: "m1"}, calls[1])
		assert.Equal(t, connectortest.Call{Op: connectortest.OpDelete, MessageID: "m2"}, calls[2])
	})
}
