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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/connector"
	"github.com/ripmav/streamcrew/internal/domain/platform"
	"github.com/ripmav/streamcrew/internal/domain/user"
	"github.com/ripmav/streamcrew/internal/httpclient"
	"github.com/ripmav/streamcrew/internal/twitch"
)

// opsServer is a fake Helix for the platform operations (roadmap 4.4).
type opsServer struct {
	mu      sync.Mutex
	chat    []string // the sent chat texts, in order
	bans404 bool     // make DELETE /moderation/bans answer 404
	offline bool     // /streams and /users answer no data
	timeout int      // the timeout duration the last POST carried
}

func (s *opsServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/chat/messages" && r.Method == http.MethodPost:
			var in struct {
				BroadcasterID string `json:"broadcaster_id"`
				SenderID      string `json:"sender_id"`
				Message       string `json:"message"`
			}
			require.NoError(t, jsonv2.Unmarshal(body, &in))
			assert.Equal(t, "7", in.BroadcasterID)
			assert.Equal(t, "7", in.SenderID)
			s.mu.Lock()
			s.chat = append(s.chat, in.Message)
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"message":{"id":"m1","message":""}}`))
		case r.URL.Path == "/chat/messages" && r.Method == http.MethodDelete:
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			assert.Equal(t, "7", r.URL.Query().Get("moderator_id"))
			assert.Equal(t, "m1", r.URL.Query().Get("id"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/moderation/timeouts" && r.Method == http.MethodPost:
			var in struct {
				FromID   string `json:"from_id"`
				Duration int    `json:"duration"`
				Reason   string `json:"reason"`
			}
			require.NoError(t, jsonv2.Unmarshal(body, &in))
			assert.Equal(t, "3", in.FromID)
			assert.Equal(t, "r", in.Reason)
			s.mu.Lock()
			s.timeout = in.Duration
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"from_id":"3"}`))
		case r.URL.Path == "/moderation/bans" && r.Method == http.MethodPost:
			var in struct {
				FromID string `json:"from_id"`
				Reason string `json:"reason"`
			}
			require.NoError(t, jsonv2.Unmarshal(body, &in))
			assert.Equal(t, "3", in.FromID)
			assert.Equal(t, "b", in.Reason)
			_, _ = w.Write([]byte(`{"from_id":"3"}`))
		case r.URL.Path == "/moderation/bans" && r.Method == http.MethodDelete:
			if s.bans404 {
				http.NotFound(w, r)
				return
			}
			assert.Equal(t, "3", r.URL.Query().Get("from_id"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/moderation/timeouts" && r.Method == http.MethodDelete:
			assert.Equal(t, "3", r.URL.Query().Get("from_id"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/moderation/chat_delete":
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			assert.Equal(t, "7", r.URL.Query().Get("moderator_id"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/moderation/moderators":
			assert.Equal(t, "3", r.URL.Query().Get("from_id"))
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/users":
			s.mu.Lock()
			unknown := s.offline
			s.mu.Unlock()
			if unknown {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"3","login":"user","display_name":"User"}]}`))
		case r.URL.Path == "/channels":
			_, _ = w.Write([]byte(`{"data":[{"id":"7","title":"T","game_name":"G"}]}`))
		case r.URL.Path == "/streams":
			if s.offline {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"user_id":"7","title":"S","viewer_count":42,"created_at":"2026-10-10T10:00:00Z"}]}`))
		case r.URL.Path == "/eventsub/subscriptions":
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"data":[]}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"id":"s1"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

// opsPlatform builds a platform wired to the fake ops server.
func opsPlatform(t *testing.T, a *fakeAuth, s *opsServer) *twitch.Platform {
	t.Helper()
	srv := httptest.NewTestServer(t, s.handler(t))
	t.Cleanup(srv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: srv.Client(), Rate: 100, Burst: 100})
	return twitch.NewPlatform(twitch.PlatformOptions{
		Auth:      a,
		Helix:     hc,
		HelixBase: srv.URL,
	})
}

// readyAuth is a fakeAuth with a valid token.
func readyAuth() *fakeAuth {
	a := &fakeAuth{}
	a.setReady(true)
	a.token = "t"
	return a
}

// target returns the moderated or looked up user of the tests.
func target() user.Identity {
	return user.Identity{Platform: platform.Twitch, PlatformUserID: "3", Login: "user", DisplayName: "User"}
}

func TestChatSendSplitsLongText(t *testing.T) {
	s := &opsServer{}
	p := opsPlatform(t, readyAuth(), s)
	long := strings.Repeat("a ", 300) // 600 runes, a space every second rune
	require.NoError(t, p.Chat().Send(context.Background(), connector.Message{Text: long, From: connector.AccountStreamer}))
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.chat, 2)
	for _, part := range s.chat {
		assert.LessOrEqual(t, len([]rune(part)), 500)
	}
	// the parts rejoin to the original
	assert.Equal(t, long, s.chat[0]+" "+s.chat[1])
}

func TestChatSendSplitsHardWithoutSpaces(t *testing.T) {
	s := &opsServer{}
	p := opsPlatform(t, readyAuth(), s)
	long := strings.Repeat("a", 1200)
	require.NoError(t, p.Chat().Send(context.Background(), connector.Message{Text: long, From: connector.AccountStreamer}))
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Len(t, s.chat, 3)
	assert.Len(t, []rune(s.chat[0]), 500)
	assert.Len(t, []rune(s.chat[1]), 500)
	assert.Len(t, []rune(s.chat[2]), 200)
}

