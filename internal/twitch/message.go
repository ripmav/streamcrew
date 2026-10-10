// SPDX-License-Identifier: MIT

package twitch

import (
	"encoding/json/jsontext"
)

// envelope is a Twitch EventSub WebSocket frame (Code-ADR-0015): a
// type plus the type's JSON object.
type envelope struct {
	// Type is "hello", "PING", "PONG", "message", "session_reconnect"
	// or "revocation".
	Type string `json:"type"`
	// Payload is the JSON object of the type, for the messages that
	// have one.
	Payload jsontext.Value `json:"payload,omitempty"`
}

// hello is the first answer of a connection.
type hello struct {
	// SessionID names the session (for the session reconnect).
	SessionID string `json:"session_id"`
	// Expiry is the Unix time at which the session expires.
	Expiry int64 `json:"expiry"`
	// KeepaliveIntervalSeconds is the PING interval of the server
	// (standard 10).
	KeepaliveIntervalSeconds int `json:"keepalive_interval_seconds"`
	// ReconnectURL is the URL for the reconnect of this session.
	ReconnectURL string `json:"reconnect_url"`
}

// sessionReconnect is the signal that the session is about to expire:
// the client disconnects and connects again at the ReconnectURL with
// the session ID (Code-ADR-0015, point 4).
type sessionReconnect struct {
	// ReconnectURL is the URL for the reconnect.
	ReconnectURL string `json:"reconnect_url"`
	// SessionID is the session for the reconnect.
	SessionID string `json:"session_id"`
	// Reason is the reason for the reconnect.
	Reason string `json:"reason"`
}

// Event is a received EventSub event, before the mapping of task 4
// decodes the event object: the message ID for the dedup (B22) and
// the event envelope.
type Event struct {
	// ID is the message ID (dedup, B22).
	ID string `json:"id"`
	// Event is the nested event object.
	Event eventEnvelope `json:"event"`
}

// eventEnvelope is the nested "event" object of a message frame.
type eventEnvelope struct {
	// EventType is the EventSub event type, e.g. "channel.follow".
	EventType string `json:"event_type"`
	// Version is the event version.
	Version string `json:"version"`
	// Event is the event object, decoded by the mapping.
	Event jsontext.Value `json:"event"`
}
