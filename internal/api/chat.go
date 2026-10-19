// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
)

// ChatMessage is a chat message as returned by History.
type ChatMessage struct {
	MessageID   string
	Login       string
	DisplayName string
	Text        string
	Bot         bool
	Streamer    bool
	OccurredAt  time.Time
}

// Chat sends and manages chat messages (roadmap 6.2.1, API).
type Chat interface {
	// Send sends a message; channel and platform empty use the home channel
	// and every connected platform.
	Send(ctx context.Context, text, channel, platform string, asStreamer bool, replyMessageID string) error
	// Delete deletes a message by its platform ID.
	Delete(ctx context.Context, messageID, channel, platform string) error
	// Clear deletes all recent messages of a channel; platform empty clears
	// on every connected platform.
	Clear(ctx context.Context, channel, platform string) error
	// History returns the recent messages, newest last; platform empty is
	// every connected platform, limit 0 the default.
	History(ctx context.Context, channel, platform string, limit int) ([]ChatMessage, error)
}

// chatService implements streamcrewv1alpha1.ChatServiceHandler.
type chatService struct {
	c Chat
}

// Send sends a chat message.
func (cs *chatService) Send(ctx context.Context, req *v1alpha1.SendChatRequest) (*v1alpha1.SendChatResponse, error) {
	m := req
	if err := cs.c.Send(ctx, m.GetText(), m.GetChannel(), m.GetPlatform(), m.GetAsStreamer(), m.GetReplyMessageId()); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.SendChatResponse{}, nil
}

// Delete deletes a chat message by its platform ID.
func (cs *chatService) Delete(ctx context.Context, req *v1alpha1.DeleteChatRequest) (*v1alpha1.DeleteChatResponse, error) {
	m := req
	if err := cs.c.Delete(ctx, m.GetMessageId(), m.GetChannel(), m.GetPlatform()); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.DeleteChatResponse{}, nil
}

// Clear deletes all recent chat messages of a channel.
func (cs *chatService) Clear(ctx context.Context, req *v1alpha1.ClearChatRequest) (*v1alpha1.ClearChatResponse, error) {
	m := req
	if err := cs.c.Clear(ctx, m.GetChannel(), m.GetPlatform()); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.ClearChatResponse{}, nil
}

// History returns the recent chat messages of a channel.
func (cs *chatService) History(ctx context.Context, req *v1alpha1.HistoryChatRequest) (*v1alpha1.HistoryChatResponse, error) {
	m := req
	msgs, err := cs.c.History(ctx, m.GetChannel(), m.GetPlatform(), int(m.GetLimit()))
	if err != nil {
		return nil, apiError(err)
	}
	resp := &v1alpha1.HistoryChatResponse{}
	for _, x := range msgs {
		v := &v1alpha1.ChatMessageView{
			MessageId:   x.MessageID,
			Login:       x.Login,
			DisplayName: x.DisplayName,
			Text:        x.Text,
			Bot:         x.Bot,
			Streamer:    x.Streamer,
		}
		if !x.OccurredAt.IsZero() {
			v.OccurredAt = timestamppb.New(x.OccurredAt)
		}
		resp.Messages = append(resp.Messages, v)
	}
	return resp, nil
}
