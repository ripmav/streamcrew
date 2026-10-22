// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// Emote is a Twitch emote (Helix GET /chat/emotes).
type Emote struct {
	// ID is the emote ID.
	ID string `json:"id"`
	// Name is the emote code viewers type in chat, e.g. "Kappa".
	Name string `json:"name"`
	// Images are the static image URLs per size; the 1x URL is the
	// image the emote catalog resolves.
	Images EmoteImages `json:"images"`
}

// EmoteImages are the image URLs of an emote (Helix GET /chat/emotes).
type EmoteImages struct {
	URL1x string `json:"url_1x"`
	URL2x string `json:"url_2x"`
	URL4x string `json:"url_4x"`
}

// GetChannelEmotes lists the custom emotes of the channel (Helix GET
// /chat/emotes, the successor of the removed GET /emotes), walking all
// pages. broadcasterID is the channel's account ID.
func (c *Client) GetChannelEmotes(ctx context.Context, broadcasterID string) ([]Emote, error) {
	q := url.Values{}
	q.Set("broadcaster_id", broadcasterID)
	req, err := c.request(ctx, "GET", "/chat/emotes", q, nil)
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
