// SPDX-License-Identifier: MIT

package requirement_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/engine"
	"github.com/ripmav/streamcrew/internal/i18n"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/settings"
)

// setDefaultPlatform changes general.defaultPlatform so the change applies
// from the next decision (spec chat-service.md, B26, B27).
func setDefaultPlatform(f *fixture, p platform.Name) {
	f.settings.setGeneral(func(g *settings.General) { g.DefaultPlatform = p })
}

// setGlobalDelete changes chat.deleteTriggerMessage so the change applies
// from the next decision (spec chat-service.md, B30).
func setGlobalDelete(f *fixture, on bool) {
	f.settings.setChat(func(c *settings.Chat) { c.DeleteTriggerMessage = on })
}

// TestDefaultPlatformArgument covers spec chat-service.md, B26 and B27: a
// run without a platform looks the user of an argument up on
// general.defaultPlatform, and a change applies from the next decision,
// not the next service.
func TestDefaultPlatformArgument(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	c := hug(arg("!target", command.ArgumentUser))
	// the default platform is Twitch; bob is on Twitch, kim on Kick.
	got, err := apply(t.Context(), f.service, c, engine.Params{Args: []string{"bob"}, ArgsText: "bob"})
	require.NoError(t, err)
	require.Equal(t, engine.VerdictMet, got.Verdict, "bob is on the default platform Twitch")
	// a changed default platform applies from the next decision.
	setDefaultPlatform(f, platform.Kick)
	got, err = apply(t.Context(), f.service, c, engine.Params{Args: []string{"bob"}, ArgsText: "bob"})
	require.NoError(t, err)
	assert.Equal(t, engine.VerdictRejected, got.Verdict, "bob is not on the new default platform Kick")
}

// TestDefaultPlatformCooldown covers spec chat-service.md, B26 and B27: a
// per-user cooldown of a run without a user and without a platform counts
// against the streamer on general.defaultPlatform, and a change applies
// from the next decision.
func TestDefaultPlatformCooldown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	c := cmd()
	c.Requirements = []command.Requirement{command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(time.Minute)}}
	// the streamer on Twitch starts the cooldown for the default platform.
	_, err := apply(t.Context(), f.service, c, engine.Params{})
	require.NoError(t, err)
	// a second run on the default platform is in the cooldown.
	got, err := apply(t.Context(), f.service, c, engine.Params{})
	require.NoError(t, err)
	assert.Equal(t, engine.VerdictRejected, got.Verdict, "still in the cooldown on Twitch")
	// move the default platform to Kick: a new cooldown there.
	setDefaultPlatform(f, platform.Kick)
	got, err = apply(t.Context(), f.service, c, engine.Params{})
	require.NoError(t, err)
	assert.Equal(t, engine.VerdictMet, got.Verdict, "the default platform is now Kick")
	got, err = apply(t.Context(), f.service, c, engine.Params{})
	require.NoError(t, err)
	assert.Equal(t, engine.VerdictRejected, got.Verdict, "in the cooldown on Kick")
}

// TestGlobalDeleteTriggerMessage covers spec chat-service.md, B30 and B38:
// the global switch of the chat section deletes the triggering message of
// every chat command, in addition to (OR) the switch of the command, and a
// change applies from the next decision.
func TestGlobalDeleteTriggerMessage(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	ada := person("ada", platform.Twitch)
	// per-command off, global on: the message is deleted (B38, OR).
	setGlobalDelete(f, true)
	require.NoError(t, f.service.Decided(t.Context(), cmd(command.SettingsRequirement{}), chat(ada)))
	assert.Equal(t, []connectortest.Call{{Op: connectortest.OpDelete, MessageID: "m1"}}, f.twitch.Calls(), "the global switch deletes")
	// the global switch off again: nothing is deleted (B38).
	f.twitch.ForgetCalls()
	setGlobalDelete(f, false)
	require.NoError(t, f.service.Decided(t.Context(), cmd(command.SettingsRequirement{}), chat(ada)))
	assert.Empty(t, f.twitch.Calls(), "the change applies from the next decision")
}

// TestGlobalDeleteTriggerMessageFails covers B30: the global switch deletes
// even a command without its own setting, so a message it cannot delete is
// an error like the per-command switch.
func TestGlobalDeleteTriggerMessageFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, language{lang: i18n.English})
	setGlobalDelete(f, true)
	ada := person("ada", platform.Twitch)
	noID := chat(ada)
	noID.MessageID = ""
	require.ErrorContains(t, f.service.Decided(t.Context(), cmd(), noID), "no ID of the message")
}
