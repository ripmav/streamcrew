// SPDX-License-Identifier: Apache-2.0

package requirement

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/i18n"
)

// TestRemaining covers B24: the two largest units, the second rounded up
// and left out when it is 0.
func TestRemaining(t *testing.T) {
	t.Parallel()
	catalog, err := i18n.Load()
	require.NoError(t, err)
	const day = 24 * time.Hour
	for _, tc := range []struct {
		d    time.Duration
		want time.Duration
		text string
	}{
		{time.Nanosecond, time.Second, "1 second"},
		{time.Second, time.Second, "1 second"},
		{59*time.Second + 500*time.Millisecond, time.Minute, "1 minute"},
		{time.Minute, time.Minute, "1 minute"},
		{61 * time.Second, 61 * time.Second, "1 minute 1 second"},
		{61*time.Second + time.Millisecond, 62 * time.Second, "1 minute 2 seconds"},
		{59*time.Minute + 59*time.Second, 59*time.Minute + 59*time.Second, "59 minutes 59 seconds"},
		{59*time.Minute + 59*time.Second + time.Millisecond, time.Hour, "1 hour"},
		{time.Hour + time.Second, time.Hour + time.Minute, "1 hour 1 minute"},
		{2*time.Hour + 30*time.Minute, 2*time.Hour + 30*time.Minute, "2 hours 30 minutes"},
		{day - time.Second, day, "1 day"},
		{day + 30*time.Second, day + time.Hour, "1 day 1 hour"},
		{3*day + 23*time.Hour + time.Second, 4 * day, "4 days"},
		{1000 * day, 1000 * day, "1,000 days"},
	} {
		got := remaining(tc.d)
		assert.Equal(t, tc.want, got, tc.d)
		text, err := catalog.Render(i18n.English, msgWith(got))
		require.NoError(t, err)
		assert.Equal(t, "This command can be used again in "+tc.text+".", text, tc.d)
	}

	largest := time.Duration(math.MaxInt64)
	assert.Equal(t, largest-largest%time.Hour, remaining(largest), "rounds down where rounding up does not fit")
}

// msgWith returns the message for everyone with the remaining time d.
func msgWith(d time.Duration) i18n.Message {
	return i18n.Message{Key: i18n.KeyRequirementCooldownAll, Args: map[string]i18n.Value{"remaining": i18n.Duration(d)}}
}
