// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"fmt"
	"time"
)

// StreamMarker is a marker of the stream (Helix
// GET/POST /streamers/{id}/stream_markers).
type StreamMarker struct {
	// ID is the marker ID.
	ID string `json:"id"`
	// Position is the position in the stream, in seconds.
	Position int64 `json:"position"`
	// CreatedAt is the time the marker was made.
	CreatedAt time.Time `json:"created_at"`
	// CreatedByLogin is the login of the account that made the marker.
	CreatedByLogin string `json:"created_by_login"`
	// TagDescription is the marker description.
	TagDescription string `json:"tag_description,omitempty"`
}

// ListStreamMarkers lists the markers of the stream (Helix
// GET /streamers/{id}/stream_markers), walking all pages.
func (c *Client) ListStreamMarkers(ctx context.Context, streamerID string) ([]StreamMarker, error) {
	req, err := c.request(ctx, "GET", "/streamers/"+streamerID+"/stream_markers", nil, nil)
	if err != nil {
		return nil, err
	}
	var markers []StreamMarker
	err = c.http.EachPage[StreamMarker](ctx, req, 0, func(items []StreamMarker, _ string) error {
		markers = append(markers, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return markers, nil
}

// CreateStreamMarkerInput is the body of CreateStreamMarker (Helix
// POST /streamers/{id}/stream_markers).
type CreateStreamMarkerInput struct {
	// Position is the position in the stream, in seconds.
	Position int64 `json:"position"`
	// Description is the marker description.
	Description string `json:"description"`
}

// CreateStreamMarker makes a marker of the stream (Helix
// POST /streamers/{id}/stream_markers, scope moderator:manage:markers).
func (c *Client) CreateStreamMarker(ctx context.Context, streamerID string, in CreateStreamMarkerInput) (string, error) {
	req, err := c.request(ctx, "POST", "/streamers/"+streamerID+"/stream_markers", nil, in)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Data []StreamMarker `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return "", err
	}
	if len(envelope.Data) == 0 {
		return "", fmt.Errorf("helix: POST /streamers/%s/stream_markers: no marker in the answer", streamerID)
	}
	return envelope.Data[0].ID, nil
}
