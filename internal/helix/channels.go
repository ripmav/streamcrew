// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// GetChannel reads the channel of the broadcaster (Helix GET /channels):
// the title, the game and the channel text.
func (c *Client) GetChannel(ctx context.Context, broadcasterID string) (*Channel, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/channels", q, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Channel `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// UpdateChannelInput is the body of UpdateChannel (Helix POST /channels).
type UpdateChannelInput struct {
	// BroadcasterID is the channel that is updated.
	BroadcasterID string `json:"broadcaster_id"`
	// GameID is the new game; empty leaves it unchanged.
	GameID string `json:"game_id,omitempty"`
	// Title is the new stream title.
	Title string `json:"title,omitempty"`
	// Description is the new "About" text.
	Description string `json:"description,omitempty"`
}

// UpdateChannel sets title, game and description of the broadcaster's
// channel (Helix POST /channels, answers 204).
func (c *Client) UpdateChannel(ctx context.Context, in UpdateChannelInput) error {
	req, err := c.request(ctx, "POST", "/channels", nil, in)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
