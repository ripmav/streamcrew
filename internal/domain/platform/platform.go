// SPDX-License-Identifier: MIT

// Package platform names the streaming platforms streamcrew connects to
// (ADR-0004: Twitch at the start, more platforms in the roadmap backlog).
package platform

import (
	"fmt"
	"net/url"
)

// Name is the stable ID of a platform, e.g. "twitch". It appears in the
// database, in the API and as the source name of platform events.
type Name string

// The platforms of plan appendix A.1 that have adapters planned.
const (
	Twitch  Name = "twitch"
	YouTube Name = "youtube"
	Kick    Name = "kick"
)

// Default is the default platform (ADR-0004). Where the order of the
// platforms matters, it comes first, e.g. when moderation without a
// platform of the run looks for a user (actions.md B82). A setting takes
// its place with the multi-platform work (roadmap backlog).
const Default = Twitch

// DisplayName returns the name of the platform as the platform writes it,
// e.g. "YouTube"; the ID for a platform without an adapter; empty for the
// empty name.
func (n Name) DisplayName() string {
	switch n {
	case Twitch:
		return "Twitch"
	case YouTube:
		return "YouTube"
	case Kick:
		return "Kick"
	default:
		return string(n)
	}
}

// ProfileURL returns the address of an account's channel page on the
// platform, from its login name or, on YouTube, its channel ID; empty for a
// platform without an adapter or a missing name.
func (n Name) ProfileURL(login, platformUserID string) string {
	var base, key string
	switch n {
	case Twitch:
		base, key = "https://www.twitch.tv/", login
	case YouTube:
		base, key = "https://www.youtube.com/channel/", platformUserID
	case Kick:
		base, key = "https://kick.com/", login
	default:
		return ""
	}
	if key == "" {
		return ""
	}
	return base + url.PathEscape(key)
}

// maxLen is the maximum length of a name.
const maxLen = 32

// Validate checks the form of a name: 1 to 32 lowercase ASCII letters and
// digits. Names this version has no adapter for are valid, so that data
// written by a newer version survives.
func (n Name) Validate() error {
	if n == "" || len(n) > maxLen {
		return fmt.Errorf("platform name %q: want 1 to %d characters", n, maxLen)
	}
	for _, r := range n {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return fmt.Errorf("platform name %q: only lowercase letters and digits are allowed", n)
		}
	}
	return nil
}
