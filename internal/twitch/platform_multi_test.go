// SPDX-License-Identifier: MIT

package twitch_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// botAuth is a fakeAuth with a bot account and the moderator status of
// the bot on the channels (twitch-multi-channel.md, B2, B3).
type botAuth struct {
	*fakeAuth
	botID      string
	botReady   bool
	botToken   string
	moderators map[string][]string
}

// Bot implements twitch.Auth.
func (b *botAuth) Bot(context.Context) (twitch.AccountState, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return twitch.AccountState{AccountID: b.botID, Login: "bot", Ready: b.botReady}, nil
}

// BotToken implements twitch.Auth.
func (b *botAuth) BotToken(context.Context) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.botToken, nil
}

func (b *botAuth) setMods(channels ...string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.moderators = make(map[string][]string)
	for _, c := range channels {
		b.moderators[c] = []string{b.botID}
	}
}

// multiHelixServer is a fake Helix with the user lookup and the
// moderator list of the channels (twitch-multi-channel.md, B3, B4).
type multiHelixServer struct {
	*helixServer
	users    map[string]string // login -> id
	mods     *botAuth
	subConds []helix.SubscriptionCondition
}

func (h *multiHelixServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	fallback := h.helixServer.handler(t)
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/users":
			login := r.URL.Query().Get("login")
			id, ok := h.users[login]
			if !ok {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			b, err := jsonv2.Marshal(struct {
				Data []struct {
					ID    string `json:"id"`
					Login string `json:"login"`
				} `json:"data"`
			}{[]struct {
				ID    string `json:"id"`
				Login string `json:"login"`
			}{{ID: id, Login: login}}})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(b)
		case r.URL.Path == "/moderation/moderators":
			h.mods.mu.Lock()
			list := h.mods.moderators[r.URL.Query().Get("broadcaster_id")]
			h.mods.mu.Unlock()
			ids := []string{}
			for _, id := range list {
				ids = append(ids, `"`+id+`"`)
			}
			_, _ = w.Write([]byte(`{"data":[{"id":` + strings.Join(ids, ",") + `}]}`))
		case r.URL.Path == "/eventsub/subscriptions" && r.Method == http.MethodPost:
			b, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var in helix.CreateSubscriptionInput
			if err := jsonv2.Unmarshal(b, &in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			h.mu.Lock()
			h.created++
			h.subConds = append(h.subConds, in.Condition)
			h.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"s1"}}`))
		default:
			fallback(w, r)
		}
	}
}

// eventSubServer is a fake EventSub server for the multi-channel
// sessions: it welcomes every connection, keeps it alive, and logs the
// tokens of the connections by their Authorization header.
type eventSubServer struct {
	mu   sync.Mutex
	seen []string
}

func (s *eventSubServer) handler(_ *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		token := r.URL.Query().Get("access_token")
		s.mu.Lock()
		s.seen = append(s.seen, token)
		s.mu.Unlock()
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(welcomeJSON("s-"+token, "1"))); err != nil {
			return
		}
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			_, _, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_ = conn.Write(r.Context(), websocket.MessageText, []byte(keepaliveJSON()))
			}
		}
	}
}

func (s *eventSubServer) tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

// multiPlatform builds the platform with the multi-channel Helix fake
// and the multi-connection EventSub server.
func multiPlatform(t *testing.T, r *fakeReceiver, a *botAuth, channels []string) (*twitch.Platform, *eventSubServer) {
	t.Helper()
	ws := &eventSubServer{}
	hl := &multiHelixServer{
		helixServer: &helixServer{},
		users:       map[string]string{"other": "100"},
		mods:        a,
	}
	hlSrv := httptest.NewTestServer(t, hl.handler(t))
	t.Cleanup(hlSrv.Close)
	wsSrv := httptest.NewTestServer(t, ws.handler(t))
	t.Cleanup(wsSrv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: hlSrv.Client(), Rate: 100, Burst: 100})
	return twitch.NewPlatform(twitch.PlatformOptions{
		Receiver:   r,
		Auth:       a,
		Helix:      hc,
		DialClient: wsSrv.Client(),
		Base:       wsSrv.URL + "/",
		HelixBase:  hlSrv.URL,
		Poll:       10 * time.Millisecond,
		Channels:   channels,
	}), ws
}

// TestPlatformAdditionalChannel operates an additional channel with the
// bot token: the platform opens a second EventSub session for it (B4)
// and reports the bot as connected.
func TestPlatformAdditionalChannel(t *testing.T) {
	r := &fakeReceiver{}
	a := &botAuth{fakeAuth: &fakeAuth{token: "tok", ready: true}, botID: "99", botReady: true, botToken: "btok"}
	a.setMods("100")
	p, ws := multiPlatform(t, r, a, []string{"other"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()
	// Both sessions connect: the home with the streamer token, the
	// additional channel with the bot token.
	waitFor(t, 10*time.Second, func() int64 {
		if len(ws.tokens()) == 2 {
			return 1
		}
		return 0
	})
	require.True(t, p.Status().Streamer && p.Status().Bot)
	require.ElementsMatch(t, []string{"tok", "btok"}, ws.tokens())
	cancel()
	<-done
	assert.False(t, p.Status().Streamer)
	assert.False(t, p.Status().Bot)
}

// TestPlatformModMissing operates no additional channel while the bot is
// no moderator of it (B3): the platform runs the home channel only.
func TestPlatformModMissing(t *testing.T) {
	r := &fakeReceiver{}
	a := &botAuth{fakeAuth: &fakeAuth{token: "tok", ready: true}, botID: "99", botReady: true, botToken: "btok"}
	a.setMods() // no channel: the bot is a moderator nowhere
	p, ws := multiPlatform(t, r, a, []string{"other"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()
	// Only the home session connected, so exactly one token.
	waitFor(t, 10*time.Second, func() int64 {
		if len(ws.tokens()) == 1 {
			return 1
		}
		return 0
	})
	require.True(t, p.Status().Streamer)
	assert.False(t, p.Status().Bot)
	require.Equal(t, []string{"tok"}, ws.tokens())
	cancel()
	<-done
}

// TestPlatformSkipsTheHomeEntry leaves an entry of the home channel out
// of the additional channels (B3): it operates no additional session.
func TestPlatformSkipsTheHomeEntry(t *testing.T) {
	r := &fakeReceiver{}
	a := &botAuth{fakeAuth: &fakeAuth{token: "tok", ready: true}, botID: "99", botReady: true, botToken: "btok"}
	a.setMods("7")
	p, ws := multiPlatform(t, r, a, []string{"streamer"})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.NoError(t, p.Run(ctx))
	}()
	// Only the home session connected, so exactly one token.
	waitFor(t, 10*time.Second, func() int64 {
		if len(ws.tokens()) == 1 {
			return 1
		}
		return 0
	})
	require.True(t, p.Status().Streamer)
	assert.False(t, p.Status().Bot)
	require.Equal(t, []string{"tok"}, ws.tokens())
	cancel()
	<-done
}

// TestPlatformChannelInTheEvent hands a chat message to the receiver with
// the channel of the session it came from (B5), and flags the bot's own
// message (B13).
func TestPlatformChannelInTheEvent(t *testing.T) {
	r := &fakeReceiver{}
	m := twitch.NewMapper(twitch.MapperOptions{
		Receiver: r,
		Dedup:    dedup(t),
		Channel:  connector.Channel("other"),
		BotID:    "99",
	})
	handle(t, m, "channel.chat.message", `{"id":"m-1","message":"hi","user_id":"5","user_login":"alice","user_display_name":"Alice"}`)
	msgs := r.received()
	require.Len(t, msgs, 1)
	assert.Equal(t, connector.Channel("other"), msgs[0].Channel)
	assert.False(t, msgs[0].FromBot)
	// The bot's message is flagged FromBot (B13).
	handle(t, m, "channel.chat.message", `{"id":"m-2","message":"hi","user_id":"99","user_login":"bot","user_display_name":"Bot"}`)
	msgs = r.received()
	require.Len(t, msgs, 2)
	assert.Equal(t, connector.Channel("other"), msgs[1].Channel)
	assert.True(t, msgs[1].FromBot)
}

func dedup(t *testing.T) *connector.Dedup {
	t.Helper()
	d, err := connector.NewDedup(connector.DefaultDedupTTL)
	require.NoError(t, err)
	return d
}
