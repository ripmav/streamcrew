// SPDX-License-Identifier: MIT

package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
)

// TestUserAnswer covers the per-user answers of user-specific chat commands
// (roadmap 5.6, P1): set, get, replace and delete.
func TestUserAnswer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openStore(t)

	ada, _, err := s.UpsertIdentity(ctx, user.Identity{Platform: platform.Twitch, PlatformUserID: "1001", Login: "ada", DisplayName: "ada"})
	require.NoError(t, err)

	// nothing stored yet
	_, found, err := s.UserAnswer(ctx, ada.ID, "note")
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, s.SetUserAnswer(ctx, ada.ID, "note", "first"))
	ans, found, err := s.UserAnswer(ctx, ada.ID, "note")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "first", ans)

	// a second key is independent
	assert.NoError(t, s.SetUserAnswer(ctx, ada.ID, "title", "queen"))
	other, found, err := s.UserAnswer(ctx, ada.ID, "title")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "queen", other)

	// the answer is replaced, not duplicated
	require.NoError(t, s.SetUserAnswer(ctx, ada.ID, "note", "second"))
	ans, found, err = s.UserAnswer(ctx, ada.ID, "note")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "second", ans)

	// deleting removes it
	require.NoError(t, s.DeleteUserAnswer(ctx, ada.ID, "note"))
	_, found, err = s.UserAnswer(ctx, ada.ID, "note")
	require.NoError(t, err)
	assert.False(t, found)
	// the other key is untouched
	_, found, err = s.UserAnswer(ctx, ada.ID, "title")
	require.NoError(t, err)
	assert.True(t, found)
}
