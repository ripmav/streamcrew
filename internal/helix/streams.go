// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"errors"
	"net/url"
)

// GetStreams reads the live streams of the given user IDs and/or logins
// (Helix GET /streams), walking all pages. An empty result means none of
// the users is live.
func (c *Client) GetStreams(ctx context.Context, ids, logins []string) ([]Stream, error) {
	if len(ids)+len(logins) == 0 {
		return nil, errors.New("helix: GetStreams: at least one ID or login is required")
	}
	q := url.Values{}
	for _, id := range ids {
		q.Add("user_id", id)
	}
	for _, login := range logins {
		q.Add("user_login", login)
	}
	req, err := c.request(ctx, "GET", "/streams", q, nil)
	if err != nil {
		return nil, err
	}
	var streams []Stream
	err = c.http.EachPage[Stream](ctx, req, 0, func(items []Stream, _ string) error {
		streams = append(streams, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return streams, nil
}
