// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"strconv"
)

// AdBreak is an ad break as answered by StartAdBreak (Helix POST /ads).
type AdBreak struct {
	// Length is the length of the ad break, in seconds (30 or 60).
	Length int `json:"length"`
	// AdURL is the URL of the ad break.
	AdURL string `json:"ad_url"`
}

// StartAdBreak starts an ad break of length seconds, 30 or 60 (Helix
// POST /ads, scope moderator:manage:ads).
func (c *Client) StartAdBreak(ctx context.Context, broadcasterID string, length int) (*AdBreak, error) {
	q := url.Values{
		"broadcaster_id": {broadcasterID},
		"length":         {strconv.Itoa(length)},
	}
	req, err := c.request(ctx, "POST", "/ads", q, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []AdBreak `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}
