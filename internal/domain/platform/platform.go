// SPDX-License-Identifier: Apache-2.0

// Package platform names the streaming platforms streamcrew connects to
// (ADR-0004: Twitch at the start, more platforms in the roadmap backlog).
package platform

import "fmt"

// Name is the stable ID of a platform, e.g. "twitch". It appears in the
// database, in the API and as the source name of platform events.
type Name string

// The platforms of plan appendix A.1 that have adapters planned.
const (
	Twitch  Name = "twitch"
	YouTube Name = "youtube"
	Kick    Name = "kick"
)

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
