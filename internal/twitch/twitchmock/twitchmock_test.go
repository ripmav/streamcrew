// SPDX-License-Identifier: MIT

package twitchmock_test

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/twitch/twitchmock"
)

// start skips the test without the twitch binary and starts the mock
// environment, which is closed with t.Cleanup.
func start(t *testing.T) *twitchmock.Client {
	t.Helper()
	_, err := twitchmock.FindBinary()
	if err != nil {
		t.Skipf("the twitch-cli binary is unavailable: %v", err)
	}
	c, err := twitchmock.New(t.Context())
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

// TestClientLifecycle is the self-test of the harness (roadmap 4.5,
// task 1): the servers start, the generated account data is
// consistent (the token is accepted by the mock API), the EventSub
// WebSocket speaks the protocol, and the RPC fires an event that the
// client receives.
func TestClientLifecycle(t *testing.T) {
	// no t.Parallel: the mock server's RPC port is fixed (44747),
	// so at most one environment runs at a time (package docs).
	c := start(t)
	ctx := t.Context()

	// The generated token is accepted by the mock API.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.MockAPIBase+"/users?id="+c.BroadcasterID, nil)
	require.NoError(t, err)
	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var users struct {
		Data []struct {
			Login string `json:"login"`
		} `json:"data"`
	}
	data, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(data, &users))
	require.NoError(t, resp.Body.Close())
	require.Len(t, users.Data, 1)
	assert.Equal(t, c.BroadcasterLogin, users.Data[0].Login)

	// Connect a raw client to the EventSub WebSocket.
	conn, resp, err := websocket.Dial(ctx, c.WSBase, nil)
	require.NoError(t, err)
	if resp.Body != nil { // the websocket library keeps a nil body after the upgrade
		require.NoError(t, resp.Body.Close())
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	read := func() *wsMessage {
		t.Helper()
		_, data, err := conn.Read(ctx)
		require.NoError(t, err)
		var m wsMessage
		require.NoError(t, json.Unmarshal(data, &m))
		return &m
	}
	welcome := read()
	assert.Equal(t, "session_welcome", welcome.Metadata.MessageType)
	require.NotEmpty(t, welcome.Payload.Session.ID)
	sessionID := welcome.Payload.Session.ID

	// The REST subscription endpoint of the WebSocket server
	// accepts a documented topic through the Helix router.
	body := fmt.Sprintf(
		`{"type":"stream.online","version":"1",`+
			`"condition":{"broadcaster_user_id":%q},`+
			`"transport":{"method":"websocket","session_id":%q}}`,
		c.BroadcasterID, sessionID)
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.HelixBase+"/eventsub/subscriptions", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Client-Id", c.ClientID)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	// Fire a stream.online event and receive it as a notification.
	event := fmt.Sprintf(
		`{"id":%q,"type":"stream","broadcaster_user_id":%q,`+
			`"broadcaster_user_login":%q,"broadcaster_user_display_name":%q,`+
			`"game_id":"1","game_name":"Test","stream_type":"live",`+
			`"started_at":"2026-01-01T00:00:00Z"}`,
		c.BroadcasterID, c.BroadcasterID, c.BroadcasterLogin, c.BroadcasterLogin)
	require.NoError(t, c.FireEvent("stream.online", "1", jsontext.Value(event)))

	deadline := time.Now().Add(10 * time.Second)
	for {
		msg := read()
		if msg.Metadata.MessageType == "notification" {
			assert.Equal(t, "stream.online", msg.Metadata.SubscriptionType)
			require.NotNil(t, msg.Payload.Event)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no notification before the deadline, last message %s", msg.Metadata.MessageType)
		}
	}
}

// wsMessage is an EventSub WebSocket message (enough of it for the
// self-test).
type wsMessage struct {
	Metadata struct {
		MessageType         string `json:"message_type"`
		SubscriptionType    string `json:"subscription_type"`
		SubscriptionVersion string `json:"subscription_version"`
	} `json:"metadata"`
	Payload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Event jsontext.Value `json:"event"`
	} `json:"payload"`
}
