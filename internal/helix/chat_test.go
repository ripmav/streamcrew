// SPDX-License-Identifier: MIT

package helix_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

//go:fix inline
func boolPtr(b bool) *bool { return new(b) }

func TestSendChatMessage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/chat/messages", r.URL.Path)
			_, _ = w.Write([]byte(`{"message":{"id":"m1","message":"hallo"}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		m, err := c.SendChatMessage(t.Context(), helix.SendChatMessageInput{
			BroadcasterID: "7",
			SenderID:      "9",
			Message:       "hallo",
		})
		require.NoError(t, err)
		assert.Equal(t, "m1", m.ID)
		assert.Equal(t, "hallo", m.Content)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.body, &body))
		assert.Equal(t, map[string]any{
			"broadcaster_id": "7",
			"sender_id":      "9",
			"message":        "hallo",
		}, body)
	})
}

func TestSendChatMessageWithoutTokenIsUnauthorized(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Bad Request","status":401,"message":"Missing or invalid access token"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "")
		_, err := c.SendChatMessage(t.Context(), helix.SendChatMessageInput{
			BroadcasterID: "7",
			SenderID:      "9",
			Message:       "hallo",
		})
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusUnauthorized, se.StatusCode)
	})
}

func TestDeleteChatMessage(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodDelete, r.Method)
			assert.Equal(t, "/chat/messages", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			assert.Equal(t, "9", r.URL.Query().Get("moderator_id"))
			assert.Equal(t, "m1", r.URL.Query().Get("id"))
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		require.NoError(t, c.DeleteChatMessage(t.Context(), "7", "9", "m1"))
	})
}

func TestGetChatSettingsNotFound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Not Found","status":404,"message":"Channel does not exist"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "tok")
		_, err := c.GetChatSettings(t.Context(), "404")
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.StatusCode)
	})
}

func TestUpdateChatSettings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, http.MethodPatch, r.Method)
			assert.Equal(t, "/chat/settings", r.URL.Path)
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"7","slow_mode":true,"slow_mode_wait_time":30,"followers_only":false,"followers_only_delay":0,"subscriber_only":false,"emote_mode":false,"unique_chatter":false,"unique_chatter_time_range":0,"unique_chatter_time_seconds":0}]}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		s, err := c.UpdateChatSettings(t.Context(), helix.UpdateChatSettingsInput{
			BroadcasterID:    "7",
			ModeratorID:      "9",
			SlowMode:         new(true),
			SlowModeWaitTime: 30,
		})
		require.NoError(t, err)
		require.NotNil(t, s)
		assert.True(t, s.SlowMode)
		assert.Equal(t, 30, s.SlowModeWaitTime)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.body, &body))
		assert.Equal(t, "7", body["broadcaster_id"])
		assert.Equal(t, "9", body["moderator_id"])
		assert.Equal(t, true, body["slow_mode"])
		assert.Equal(t, float64(30), body["slow_mode_wait_time"])
		_, hasFollowers := body["followers_only"]
		assert.False(t, hasFollowers, "a nil pointer is not sent")
	})
}

func TestSendAnnouncement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/chat/announcements", r.URL.Path)
			_, _ = w.Write([]byte(`{"message":"hallo alle","color":"blue"}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		a, err := c.SendAnnouncement(t.Context(), helix.AnnouncementInput{
			BroadcasterID: "7",
			ModeratorID:   "9",
			Message:       "hallo alle",
			Color:         "blue",
		})
		require.NoError(t, err)
		assert.Equal(t, "hallo alle", a.Message)
		assert.Equal(t, "blue", a.Color)
	})
}

func TestSendShoutout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/chat/shoutouts", r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		require.NoError(t, c.SendShoutout(t.Context(), helix.ShoutoutInput{
			BroadcasterID: "7",
			ModeratorID:   "9",
			FromID:        "7",
			ToID:          "8",
		}))
		rec.mu.Lock()
		defer rec.mu.Unlock()
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.body, &body))
		assert.Equal(t, map[string]any{
			"broadcaster_id": "7",
			"moderator_id":   "9",
			"from_id":        "7",
			"to_id":          "8",
		}, body)
	})
}

