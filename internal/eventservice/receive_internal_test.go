// SPDX-License-Identifier: MIT

package eventservice

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPurgeFiredGifts covers T1a (Nachprüfung 2026-10-25): a fired mass
// gift keeps its single gifts dropped only for firedGiftTTL, so the map
// of fired actions does not grow until the next Run.
func TestPurgeFiredGifts(t *testing.T) {
	t.Parallel()
	s := &Service{firedGifts: map[string]time.Time{
		"old": time.Now().Add(-firedGiftTTL - time.Minute),
		"new": time.Now().Add(-time.Minute),
	}}
	s.purgeFiredGifts()
	_, old := s.firedGifts["old"]
	assert.False(t, old, "the entry of the finished window is gone")
	_, fresh := s.firedGifts["new"]
	assert.True(t, fresh, "the entry of the current window stays")
}
