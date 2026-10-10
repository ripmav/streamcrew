// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// SendChatMessageInput is the body of SendChatMessage (Helix
// POST /chat/messages).
type SendChatMessageInput struct {
	// BroadcasterID is the channel that receives the message.
	BroadcasterID string `json:"broadcaster_id"`
	// SenderID is the account that sends the message.
	SenderID string `json:"sender_id"`
	// Message is the chat text.
	Message string `json:"message"`
	// TagParams are the custom moderation tag parameters.
	TagParams map[string]string `json:"tag_params,omitempty"`
}

// ChatMessage is the sent message as answered by Helix.
type ChatMessage struct {
	// ID is the message ID (for deletion).
	ID string `json:"id"`
	// Content is the message text.
	Content string `json:"message"`
}

// SendChatMessage sends a chat message as the given account (Helix
// POST /chat/messages, scope chat:send).
func (c *Client) SendChatMessage(ctx context.Context, in SendChatMessageInput) (*ChatMessage, error) {
	req, err := c.request(ctx, "POST", "/chat/messages", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Message ChatMessage `json:"message"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	return &envelope.Message, nil
}

// DeleteChatMessage deletes a chat message by the moderator (Helix
// DELETE /chat/messages, answers 204).
func (c *Client) DeleteChatMessage(ctx context.Context, broadcasterID, moderatorID, messageID string) error {
	q := url.Values{
		"broadcaster_id": {broadcasterID},
		"moderator_id":   {moderatorID},
		"id":             {messageID},
	}
	req, err := c.request(ctx, "DELETE", "/chat/messages", q, nil)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}

// ChatSettings is the Helix chat settings object (GET/PATCH
// /chat/settings).
type ChatSettings struct {
	// BroadcasterID is the channel the settings belong to.
	BroadcasterID string `json:"broadcaster_id"`
	// SlowMode enables slow mode.
	SlowMode bool `json:"slow_mode"`
	// SlowModeWaitTime is the seconds between messages in slow mode.
	SlowModeWaitTime int `json:"slow_mode_wait_time"`
	// FollowersOnly limits chat to followers.
	FollowersOnly bool `json:"followers_only"`
	// FollowersOnlyDelay is the follower age in minutes.
	FollowersOnlyDelay int `json:"followers_only_delay"`
	// SubscriberOnly limits chat to subscribers.
	SubscriberOnly bool `json:"subscriber_only"`
	// EmoteMode limits chat to emotes.
	EmoteMode bool `json:"emote_mode"`
	// UniqueChatter enables non-repeating chat.
	UniqueChatter bool `json:"unique_chatter"`
	// UniqueChatterTimeRange is the range in minutes (0 = all time).
	UniqueChatterTimeRange int `json:"unique_chatter_time_range"`
	// UniqueChatterTimeSeconds is the range in seconds.
	UniqueChatterTimeSeconds int `json:"unique_chatter_time_seconds"`
}

// GetChatSettings reads the chat settings of the broadcaster (Helix
// GET /chat/settings).
func (c *Client) GetChatSettings(ctx context.Context, broadcasterID string) (*ChatSettings, error) {
	q := url.Values{"broadcaster_id": {broadcasterID}}
	req, err := c.request(ctx, "GET", "/chat/settings", q, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []ChatSettings `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// UpdateChatSettingsInput is the body of UpdateChatSettings (Helix
// PATCH /chat/settings). The boolean settings are pointers: a nil
// pointer leaves the value unchanged, 0 leaves the seconds values
// unchanged.
type UpdateChatSettingsInput struct {
	// BroadcasterID is the channel whose settings are updated.
	BroadcasterID string `json:"broadcaster_id"`
	// ModeratorID is the account that applies the changes.
	ModeratorID              string `json:"moderator_id"`
	SlowMode                 *bool  `json:"slow_mode,omitempty"`
	SlowModeWaitTime         int    `json:"slow_mode_wait_time,omitempty"`
	FollowersOnly            *bool  `json:"followers_only,omitempty"`
	FollowersOnlyDelay       int    `json:"followers_only_delay,omitempty"`
	SubscriberOnly           *bool  `json:"subscriber_only,omitempty"`
	EmoteMode                *bool  `json:"emote_mode,omitempty"`
	UniqueChatter            *bool  `json:"unique_chatter,omitempty"`
	UniqueChatterTimeRange   int    `json:"unique_chatter_time_range,omitempty"`
	UniqueChatterTimeSeconds int    `json:"unique_chatter_time_seconds,omitempty"`
}

// UpdateChatSettings changes the chat settings of the broadcaster
// (Helix PATCH /chat/settings) and returns the new settings.
func (c *Client) UpdateChatSettings(ctx context.Context, in UpdateChatSettingsInput) (*ChatSettings, error) {
	req, err := c.request(ctx, "PATCH", "/chat/settings", nil, in)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []ChatSettings `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 {
		return nil, nil
	}
	return &envelope.Data[0], nil
}

// AnnouncementInput is the body of SendAnnouncement (Helix
// POST /chat/announcements).
type AnnouncementInput struct {
	// BroadcasterID is the channel.
	BroadcasterID string `json:"broadcaster_id"`
	// ModeratorID is the account that announces.
	ModeratorID string `json:"moderator_id"`
	// Message is the announcement text.
	Message string `json:"message"`
	// Color is the highlight color.
	Color string `json:"color"`
}

// Announcement is the sent announcement as answered by Helix.
type Announcement struct {
	// Message is the announcement text.
	Message string `json:"message"`
	// Color is the highlight color.
	Color string `json:"color"`
}

// SendAnnouncement posts an announcement into the chat (Helix
// POST /chat/announcements, scope moderator:manage_chat).
func (c *Client) SendAnnouncement(ctx context.Context, in AnnouncementInput) (*Announcement, error) {
	req, err := c.request(ctx, "POST", "/chat/announcements", nil, in)
	if err != nil {
		return nil, err
	}
	var a Announcement
	if err := c.http.DoJSON(ctx, req, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// ShoutoutInput is the body of SendShoutout (Helix
// POST /chat/shoutouts).
type ShoutoutInput struct {
	// BroadcasterID is the channel.
	BroadcasterID string `json:"broadcaster_id"`
	// ModeratorID is the account that sends the shoutout.
	ModeratorID string `json:"moderator_id"`
	// FromID is the account that receives the shoutout.
	FromID string `json:"from_id"`
	// ToID is the account that is shouted out.
	ToID string `json:"to_id"`
}

// SendShoutout sends a shoutout from one channel to another (Helix
// POST /chat/shoutouts, answers 204).
func (c *Client) SendShoutout(ctx context.Context, in ShoutoutInput) error {
	req, err := c.request(ctx, "POST", "/chat/shoutouts", nil, in)
	if err != nil {
		return err
	}
	return c.doAnswer(ctx, req)
}
