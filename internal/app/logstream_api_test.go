// SPDX-License-Identifier: MIT

package app_test

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v1alpha1 "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1"
	streamcrewv1alpha1connect "github.com/ripmav/streamcrew/api/gen/streamcrew/v1alpha1/streamcrewv1alpha1connect"
)

// TestSubscribeFiltersLogEventsByMinLevel covers the read-side log filter
// of the stream (roadmap 6.6.3): min_level delivers the log events at and
// above the level only, and an invalid value is rejected.
func TestSubscribeFiltersLogEventsByMinLevel(t *testing.T) {
	a, path := hotCore(t, &outPub{})
	base := "http://" + mustServerAddr(t, a)
	stream := streamcrewv1alpha1connect.NewStreamServiceClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, base, connecthttp.WithReadMaxBytes(0))))

	// The invalid level is rejected: the stream breaks with invalid
	// argument before it delivers anything.
	bad, err := stream.Subscribe(t.Context(), &v1alpha1.SubscribeRequest{
		Filter: &v1alpha1.EventFilter{MinLevel: "verbose"},
	})
	require.NoError(t, err)
	_, err = bad.Receive()
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	sub, err := stream.Subscribe(t.Context(), &v1alpha1.SubscribeRequest{
		Filter: &v1alpha1.EventFilter{MinLevel: "warn"},
	})
	require.NoError(t, err)

	// Provoke log events: the watcher warns once on an invalid file; the
	// info lines below the level stay out of the filtered stream.
	rewriteFile(t, path, "not: [valid yaml\n")
	var warned *v1alpha1.Event
	require.Eventually(t, func() bool {
		event, err := sub.Receive()
		if err != nil {
			return true
		}
		warned = event
		return warned.GetLog() != nil && warned.GetLog().GetLevel() == "warn"
	}, 15*time.Second, 50*time.Millisecond, "the warned configuration problem to reach the filtered stream")
	require.NotNil(t, warned)
	assert.Equal(t, "warn", warned.GetLog().GetLevel())

	// Other event types are untouched by the log filter: a chat message
	// still arrives on the filtered stream.
	mp := a.MockPlatform()
	require.NotNil(t, mp)
	_, err = mp.Say(t.Context(), "alice", "still here")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		event, err := sub.Receive()
		return err == nil && event.GetChat() != nil
	}, 10*time.Second, 50*time.Millisecond, "the chat message to arrive despite the log filter")
	rewriteFile(t, path, "log_level: debug\n")
}
