// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
	"github.com/ripmav/streamcrew/internal/twitch/twitchmock"
)

// sessionID waits until the platform reports an EventSub session ID.
func sessionID(t *testing.T, p *twitch.Platform) string {
	t.Helper()
	var id string
	require.Eventually(t, func() bool {
		id = p.SessionID()
		return id != ""
	}, 15*time.Second, 50*time.Millisecond, "the platform must report the session")
	return id
}

// freshSession waits until the session of the platform has changed
// away from old (the reconnect is done) and returns the new ID.
func freshSession(t *testing.T, p *twitch.Platform, old string) string {
	t.Helper()
	id := ""
	require.Eventually(t, func() bool {
		id = p.SessionID()
		return id != "" && id != old
	}, 45*time.Second, 200*time.Millisecond, "a new session must replace the old one")
	return id
}

// fireChat fires a chat message (channel.chat.message v2) with the
// given message ID and text; the body is identical for identical
// arguments, so the B22 dedup key is identical.
func fireChat(t *testing.T, c *twitchmock.Client, id, text string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"message_id":%q,"badges":[],`+
			`"message":{"text":%q},`+
			`"chatter_user_id":%q,"chatter_user_login":%q,"chatter_user_name":"MockViewer"}`,
		id, text, c.BroadcasterID, c.BroadcasterLogin)
	require.NoError(t, c.FireEvent("channel.chat.message", "2", json.RawMessage(body)))
}

// TestChaosAgainstTheMock runs the reconnect paths of the EventSub
// client against the real twitch-cli mock (plan 4.5, task 6): the
// session reconnect (reconnect URL, the subscriptions carry over with
// the session, the repeats of the grace period are dropped by the
// B22 dedup), a forced drop (close 4006 — the mock supports 4003–
// 4007, not the production 4008), and the keepalive timeout
// (silence of the server).
//
// The scenarios run in this order: the mock keeps the subscriptions
// per connection, a hard drop disables them, and only the session
// reconnect carries them over — so the reconnect runs while the
// subscriptions are still enabled on the current session.
//
// No t.Parallel: the mock server's RPC port is fixed (44747). The
// keepalive scenario waits real time (the mock's keepalive floor is
// 10 s, the client tolerates two intervals), and the mock blocks the
// close/keepalive RPCs for about 30 s after a reconnect test — so
// the test takes a minute or two.
//
// The server-restart scenario of the plan is covered by the forced
// drop: to the client a dead server and a dropped connection are the
// same path (read error, base URL, backoff).
func TestChaosAgainstTheMock(t *testing.T) {
	c := startMock(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

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
	go func() {
		assert.NoError(t, p.Run(ctx))
	}()

	sid := sessionID(t, p)
	// The manager reconciles the subscriptions to the current session
	// right after the connect.
	require.Eventually(t, func() bool {
		got := subscriptionTypes(t, c)
		return got["stream.online"] && got["channel.cheer"] && got["channel.raid"]
	}, 15*time.Second, 200*time.Millisecond, "the subscriptions must be registered in the mock")

	// Baseline: an event of the current session arrives.
	before := r.msgCount()
	fireChat(t, c, "chaos-a", "hello baseline")
	require.Eventually(t, func() bool { return r.msgCount() == before+1 },
		10*time.Second, 50*time.Millisecond, "the baseline message must arrive")

	// 1. Session reconnect: the server announces the load balancer
	//    restart; the adapter follows the reconnect URL at once (no
	//    backoff), the subscriptions carry over with the session, the
	//    events after the restart arrive (no event loss), and the
	//    repeat of a seen payload within the grace period is dropped
	//    by the B22 dedup.
	require.NoError(t, c.Reconnect())
	sid = freshSession(t, p, sid)
	got := subscriptionTypes(t, c)
	for _, want := range []string{"stream.online", "channel.cheer", "channel.raid"} {
		assert.True(t, got[want], "the subscription %s must carry over to the new session", want)
	}
	fireChat(t, c, "chaos-a", "hello baseline") // the repeat of the baseline payload
	// limit is immutable: testify's Never polls the condition in a
	// goroutine that may still run when the next line reassigns before.
	limit := before + 1
	require.Never(t, func() bool { return r.msgCount() > limit },
		3*time.Second, 100*time.Millisecond, "the repeated payload must be dropped")
	before = r.msgCount()
	fireChat(t, c, "chaos-d", "hello after the restart")
	require.Eventually(t, func() bool { return r.msgCount() == before+1 },
		10*time.Second, 50*time.Millisecond, "the message after the restart must arrive")

	// 2. Forced drop: the server closes the client with a network
	//    failure (4006); the adapter reconnects at the base URL
	//    (backoff) with a fresh session, and events arrive again. The
	//    mock blocks the close RPC for about 30 s while the old
	//    server of the reconnect test shuts down, so retry.
	require.Eventually(t, func() bool {
		return c.CloseClient(sid, 4006) == nil
	}, 45*time.Second, time.Second, "the mock must accept the close")
	sid = freshSession(t, p, sid)
	before = r.msgCount()
	fireChat(t, c, "chaos-b", "hello after the drop")
	require.Eventually(t, func() bool { return r.msgCount() == before+1 },
		10*time.Second, 50*time.Millisecond, "the message after the drop must arrive")

	// 3. Keepalive timeout: the server stops the keepalives; after
	//    two keepalive intervals (the pong factor) the adapter gives
	//    up on the silent connection and reconnects.
	require.NoError(t, c.SetKeepalive(sid, false))
	sid = freshSession(t, p, sid)
	before = r.msgCount()
	fireChat(t, c, "chaos-c", "hello after the silence")
	require.Eventually(t, func() bool { return r.msgCount() == before+1 },
		10*time.Second, 50*time.Millisecond, "the message after the silence must arrive")
}
