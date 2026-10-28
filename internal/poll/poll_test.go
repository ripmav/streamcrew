// SPDX-License-Identifier: MIT

// The behavior test of the poll service (roadmap 7.5, spec overlays.md,
// B34): the subscribers see the state changes of the active vote, not
// every update.
package poll_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/poll"
)

// TestPollChanges covers the OnChange subscribers: they get the first
// state and every change after it, and no repeated state.
func TestPollChanges(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	svc := poll.New()
	_, ok := svc.Active()
	assert.False(t, ok)

	var got []poll.State
	svc.OnChange(func(_ context.Context, st poll.State) {
		got = append(got, st)
	})
	// Each update copies the outcomes, because the service keeps the
	// slice it is given.
	st := poll.State{Kind: poll.KindPoll, Name: "The vote", Outcomes: []poll.Outcome{{Name: "Yes"}}}
	svc.Update(ctx, st)
	svc.Update(ctx, st) // the same state: no notification
	next := st
	next.Outcomes = append([]poll.Outcome(nil), st.Outcomes...)
	next.Outcomes[0].Votes = 2
	svc.Update(ctx, next)
	next.Ended, next.Winner = true, "Yes"
	svc.Update(ctx, next)

	require.Len(t, got, 3)
	assert.Equal(t, int64(2), got[1].Outcomes[0].Votes)
	assert.True(t, got[2].Ended)
	assert.Equal(t, "Yes", got[2].Winner)
	active, ok := svc.Active()
	require.True(t, ok)
	assert.Equal(t, next, active)
}

// TestPredictionKind covers the prediction with its points and colors.
func TestPredictionKind(t *testing.T) {
	t.Parallel()
	svc := poll.New()
	var got []poll.State
	svc.OnChange(func(_ context.Context, st poll.State) {
		got = append(got, st)
	})
	st := poll.State{
		Kind: poll.KindPrediction,
		Outcomes: []poll.Outcome{
			{Name: "Yes", Points: 10, Color: "green"},
			{Name: "No", Points: 5, Color: "blue"},
		},
	}
	svc.Update(t.Context(), st)
	next := st
	next.Outcomes = append([]poll.Outcome(nil), st.Outcomes...)
	next.Outcomes[0].Points = 12 // a change of one outcome is a change
	svc.Update(t.Context(), next)
	require.Len(t, got, 2)
	assert.Equal(t, int64(12), got[1].Outcomes[0].Points)
}
