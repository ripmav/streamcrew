// SPDX-License-Identifier: MIT

package chat_test

import (
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/action"
	"github.com/ripmav/streamcrew/internal/action/actiontest"
	"github.com/ripmav/streamcrew/internal/action/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/connector/connectortest"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/template"
)

// platformMessage returns a new platform message action with the message
// on the platform name.
func (f *fixture) platformMessage(name platform.Name, message string) chat.PlatformMessage {
	f.t.Helper()
	d, ok := f.reg.Descriptor(chat.TypePlatformMessage)
	require.True(f.t, ok)
	doc := `{"platform":"` + string(name) + `","message":` + quote(message) + `}`
	a, err := d.Decode([]byte(doc), json.DefaultOptionsV2())
	require.NoError(f.t, err)
	m, ok := a.(chat.PlatformMessage)
	require.True(f.t, ok)
	return m
}

func TestPlatformMessageConformance(t *testing.T) {
	t.Parallel()
	reg := registry(t, &connector.Set{}, &logs{})
	d, ok := reg.Descriptor(chat.TypePlatformMessage)
	require.True(t, ok)
	actiontest.Suite{Descriptor: d, Update: update(), Examples: []actiontest.Example{
		{Name: "message", Doc: `{"type":"platform_message","platform":"youtube","message":"Hi $username","asStreamer":true,"reply":true}`, Valid: true},
		{Name: "with the defaults", Doc: `{"type":"platform_message","platform":"twitch","message":"Hi"}`, Valid: true},
		{Name: "platform without adapter", Doc: `{"type":"platform_message","enabled":false,"platform":"velora2","message":"Hi"}`, Valid: true},
		{Name: "platform missing", Doc: `{"type":"platform_message","message":"Hi"}`},
		{Name: "empty platform", Doc: `{"type":"platform_message","platform":"","message":"Hi"}`},
		{Name: "platform in uppercase", Doc: `{"type":"platform_message","platform":"Twitch","message":"Hi"}`},
		{Name: "platform too long", Doc: `{"type":"platform_message","platform":"` + strings.Repeat("a", 33) + `","message":"Hi"}`},
		{Name: "message missing", Doc: `{"type":"platform_message","platform":"twitch"}`},
		{Name: "empty message", Doc: `{"type":"platform_message","platform":"twitch","message":""}`},
		{Name: "no whispers", Doc: `{"type":"platform_message","platform":"twitch","message":"Hi","recipient":"bob"}`},
		{Name: "no kinds", Doc: `{"type":"platform_message","kind":"message","platform":"twitch","message":"Hi"}`},
	}}.Run(t)
}

// TestPlatformMessageNew: a new platform message has no platform and no
// message, sends as the bot and does not reply (actions.md B67).
func TestPlatformMessageNew(t *testing.T) {
	t.Parallel()
	reg := registry(t, &connector.Set{}, &logs{})
	d, ok := reg.Descriptor(chat.TypePlatformMessage)
	require.True(t, ok)
	assert.Equal(t, chat.PlatformMessage{Common: action.On()}, withoutPorts(t, d.New()))
}

// withoutPorts returns the platform message a without its ports, to compare
// its configuration.
func withoutPorts(t *testing.T, a any) chat.PlatformMessage {
	t.Helper()
	m, ok := a.(chat.PlatformMessage)
	require.True(t, ok)
	return chat.PlatformMessage{Common: m.Common, Platform: m.Platform, Message: m.Message, AsStreamer: m.AsStreamer, Reply: m.Reply}
}

// TestPlatformMessage covers actions.md B67: the message goes to the chosen
// platform only, from the account of B61, and answers the triggering
// message there if the run was triggered on it (B64).
func TestPlatformMessage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		streamer := f.platformMessage(platform.Twitch, "as streamer")
		streamer.AsStreamer = true
		reply := f.platformMessage(platform.Twitch, "reply")
		reply.Reply = true
		elsewhere := f.platformMessage(platform.Kick, "reply on Kick")
		elsewhere.Reply = true
		in := f.start(chatParams(),
			f.platformMessage(platform.Twitch, "Hi $username"), streamer, reply,
			f.platformMessage(platform.YouTube, "on YouTube"), elsewhere)
		assert.Empty(t, in.Errors)
		assert.Equal(t, []connectortest.Call{
			sent(connector.AccountBot, "Hi alice"),
			sent(connector.AccountStreamer, "as streamer"),
			{Op: connectortest.OpReply, From: connector.AccountBot, Text: "reply", MessageID: "m1"},
		}, f.twitch.Calls())
		assert.Equal(t, []connectortest.Call{sent(connector.AccountStreamer, "on YouTube")}, f.youtube.Calls())
		assert.Equal(t, []connectortest.Call{sent(connector.AccountStreamer, "reply on Kick")}, f.kick.Calls(),
			"the run was not triggered on Kick")
	})
}

// TestPlatformMessageNotConnected covers actions.md B67: on a platform
// that is not connected, or that the profile does not have, nothing
// happens; the core logs it, and the action does not fail. The same holds
// for an empty message (B65).
func TestPlatformMessageNotConnected(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.kick.SetStatus(connector.Status{Bot: true})
		p := chatParams()
		p.Values = map[string]template.Value{"blank": template.TextValue("  ")}
		in := f.start(p,
			f.platformMessage(platform.Kick, "Hi"),
			f.platformMessage("velora", "Hi"),
			f.platformMessage(platform.Twitch, "$blank"))
		assert.Empty(t, in.Errors)
		assert.Equal(t, 2, strings.Count(f.logs.String(), "platform message not sent: platform not connected"))
		assert.Contains(t, f.logs.String(), "platform=velora")
		assert.Contains(t, f.logs.String(), "empty after rendering")
		for _, pl := range []*connectortest.Platform{f.twitch, f.youtube, f.kick} {
			assert.Empty(t, pl.Calls(), pl.Name())
		}
	})
}

// TestPlatformMessageFails covers actions.md B66 for one platform.
func TestPlatformMessageFails(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t)
		f.youtube.Fail(connectortest.OpSend, errors.New("quota exceeded"))
		in := f.start(chatParams(), f.platformMessage(platform.YouTube, "Hi"))
		require.Len(t, in.Errors, 1)
		assert.Equal(t, chat.TypePlatformMessage, in.Errors[0].Type)
		assert.Equal(t, "not sent on youtube: youtube: quota exceeded", in.Errors[0].Message)
	})
}
