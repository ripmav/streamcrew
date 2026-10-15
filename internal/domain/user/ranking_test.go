// SPDX-License-Identifier: MIT

package user_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/user"
)

func TestStatValid(t *testing.T) {
	t.Parallel()
	for _, s := range []user.Stat{
		user.StatWatchMinutes, user.StatMessages, user.StatCommandsRun, user.StatMentions, user.StatStreamsWatched,
	} {
		assert.True(t, s.Valid(), s)
	}
	assert.False(t, user.Stat("donated_cents").Valid())
	assert.False(t, user.Stat("strikes").Valid())
	assert.False(t, user.Stat("").Valid())
	assert.False(t, user.Stat("watch minutes").Valid())
}
