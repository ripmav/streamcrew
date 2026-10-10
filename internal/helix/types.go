// SPDX-License-Identifier: MIT

package helix

import "time"

// User is the Helix user object (GET /users).
type User struct {
	// ID is the numeric user ID.
	ID string `json:"id"`
	// Login is the username in the form used at login (lowercase).
	Login string `json:"login"`
	// DisplayName is the display name.
	DisplayName string `json:"display_name"`
	// Type is the legacy account type; usually empty.
	Type string `json:"type,omitempty"`
	// BroadcasterType is "", "affiliate" or "partner".
	BroadcasterType string `json:"broadcaster_type,omitempty"`
	// Description is the "About" text.
	Description string `json:"description,omitempty"`
	// ProfileImageURL is the profile picture.
	ProfileImageURL string `json:"profile_image_url,omitempty"`
	// CreatedAt is the account creation time.
	CreatedAt time.Time `json:"created_at"`
	// ViewCount is the total follower view count.
	ViewCount uint64 `json:"view_count,omitempty"`
}

// Channel is the Helix channel object (GET/POST /channels).
type Channel struct {
	// ID is the channel's user ID.
	ID string `json:"id"`
	// BroadcasterID is the channel's user ID.
	BroadcasterID string `json:"broadcaster_id"`
	// BroadcasterName is the channel's login name.
	BroadcasterName string `json:"broadcaster_name"`
	// GameID is the currently set game.
	GameID string `json:"game_id,omitempty"`
	// GameName is the name of the currently set game.
	GameName string `json:"game_name,omitempty"`
	// Language is the broadcast language (BCP 47).
	Language string `json:"language"`
	// Title is the stream title.
	Title string `json:"title"`
	// Description is the "About" text.
	Description string `json:"description,omitempty"`
	// BroadcasterLanguage is the preferred language.
	BroadcasterLanguage string `json:"broadcaster_language,omitempty"`
	// Tags are the channel tags.
	Tags []string `json:"tags,omitempty"`
}

// Stream is the Helix stream object (GET /streams).
type Stream struct {
	// ID is the stream ID.
	ID string `json:"id"`
	// UserID is the broadcaster's user ID.
	UserID string `json:"user_id"`
	// UserLogin is the broadcaster's login name.
	UserLogin string `json:"user_login"`
	// UserName is the broadcaster's display name.
	UserName string `json:"user_name"`
	// GameID is the game being played.
	GameID string `json:"game_id,omitempty"`
	// GameName is the name of the game being played.
	GameName string `json:"game_name,omitempty"`
	// Type is usually "live".
	Type string `json:"type,omitempty"`
	// Title is the stream title.
	Title string `json:"title"`
	// Language is the broadcast language (BCP 47).
	Language string `json:"language,omitempty"`
	// ViewerCount is the current viewer count.
	ViewerCount uint64 `json:"viewer_count"`
	// CreatedAt is the start time of the stream.
	CreatedAt time.Time `json:"created_at"`
}
