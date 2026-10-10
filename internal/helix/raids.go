// SPDX-License-Identifier: MIT

package helix

import "context"

// SendRaid raids the channel toID from the channel fromID (Helix
// POST /raids, scope moderator:manage:raids), answers 204.
func (c *Client) SendRaid(ctx context.Context, fromID, toID string) error {
	in := struct {
		FromID string `json:"from_id"`
		ToID   string `json:"to_id"`
	}{FromID: fromID, ToID: toID}
	req, err := c.request(ctx, "POST", "/raids", nil, in)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
