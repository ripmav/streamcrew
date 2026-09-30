// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/domain/role"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/store"
)

func commandService(t *testing.T) (*command.Service, *command.Codec) {
	t.Helper()
	codec, err := command.NewCodec()
	require.NoError(t, err)
	return command.NewService(openStore(t), codec), codec
}

func chatCommand(name string, enabled bool, triggers ...string) command.Command {
	return command.Command{Name: name, Kind: command.KindChat, Enabled: enabled, Triggers: triggers}
}

// TestCommandKeepsUnknownActions covers B1 and B4: a command with actions of
// unknown types is saved, loaded and written back unchanged.
func TestCommandKeepsUnknownActions(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, codec := commandService(t)

	actions := json.RawMessage(`[{"type":"chat.send","schemaVersion":1,"message":"Hi $username!"},{"type":"obs.scene","schemaVersion":4,"scene":"Main"}]`)
	cmd, err := codec.Command(command.Record{
		Name: "hug", Kind: command.KindChat, Enabled: true, Unlocked: true, Triggers: []string{"hug"}, Wildcard: true,
		Actions: actions,
	})
	require.NoError(t, err)
	cmd.Requirements = []command.Requirement{
		command.RoleRequirement{Role: role.Follower},
		command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(30 * time.Second)},
	}

	saved, err := svc.Save(ctx, cmd)
	require.NoError(t, err)
	assert.False(t, saved.ID.IsZero())
	assert.Equal(t, saved.CreatedAt, saved.UpdatedAt)
	assert.True(t, saved.Enabled)
	assert.True(t, saved.Unlocked)
	assert.True(t, saved.Wildcard)
	assert.Equal(t, command.ErrorContinue, saved.ErrorPolicy, "the default (spec command-engine.md, B71)")
	assert.Equal(t, cmd.Requirements, saved.Requirements)

	loaded, err := svc.Command(ctx, saved.ID)
	require.NoError(t, err)
	assert.Equal(t, saved, loaded)
	rec, err := codec.Record(loaded)
	require.NoError(t, err)
	assert.JSONEq(t, string(actions), string(rec.Actions))

	// Saving again keeps the creation time and the unknown actions.
	resaved, err := svc.Save(ctx, loaded)
	require.NoError(t, err)
	assert.Equal(t, saved.CreatedAt, resaved.CreatedAt)
	assert.Equal(t, loaded.Actions, resaved.Actions)

	loaded.ErrorPolicy = command.ErrorAbort
	aborting, err := svc.Save(ctx, loaded)
	require.NoError(t, err)
	assert.Equal(t, command.ErrorAbort, aborting.ErrorPolicy)
}

// TestTriggersAsEntered covers B11, B12 and B60: triggers are stored without
// "!", as a list and in the spelling they were entered in.
func TestTriggersAsEntered(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	cmd, err := svc.Save(ctx, chatCommand("hug", true, command.ParseTriggers("!Hug;good night; !umarmen")...))
	require.NoError(t, err)
	assert.Equal(t, []string{"Hug", "good night", "umarmen"}, cmd.Triggers)

	_, err = svc.Save(ctx, chatCommand("empty", true))
	require.ErrorIs(t, err, command.ErrInvalid, "B61")
}

// TestTriggerUniqueAmongEnabledChatCommands covers B14.
func TestTriggerUniqueAmongEnabledChatCommands(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	first, err := svc.Save(ctx, chatCommand("hug", true, "hug"))
	require.NoError(t, err)
	_, err = svc.Save(ctx, chatCommand("hug 2", true, "HUG"))
	require.ErrorIs(t, err, store.ErrConflict, "same trigger regardless of case")

	second, err := svc.Save(ctx, chatCommand("hug 2", false, "HUG"))
	require.NoError(t, err, "a disabled command may reuse the trigger")
	second.Enabled = true
	_, err = svc.Save(ctx, second)
	require.ErrorIs(t, err, store.ErrConflict, "enabling it collides")

	first.Enabled = false
	_, err = svc.Save(ctx, first)
	require.NoError(t, err)
	_, err = svc.Save(ctx, second)
	require.NoError(t, err, "free once the first one is disabled")

	all, err := svc.Commands(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, []string{"hug"}, all[0].Triggers)
	assert.Equal(t, []string{"HUG"}, all[1].Triggers)
}

// TestOneCommandPerEventType covers B20.
func TestOneCommandPerEventType(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	follow := command.Command{Name: "follow alert", Kind: command.KindEvent, Event: eventtype.ChannelFollow}
	saved, err := svc.Save(ctx, follow)
	require.NoError(t, err)
	assert.Equal(t, eventtype.ChannelFollow, saved.Event)

	follow.Name = "second follow alert"
	_, err = svc.Save(ctx, follow)
	require.ErrorIs(t, err, store.ErrConflict)

	follow.Event = eventtype.TwitchChannelFollow
	_, err = svc.Save(ctx, follow)
	require.NoError(t, err, "the Twitch-specific type is a type of its own (B2)")
}

// TestGroups covers B30, B31 and B62.
func TestGroups(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	fun, err := svc.SaveGroup(ctx, command.Group{Name: "Spaß", TimerInterval: 15 * time.Minute})
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, fun.TimerInterval)
	_, err = svc.SaveGroup(ctx, command.Group{Name: "SPASS"})
	require.NoError(t, err, "different letters")
	_, err = svc.SaveGroup(ctx, command.Group{Name: "spaß"})
	require.ErrorIs(t, err, store.ErrConflict, "names are unique regardless of case")

	cmd := chatCommand("hug", true, "hug")
	cmd.GroupID = fun.ID
	cmd, err = svc.Save(ctx, cmd)
	require.NoError(t, err)
	assert.Equal(t, fun.ID, cmd.GroupID)

	missing := chatCommand("lost", true, "lost")
	missing.GroupID = id.New()
	_, err = svc.Save(ctx, missing)
	require.ErrorIs(t, err, store.ErrConflict, "the group must exist")

	groups, err := svc.Groups(ctx)
	require.NoError(t, err)
	require.Len(t, groups, 2)

	require.NoError(t, svc.DeleteGroup(ctx, fun.ID))
	cmd, err = svc.Command(ctx, cmd.ID)
	require.NoError(t, err, "B62: the command stays")
	assert.True(t, cmd.GroupID.IsZero())
	require.ErrorIs(t, svc.DeleteGroup(ctx, fun.ID), store.ErrNotFound)
	_, err = svc.Group(ctx, fun.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestDeleteCommand(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc, _ := commandService(t)

	cmd, err := svc.Save(ctx, chatCommand("hug", true, "hug"))
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, cmd.ID))
	_, err = svc.Command(ctx, cmd.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.ErrorIs(t, svc.Delete(ctx, cmd.ID), store.ErrNotFound)

	_, err = svc.Save(ctx, chatCommand("hug again", true, "hug"))
	require.NoError(t, err, "the trigger is free again")
}
