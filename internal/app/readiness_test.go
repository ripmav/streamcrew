// SPDX-License-Identifier: MIT

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/supervisor"
)

// TestReadinessToleratesFailedNonCriticalRunnables covers the readiness
// semantics of roadmap 7.1 (B1): the first non-critical runnable (the
// overlay server) may fail permanently — e.g. its port is taken — without
// the core losing its readiness; a failed critical runnable still blocks.
func TestReadinessToleratesFailedNonCriticalRunnables(t *testing.T) {
	t.Parallel()
	r := newReadiness()
	r.update(supervisor.Status{Name: "http", State: supervisor.StateRunning, Critical: true})
	assert.True(t, r.Ready())
	assert.True(t, r.doneIsClosed(), "one running runnable: done")

	// A failed or stopped non-critical runnable does not block.
	r.update(supervisor.Status{Name: "overlay", State: supervisor.StateFailed, Critical: false})
	assert.True(t, r.Ready())
	r.update(supervisor.Status{Name: "backup", State: supervisor.StateStopped, Critical: false})
	assert.True(t, r.Ready())

	// A failed critical runnable blocks.
	r.update(supervisor.Status{Name: "http", State: supervisor.StateFailed, Critical: true})
	assert.False(t, r.Ready())
}

func (r *readiness) doneIsClosed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
