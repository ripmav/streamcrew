// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	json "encoding/json/v2"
	"io"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/profile"
	"github.com/ripmav/streamcrew/internal/settings"
	"github.com/ripmav/streamcrew/internal/store"
)

// channelsEnv prepares a data directory with the profile "demo" (like
// authEnv), for the commands that manage the additional channels.
func channelsEnv(ctx context.Context, t *testing.T) (*Env, string) {
	t.Helper()
	return authEnv(ctx, t)
}

// channelsIn reads the channels section of the demo profile read-only.
func channelsIn(ctx context.Context, t *testing.T, dataDir string) settings.Channels {
	t.Helper()
	ro, err := store.OpenReadOnly(ctx, profilePath(dataDir, "demo"))
	require.NoError(t, err)
	defer ro.Close()
	doc, found, err := ro.Settings(ctx, "channels")
	require.NoError(t, err)
	if !found {
		return settings.DefaultChannels()
	}
	var chs settings.Channels
	require.NoError(t, json.Unmarshal(doc, &chs))
	return chs
}

func profilePath(dataDir, id string) string {
	return profile.NewManager(dataDir).Path(id)
}

// TestChannelsKong covers the kong structure: the commands parse and an
// unknown subcommand is a usage error.
func TestChannelsKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}

	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the channels commands are a valid kong structure")
	kctx, err := parser.Parse([]string{"profile", "channels", "add", "somechannel"})
	require.NoError(t, err)
	assert.Equal(t, "profile channels add <login>", kctx.Command())
	assert.Equal(t, "somechannel", root.ProfileCmd.Channels.Add.Login)

	kctx, err = parser.Parse([]string{"profile", "channels", "remove", "somechannel"})
	require.NoError(t, err)
	assert.Equal(t, "profile channels remove <login>", kctx.Command())
	assert.Equal(t, "somechannel", root.ProfileCmd.Channels.Remove.Login)

	kctx, err = parser.Parse([]string{"profile", "channels", "list", "-o", "json"})
	require.NoError(t, err)
	assert.Equal(t, "profile channels list", kctx.Command())
	assert.Equal(t, "json", root.ProfileCmd.Channels.List.Output)

	kctx, err = parser.Parse([]string{"profile", "channels", "chat", "irc"})
	require.NoError(t, err)
	assert.Equal(t, "profile channels chat <adapter>", kctx.Command())
	assert.Equal(t, "irc", root.ProfileCmd.Channels.Chat.Adapter)
}

// TestChannelsAddRemoveList covers the commands end to end against a real
// profile database: add, list, duplicate and invalid logins, and remove by
// a different case.
func TestChannelsAddRemoveList(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := channelsEnv(ctx, t)

	add := func(t *testing.T, login string) error {
		t.Helper()
		stdout, stderr := &syncedLog{}, &syncedLog{}
		env.Stdout, env.Stderr = stdout, stderr
		err := (channelsAddCmd{Login: login}).Run(ctx, env)
		if err != nil {
			t.Logf("stderr: %s", stderr.String())
		}
		return err
	}
	remove := func(t *testing.T, login string) error {
		t.Helper()
		stdout, stderr := &syncedLog{}, &syncedLog{}
		env.Stdout, env.Stderr = stdout, stderr
		err := (channelsRemoveCmd{Login: login}).Run(ctx, env)
		if err != nil {
			t.Logf("stderr: %s", stderr.String())
		}
		return err
	}
	list := func(t *testing.T) (string, error) {
		t.Helper()
		stdout, stderr := &syncedLog{}, &syncedLog{}
		env.Stdout, env.Stderr = stdout, stderr
		err := (channelsListCmd{}).Run(ctx, env)
		if err != nil {
			t.Logf("stderr: %s", stderr.String())
		}
		return stdout.String(), err
	}
	chat := func(t *testing.T, adapter string) error {
		t.Helper()
		stdout, stderr := &syncedLog{}, &syncedLog{}
		env.Stdout, env.Stderr = stdout, stderr
		err := (channelsChatCmd{Adapter: adapter}).Run(ctx, env)
		if err != nil {
			t.Logf("stderr: %s", stderr.String())
		}
		return err
	}

	out, err := list(t)
	require.NoError(t, err)
	assert.Equal(t, "no additional channels\n", out)

	require.NoError(t, add(t, "somechannel"), "add the first channel")
	out, err = list(t)
	require.NoError(t, err)
	assert.Equal(t, "somechannel\nchat adapter: eventsub\n", out)
	assert.Equal(t, []string{"somechannel"}, channelsIn(ctx, t, dataDir).Additional, "the section is stored")

	require.NoError(t, add(t, "other_channel"), "add a second channel")

	var ue *usageError
	assert.ErrorAs(t, add(t, "SOMECHANNEL"), &ue, "a duplicate by a different case is a usage error")
	assert.ErrorAs(t, add(t, "bad login!"), &ue, "a login with an invalid shape is a usage error")

	require.NoError(t, remove(t, "Somechannel"), "remove by a different case")
	out, err = list(t)
	require.NoError(t, err)
	assert.Equal(t, "other_channel\nchat adapter: eventsub\n", out)
	assert.Equal(t, []string{"other_channel"}, channelsIn(ctx, t, dataDir).Additional)

	assert.ErrorAs(t, remove(t, "missing"), &ue, "removing a channel that is not listed is a usage error")
	assert.Equal(t, []string{"other_channel"}, channelsIn(ctx, t, dataDir).Additional, "a failed remove changes nothing")

	assert.ErrorAs(t, chat(t, "websocket"), &ue, "an unknown adapter is a usage error")
	require.NoError(t, chat(t, "irc"))
	out, err = list(t)
	require.NoError(t, err)
	assert.Equal(t, "other_channel\nchat adapter: irc\n", out)
	assert.Equal(t, settings.ChatAdapterIRC, channelsIn(ctx, t, dataDir).ChatAdapter, "the adapter is stored")

	require.NoError(t, remove(t, "other_channel"))
	out, err = list(t)
	require.NoError(t, err)
	assert.Equal(t, "no additional channels\n", out)
}
