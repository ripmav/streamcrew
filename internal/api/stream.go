// SPDX-License-Identifier: MIT

package api

import (
	"context"

	"connectrpc.com/connect/v2"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// EventFilter limits which events are delivered; empty lists mean all.
type EventFilter struct {
	// Kinds are event type prefixes; empty matches everything.
	Kinds []string
	// Platforms are platform names; empty matches everything.
	Platforms []string
	// Channels are channel logins; empty matches everything.
	Channels []string
}

// Stream is the port the StreamService handler needs (roadmap 6.2.1).
type Stream interface {
	// Stream returns a channel of converted events matching the filter,
	// starting with the last since events from the buffer. The channel is
	// closed when the context ends.
	Stream(ctx context.Context, filter EventFilter, since uint32) <-chan *v1alpha1.Event
	// AnswerPrompt answers the prompt with the ID; it errors if it is not
	// pending.
	AnswerPrompt(ctx context.Context, id, value string, confirmed bool) error
	// Simulate publishes a platform-neutral event of the mock platform to
	// the core; it returns the platform ID of the event.
	Simulate(ctx context.Context, eventType, user, target, text string) (string, error)
}

// streamService implements the StreamService handler.
type streamService struct {
	s Stream
}

// Subscribe streams events to the client until the client cancels.
func (s *streamService) Subscribe(ctx context.Context, req *v1alpha1.SubscribeRequest, stream streamcrewv1alpha1connect.StreamServiceSubscribeServerStream) error {
	// A no-op send flushes the response headers, so the client's Subscribe
	// call returns before the first event arrives (over HTTP/1.1 the headers
	// are only sent with the first write).
	if err := stream.Send(nil); err != nil {
		return err
	}
	events := s.s.Stream(ctx, filterFrom(req.GetFilter()), req.GetSince())
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-events:
			if !ok {
				return nil
			}
			if err := stream.Send(e); err != nil {
				return err
			}
		}
	}
}

// AnswerPrompt answers a prompt delivered through the stream.
func (s *streamService) AnswerPrompt(ctx context.Context, req *v1alpha1.AnswerPromptRequest) (*v1alpha1.AnswerPromptResponse, error) {
	if req.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "id is required")
	}
	if err := s.s.AnswerPrompt(ctx, req.Id, req.Value, req.Confirmed); err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.AnswerPromptResponse{}, nil
}

// Simulate publishes a simulated event of the mock platform (roadmap
// 6.3).
func (s *streamService) Simulate(ctx context.Context, req *v1alpha1.SimulateEventRequest) (*v1alpha1.SimulateEventResponse, error) {
	if req.EventType == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "event_type is required")
	}
	id, err := s.s.Simulate(ctx, req.EventType, req.User, req.Target, req.Text)
	if err != nil {
		return nil, apiError(err)
	}
	return &v1alpha1.SimulateEventResponse{EventId: id}, nil
}

// filterFrom converts the protocol filter.
func filterFrom(f *v1alpha1.EventFilter) EventFilter {
	if f == nil {
		return EventFilter{}
	}
	return EventFilter{Kinds: f.GetKinds(), Platforms: f.GetPlatforms(), Channels: f.GetChannels()}
}