func TestBanAndUnban(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				assert.Equal(t, "/moderation/bans", r.URL.Path)
				_, _ = w.Write([]byte(`{"from_id":"8","from_login":"mallory","reason":"spam"}`))
			case http.MethodDelete:
				assert.Equal(t, "/moderation/bans", r.URL.Path)
				assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
				assert.Equal(t, "9", r.URL.Query().Get("moderator_id"))
				assert.Equal(t, "8", r.URL.Query().Get("from_id"))
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		r, err := c.Ban(t.Context(), helix.BanInput{
			BroadcasterID: "7",
			ModeratorID:   "9",
			FromID:        "8",
			Reason:        "spam",
		})
		require.NoError(t, err)
		assert.Equal(t, "mallory", r.FromLogin)
		assert.Equal(t, "spam", r.Reason)
		require.NoError(t, c.Unban(t.Context(), "7", "9", "8"))
	})
}

func TestTimeoutAndUntimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			switch r.Method {
			case http.MethodPost:
				assert.Equal(t, "/moderation/timeouts", r.URL.Path)
				_, _ = w.Write([]byte(`{"from_id":"8","from_login":"mallory","timeout":600}`))
			case http.MethodDelete:
				assert.Equal(t, "/moderation/timeouts", r.URL.Path)
				assert.Equal(t, "8", r.URL.Query().Get("from_id"))
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		r, err := c.Timeout(t.Context(), helix.TimeoutInput{
			BroadcasterID: "7",
			ModeratorID:   "9",
			FromID:        "8",
			Duration:      600,
		})
		require.NoError(t, err)
		assert.Equal(t, 600, r.Timeout)
		rec.mu.Lock()
		var body map[string]any
		_ = json.Unmarshal(rec.body, &body)
		rec.mu.Unlock()
		assert.Equal(t, float64(600), body["duration"])
		require.NoError(t, c.Untimeout(t.Context(), "7", "9", "8"))
	})
}

func TestGetModeratorsPaginates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls int
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/moderation/moderators", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			calls++
			switch calls {
			case 1:
				_, _ = w.Write([]byte(`{"data":[{"id":"9","login":"mod1","display_name":"Mod One"}],"pagination":{"cursor":"CUR"}}`))
			case 2:
				assert.Equal(t, "CUR", r.URL.Query().Get("after"))
				_, _ = w.Write([]byte(`{"data":[{"id":"10","login":"mod2","display_name":"Mod Two"}],"pagination":{"cursor":""}}`))
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		mods, err := c.GetModerators(t.Context(), "7")
		require.NoError(t, err)
		require.Len(t, mods, 2)
		assert.Equal(t, "mod1", mods[0].Login)
		assert.Equal(t, "Mod Two", mods[1].DisplayName)
	})
}

func TestVIPs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			switch r.Method {
			case http.MethodGet:
				assert.Equal(t, "/moderation/vips", r.URL.Path)
				assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
				_, _ = w.Write([]byte(`{"data":[{"id":"8","login":"fan","display_name":"Fan"}],"pagination":{}}`))
			case http.MethodPost:
				assert.Equal(t, "/moderation/vips", r.URL.Path)
				_, _ = w.Write([]byte(`{"id":"8","login":"fan","display_name":"Fan"}`))
			case http.MethodDelete:
				assert.Equal(t, "/moderation/vips", r.URL.Path)
				assert.Equal(t, "8", r.URL.Query().Get("from_id"))
				w.WriteHeader(http.StatusNoContent)
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		vips, err := c.GetVIPs(t.Context(), "7")
		require.NoError(t, err)
		require.Len(t, vips, 1)
		assert.Equal(t, "fan", vips[0].Login)
		vip, err := c.AddVIP(t.Context(), "7", "9", "8")
		require.NoError(t, err)
		assert.Equal(t, "8", vip.ID)
		rec.mu.Lock()
		var body map[string]any
		_ = json.Unmarshal(rec.body, &body)
		rec.mu.Unlock()
		assert.Equal(t, "8", body["from_id"])
		require.NoError(t, c.RemoveVIP(t.Context(), "7", "9", "8"))
	})
}
