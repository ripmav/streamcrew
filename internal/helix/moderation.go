// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// BanInput is the body of Ban (Helix POST /moderation/bans).
type BanInput struct {
	// BroadcasterID is the channel.
	BroadcasterID string `json:"broadcaster_id"`
	// ModeratorID is the account that bans.
	ModeratorID string `json:"moderator_id"`
	// FromID is the banned user.
	FromID string `json:"from_id"`
	// Reason is the shown ban reason.
	Reason string `json:"reason,omitempty"`
}

// BanResult is the ban as answered by Helix.
type BanResult struct {
	// FromID is the banned user's ID.
	FromID string `json:"from_id"`
	// FromLogin is the banned user's login.
	FromLogin string `json:"from_login"`
	// Reason is the shown ban reason.
	Reason string `json:"reason,omitempty"`
}

// Ban bans a user from the channel (Helix POST /moderation/bans, scope
// moderator:manage_bans).
func (c *Client) Ban(ctx context.Context, in BanInput) (*BanResult, error) {
	req, err := c.request(ctx, "POST", "/moderation/bans", nil, in)
	if err != nil {
		return nil, err
	}
	var r BanResult
	if err := c.http.DoJSON(ctx, req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Unban removes a ban (Helix DELETE /moderation/bans, answers 204).
func (c *Client) Unban(ctx context.Context, broadcasterID, moderatorID, fromID string) error {
	q := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
		"from_id":        {fromID},
	}
	req, err := c.request(ctx, "DELETE", "/moderation/bans", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}

// TimeoutInput is the body of Timeout (Helix POST
// /moderation/timeouts).
type TimeoutInput struct {
	// BroadcasterID is the channel.
	BroadcasterID string `json:"broadcaster_id"`
	// ModeratorID is the account that times out.
	ModeratorID string `json:"moderator_id"`
	// FromID is the timed out user.
	FromID string `json:"from_id"`
	// Duration is the timeout length in seconds (60–86400).
	Duration int `json:"duration"`
	// Reason is the shown timeout reason.
	Reason string `json:"reason,omitempty"`
}

// TimeoutResult is the timeout as answered by Helix.
type TimeoutResult struct {
	// FromID is the timed out user's ID.
	FromID string `json:"from_id"`
	// FromLogin is the timed out user's login.
	FromLogin string `json:"from_login"`
	// Timeout is the timeout length in seconds.
	Timeout int `json:"timeout"`
	// Reason is the shown timeout reason.
	Reason string `json:"reason,omitempty"`
}

// Timeout times a user out of the chat (Helix POST
// /moderation/timeouts, scope moderator:manage_bans).
func (c *Client) Timeout(ctx context.Context, in TimeoutInput) (*TimeoutResult, error) {
	req, err := c.request(ctx, "POST", "/moderation/timeouts", nil, in)
	if err != nil {
		return nil, err
	}
	var r TimeoutResult
	if err := c.http.DoJSON(ctx, req, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Untimeout removes a timeout (Helix DELETE /moderation/timeouts,
// answers 204).
func (c *Client) Untimeout(ctx context.Context, broadcasterID, moderatorID, fromID string) error {
	q := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
		"from_id":        {fromID},
	}
	req, err := c.request(ctx, "DELETE", "/moderation/timeouts", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}

// Moderator is a channel moderator (GET /moderation/moderators).
type Moderator struct {
	// ID is the user ID.
	ID string `json:"id"`
	// Login is the username.
	Login string `json:"login"`
	// DisplayName is the display name.
	DisplayName string `json:"display_name"`
}

// GetModerators lists the moderators of the channel (Helix
// GET /moderation/moderators), walking all pages.
func (c *Client) GetModerators(ctx context.Context, broadcasterID string) ([]Moderator, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/moderation/moderators", q, nil)
	if err != nil {
		return nil, err
	}
	var moderators []Moderator
	err = c.http.EachPage[Moderator](ctx, req, 0, func(items []Moderator, _ string) error {
		moderators = append(moderators, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moderators, nil
}

// VIP is a channel VIP (GET/POST /moderation/vips).
type VIP struct {
	// ID is the user ID.
	ID string `json:"id"`
	// Login is the username.
	Login string `json:"login"`
	// DisplayName is the display name.
	DisplayName string `json:"display_name"`
}

// GetVIPs lists the VIPs of the channel (Helix GET /moderation/vips),
// walking all pages.
func (c *Client) GetVIPs(ctx context.Context, broadcasterID string) ([]VIP, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/moderation/vips", q, nil)
	if err != nil {
		return nil, err
	}
	var vips []VIP
	err = c.http.EachPage[VIP](ctx, req, 0, func(items []VIP, _ string) error {
		vips = append(vips, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return vips, nil
}

// AddVIP makes a user a VIP (Helix POST /moderation/vips, scope
// moderator:manage_vips).
func (c *Client) AddVIP(ctx context.Context, broadcasterID, moderatorID, fromID string) (*VIP, error) {
	in := struct {
		BroadcasterID string `json:"broadcaster_id"`
		ModeratorID   string `json:"moderator_id"`
		FromID        string `json:"from_id"`
	}{broadcasterID, moderatorID, fromID}
	req, err := c.request(ctx, "POST", "/moderation/vips", nil, in)
	if err != nil {
		return nil, err
	}
	var v VIP
	if err := c.http.DoJSON(ctx, req, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// RemoveVIP removes a VIP (Helix DELETE /moderation/vips, answers 204).
func (c *Client) RemoveVIP(ctx context.Context, broadcasterID, moderatorID, fromID string) error {
	q := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
		"from_id":        {fromID},
	}
	req, err := c.request(ctx, "DELETE", "/moderation/vips", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
