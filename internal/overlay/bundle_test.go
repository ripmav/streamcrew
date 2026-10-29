// SPDX-License-Identifier: MIT

package overlay_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/overlay/web"
)

// TestBundleCarriesTheRuntimeMarkers is the smoke test of the embedded
// runtime bundle (Code-ADR-0016, plan phase 7, section 4): the CI cannot
// run a headless browser, so it checks that the bundle still carries the
// markers the runtime needs: the endpoint injection, the state messages,
// the item lifecycle, the sandbox, the animations and the batching
// values (B8, B10, B12).
func TestBundleCarriesTheRuntimeMarkers(t *testing.T) {
	t.Parallel()
	for _, marker := range []string{
		"window.__OVERLAY__", // the per-endpoint injection
		"state.widgets",      // the state sync (B4)
		"item.ended",         // the runtime tells the server an item ended (B14)
		"tts.speak",          // the TTS item (B17)
		"sc-slide-left",      // the animations (B10)
		"sc-fade",            // the animations (B10)
		"sandbox",            // the iframe sandbox (B12)
		"500",                // the batch window in ms (B8)
		"300",                // the stagger and the default animation in ms (B8, B10)
		"URLSearchParams(location.search).get(\"token\")", // the token the page was loaded with (B5)
		"\"token=\"", // the token goes into the WS and media URLs (B5)
	} {
		assert.True(t, strings.Contains(web.Bundle, marker), "the bundle misses %q", marker)
	}
}

// TestBundleHasNoBottomYOffset is the regression test of the bottom
// positions (Review 342-4, B9): bottom:0 already places the element at
// the bottom edge; the old code lifted it by its own height on top of
// that with translateY(-100%).
func TestBundleHasNoBottomYOffset(t *testing.T) {
	t.Parallel()
	assert.False(t, strings.Contains(web.Bundle, "-100%"),
		"the bottom positions must not lift the element by its own height")
}

// TestBundlePositionsUseTheTranslateProperty is the regression test of
// the centering (Review 342-5, B9/B10): the named positions center via
// the CSS property translate, so the show animations, which animate
// transform with the fill mode both, cannot override the centering.
func TestBundlePositionsUseTheTranslateProperty(t *testing.T) {
	t.Parallel()
	assert.True(t, strings.Contains(web.Bundle, "translate:-50% -50%"),
		"the center point centers via the CSS translate property")
	assert.False(t, strings.Contains(web.Bundle, "translate(-50%"),
		"no transform-based centering is left in the positions")
}
