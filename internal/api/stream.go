// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect/v2"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// EventFilter limits which events are delivered; empty lists mean all.
type EventFilter struct {
	// Kinds are event type prefixes; empty matches everything.
	Kinds []string
	// MinLevel is the lowest level of the log events that are delivered:
	// "debug", "info", "warn" or "error"; empty delivers all log events.
	// It is a read-side filter (roadmap 6.6.3): the core's log level is
	// unchanged, and no other event type is touched.
	MinLevel string
	// Platforms are platform names; empty matches everything.
	Platforms []string
	// Channels are channel logins; empty matches everything.
	Channels []string
}

// ErrMinLevel names an invalid min_level of the stream filter.
var ErrMinLevel = errors.New("min_level is not debug, info, warn or error")

// minLevel parses the filter's min_level into a comparison level; empty
// accepts every log event.
func (f EventFilter) minLevel() (slog.Level, error) {
	switch strings.ToLower(f.MinLevel) {
	case "", "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, ErrMinLevel
}

// MinLevelOr parses the filter's min_level like MinLevel but returns def
// instead of an error for an unknown value; the stream path is already
// validated by the handler.
func (f EventFilter) MinLevelOr(def slog.Level) slog.Level {
	l, err := f.minLevel()
	if err != nil {
		return def
	}
	return l
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
	f := filterFrom(req.GetFilter())
	if _, err := f.minLevel(); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// A no-op send flushes the response headers, so the client's Subscribe
	// call returns before the first event arrives (over HTTP/1.1 the headers
	// are only sent with the first write).
	if err := stream.Send(nil); err != nil {
		return err
	}
	events := s.s.Stream(ctx, f, req.GetSince())
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
	return EventFilter{Kinds: f.GetKinds(), MinLevel: f.GetMinLevel(), Platforms: f.GetPlatforms(), Channels: f.GetChannels()}
}
