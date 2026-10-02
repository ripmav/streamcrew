// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/command"
	"github.com/ripmav/streamcrew/internal/domain/id"
	"github.com/ripmav/streamcrew/internal/polydoc"
	"github.com/ripmav/streamcrew/internal/store"
)

// TestCooldowns covers requirements.md, B20 to B23: one stored end per
// command or cooldown group and user, which a new start replaces, and ended
// cooldowns are forgotten.
func TestCooldowns(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)
	codec, err := command.NewCodec()
	require.NoError(t, err)
	svc := newCommandService(t, s, codec)

	cmd := chatCommand("boom", true, "boom")
	cmd.Requirements = []command.Requirement{command.CooldownRequirement{Scope: command.CooldownPerUser, Duration: polydoc.Duration(time.Minute)}}
	cmd, err = svc.Save(ctx, cmd)
	require.NoError(t, err)
	sounds, err := svc.SaveCooldownGroup(ctx, command.CooldownGroup{Name: "Sounds", Duration: time.Minute})
	require.NoError(t, err)
	ada, _, err := s.UpsertIdentity(ctx, twitchIdentity("1001", "ada"))
	require.NoError(t, err)
	bob, _, err := s.UpsertIdentity(ctx, twitchIdentity("1002", "bob"))
	require.NoError(t, err)

	base := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	keys := []command.CooldownKey{
		{Command: cmd.ID},
		{Command: cmd.ID, User: ada.ID},
		{Command: cmd.ID, User: bob.ID},
		{Group: sounds.ID},
		{Group: sounds.ID, User: ada.ID},
	}
	for _, k := range keys {
		_, ok, err := s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		assert.False(t, ok, "no cooldown before the first start: %v", k)
	}
	ends := func(i int) time.Time { return base.Add(time.Duration(i+1)*time.Minute + 1500*time.Microsecond) }
	for i, k := range keys {
		require.NoError(t, s.PutCooldown(ctx, k, ends(i), base))
	}
	for i, k := range keys {
		got, ok, err := s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		require.True(t, ok, k)
		assert.Equal(t, ends(i).Truncate(time.Millisecond), got, "each key its own end, in milliseconds: %v", k)
	}

	t.Run("a new start replaces the end", func(t *testing.T) {
		later := base.Add(time.Hour)
		require.NoError(t, s.PutCooldown(ctx, keys[1], later, base))
		got, ok, err := s.CooldownEnd(ctx, keys[1])
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, later, got)
		got, ok, err = s.CooldownEnd(ctx, keys[2])
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, ends(2).Truncate(time.Millisecond), got, "other users keep theirs")
	})

	t.Run("taking back a start", func(t *testing.T) {
		k := command.CooldownKey{Command: cmd.ID, User: ada.ID}
		first, second := base.Add(2*time.Hour+time.Microsecond), base.Add(3*time.Hour)
		require.NoError(t, s.PutCooldown(ctx, k, first, base))
		require.NoError(t, s.PutCooldown(ctx, k, second, base))
		require.NoError(t, s.DeleteCooldown(ctx, k, first))
		got, ok, err := s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, second, got, "a later start stays")
		require.NoError(t, s.DeleteCooldown(ctx, k, second.Add(time.Microsecond)))
		_, ok, err = s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		assert.False(t, ok, "the end in milliseconds")
		require.NoError(t, s.DeleteCooldown(ctx, k, second), "nothing to delete is no error")
		require.NoError(t, s.PutCooldown(ctx, k, base.Add(time.Hour), base))
	})

	t.Run("invalid keys", func(t *testing.T) {
		for _, k := range []command.CooldownKey{{}, {User: ada.ID}, {Command: cmd.ID, Group: sounds.ID}} {
			_, _, err := s.CooldownEnd(ctx, k)
			require.Error(t, err, k)
			require.Error(t, s.PutCooldown(ctx, k, base, base), k)
			require.Error(t, s.DeleteCooldown(ctx, k, base), k)
		}
		require.ErrorIs(t, s.PutCooldown(ctx, command.CooldownKey{Command: cmd.ID, User: id.New()}, base, base), store.ErrConflict, "unknown user")
		require.ErrorIs(t, s.PutCooldown(ctx, command.CooldownKey{Group: id.New()}, base, base), store.ErrConflict, "unknown cooldown group")
	})

	// The cooldown of keys[0] ended at ends(0); keys[2] and keys[3] have not.
	require.NoError(t, s.PutCooldown(ctx, keys[4], base.Add(time.Hour), ends(0).Truncate(time.Millisecond)))
	_, ok, err := s.CooldownEnd(ctx, keys[0])
	require.NoError(t, err)
	assert.False(t, ok, "a start forgets the cooldowns that ended")
	for _, k := range keys[1:] {
		_, ok, err := s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		assert.True(t, ok, "running cooldowns stay: %v", k)
	}

	require.NoError(t, s.DeleteUser(ctx, bob.ID))
	_, ok, err = s.CooldownEnd(ctx, keys[2])
	require.NoError(t, err)
	assert.False(t, ok, "the cooldowns of a deleted user go")
	require.NoError(t, svc.DeleteCooldownGroup(ctx, sounds.ID))
	for _, k := range keys[3:] {
		_, ok, err = s.CooldownEnd(ctx, k)
		require.NoError(t, err)
		assert.False(t, ok, "the cooldowns of a deleted cooldown group go: %v", k)
	}
	require.NoError(t, svc.Delete(ctx, cmd.ID))
	_, ok, err = s.CooldownEnd(ctx, keys[1])
	require.NoError(t, err)
	assert.False(t, ok, "the cooldowns of a deleted command go")
}
