// SPDX-License-Identifier: MIT

package cli

import (
	"io"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestUserKong covers the kong structure of the user commands.
func TestUserKong(t *testing.T) {
	t.Parallel()
	opts := []kong.Option{
		kong.Name("streamcrew"),
		kong.Writers(io.Discard, io.Discard),
		kong.UsageOnError(),
		kong.Vars{"default_data_dir": t.TempDir()},
	}
	var root Root
	parser, err := kong.New(&root, append(opts, kong.Exit(func(int) { t.Error("kong exited on a valid command line") }))...)
	require.NoError(t, err, "the user commands are a valid kong structure")
	kctx, err := parser.Parse([]string{"user", "merge", "twitch", "ada", "kick", "bob"})
	require.NoError(t, err)
	assert.Equal(t, "user merge <keep-platform> <keep-login> <drop-platform> <drop-login>", kctx.Command())
	assert.Equal(t, "twitch", root.User.Merge.KeepPlatform)
	assert.Equal(t, "bob", root.User.Merge.DropLogin)
}

// TestUserMerge covers the merge end to end: the accounts of the second
// user are linked to the first, the second is deleted; the same user, a
// missing login and an invalid platform are usage errors.
func TestUserMerge(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, dataDir := authEnv(ctx, t)

	// ada on twitch + youtube, bob on kick.
	st, err := store.Open(ctx, profilePath(dataDir, "demo"))
	require.NoError(t, err)
	ada, _, err := st.UpsertIdentity(ctx, user.Identity{Platform: platform.Twitch, PlatformUserID: "1001", Login: "ada", DisplayName: "Ada"})
	require.NoError(t, err)
	require.NoError(t, st.AddIdentity(ctx, ada.ID, user.Identity{Platform: platform.YouTube, PlatformUserID: "UC7", Login: "ada", DisplayName: "Ada"}))
	bob, _, err := st.UpsertIdentity(ctx, user.Identity{Platform: platform.Kick, PlatformUserID: "k9", Login: "bob", DisplayName: "Bob"})
	require.NoError(t, err)
	require.NoError(t, st.Close())

	merge := func(t *testing.T, kp, kl, dp, dl string) (string, error) {
		t.Helper()
		stdout, stderr := &syncedLog{}, &syncedLog{}
		env.Stdout, env.Stderr = stdout, stderr
		err := (userMergeCmd{KeepPlatform: kp, KeepLogin: kl, DropPlatform: dp, DropLogin: dl}).Run(ctx, env)
		if err != nil {
			t.Logf("stderr: %s", stderr.String())
		}
		return stdout.String(), err
	}

	out, err := merge(t, "twitch", "ada", "kick", "bob")
	require.NoError(t, err, "the merge succeeds")
	assert.Contains(t, out, "merged kick \"bob\" into twitch \"ada\"")

	st, err = store.Open(ctx, profilePath(dataDir, "demo"))
	require.NoError(t, err)
	merged, err := st.User(ctx, ada.ID)
	require.NoError(t, err)
	require.Len(t, merged.Identities, 3, "ada gains bob's account")
	_, err = st.UserByIdentity(ctx, platform.Kick, "k9")
	require.NoError(t, err)
	assert.Equal(t, ada.ID, merged.ID)
	_, err = st.User(ctx, bob.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "the merged-in user is deleted")
	require.NoError(t, st.Close())

	var ue *usageError
	_, err = merge(t, "twitch", "ada", "youtube", "ada")
	assert.ErrorAs(t, err, &ue, "the same user is a usage error")
	_, err = merge(t, "twitch", "ada", "kick", "nobody")
	assert.ErrorAs(t, err, &ue, "a missing login is a usage error")
	_, err = merge(t, "Twitch", "ada", "kick", "nobody")
	assert.ErrorAs(t, err, &ue, "an invalid platform is a usage error")
}
