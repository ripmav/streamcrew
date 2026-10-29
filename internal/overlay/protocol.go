// SPDX-License-Identifier: MIT

package overlay

import (
	"encoding/json/v2"
	"time"

	"github.com/ripmav/streamcrew/internal/settings"
)

// Position is the position model of the settings (spec overlays.md,
// B9), named here for the protocol messages.
type Position = settings.Position

// The message types of the overlay protocol (ADR-0019 §3, spec
// overlays.md, B3–B14). Every message is a JSON object {type, data}; the
// server sends the handshake hello, state.widgets and the active items
// before it runs (B4).
const (
	TypeHello        = "hello"
	TypeStateWidgets = "state.widgets"
	TypeItemShow     = "item.show"
	TypeItemRemove   = "item.remove"
	TypeWidgetShow   = "widget.show"
	TypeWidgetHide   = "widget.hide"
	TypeWidgetUpdate = "widget.update"
	TypeTTSSpeak     = "tts.speak"
	TypeReady        = "ready"
	TypeItemEnded    = "item.ended"
	TypeTTSEnded     = "tts.ended"
)

// Message is one protocol message.
type Message struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// encode returns the JSON of the message; a failure here is a bug, so it
// panics.
func encode(msg Message) []byte {
	data, err := json.Marshal(msg)
	if err != nil {
		panic("overlay: encode " + msg.Type + ": " + err.Error())
	}
	return data
}

// HelloData is the greeting the server sends first on a (re)connect (B4).
type HelloData struct {
	// Version is the version of the core.
	Version string `json:"version"`
	// Endpoint is the ID of the endpoint the client is on.
	Endpoint string `json:"endpoint"`
	// Name is the display name of the endpoint.
	Name string `json:"name"`
	// Time is the server time; the runtime uses it as the reference of
	// its clocks (B4, B26).
	Time time.Time `json:"time"`
}

// StateWidgetsData is the state of all visible widgets of the endpoint
// the server sends on a (re)connect (B4).
type StateWidgetsData struct {
	// Widgets are the visible widgets, in the order of the section.
	Widgets []WidgetState `json:"widgets"`
}

// WidgetState is one widget with its state, as the runtime renders it
// (B4, B19, B20).
type WidgetState struct {
	// ID is the ID of the widget, unique in the profile.
	ID string `json:"id"`
	// Kind is the kind of the widget.
	Kind string `json:"kind"`
	// Name is the display name of the widget.
	Name string `json:"name"`
	// Position is where the widget shows (B9).
	Position Position `json:"position"`
	// State is the mutable state of the widget (B20); null in 7.1,
	// filled by the widget services from 7.2.
	State any `json:"state,omitempty"`
}

// ItemData is an item the server shows on an endpoint (B9–B13).
type ItemData struct {
	// ID is the ID of the item, unique while it is active.
	ID string `json:"id"`
	// Kind is the kind of the item, one of ItemKinds.
	Kind string `json:"kind"`
	// Position is where the item shows (B9); a random position is
	// rolled by the server into a concrete point before it shows.
	Position Position `json:"position"`
	// Layer is the integer layer; a higher layer shows above (B9).
	Layer int `json:"layer"`
	// Animation is the show and hide animation, one of Animations;
	// empty for none (B10).
	Animation string `json:"animation,omitempty"`
	// AnimationDuration is the time of the animation in milliseconds;
	// 0 for the default of 300 (B10).
	AnimationDuration int `json:"animationDuration,omitempty"`
	// Duration is the show time in milliseconds; 0 for until the
	// playback ends (B11).
	Duration int `json:"duration,omitempty"`
	// Content is the kind-specific content as a JSON document; empty
	// for none.
	Content any `json:"content,omitempty"`
}

// RemoveData is the ID of the item or widget a remove message names.
type RemoveData struct {
	// ID is the ID of the item.
	ID string `json:"id"`
}

// WidgetIDData is the ID of a widget for the widget.show, widget.hide
// and widget.update messages (B21, B22).
type WidgetIDData struct {
	// ID is the ID of the widget, unique in the profile.
	ID string `json:"id"`
}

// WidgetUpdateData is a widget update with the new state (B22).
type WidgetUpdateData struct {
	// ID is the ID of the widget, unique in the profile.
	ID string `json:"id"`
	// State is the new state (B20); null for none.
	State any `json:"state,omitempty"`
}

// TTSSpeakData is the text the browser TTS speaks (B41).
type TTSSpeakData struct {
	// Text is the text to speak.
	Text string `json:"text"`
	// Voice is the voice name; empty for the default voice.
	Voice string `json:"voice,omitempty"`
	// Volume is the volume from 0 to 1; 1 for full.
	Volume float64 `json:"volume,omitempty"`
	// Rate is the speaking rate; 1 for normal.
	Rate float64 `json:"rate,omitempty"`
	// Pitch is the pitch; 1 for normal.
	Pitch float64 `json:"pitch,omitempty"`
}

// EndedData is the ID of the item whose playback the client reports
// finished (B14).
type EndedData struct {
	// ID is the ID of the item.
	ID string `json:"id"`
}
