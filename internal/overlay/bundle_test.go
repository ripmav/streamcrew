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

// TestBundleHTMLItemEnforcesTheCSP is the regression test of the html
// item (Review 341-9c, B12): before the srcdoc is set, the runtime
// rewrites the /media/ URLs with the token of the page and prepends
// the CSP meta that enforces the relative /media/ and absolute https
// rule (ADR-0019 section 4).
func TestBundleHTMLItemEnforcesTheCSP(t *testing.T) {
	t.Parallel()
	assert.True(t, strings.Contains(web.Bundle, "DOMParser"),
		"the HTML is parsed before the srcdoc is set")
	assert.True(t, strings.Contains(web.Bundle, "Content-Security-Policy"),
		"the CSP meta is prepended")
	assert.True(t, strings.Contains(web.Bundle, "default-src 'none'"),
		"the CSP policy of ADR-0019 section 4")
}

// TestBundleMediaItemsCarryTheToken is the regression of the media
// items (Review 345-4, B5/B14): the src of the image, video and audio
// items goes through withToken for the relative /media/ paths (like the
// html rewrite), so the media route of the overlay server answers it,
// and an error of the audio or video ends the item with item.ended, so
// a broken source does not stay.
func TestBundleMediaItemsCarryTheToken(t *testing.T) {
	t.Parallel()
	// The three occurrences are the html rewrite (src/href/poster and
	// the two srcset branches); the fourth is the media items.
	assert.Equal(t, 4, strings.Count(web.Bundle, `startsWith("/media/")`),
		"the src of the image, video and audio items joins withToken")
	assert.Equal(t, 2, strings.Count(web.Bundle, `addEventListener("error"`),
		"an error of the video and the audio ends the item")
}

// TestBundleClipEmbedIsSafe is the regression test of the clip embed
// (Review 344-6/344-7, B15): the embed URL carries the parent
// parameter Twitch requires, the slug regex keeps dashes and
// underscores, and an unmatched URL no longer reaches the iframe raw.
func TestBundleClipEmbedIsSafe(t *testing.T) {
	t.Parallel()
	assert.True(t, strings.Contains(web.Bundle, "([A-Za-z0-9_-]+)"),
		"the slug regex keeps dashes and underscores")
	assert.True(t, strings.Contains(web.Bundle, "&parent="),
		"the embed URL carries the parent parameter")
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
