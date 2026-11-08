// SPDX-License-Identifier: MIT

package overlay_test

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRuntimeBundleBehavior runs the embedded runtime bundle in node
// with a stubbed DOM, a virtual clock and a fake WebSocket
// (runtime/test/harness.mjs). The CI has no headless browser, so the
// spec behavior of the runtime (positions B9, animations B10, resync
// B4, timer format B17, clip embed B15) is checked through the
// protocol against the checked-in bundle.
func TestRuntimeBundleBehavior(t *testing.T) {
	t.Parallel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is not available, the runtime behavior test is skipped: %v", err)
	}
	out, err := exec.CommandContext(t.Context(), node, "runtime/test/harness.mjs").CombinedOutput()
	assert.NoError(t, err, "harness output:\n%s", out)
	t.Log(string(out))
}
