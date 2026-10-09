// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// Follower is a follower entry (Helix GET /channels/followers).
type Follower struct {
	// BroadcasterID is the channel the user follows.
	BroadcasterID string `json:"broadcaster_id"`
	// BroadcasterLogin is the channel's login name.
	BroadcasterLogin string `json:"broadcaster_login"`
	// BroadcasterName is the channel's display name.
	BroadcasterName string `json:"broadcaster_name"`
	// FollowerID is the follower's user ID.
	FollowerID string `json:"follower_id"`
	// FollowerLogin is the follower's login name.
	FollowerLogin string `json:"follower_login"`
	// FollowerName is the follower's display name.
	FollowerName string `json:"follower_name"`
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

// Follow is a follow entry (Helix GET /users/follows).
type Follow struct {
	// FromID is the user who follows.
	FromID string `json:"from_id"`
	// FromLogin is the login name of the user who follows.
	FromLogin string `json:"from_login"`
	// FromName is the display name of the user who follows.
	FromName string `json:"from_name"`
	// ToID is the followed user.
	ToID string `json:"to_id"`
	// ToLogin is the login name of the followed user.
	ToLogin string `json:"to_login"`
	// ToName is the display name of the followed user.
	ToName string `json:"to_name"`
	// FollowedAt is the time the follow was made.
	FollowedAt time.Time `json:"followed_at"`
}

// GetFollows lists the follows of the broadcaster (Helix
// GET /users/follows), walking all pages. A non-empty toID filters for
// one followed user.
func (c *Client) GetFollows(ctx context.Context, fromID, toID string) ([]Follow, error) {
	q := url.Values{"from_id": {fromID}}
	if toID != "" {
		q.Set("to_id", toID)
	}
	req, err := c.request(ctx, "GET", "/users/follows", q, nil)
	if err != nil {
		return nil, err
	}
	var follows []Follow
	err = c.http.EachPage[Follow](ctx, req, 0, func(items []Follow, _ string) error {
		follows = append(follows, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return follows, nil
}

// Subscription is a subscription entry (Helix GET /subscriptions).
type Subscription struct {
	// BroadcasterID is the subscribed channel.
	BroadcasterID string `json:"broadcaster_id"`
	// BroadcasterName is the display name of the channel.
	BroadcasterName string `json:"broadcaster_name"`
	// ForeignID is the ID of the subscription on the payment provider.
	ForeignID string `json:"foreign_id"`
	// ForeignType is the payment provider (e.g. "prime", "amazon").
	ForeignType string `json:"foreign_type"`
	// SubscriberID is the subscriber's user ID.
	SubscriberID string `json:"subscriber_id"`
	// SubscriberName is the display name of the subscriber.
	SubscriberName string `json:"subscriber_name"`
	// Tier is the subscription tier ("1000", "2000", "3000").
	Tier string `json:"tier"`
	// GifterID is the gifter's user ID (gifted subscriptions).
	GifterID string `json:"gifter_id,omitempty"`
	// GifterName is the gifter's display name.
	GifterName string `json:"gifter_name,omitempty"`
}

// GetSubscriptions lists the active subscriptions of the broadcaster
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
