// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// Follower is a follower entry (Helix GET /channels/followers).
type Follower struct {
	// UserID is the follower's user ID.
	UserID string `json:"user_id"`
	// UserLogin is the follower's login name.
	UserLogin string `json:"user_login"`
	// UserName is the follower's display name.
	UserName string `json:"user_name"`
	// FollowedAt is the time the follow was made.
	FollowedAt time.Time `json:"followed_at"`
}

// GetFollowers lists the followers of the broadcaster (Helix
// GET /channels/followers), walking all pages. A non-empty followerID
// filters for one follower.
func (c *Client) GetFollowers(ctx context.Context, broadcasterID, followerID string) ([]Follower, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	if followerID != "" {
		q.Set("user_id", followerID)
	}
	req, err := c.request(ctx, "GET", "/channels/followers", q, nil)
	if err != nil {
		return nil, err
	}
	var followers []Follower
	err = c.http.EachPage[Follower](ctx, req, 0, func(items []Follower, _ string) error {
		followers = append(followers, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return followers, nil
}

// Subscription is a subscription entry (Helix GET /subscriptions).
type Subscription struct {
	// BroadcasterID is the subscribed channel.
	BroadcasterID string `json:"broadcaster_id"`
	// BroadcasterName is the display name of the channel.
	BroadcasterName string `json:"broadcaster_name"`
	// SubscriberID is the subscriber's user ID.
	SubscriberID string `json:"user_id"`
	// SubscriberName is the display name of the subscriber.
	SubscriberName string `json:"user_name"`
	// Tier is the subscription tier ("1000", "2000", "3000").
	Tier string `json:"tier"`
	// GifterID is the gifter's user ID (gifted subscriptions).
	GifterID string `json:"gifter_id,omitempty"`
	// GifterName is the gifter's display name.
	GifterName string `json:"gifter_name,omitempty"`
}

// GetChannelSubscriptions lists the active subscriptions of the broadcaster
// (Helix GET /subscriptions, scope subscriptions:read), walking all
// pages.
func (c *Client) GetChannelSubscriptions(ctx context.Context, broadcasterID string) ([]Subscription, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/subscriptions", q, nil)
	if err != nil {
		return nil, err
	}
	var subscriptions []Subscription
	err = c.http.EachPage[Subscription](ctx, req, 0, func(items []Subscription, _ string) error {
		subscriptions = append(subscriptions, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return subscriptions, nil
}
