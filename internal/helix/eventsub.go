// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// SubscriptionCondition is the condition of an EventSub subscription
// (Helix POST/GET /eventsub/subscriptions): exactly one of the fields
// names the object the events are about.
type SubscriptionCondition struct {
	// BroadcasterUserID is the channel the events are about (channel
	// events).
	BroadcasterUserID string `json:"broadcaster_user_id,omitempty"`
	// UserID is the user the events are about (e.g. raid).
	UserID string `json:"user_id,omitempty"`
	// GameID is the game the events are about (game events).
	GameID string `json:"game_id,omitempty"`
	// CollectionID is the charity collection the events are about.
	CollectionID string `json:"collection_id,omitempty"`
}

// CreateSubscriptionInput is the body of CreateSubscription (Helix
// POST /eventsub/subscriptions). The transport is always websocket
// (the Webhook transport is deprecated).
type CreateSubscriptionInput struct {
	// Condition names the object the events are about.
	Condition SubscriptionCondition `json:"condition"`
	// EventType is the event type, e.g. "channel.follow"; under the
	// field name the Helix API takes it.
	EventType string `json:"eventtype"`
	// Type is the event type under the field name the twitch-cli mock
	// expects (the production API reads EventType, which is kept
	// filled by CreateSubscription).
	Type string `json:"type,omitempty"`
	// Version is the event version, e.g. "2".
	Version string `json:"version"`
	// Transport is the delivery transport (websocket, with the ID of
	// the session the events are delivered to).
	Transport EventSubTransport `json:"transport"`
}

// EventSubTransport is the transport of a subscription (always
// websocket for the new API).
type EventSubTransport struct {
	// Method is "websocket".
	Method string `json:"method"`
	// SessionID is the ID of the EventSub session the subscription is
	// delivered to (mandatory for the websocket transport).
	SessionID string `json:"session_id,omitempty"`
}

// EventSubSubscription is a subscription as answered by Helix.
type EventSubSubscription struct {
	// ID is the subscription ID (for revocation).
	ID string `json:"id"`
	// Status is the connection state (e.g. "websockets_connected").
	Status string `json:"status"`
	// Condition names the object the events are about.
	Condition SubscriptionCondition `json:"condition"`
	// EventType is the event type.
	EventType string `json:"eventtype"`
	// Type is the event type under the field name the twitch-cli mock
	// answers (CreateSubscription and GetSubscriptions fold it into
	// EventType).
	Type string `json:"type"`
	// Version is the event version.
	Version string `json:"version"`
	// Network is "helix".
	Network string `json:"network"`
	// Transport is the delivery transport.
	Transport EventSubTransport `json:"transport"`
	// Callback is the webhook URL (absent for websocket).
	Callback string `json:"callback,omitempty"`
}

// CreateSubscription registers an EventSub subscription (Helix
// POST /eventsub/subscriptions, answers 201). The transport is
// websocket; the 4.3 subscription manager maintains the
// subscriptions against this endpoint.
func (c *Client) CreateSubscription(ctx context.Context, in CreateSubscriptionInput) (*EventSubSubscription, error) {
	if in.Type == "" {
		in.Type = in.EventType
	}
	req, err := c.request(ctx, "POST", "/eventsub/subscriptions", nil, in)
	if err != nil {
		return nil, err
	}
	var s EventSubSubscription
	if err := c.http.DoJSON(ctx, req, &s); err != nil {
		return nil, err
	}
	if s.EventType == "" {
		s.EventType = s.Type
	}
	return &s, nil
}

// RevokeSubscription revokes the subscription (Helix
// DELETE /eventsub/subscriptions, answers 204).
func (c *Client) RevokeSubscription(ctx context.Context, id string) error {
	q := url.Values{"id": {id}}
	req, err := c.request(ctx, "DELETE", "/eventsub/subscriptions", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}

// GetSubscriptions lists the subscriptions of the client (Helix
// GET /eventsub/subscriptions), walking all pages.
func (c *Client) GetSubscriptions(ctx context.Context) ([]EventSubSubscription, error) {
	req, err := c.request(ctx, "GET", "/eventsub/subscriptions", nil, nil)
	if err != nil {
		return nil, err
	}
	var subscriptions []EventSubSubscription
	err = c.http.EachPage[EventSubSubscription](ctx, req, 0, func(items []EventSubSubscription, _ string) error {
		subscriptions = append(subscriptions, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i := range subscriptions {
		if subscriptions[i].EventType == "" {
			subscriptions[i].EventType = subscriptions[i].Type
		}
	}
	return subscriptions, nil
}
