// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/eventtype"
	"github.com/ripmav/streamcrew/internal/event"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
	"github.com/ripmav/streamcrew/internal/twitch/twitchmock"
)

// startMock skips the test without the twitch binary and starts the
// mock environment, which is closed with t.Cleanup.
func startMock(t *testing.T) *twitchmock.Client {
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

// mockUser is an account of the generated mock API database.
type mockUser struct {
	id    string
	login string
}

// otherUser is the generated user that is not the partner broadcaster:
// the "alice" of the test, who exists in the mock database, so write
// actions on her (the ban) are accepted by the mock API.
func otherUser(t *testing.T, c *twitchmock.Client) mockUser {
	t.Helper()
	// The units endpoints are unauthenticated and sit directly on the
	// port, not under the "/mock" prefix of MockAPIBase.
	base, err := url.Parse(c.MockAPIBase)
	require.NoError(t, err)
	resp := getJSON(t, base.Scheme+"://"+base.Host+"/units/users", "", "")
	var body struct {
		Data []struct {
			ID    string `json:"id"`
			Login string `json:"login"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp, &body))
	for _, u := range body.Data {
		if u.ID != c.BroadcasterID {
			return mockUser{id: u.ID, login: u.Login}
		}
	}
	t.Fatal("the generated users have no non-partner")
	return mockUser{}
}

// getJSON does a GET against the mock API and returns the body; the
// credentials are empty for the unauthenticated units endpoints.
func getJSON(t *testing.T, target, clientID, token string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	if clientID != "" {
		req.Header.Set("Client-ID", clientID)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Less(t, resp.StatusCode, 300, "GET %s", target)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return data
}

// subscriptionTypes is the set of the event types the mock EventSub
// server has as subscriptions right now.
func subscriptionTypes(t *testing.T, c *twitchmock.Client) map[string]bool {
	t.Helper()
	data := getJSON(t, c.HelixBase+"/eventsub/subscriptions", c.ClientID, c.Token)
	var body struct {
		Data []struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(data, &body))
	m := make(map[string]bool, len(body.Data))
	for _, s := range body.Data {
		m[s.Type] = true
	}
	return m
}

// lastEventOfType returns the last received event of the type.
func (r *fakeReceiver) lastEventOfType(t *testing.T, typ event.Type) connector.Event {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.events) - 1; i >= 0; i-- {
		if r.events[i].Type == typ {
			return r.events[i]
		}
	}
	t.Fatal("no event of the type received")
	return connector.Event{}
}

func (r *fakeReceiver) msgCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

// TestPlatformAgainstTheMock is the end-to-end integration test
// (plan 4.5, task 5): the platform connects to the official twitch-cli
// mock EventSub WebSocket server, keeps its subscriptions in line with
// the mock, and maps the fired events to the receiver.
//
// No write action against the mock API is tested: the generated token
// carries no scopes, and every mock write endpoint demands one (401).
// See the plan, open item 3 (mock API scopes).
//
// There is no t.Parallel: the mock server's RPC port is fixed
// (44747), so at most one environment runs at a time (package docs of
// twitchmock).
func TestPlatformAgainstTheMock(t *testing.T) {
	// no t.Parallel: the mock server's RPC port is fixed (44747).
	c := startMock(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	alice := otherUser(t, c)

	r := &fakeReceiver{}
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: http.DefaultClient, Rate: 100, Burst: 100})
	p := twitch.NewPlatform(twitch.PlatformOptions{
		Receiver: r,
		Auth: &twitchmock.Auth{
			AccountID:   c.BroadcasterID,
			Login:       c.BroadcasterLogin,
			AccessToken: c.Token,
			AppClientID: c.ClientID,
		},
		Helix:     hc,
		Base:      c.WSBase,
		HelixBase: c.HelixBase,
		Poll:      50 * time.Millisecond,
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()

	// Connection: the platform accepts the session_welcome and reports
	// connected.
	require.Eventually(t, func() bool { return p.Status().Streamer }, 10*time.Second, 50*time.Millisecond)

	// Subscriptions: the topics the mock accepts are created through
	// REST and appear in the GET list; the topics the mock rejects
	// (400/410) are skipped by the manager.
	accepted := []string{
		"stream.online", "stream.offline", "channel.follow",
		"channel.raid", "channel.cheer",
		"channel.channel_points_custom_reward_redemption.add",
	}
	require.Eventually(t, func() bool {
		got := subscriptionTypes(t, c)
		for _, typ := range accepted {
			if !got[typ] {
				return false
			}
		}
		return true
	}, 10*time.Second, 100*time.Millisecond, "the accepted subscriptions are not in the mock")
	assert.False(t, subscriptionTypes(t, c)["channel.chat.message"],
		"the mock rejects the chat topic, the manager must skip it")

	// Event flow: stream.online is mapped to the stream state. The
	// platform already reported the state of the stream once on the
	// connect, so wait for the second report, the one of the event.
	streamsBefore := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.streams)
	}
	before := streamsBefore()
	online := fmt.Sprintf(
		`{"id":"1","type":"stream","broadcaster_user_id":%q,`+
			`"broadcaster_user_login":%q,"broadcaster_user_display_name":%q,`+
			`"game_id":"1","game_name":"Test","stream_type":"live","started_at":"2026-01-01T00:00:00Z"}`,
		c.BroadcasterID, c.BroadcasterLogin, c.BroadcasterLogin)
	require.NoError(t, c.FireEvent("stream.online", "1", json.RawMessage(online)))
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.streams) > before && r.streams[len(r.streams)-1]
	}, 10*time.Second, 50*time.Millisecond)

	// Event flow: a chat message is mapped to the incoming message
	// with the author and the emote code; the repeated delivery of
	// the same message is dropped (B22).
	msg := fmt.Sprintf(
		`{"id":"msg-int-1","badges":[],"badge_info":{},`+
			`"channel_id":%q,"channel_login":%q,"channel_display_name":%q,`+
			`"message":"!hello :Kappa:","emotes":[{"id":"25","start":7,"end":13}],`+
			`"user_id":%q,"user_login":%q,"user_display_name":"Alice"}`,
		c.BroadcasterID, c.BroadcasterLogin, c.BroadcasterLogin, alice.id, alice.login)
	before = r.msgCount()
	require.NoError(t, c.FireEvent("channel.chat.message", "2", json.RawMessage(msg)))
	require.NoError(t, c.FireEvent("channel.chat.message", "2", json.RawMessage(msg))) // the repeat
	require.Eventually(t, func() bool { return r.msgCount() == before+1 },
		10*time.Second, 50*time.Millisecond)
	m := r.lastMessage(t)
	assert.Equal(t, "msg-int-1", m.Message.ID)
	assert.Equal(t, "!hello :Kappa:", m.Message.Text)
	assert.Equal(t, []string{":Kappa"}, m.Message.Emotes)
	assert.Equal(t, alice.id, m.Author.PlatformUserID)
	assert.Equal(t, alice.login, m.Author.Login)

	// Event flow: a cheer is mapped to the bits event with the user
	// and the amount.
	cheer := fmt.Sprintf(
		`{"id":"cheer-int-1","broadcaster_user_id":%q,"broadcaster_user_login":%q,`+
			`"broadcaster_user_display_name":%q,"from_user_id":%q,"from_user_login":%q,`+
			`"from_user_display_name":"Alice","cumulative_bits":100,"message":"cheer"}`,
		c.BroadcasterID, c.BroadcasterLogin, c.BroadcasterLogin, alice.id, alice.login)
	require.NoError(t, c.FireEvent("channel.cheer", "1", json.RawMessage(cheer)))
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.events {
			if e.Type == eventtype.TwitchBitsCheer {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)
	ev := r.lastEventOfType(t, eventtype.TwitchBitsCheer)
	require.NotNil(t, ev.User)
	assert.Equal(t, alice.id, ev.User.PlatformUserID)
	require.NotNil(t, ev.Details.Bits)
	assert.Equal(t, int64(100), ev.Details.Bits.Amount)

	// Event flow: a channel points redemption is mapped to the
	// custom power-up event with the reward and the user input.
	redeem := fmt.Sprintf(
		`{"id":"redemption-int-1","redemption_status":"unstarted",`+
			`"reward":{"id":"reward-int","title":"Shoutout","prompt":"Your shoutout",`+
			`"cost":100,"is_enabled":true,"is_user_input_required":true,"channel_id":%q},`+
			`"user_input":"hello from the integration test",`+
			`"user_id":%q,"user_name":%q,"user_display_name":"Alice",`+
			`"custom_reward_cost":100,"custom_reward_id":"reward-int","stream_id":"1"}`,
		c.BroadcasterID, alice.id, alice.login)
	require.NoError(t, c.FireEvent("channel.channel_points_custom_reward_redemption.add", "1", json.RawMessage(redeem)))
	require.Eventually(t, func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, e := range r.events {
			if e.Type == eventtype.TwitchCustomPowerUpRedeem {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond)
	ev = r.lastEventOfType(t, eventtype.TwitchCustomPowerUpRedeem)
	require.NotNil(t, ev.User)
	assert.Equal(t, alice.id, ev.User.PlatformUserID)
	require.NotNil(t, ev.Details.CustomPowerUp)
	assert.Equal(t, "reward-int", ev.Details.CustomPowerUp.Reward)
	require.NotNil(t, ev.Details.Message)
	assert.Equal(t, "hello from the integration test", ev.Details.Message.Text)

	cancel()
	<-done
}
