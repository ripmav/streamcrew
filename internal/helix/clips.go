// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
	"time"
)

// Clip is a clip of the channel (Helix GET/POST /clips).
type Clip struct {
	// ID is the clip ID.
	ID string `json:"id"`
	// BroadcasterID is the channel the clip belongs to.
	BroadcasterID string `json:"broadcaster_id"`
	// BroadcasterName is the channel's display name.
	BroadcasterName string `json:"broadcaster_name"`
	// CreatorID is the user who made the clip.
	CreatorID string `json:"creator_id"`
	// CreatorName is the clip creator's display name.
	CreatorName string `json:"creator_name"`
	// GameName is the game of the clipped stream.
	GameName string `json:"game_name,omitempty"`
	// Title is the clip title.
	Title string `json:"title"`
	// URL is the URL of the clip.
	URL string `json:"url"`
	// ViewCount is the clip's view count.
	ViewCount uint64 `json:"view_count"`
	// CreatedAt is the time the clip was made.
	CreatedAt time.Time `json:"created_at"`
}

// ListClips lists the latest clips of the channel (Helix
// GET /clips), walking all pages.
func (c *Client) ListClips(ctx context.Context, broadcasterID string) ([]Clip, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/clips", q, nil)
	if err != nil {
		return nil, err
	}
	var clips []Clip
	err = c.http.EachPage[Clip](ctx, req, 0, func(items []Clip, _ string) error {
		clips = append(clips, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return clips, nil
}

// CreateClip makes a clip of the current stream (Helix POST /clips,
// scope moderator:manage:clips). The clip is attributed to targetID if
// given, else to the moderator.
func (c *Client) CreateClip(ctx context.Context, broadcasterID, targetID string) (*Clip, error) {
	in := struct {
		BroadcasterID string `json:"broadcaster_id"`
		TargetID      string `json:"target_id,omitempty"`
	}{BroadcasterID: broadcasterID, TargetID: targetID}
	req, err := c.request(ctx, "POST", "/clips", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Clip `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}