func TestChatSendBotIsRefused(t *testing.T) {
	s := &opsServer{}
	p := opsPlatform(t, readyAuth(), s)
	err := p.Chat().Send(context.Background(), connector.Message{Text: "x", From: connector.AccountBot})
	assert.ErrorIs(t, err, connector.ErrNotConnected)
	s.mu.Lock()
	defer s.mu.Unlock()
	assert.Empty(t, s.chat)
}

func TestChatSendWithoutToken(t *testing.T) {
	a := readyAuth()
	a.setReady(false)
	p := opsPlatform(t, a, &opsServer{})
	err := p.Chat().Send(context.Background(), connector.Message{Text: "x", From: connector.AccountStreamer})
	assert.ErrorIs(t, err, connector.ErrNotConnected)
}

func TestChatSendRefusedByPlatform(t *testing.T) {
	srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no scope", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	hc := httpclient.New(httpclient.Options{Name: "twitch.helix", Base: srv.Client(), Rate: 100, Burst: 100})
	p := twitch.NewPlatform(twitch.PlatformOptions{Auth: readyAuth(), Helix: hc, HelixBase: srv.URL})
	err := p.Chat().Send(context.Background(), connector.Message{Text: "x", From: connector.AccountStreamer})
	assert.ErrorIs(t, err, connector.ErrRefused)
}

func TestChatDelete(t *testing.T) {
	p := opsPlatform(t, readyAuth(), &opsServer{})
	assert.NoError(t, p.Chat().Delete(context.Background(), "m1"))
}

func TestModerationTimeoutClampsDuration(t *testing.T) {
	s := &opsServer{}
	p := opsPlatform(t, readyAuth(), s)
	require.NoError(t, p.Moderation().Timeout(context.Background(), target(), 30*time.Second, "r"))
	s.mu.Lock()
	assert.Equal(t, 60, s.timeout) // below the Twitch minimum
	s.mu.Unlock()

	require.NoError(t, p.Moderation().Timeout(context.Background(), target(), 48*time.Hour, "r"))
	s.mu.Lock()
	assert.Equal(t, 86400, s.timeout) // above the Twitch maximum
	s.mu.Unlock()
}

func TestModerationPurgeIsRefused(t *testing.T) {
	s := &opsServer{}
	p := opsPlatform(t, readyAuth(), s)
	err := p.Moderation().Purge(context.Background(), target())
	assert.ErrorIs(t, err, connector.ErrRefused)
}

func TestModerationClearChat(t *testing.T) {
	p := opsPlatform(t, readyAuth(), &opsServer{})
	assert.NoError(t, p.Moderation().ClearChat(context.Background()))
}

func TestModerationBanAndMods(t *testing.T) {
	p := opsPlatform(t, readyAuth(), &opsServer{})
	require.NoError(t, p.Moderation().Ban(context.Background(), target(), "b"))
	require.NoError(t, p.Moderation().Mod(context.Background(), target()))
	assert.NoError(t, p.Moderation().Unmod(context.Background(), target()))
}

func TestModerationUnbanFallsBackToUntimeout(t *testing.T) {
	// the ban is none (404), so the timeout is lifted instead
	p := opsPlatform(t, readyAuth(), &opsServer{bans404: true})
	assert.NoError(t, p.Moderation().Unban(context.Background(), target()))
}

func TestUsersLookup(t *testing.T) {
	p := opsPlatform(t, readyAuth(), &opsServer{})
	got, err := p.Users().UserByLogin(context.Background(), "user")
	require.NoError(t, err)
	assert.Equal(t, target(), got)
	got, err = p.Users().UserByID(context.Background(), "3")
	require.NoError(t, err)
	assert.Equal(t, target(), got)

	pOff := opsPlatform(t, readyAuth(), &opsServer{offline: true})
	_, err = pOff.Users().UserByLogin(context.Background(), "user")
	assert.ErrorIs(t, err, connector.ErrUnknownUser)
}

func TestChannelLiveAndOffline(t *testing.T) {
	p := opsPlatform(t, readyAuth(), &opsServer{})
	info, err := p.Channel(context.Background())
	require.NoError(t, err)
	assert.True(t, info.Live)
	assert.Equal(t, "S", info.Title) // the live title wins
	assert.Equal(t, "G", info.Game)
	require.NotNil(t, info.Viewers)
	assert.Equal(t, int64(42), *info.Viewers)
	assert.Equal(t, time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), info.StartedAt)

	pOff := opsPlatform(t, readyAuth(), &opsServer{offline: true})
	info, err = pOff.Channel(context.Background())
	require.NoError(t, err)
	assert.False(t, info.Live)
	assert.Equal(t, "T", info.Title)
	assert.Nil(t, info.Viewers)
}

func TestIdentities(t *testing.T) {
	// before the first poll the account is unknown
	p := opsPlatform(t, readyAuth(), &opsServer{})
	_, ok := p.Identity(connector.AccountStreamer)
	assert.False(t, ok)
	_, ok = p.Identity(connector.AccountBot)
	assert.False(t, ok)

	// a running platform learns the streamer from the account poll
	p = platformFor(t, &fakeReceiver{}, readyAuth(), &keepAliveServer{})
	ctx := t.Context()
	go func() { _ = p.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if id, ok := p.Identity(connector.AccountStreamer); ok {
			assert.Equal(t, "7", id.PlatformUserID)
			assert.Equal(t, "streamer", id.Login)
			assert.Equal(t, platform.Twitch, id.Platform)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the streamer identity never became known")
}
