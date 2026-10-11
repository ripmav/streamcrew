// SPDX-License-Identifier: MIT

package twitch

import (
	"encoding/json/jsontext"
)

// envelope is a Twitch EventSub WebSocket frame (Code-ADR-0015): the
// metadata naming the message type plus the type's payload object.
// The message types are "session_welcome", "session_keepalive",
// "notification", "session_reconnect" and "revocation".
type envelope struct {
	Metadata struct {
		MessageType string `json:"message_type"`
	} `json:"metadata"`
	// Payload is the JSON object of the type.
	Payload jsontext.Value `json:"payload"`
}

// welcome is the payload of the session_welcome frame, the first
// answer of a connection.
type welcome struct {
	Session struct {
		// ID names the session.
		ID string `json:"id"`
		// KeepaliveTimeoutSeconds is the server keepalive interval
		// (the session_keepalive period); the production server
		// documents the same value as KeepaliveIntervalSeconds, so
		// the client reads both.
		KeepaliveTimeoutSeconds  int `json:"keepalive_timeout_seconds"`
		KeepaliveIntervalSeconds int `json:"keepalive_interval_seconds"`
	} `json:"session"`
}

// reconnect is the payload of the session_reconnect frame: the session
// object with the status "reconnecting" and the URL to reconnect at
// (it carries the reconnect identity itself, so the client needs no
// other session data).
type reconnect struct {
	Session struct {
		ID           string `json:"id"`
		ReconnectURL string `json:"reconnect_url"`
	} `json:"session"`
}

// Event is a received EventSub event: the payload of a notification
// frame, the EventsubResponse of the subscription. The subscription
// names the event type and version; the event object carries the
// values, decoded by the mapping.
type Event struct {
	// ID is the key the dedup remembers the message by (B22); the
	// adapter sets it from the payload before the mapping, so a
	// repeated delivery is recognized as the repeat it is.
	ID string
	// Subscription names the event type and version of the event.
	Subscription struct {
		Type    string `json:"type"`
		Version string `json:"version"`
	} `json:"subscription"`
	// Body is the event object of the payload, decoded by the mapping.
	Body jsontext.Value `json:"event"`
}
