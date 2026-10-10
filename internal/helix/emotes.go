// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// Emote is a Twitch emote (Helix GET /emotes).
type Emote struct {
	// ID is the emote ID.
	ID string `json:"id"`
	// Name is the emote code, e.g. "Kappa".
	Name string `json:"name"`
	// Format is the image format, e.g. "png", "gif" or "webp".
	Format string `json:"format"`
	// Tier is "0" (all users) or "1" (subscribers).
	Tier string `json:"tier"`
	// Image is the base of the emote image URLs, without the size
	// suffix.
	Image string `json:"image"`
}

// GetEmotes lists the emotes of the channel (Helix GET /emotes),
// walking all pages. userID narrows to the channel's own emotes;
// empty for the global set plus the channel's emotes.
func (c *Client) GetEmotes(ctx context.Context, userID string) ([]Emote, error) {
	q := url.Values{}
	if userID != "" {
		q.Set("user_id", userID)
	}
	req, err := c.request(ctx, "GET", "/emotes", q, nil)
	if err != nil {
		return nil, err
	}
	var emotes []Emote
	err = c.http.EachPage[Emote](ctx, req, 0, func(items []Emote, _ string) error {
		emotes = append(emotes, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return emotes, nil
}
