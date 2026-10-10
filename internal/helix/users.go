// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"errors"
	"net/url"
)

// GetUsers looks up users by their IDs and/or logins (Helix GET /users),
// walking all pages. IDs and logins are combined in one call, up to 100
// per page.
func (c *Client) GetUsers(ctx context.Context, ids, logins []string) ([]User, error) {
	if len(ids)+len(logins) == 0 {
		return nil, errors.New("helix: GetUsers: at least one ID or login is required")
	}
	q := url.Values{}
	for _, id := range ids {
		q.Add("id", id)
	}
	for _, login := range logins {
		q.Add("login", login)
	}
	req, err := c.request(ctx, "GET", "/users", q, nil)
	if err != nil {
		return nil, err
	}
	var users []User
	err = c.http.EachPage[User](ctx, req, 0, func(items []User, _ string) error {
		users = append(users, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return users, nil
}
