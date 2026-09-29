// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestJitter(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{time.Second, time.Minute, 3} {
		seen := map[time.Duration]bool{}
		for range 200 {
			got := jitter(d)
			assert.GreaterOrEqual(t, got, d-d/2)
			assert.LessOrEqual(t, got, d)
			seen[got] = true
		}
		assert.Greater(t, len(seen), 1, "jitter(%s) must vary", d)
	}
	assert.Equal(t, time.Duration(0), jitter(0))
	assert.Equal(t, time.Nanosecond, jitter(time.Nanosecond))
}
