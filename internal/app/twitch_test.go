// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/helix"
)

// TestPollStatusTheWinnerOnlyWhenEnded is the regression of review
// 347-9: while the poll runs, the leading choice is no winner; the
// winner stands only once the poll is over.
func TestPollStatusTheWinnerOnlyWhenEnded(t *testing.T) {
	t.Parallel()
	poll := &helix.Poll{
		Status:   helix.PollVoting,
		Outcomes: []helix.PollOutcome{{Choice: "yes", Votes: 3}, {Choice: "no", Votes: 1}},
	}

	// While it runs: not ended, no winner, even though a choice leads.
	st := pollStatus(poll)
	assert.False(t, st.Ended)
	assert.Empty(t, st.Winner, "the leading choice is no winner while the poll runs")

	// Once it is over: the leading choice is the winner.
	poll.Status = helix.PollCompleted
	st = pollStatus(poll)
	assert.True(t, st.Ended)
	assert.Equal(t, "yes", st.Winner)
}
