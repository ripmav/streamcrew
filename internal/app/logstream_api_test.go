// SPDX-License-Identifier: MIT

package app_test

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
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
	stream := streamcrewv1alpha1connect.NewStreamServiceClient(http.DefaultClient, base)

	// The invalid level is rejected: the stream breaks with invalid
	// argument before it delivers anything.
	bad, err := stream.Subscribe(t.Context(), connect.NewRequest(&v1alpha1.SubscribeRequest{
		Filter: &v1alpha1.EventFilter{MinLevel: "verbose"},
	}))
	require.NoError(t, err)
	require.False(t, bad.Receive())
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(bad.Err()))

	sub, err := stream.Subscribe(t.Context(), connect.NewRequest(&v1alpha1.SubscribeRequest{
		Filter: &v1alpha1.EventFilter{MinLevel: "warn"},
	}))
	require.NoError(t, err)

	// Provoke log events: the watcher warns once on an invalid file; the
	// info lines below the level stay out of the filtered stream.
	rewriteFile(t, path, "not: [valid yaml\n")
	require.Eventually(t, func() bool {
		if !sub.Receive() {
			return true
		}
		if l := sub.Msg().GetLog(); l != nil && l.GetLevel() == "warn" {
			return true
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "the warned configuration problem to reach the filtered stream")
	assert.Equal(t, "warn", sub.Msg().GetLog().GetLevel())

	// Other event types are untouched by the log filter: a chat message
	// still arrives on the filtered stream.
	mp := a.MockPlatform()
	require.NotNil(t, mp)
	_, err = mp.Say(t.Context(), "alice", "still here")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		if !sub.Receive() {
			return true
		}
		return sub.Msg().GetChat() != nil
	}, 10*time.Second, 50*time.Millisecond, "the chat message to arrive despite the log filter")
	rewriteFile(t, path, "log_level: debug\n")
}
