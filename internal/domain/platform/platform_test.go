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

func TestDisplayName(t *testing.T) {
	t.Parallel()
	for name, want := range map[platform.Name]string{
		platform.Twitch:  "Twitch",
		platform.YouTube: "YouTube",
		platform.Kick:    "Kick",
		platform.Mock:    "Mock",
		"velora":         "velora",
		"":               "",
	} {
		assert.Equal(t, want, name.DisplayName())
	}
}

func TestProfileURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  platform.Name
		login string
		id    string
		want  string
	}{
		{platform.Twitch, "alice", "123", "https://www.twitch.tv/alice"},
		{platform.YouTube, "Alice", "UC123", "https://www.youtube.com/channel/UC123"},
		{platform.Kick, "alice_99", "7", "https://kick.com/alice_99"},
		{platform.Kick, "a/b?c", "7", "https://kick.com/a%2Fb%3Fc"},
		{platform.Twitch, "", "123", ""},
		{platform.YouTube, "alice", "", ""},
		{"velora", "alice", "1", ""},
		{platform.Mock, "alice", "alice", ""},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.name.ProfileURL(tc.login, tc.id), tc.name)
	}
}
