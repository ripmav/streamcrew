// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// The statuses of a redemption (Helix /redemptions).
const (
	RedemptionUnfulfilled = "UNFULFILLED"
	RedemptionFulfilled   = "FULFILLED"
	RedemptionCanceled    = "CANCELED"
)

// Redemption is a channel points redemption (Helix GET/POST
// /redemptions).
type Redemption struct {
	// ID is the redemption ID.
	ID string `json:"id"`
	// RewardID is the redeemed reward.
	RewardID string `json:"reward_id"`
	// Status is one of RedemptionUnfulfilled, RedemptionFulfilled and
	// RedemptionCanceled.
	Status string `json:"status"`
	// UserID and UserName are the redeemer.
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	// Message is the redeemer's message, if the reward asks for one.
	Message string `json:"redeemed_at_message"`
	// RedeemedAt is the time the reward was redeemed.
	RedeemedAt time.Time `json:"redeemed_at"`
}

// ListRedemptions lists the redemptions of the channel (Helix
// GET /redemptions), walking all pages. rewardID and status narrow the
// list; empty for all.
func (c *Client) ListRedemptions(ctx context.Context, broadcasterID, rewardID, status string) ([]Redemption, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	if rewardID != "" {
		q.Set("reward_id", rewardID)
	}
	if status != "" {
		q.Set("status", status)
	}
	req, err := c.request(ctx, "GET", "/redemptions", q, nil)
	if err != nil {
		return nil, err
	}
	var redemptions []Redemption
	err = c.http.EachPage[Redemption](ctx, req, 0, func(items []Redemption, _ string) error {
		redemptions = append(redemptions, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return redemptions, nil
}

// UpdateRedemption fulfills (RedemptionFulfilled) or cancels
// (RedemptionCanceled) the redemption with the ID redemptionID (Helix
// POST /redemptions, scope moderator:manage:redemptions).
func (c *Client) UpdateRedemption(ctx context.Context, broadcasterID, redemptionID, status string) (*Redemption, error) {
	in := struct {
		Redemptions []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"redemptions"`
	}{Redemptions: []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}{{ID: redemptionID, Status: status}}}
	req, err := c.request(ctx, "POST", "/redemptions?broadcaster_id="+broadcasterID, nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Redemption `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}
