// SPDX-License-Identifier: MIT

package app

import (
	"context"
	"errors"
	"sort"

	"github.com/ripmav/streamcrew/internal/api"
	chatsvc "github.com/ripmav/streamcrew/internal/chat"
	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// apiChat adapts the chat service, the platform set and the user service to
// the api.Chat port (roadmap 6.2.1, API).
type apiChat struct{ a *App }

// Send sends a message through the chat service, the only write path of the
// core into the platform chats. A reply is answered on the send platform.
func (c apiChat) Send(ctx context.Context, text, channel, platformName string, asStreamer bool, replyMessageID string) error {
	req := chatsvc.SendRequest{
		Text:       text,
		AsStreamer: asStreamer,
		Channel:    connector.Channel(channel),
		Platform:   platform.Name(platformName),
	}
	if replyMessageID != "" {
		req.ReplyPlatform = platform.Name(platformName)
		req.ReplyMessageID = replyMessageID
	}
	return c.a.chat.Send(ctx, req)
}

// Delete deletes a message by its platform ID; the platform is required.
func (c apiChat) Delete(ctx context.Context, messageID, channel, platformName string) error {
	if platformName == "" {
		return errors.New("delete chat message: platform is required")
	}
	return c.a.chat.Delete(ctx, platform.Name(platformName), connector.Channel(channel), messageID)
}

// Clear clears a channel; platform empty clears every connected platform.
func (c apiChat) Clear(ctx context.Context, channel, platformName string) error {
	ch := connector.Channel(channel)
	if platformName != "" {
		return c.a.chat.ClearChat(ctx, platform.Name(platformName), ch)
	}
	var errs []error
	for _, p := range c.a.platforms.Connected() {
		if err := c.a.chat.ClearChat(ctx, p.Name(), ch); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// History returns the recent messages, newest last; platform empty is every
// connected platform, channel empty is every channel, limit 0 the default.
func (c apiChat) History(ctx context.Context, channel, platformName string, limit int) ([]api.ChatMessage, error) {
	var names []platform.Name
	if platformName != "" {
		names = []platform.Name{platform.Name(platformName)}
	} else {
		for _, p := range c.a.platforms.Connected() {
			names = append(names, p.Name())
		}
	}
	msgs := make([]api.ChatMessage, 0)
	for _, p := range names {
		streamerID, _ := c.a.userSvc.StreamerUser(ctx, p)
		for _, e := range c.a.chat.History(p) {
			if channel != "" && e.Channel != channel {
				continue
			}
			msgs = append(msgs, api.ChatMessage{
				MessageID:   e.MessageID,
				Login:       e.Name,
				DisplayName: e.Name,
				Text:        e.Text,
				Bot:         e.FromBot,
				Streamer:    !streamerID.IsZero() && e.UserID == streamerID,
				OccurredAt:  e.Time,
			})
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].OccurredAt.Before(msgs[j].OccurredAt) })
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}
