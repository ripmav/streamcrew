// SPDX-License-Identifier: MIT

package helix_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/helix"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// captured holds the header and body data of one request, captured in
// the test server's handler.
type captured struct {
	mu       sync.Mutex
	clientID string
	auth     string
	body     []byte
}

func (c *captured) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.clientID = r.Header.Get("Client-Id")
	c.auth = r.Header.Get("Authorization")
	c.body = make([]byte, 0)
	if r.Body != nil {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		c.body = append(c.body, buf[:n]...)
	}
}

// newClient builds a helix.Client on the test server's in-memory
// network; token nil means no bearer header.
func newClient(t *testing.T, srv *httptest.Server, token string) *helix.Client {
	t.Helper()
	var tf helix.TokenFunc
	if token != "" {
		tf = func(context.Context) (string, error) { return token, nil }
	}
	hc := httpclient.New(httpclient.Options{
		Name: "twitch.helix",
		Base: srv.Client(),
	})
	return helix.New(hc, helix.Options{
		ClientID: "test-client-id",
		Token:    tf,
		BaseURL:  srv.URL,
	})
}

// newNoRetryClient is newClient with a single attempt: 429 and 5xx
// answers surface without a wait.
func newNoRetryClient(t *testing.T, srv *httptest.Server, token string) *helix.Client {
	t.Helper()
	var tf helix.TokenFunc
	if token != "" {
		tf = func(context.Context) (string, error) { return token, nil }
	}
	hc := httpclient.New(httpclient.Options{
		Name:  "twitch.helix",
		Base:  srv.Client(),
		Retry: httpclient.RetryPolicy{Attempts: 1},
	})
	return helix.New(hc, helix.Options{
		ClientID: "test-client-id",
		Token:    tf,
		BaseURL:  srv.URL,
	})
}

func TestGetUsersPaginatesAndSetsHeaders(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		var calls int
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, "/users", r.URL.Path)
			assert.Equal(t, []string{"1", "2"}, r.URL.Query()["id"])
			assert.Equal(t, []string{"alice"}, r.URL.Query()["login"])
			assert.Equal(t, "100", r.URL.Query().Get("first"))
			calls++
			switch calls {
			case 1:
				_, _ = w.Write([]byte(`{"data":[{"id":"1","login":"alice","display_name":"Alice","broadcaster_type":"affiliate","created_at":"2018-05-09T23:03:27Z","view_count":42}],"pagination":{"cursor":"CUR"}}`))
			case 2:
				assert.Equal(t, "CUR", r.URL.Query().Get("after"))
				_, _ = w.Write([]byte(`{"data":[{"id":"2","login":"bob","display_name":"Bob"}],"pagination":{"cursor":""}}`))
			default:
				t.Errorf("unexpected call %d", calls)
			}
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		users, err := c.GetUsers(t.Context(), []string{"1", "2"}, []string{"alice"})
		require.NoError(t, err)
		require.Len(t, users, 2)
		assert.Equal(t, "alice", users[0].Login)
		assert.Equal(t, "affiliate", users[0].BroadcasterType)
		assert.Equal(t, time.Date(2018, 5, 9, 23, 3, 27, 0, time.UTC), users[0].CreatedAt)
		assert.Equal(t, uint64(42), users[0].ViewCount)
		assert.Equal(t, "bob", users[1].Login)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		assert.Equal(t, "test-client-id", rec.clientID)
		assert.Equal(t, "Bearer tok", rec.auth)
	})
}

func TestGetUsersWithoutTokenOmitsAuthorization(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			_, _ = w.Write([]byte(`{"data":[{"id":"1","login":"alice","display_name":"Alice"}],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "")
		_, err := c.GetUsers(t.Context(), nil, []string{"alice"})
		require.NoError(t, err)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		assert.Equal(t, "", rec.auth)
		assert.Equal(t, "test-client-id", rec.clientID)
	})
}

func TestGetUsersRequiresAnIdentifier(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := newClient(t, httptest.NewTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), "tok")
		_, err := c.GetUsers(t.Context(), nil, nil)
		require.Error(t, err)
	})
}

func TestGetChannel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/channels", r.URL.Path)
			assert.Equal(t, "7", r.URL.Query().Get("broadcaster_id"))
			_, _ = w.Write([]byte(`{"data":[{"id":"7","broadcaster_id":"7","broadcaster_name":"alice","game_id":"509658","game_name":"Doom","language":"en","title":"doom","tags":["pve"]}]}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		ch, err := c.GetChannel(t.Context(), "7")
		require.NoError(t, err)
		require.NotNil(t, ch)
		assert.Equal(t, "doom", ch.Title)
		assert.Equal(t, "Doom", ch.GameName)
		assert.Equal(t, []string{"pve"}, ch.Tags)
	})
}

func TestGetChannelNotFound(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Not Found","status":404,"message":"Channel does not exist"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "tok")
		_, err := c.GetChannel(t.Context(), "404")
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusNotFound, se.StatusCode)
		assert.Contains(t, se.Snippet, "Channel does not exist")
	})
}

func TestUpdateChannelSendsBodyAndAccepts204(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var rec captured
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec.record(r)
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/channels", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		err := c.UpdateChannel(t.Context(), helix.UpdateChannelInput{
			BroadcasterID: "7",
			Title:         "doom, take 2",
		})
		require.NoError(t, err)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		assert.Equal(t, "Bearer tok", rec.auth)
		var body map[string]any
		require.NoError(t, json.Unmarshal(rec.body, &body))
		assert.Equal(t, map[string]any{
			"broadcaster_id": "7",
			"title":          "doom, take 2",
		}, body)
	})
}

func TestUpdateChannelUnauthorized(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Bad Request","status":401,"message":"Missing or invalid access token"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "tok")
		err := c.UpdateChannel(t.Context(), helix.UpdateChannelInput{BroadcasterID: "7", Title: "x"})
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, http.StatusUnauthorized, se.StatusCode)
	})
}

func TestGetStreams(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/streams", r.URL.Path)
			assert.Equal(t, []string{"7"}, r.URL.Query()["user_id"])
			assert.Equal(t, []string{"alice"}, r.URL.Query()["user_login"])
			_, _ = w.Write([]byte(`{"data":[{"id":"s1","user_id":"7","user_login":"alice","user_name":"Alice","game_name":"Doom","type":"live","title":"doom","viewer_count":1337,"created_at":"2026-10-09T18:00:00Z"}],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")
		streams, err := c.GetStreams(t.Context(), []string{"7"}, []string{"alice"})
		require.NoError(t, err)
		require.Len(t, streams, 1)
		assert.Equal(t, "doom", streams[0].Title)
		assert.Equal(t, uint64(1337), streams[0].ViewerCount)
		assert.Equal(t, time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC), streams[0].CreatedAt)
	})
}

func TestGetStreamsRateLimited(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		reset := time.Now().Add(5 * time.Second).Unix()
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("x-ratelimit-remaining", "0")
			w.Header().Set("x-ratelimit-reset", strconv.FormatInt(reset, 10))
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"Limit Exceeded","status":429,"message":"You exceeded the rate limit"}`))
		}))
		defer srv.Close()
		c := newNoRetryClient(t, srv, "tok")
		_, err := c.GetStreams(t.Context(), []string{"7"}, nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, httpclient.ErrTooManyRequests))
		var se *httpclient.StatusError
		require.ErrorAs(t, err, &se)
		assert.Equal(t, time.Unix(reset, 0), se.RateLimit.Reset)
	})
}
