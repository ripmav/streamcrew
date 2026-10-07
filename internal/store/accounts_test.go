// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/store"
)

// TestAccounts covers ADR-0014: the metadata of platform logins, one per
// platform and role, a new login replacing the account, and the removal by
// logout.
func TestAccounts(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	streamer := store.Account{Platform: "twitch", Role: "streamer", Login: "ada", UserID: "1001",
		Scopes: "chat:read user:write:chat", ClientID: "client-1"}
	bot := store.Account{Platform: "twitch", Role: "bot", Login: "ada-bot", UserID: "1002",
		Scopes: "chat:read", ClientID: "client-1"}
	other := store.Account{Platform: "youtube", Role: "streamer", Login: "ada", UserID: "UC-9",
		Scopes: "youtube.read", ClientID: "client-2"}

	t.Run("no account before the first login", func(t *testing.T) {
		_, found, err := s.Account(ctx, "twitch", "streamer")
		require.NoError(t, err)
		assert.False(t, found)
		all, err := s.Accounts(ctx)
		require.NoError(t, err)
		assert.Empty(t, all)
	})

	t.Run("a login saves its metadata", func(t *testing.T) {
		before := time.Now().Truncate(time.Millisecond)
		require.NoError(t, s.UpsertAccount(ctx, streamer))
		got, found, err := s.Account(ctx, "twitch", "streamer")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, streamer.Platform, got.Platform)
		assert.Equal(t, streamer.Role, got.Role)
		assert.Equal(t, streamer.Login, got.Login)
		assert.Equal(t, streamer.UserID, got.UserID)
		assert.Equal(t, streamer.Scopes, got.Scopes)
		assert.Equal(t, streamer.ClientID, got.ClientID)
		assert.False(t, got.UpdatedAt.Before(before), "the updated time is now")
		assert.Equal(t, got.UpdatedAt, got.UpdatedAt.UTC(), "the updated time is UTC")
	})

	t.Run("a new login replaces the account", func(t *testing.T) {
		first, found, err := s.Account(ctx, "twitch", "streamer")
		require.NoError(t, err)
		require.True(t, found)
		time.Sleep(2 * time.Millisecond)
		again := streamer
		again.Login = "ada-live"
		again.ClientID = "client-2"
		require.NoError(t, s.UpsertAccount(ctx, again))
		got, found, err := s.Account(ctx, "twitch", "streamer")
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "ada-live", got.Login)
		assert.Equal(t, "client-2", got.ClientID)
		assert.True(t, got.UpdatedAt.After(first.UpdatedAt), "a new login updates the time")
		all, err := s.Accounts(ctx)
		require.NoError(t, err)
		assert.Len(t, all, 1, "a replacement is not a second account")
	})

	t.Run("accounts of other roles and platforms coexist", func(t *testing.T) {
		require.NoError(t, s.UpsertAccount(ctx, bot))
		require.NoError(t, s.UpsertAccount(ctx, other))
		all, err := s.Accounts(ctx)
		require.NoError(t, err)
		var pairs [][2]string
		for _, a := range all {
			pairs = append(pairs, [2]string{a.Platform, a.Role})
		}
		assert.Equal(t, [][2]string{{"twitch", "bot"}, {"twitch", "streamer"}, {"youtube", "streamer"}},
			pairs, "by platform and role")
	})

	t.Run("logout removes the account", func(t *testing.T) {
		deleted, err := s.DeleteAccount(ctx, "twitch", "bot")
		require.NoError(t, err)
		assert.True(t, deleted)
		_, found, err := s.Account(ctx, "twitch", "bot")
		require.NoError(t, err)
		assert.False(t, found)
		deleted, err = s.DeleteAccount(ctx, "twitch", "bot")
		require.NoError(t, err)
		assert.False(t, deleted, "a second removal finds nothing")
		_, found, err = s.Account(ctx, "twitch", "streamer")
		require.NoError(t, err)
		assert.True(t, found, "the other accounts stay")
	})

	t.Run("a role is streamer or bot", func(t *testing.T) {
		bad := streamer
		bad.Role = "admin"
		require.Error(t, s.UpsertAccount(ctx, bad), "the table rejects a foreign role")
		_, found, err := s.Account(ctx, "twitch", "admin")
		require.NoError(t, err)
		assert.False(t, found)
	})
}
