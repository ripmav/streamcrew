// SPDX-License-Identifier: MIT

package platform_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ripmav/streamcrew/internal/domain/platform"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, n := range []platform.Name{platform.Twitch, platform.YouTube, platform.Kick, "vpzone", "p2"} {
		assert.NoError(t, n.Validate(), n)
	}
	for _, n := range []platform.Name{"", "Twitch", "you tube", "kick.com", platform.Name(strings.Repeat("a", 33))} {
		assert.Error(t, n.Validate(), n)
	}
}
