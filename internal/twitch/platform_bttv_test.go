// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/emote"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// TestPlatformBTTVSocket joins the home channel on the BetterTTV socket
// while the platform runs (plan phase 2).
func TestPlatformBTTVSocket(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		joins []string
	)
	sockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		for {
			typ, payload, err := c.Read(r.Context())
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				mu.Lock()
				joins = append(joins, string(payload))
				mu.Unlock()
			}
		}
	}))
	defer sockSrv.Close()

	r := &fakeReceiver{}
	a := &fakeAuth{ready: true, token: "tok"}
	ws := &keepAliveServer{}
	hl := &helixServer{}
	hlSrv := httptest.NewTestServer(t, hl.handler(t))
	t.Cleanup(hlSrv.Close)
	wsSrv := httptest.NewTestServer(t, ws.handler(t))
	t.Cleanup(wsSrv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: hlSrv.Client(), Rate: 100, Burst: 100})
	p := twitch.NewPlatform(twitch.PlatformOptions{
		Receiver:       r,
		Auth:           a,
		Helix:          hc,
		DialClient:     wsSrv.Client(),
		Base:           wsSrv.URL + "/",
		HelixBase:      hlSrv.URL,
		Poll:           10 * time.Millisecond,
		Emotes:         emote.New(nil, time.Hour, nil),
		BTTVSocketBase: "ws" + strings.TrimPrefix(sockSrv.URL, "http") + "/",
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Run(ctx)
	}()

	// The home channel (AccountID 7 of the fake streamer) is joined.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, j := range joins {
			if strings.Contains(j, `"twitch:7"`) {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the platform did not stop")
	}
}
