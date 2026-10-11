// SPDX-License-Identifier: MIT

package emote

import (
	"context"
)

// TwitchFetch lists the emotes of a channel by its login; the app wires it
// to the Helix GetEmotes call, resolving the login to the account ID.
type TwitchFetch func(ctx context.Context, channel string) ([]Emote, error)

// twitchSource is the Source of the Twitch emotes (roadmap 4.4, task 9).
type twitchSource struct {
	fetch TwitchFetch
}

// NewTwitchSource makes the Source that lists a channel's emotes with the
// Helix API, through fetch.
func NewTwitchSource(fetch TwitchFetch) Source {
	return twitchSource{fetch: fetch}
}

// Name implements Source.
func (twitchSource) Name() string { return "twitch" }

// Emotes implements Source.
func (t twitchSource) Emotes(ctx context.Context, channel string) ([]Emote, error) {
	return t.fetch(ctx, channel)
}
