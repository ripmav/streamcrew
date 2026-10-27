// SPDX-License-Identifier: MIT

package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// flakyStream is the StreamService handler of the reconnect tests: it fails
// the first subscribe when failFirst, then streams events until the channel
// closes.
type flakyStream struct {
	failFirst atomic.Bool
	events    chan *v1alpha1.Event
	attempts  atomic.Int32
}

func (f *flakyStream) Subscribe(_ context.Context, _ *v1alpha1.SubscribeRequest, ss streamcrewv1alpha1connect.StreamServiceSubscribeServerStream) error {
	f.attempts.Add(1)
	if f.failFirst.CompareAndSwap(true, false) {
		return connect.NewError(connect.CodeUnavailable, "the first subscribe fails")
	}
	for {
		e, ok := <-f.events
		if !ok {
			return nil
		}
		if err := ss.Send(e); err != nil {
			return err
		}
	}
}

func (f *flakyStream) AnswerPrompt(context.Context, *v1alpha1.AnswerPromptRequest) (*v1alpha1.AnswerPromptResponse, error) {
	return &v1alpha1.AnswerPromptResponse{}, nil
}

func (f *flakyStream) Simulate(context.Context, *v1alpha1.SimulateEventRequest) (*v1alpha1.SimulateEventResponse, error) {
	return &v1alpha1.SimulateEventResponse{}, nil
}

// streamTestCore mounts the handler and returns the model pointed at it,
// with the stream loop running in a goroutine.
func streamTestCore(t *testing.T, svc *flakyStream) (model, context.CancelFunc, <-chan struct{}) {
	t.Helper()
	cs := connect.NewServer()
	streamcrewv1alpha1connect.RegisterStreamServiceHandler(cs, svc)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, cs, connecthttp.WithReadMaxBytes(0))
	ht := httptest.NewServer(mux)
	t.Cleanup(ht.Close)

	ctx, cancel := context.WithCancel(t.Context())
	m := newModel(ctx, Options{})
	m.cl = newClients(http.DefaultClient, ht.URL, "")
	done := make(chan struct{})
	go func() { m.subscribeLoop(); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the stream loop did not end on the context cancel")
		}
	})
	return m, cancel, done
}

// TestStreamReconnectsAfterABreak covers the reconnect of the TUI stream
// (U6 of the code review 2026-10-26): a broken subscription is retried,
// the events flow again, and the context cancel ends the loop.
func TestStreamReconnectsAfterABreak(t *testing.T) {
	t.Parallel()
	events := make(chan *v1alpha1.Event, 4)
	svc := &flakyStream{events: events}
	svc.failFirst.Store(true)
	m, cancel, done := streamTestCore(t, svc)
	// Registered after the server close: it must run first, or the server
	// close waits for a handler that waits for the channel.
	t.Cleanup(func() { close(events) })

	events <- &v1alpha1.Event{Type: "feed"}
	var got *v1alpha1.Event
	require.Eventually(t, func() bool {
		for {
			select {
			case msg := <-m.streamCh:
				if msg.event != nil {
					got = msg.event
					return true
				}
				continue
			default:
				return false
			}
		}
	}, 10*time.Second, 10*time.Millisecond, "the stream reconnects and delivers again")
	assert.Equal(t, "feed", got.GetType())
	assert.Equal(t, int32(2), svc.attempts.Load(), "the first attempt fails, the second connects")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream loop did not end on the context cancel")
	}
}

// TestStreamReportsTheDroppedEvents covers the drop report (U6 of the code
// review 2026-10-26): events that do not fit the full buffer are counted
// and the break notice carries the count. Every event is either delivered
// to the buffer or counted as a drop, so the sum is the number of events
// independent of the delivery timing.
func TestStreamReportsTheDroppedEvents(t *testing.T) {
	t.Parallel()
	events := make(chan *v1alpha1.Event, 256)
	svc := &flakyStream{events: events}
	m, cancel, done := streamTestCore(t, svc)

	// Fill the stream buffer, so the next events do not fit.
	for range cap(m.streamCh) {
		m.streamCh <- streamMsg{event: &v1alpha1.Event{Type: "filler"}}
	}
	const total = 200
	for range total {
		events <- &v1alpha1.Event{Type: "feed"}
	}
	close(events) // end the stream

	// The wait consumes messages as it polls, so it counts the delivered
	// events itself; after the break the buffer holds the rest.
	var notice streamMsg
	var feeds int
	require.Eventually(t, func() bool {
		select {
		case msg := <-m.streamCh:
			switch {
			case msg.event == nil && msg.drops > 0:
				notice = msg
				return true
			case msg.event != nil && msg.event.GetType() == "feed":
				feeds++
			}
			return false
		default:
			return false
		}
	}, 10*time.Second, 10*time.Millisecond, "the break notice arrives")
	for len(m.streamCh) > 0 {
		if msg := <-m.streamCh; msg.event != nil && msg.event.GetType() == "feed" {
			feeds++
		}
	}
	assert.Equal(t, int64(total), notice.drops+int64(feeds), "every event is delivered or dropped")
	assert.Greater(t, notice.drops, int64(0))

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream loop did not end on the context cancel")
	}
}
