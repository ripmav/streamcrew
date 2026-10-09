// SPDX-License-Identifier: MIT

package helix_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFollowersPaginates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls int
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/channels/followers", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			assert.Empty(t, r.URL.Query().Get("user_id"))
			calls++
			switch calls {
			case 1:
				_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"7","broadcaster_login":"alice","broadcaster_name":"Alice","follower_id":"8","follower_login":"fan","follower_name":"Fan","followed_at":"2026-10-01T10:00:00Z"}],"pagination":{"cursor":"CUR"}}`))
			case 2:
				assert.Equal(t, "CUR", r.URL.Query().Get("after"))
				_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"7","broadcaster_login":"alice","broadcaster_name":"Alice","follower_id":"9","follower_login":"fan2","follower_name":"Fan Two","followed_at":"2026-10-02T10:00:00Z"}],"pagination":{"cursor":""}}`))
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		followers, err := c.GetFollowers(t.Context(), "7", "")
		require.NoError(t, err)
		require.Len(t, followers, 2)
		assert.Equal(t, "fan", followers[0].FollowerLogin)
		assert.Equal(t, time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC), followers[0].FollowedAt)
		assert.Equal(t, "Fan Two", followers[1].FollowerName)
	})
}

func TestGetFollowersFilterByFollower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "8", r.URL.Query().Get("user_id"))
			_, _ = w.Write([]byte(`{"data":[],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		followers, err := c.GetFollowers(t.Context(), "7", "8")
		require.NoError(t, err)
		assert.Empty(t, followers)
	})
}

func TestGetFollows(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/users/follows", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("from_id"))
			assert.Empty(t, r.URL.Query().Get("to_id"))
			_, _ = w.Write([]byte(`{"data":[{"from_id":"7","from_login":"alice","from_name":"Alice","to_id":"509658","to_login":"doom","to_name":"Doom","followed_at":"2026-09-30T08:00:00Z"}],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		follows, err := c.GetFollows(t.Context(), "7", "")
		require.NoError(t, err)
		require.Len(t, follows, 1)
		assert.Equal(t, "doom", follows[0].ToLogin)
	})
}

func TestGetChannelSubscriptions(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/subscriptions", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			_, _ = w.Write([]byte(`{"data":[{"broadcaster_id":"7","broadcaster_name":"Alice","foreign_id":"f1","foreign_type":"prime","subscriber_id":"8","subscriber_name":"Fan","tier":"1000","gifter_id":"9","gifter_name":"Giftor"}],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		subs, err := c.GetChannelSubscriptions(t.Context(), "7")
		require.NoError(t, err)
		require.Len(t, subs, 1)
		assert.Equal(t, "prime", subs[0].ForeignType)
		assert.Equal(t, "1000", subs[0].Tier)
		assert.Equal(t, "Giftor", subs[0].GifterName)
	})
}

func TestGetGames(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/games", r.URL.Path)
			assert.Equal(t, []string{"509658"}, r.URL.Query()["id"])
			assert.Equal(t, "de", r.URL.Query().Get("language"))
			assert.Empty(t, r.URL.Query().Get("name"))
			_, _ = w.Write([]byte(`{"data":[{"id":"509658","name":"Doom","box_art_url":"https://example.com/box.png","image_url":"https://example.com/img.png","language":"en","ofrt_ids":["ofrt-1"],"tags":["action"]}]}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		games, err := c.GetGames(t.Context(), []string{"509658"}, "de", "")
		require.NoError(t, err)
		require.Len(t, games, 1)
		assert.Equal(t, "Doom", games[0].Name)
		assert.Equal(t, []string{"action"}, games[0].Tags)
	})
}

func TestSearchGames(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/games/search", r.URL.Path)
			assert.Equal(t, "doom", r.URL.Query().Get("query"))
			_, _ = w.Write([]byte(`{"data":[{"id":"509658","name":"Doom"},{"id":"516575","name":"Doom Eternal"}]}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		games, err := c.SearchGames(t.Context(), "doom")
		require.NoError(t, err)
		require.Len(t, games, 2)
		assert.Equal(t, "Doom Eternal", games[1].Name)
	})
}
