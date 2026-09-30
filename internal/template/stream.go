// SPDX-License-Identifier: Apache-2.0

package template

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/ripmav/streamcrew/internal/domain/platform"
)

// [Interop] The identifier names in this file follow the original (spec
// template.md, purpose and scope) and may be replaced after the legal
// assessment (roadmap Gate O, O.1).

// StreamState is what a platform reports about the stream of the channel.
type StreamState struct {
	Live bool
	// Title and Game are empty if unknown.
	Title string
	Game  string
	// Viewers, Chatters and Followers are nil if unknown.
	Viewers   *int64
	Chatters  *int64
	Followers *int64
	// StartedAt is the start of the current stream; zero if the stream is
	// offline or the start is unknown.
	StartedAt time.Time
}

// StreamStates reports the state of the stream on a platform; the platform
// adapters implement it (roadmap phase 4).
type StreamStates interface {
	StreamState(ctx context.Context, p platform.Name) (StreamState, error)
}

// StreamFamily returns the identifiers of the stream on the platform of the
// run: $streamtitle, $streamgamename, $streamviewercount,
// $streamchattercount, $streamfollowercount, $streamislive,
// $streamstartdatetime, $streamstartdate, $streamstarttime and the uptime
// (B43). It asks states once per render; values the platform does not
// report have no value (B4). With nil states no identifier has a value.
func StreamFamily(states StreamStates) Family {
	state := func(ctx context.Context, s *Scope) (StreamState, bool, error) {
		if states == nil {
			return StreamState{}, false, nil
		}
		st, err := s.Memo("stream.state", func() (StreamState, error) {
			return states.StreamState(ctx, s.Platform)
		})
		if err != nil {
			return StreamState{}, false, fmt.Errorf("stream state: %w", err)
		}
		return st, true, nil
	}
	text := func(field func(StreamState) string) Resolver {
		return func(ctx context.Context, s *Scope) (Value, bool, error) {
			st, ok, err := state(ctx, s)
			if !ok || field(st) == "" {
				return Value{}, false, err
			}
			return TextValue(field(st)), true, nil
		}
	}
	count := func(field func(StreamState) *int64) Resolver {
		return func(ctx context.Context, s *Scope) (Value, bool, error) {
			st, ok, err := state(ctx, s)
			if !ok || field(st) == nil {
				return Value{}, false, err
			}
			return IntValue(*field(st)), true, nil
		}
	}
	// started returns the start in the time zone of the profile and the
	// uptime; ok is false while the stream is offline.
	started := func(value func(start time.Time, uptime time.Duration) Value) Resolver {
		return func(ctx context.Context, s *Scope) (Value, bool, error) {
			st, ok, err := state(ctx, s)
			if !ok || !st.Live || st.StartedAt.IsZero() {
				return Value{}, false, err
			}
			return value(st.StartedAt.In(s.location()), max(s.now().Sub(st.StartedAt), 0)), true, nil
		}
	}
	return Family{
		Name: "stream",
		Identifiers: []Identifier{
			{Name: "streamtitle", Resolve: text(func(st StreamState) string { return st.Title })},
			{Name: "streamgamename", Resolve: text(func(st StreamState) string { return st.Game })},
			{Name: "streamviewercount", Resolve: count(func(st StreamState) *int64 { return st.Viewers })},
			{Name: "streamchattercount", Resolve: count(func(st StreamState) *int64 { return st.Chatters })},
			{Name: "streamfollowercount", Resolve: count(func(st StreamState) *int64 { return st.Followers })},
			{Name: "streamislive", Resolve: func(ctx context.Context, s *Scope) (Value, bool, error) {
				st, ok, err := state(ctx, s)
				if !ok {
					return Value{}, false, err
				}
				return TextValue(strconv.FormatBool(st.Live)), true, nil
			}},
			{Name: "streamstartdatetime", Resolve: started(func(start time.Time, _ time.Duration) Value {
				return TextValue(start.Format(layoutDateTime))
			})},
			{Name: "streamstartdate", Resolve: started(func(start time.Time, _ time.Duration) Value {
				return TextValue(start.Format(layoutDate))
			})},
			{Name: "streamstarttime", Resolve: started(func(start time.Time, _ time.Duration) Value {
				return TextValue(start.Format(layoutTime))
			})},
			// The uptime parts are the places of a clock (B43).
			{Name: "streamuptimetotal", Resolve: started(func(_ time.Time, up time.Duration) Value {
				return TextValue(fmt.Sprintf("%d:%02d", int64(up/time.Hour), int64(up/time.Minute%60)))
			})},
			{Name: "streamuptimehours", Resolve: started(func(_ time.Time, up time.Duration) Value {
				return IntValue(int64(up / time.Hour))
			})},
			{Name: "streamuptimeminutes", Resolve: started(func(_ time.Time, up time.Duration) Value {
				return IntValue(int64(up / time.Minute % 60))
			})},
			{Name: "streamuptimeseconds", Resolve: started(func(_ time.Time, up time.Duration) Value {
				return IntValue(int64(up / time.Second % 60))
			})},
		},
	}
}
