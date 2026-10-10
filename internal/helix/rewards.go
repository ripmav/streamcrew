// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// Reward is a channel points reward (Helix GET/POST/PATCH/DELETE
// /rewards).
type Reward struct {
	// ID is the reward ID.
	ID string `json:"id"`
	// Title is the reward title.
	Title string `json:"title"`
	// Cost is the channel points a redemption costs; zero for the
	// default (100).
	Cost int64 `json:"cost"`
	// IsEnabled reports whether the reward can be redeemed.
	IsEnabled bool `json:"is_enabled"`
	// IsUserInputRequired reports whether the redeemer gives a message.
	IsUserInputRequired bool `json:"is_user_input_required"`
	// IsStockEnabled reports whether the reward has a stock limit.
	IsStockEnabled bool `json:"is_stock_enabled"`
	// Stock is the current stock; zero or negative for none.
	Stock int64 `json:"stock"`
	// RedemptionsFulfilled is the number of fulfilled redemptions.
	RedemptionsFulfilled int64 `json:"redemptions_fulfilled"`
}

// RewardInput is the body of CreateReward (Helix POST /rewards, scope
// moderator:manage:widgets).
type RewardInput struct {
	// BroadcasterID is the channel the reward belongs to.
	BroadcasterID string `json:"broadcaster_id"`
	// Title is the reward title.
	Title string `json:"title"`
	// Cost is the channel points a redemption costs; nil for the
	// default.
	Cost *int64 `json:"cost,omitempty"`
	// IsUserInputRequired reports whether the redeemer gives a message.
	IsUserInputRequired bool `json:"is_user_input_required"`
	// IsStockEnabled reports whether the reward has a stock limit.
	IsStockEnabled bool `json:"is_stock_enabled"`
	// Stock is the initial stock, if IsStockEnabled; nil for none.
	Stock *int64 `json:"stock,omitempty"`
}

// RewardUpdate is the body of UpdateReward (Helix PATCH /rewards).
// Nil fields leave the value unchanged.
type RewardUpdate struct {
	// Title is the new reward title.
	Title *string `json:"title,omitempty"`
	// Cost is the new cost.
	Cost *int64 `json:"cost,omitempty"`
	// IsEnabled is the new enabled state.
	IsEnabled *bool `json:"is_enabled,omitempty"`
	// IsUserInputRequired is the new user input state.
	IsUserInputRequired *bool `json:"is_user_input_required,omitempty"`
	// IsStockEnabled is the new stock state.
	IsStockEnabled *bool `json:"is_stock_enabled,omitempty"`
	// Stock is the new stock.
	Stock *int64 `json:"stock,omitempty"`
}

// ListRewards lists the channel points rewards of the channel (Helix
// GET /rewards), walking all pages.
func (c *Client) ListRewards(ctx context.Context, broadcasterID string) ([]Reward, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/rewards", q, nil)
	if err != nil {
		return nil, err
	}
	var rewards []Reward
	err = c.http.EachPage[Reward](ctx, req, 0, func(items []Reward, _ string) error {
		rewards = append(rewards, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rewards, nil
}

// CreateReward creates a channel points reward (Helix POST /rewards,
// scope moderator:manage:widgets).
func (c *Client) CreateReward(ctx context.Context, in RewardInput) (*Reward, error) {
	req, err := c.request(ctx, "POST", "/rewards", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Reward `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// UpdateReward updates the reward with the ID rewardID (Helix
// PATCH /rewards/{id}, scope moderator:manage:widgets).
func (c *Client) UpdateReward(ctx context.Context, broadcasterID, rewardID string, in RewardUpdate) (*Reward, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "PATCH", "/rewards/"+rewardID, q, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Reward `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// DeleteReward deletes the reward with the ID rewardID (Helix
// DELETE /rewards/{id}, scope moderator:manage:widgets), answers 204.
func (c *Client) DeleteReward(ctx context.Context, broadcasterID, rewardID string) error {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "DELETE", "/rewards/"+rewardID, q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
