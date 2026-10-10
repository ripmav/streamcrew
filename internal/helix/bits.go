// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// CheermoteTier is a cheer tier of a cheermote (Helix
// GET /bits/cheermotes).
type CheermoteTier struct {
	// Tier is the tier number, e.g. 1, 2, 3.
	Tier int64 `json:"tier"`
	// MinimumAmount is the minimum bits for the tier.
	MinimumAmount int64 `json:"minimum_amount"`
}

// Cheermote is a cheer emote set (Helix GET /bits/cheermotes).
type Cheermote struct {
	// EmoteID is the cheermote ID.
	EmoteID string `json:"emote_id"`
	// Color is the cheermote color, e.g. "#000000"; empty for the
	// default.
	Color string `json:"color"`
	// IsAnimated reports whether the cheermote animates.
	IsAnimated bool `json:"is_animated"`
	// Tiers are the cheer tiers.
	Tiers []CheermoteTier `json:"tiers"`
}

// GetCheermotes lists the cheermotes of the channel (Helix
// GET /bits/cheermotes), walking all pages.
func (c *Client) GetCheermotes(ctx context.Context, broadcasterID string) ([]Cheermote, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/bits/cheermotes", q, nil)
	if err != nil {
		return nil, err
	}
	var cheermotes []Cheermote
	err = c.http.EachPage[Cheermote](ctx, req, 0, func(items []Cheermote, _ string) error {
		cheermotes = append(cheermotes, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cheermotes, nil
}
