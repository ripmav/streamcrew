// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// fakeSource is a controllable Source that counts its calls.
type fakeSource struct {
	name   string
	calls  int
	emotes []Emote
	err    error
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Emotes(context.Context, string) ([]Emote, error) {
	f.calls++
	return f.emotes, f.err
}

// TestCatalogResolve lists the channel's emotes and resolves an ID.
func TestCatalogResolve(t *testing.T) {
	tw := &fakeSource{name: "twitch", emotes: []Emote{{ID: "1", Code: "Kappa", Image: "img/1"}}}
	bt := &fakeSource{name: "bttv", emotes: []Emote{{ID: "2", Code: "B", Image: "img/2"}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := New([]Source{tw, bt}, time.Hour, nil)
	c.now = func() time.Time { return now }

	c.Refresh(t.Context(), "chan")

	e, ok := c.Resolve("chan", "1")
	require.True(t, ok)
	assert.Equal(t, "Kappa", e.Code)
	assert.Equal(t, "img/1", e.Image)
	_, ok = c.Resolve("chan", "nope")
	assert.False(t, ok)
	assert.Equal(t, []Emote{{ID: "1", Code: "Kappa", Image: "img/1"}, {ID: "2", Code: "B", Image: "img/2"}}, c.List("chan"))
	assert.Equal(t, 1, tw.calls)
	assert.Equal(t, 1, bt.calls)
}

// TestCatalogCacheHit does not refetch within the TTL.
func TestCatalogCacheHit(t *testing.T) {
	tw := &fakeSource{name: "twitch", emotes: []Emote{{ID: "1", Code: "Kappa"}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := New([]Source{tw}, time.Hour, nil)
	c.now = func() time.Time { return now }

	c.Refresh(t.Context(), "chan")
	now = now.Add(30 * time.Minute)
	c.Refresh(t.Context(), "chan")
	assert.Equal(t, 1, tw.calls, "a fresh list is not refetched")
}

// TestCatalogExpiry refetches once the list is older than the TTL.
func TestCatalogExpiry(t *testing.T) {
	tw := &fakeSource{name: "twitch", emotes: []Emote{{ID: "1", Code: "Kappa"}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := New([]Source{tw}, time.Hour, nil)
	c.now = func() time.Time { return now }

	c.Refresh(t.Context(), "chan")
	now = now.Add(time.Hour + time.Second)
	c.Refresh(t.Context(), "chan")
	assert.Equal(t, 2, tw.calls, "an expired list is refetched")
}

// TestCatalogFallback keeps the last good list when a source fails.
func TestCatalogFallback(t *testing.T) {
	tw := &fakeSource{name: "twitch", emotes: []Emote{{ID: "1", Code: "Kappa"}}}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := New([]Source{tw}, time.Hour, nil)
	c.now = func() time.Time { return now }

	c.Refresh(t.Context(), "chan")
	tw.err = errors.New("down")
	now = now.Add(time.Hour + time.Second)
	c.Refresh(t.Context(), "chan")

	e, ok := c.Resolve("chan", "1")
	require.True(t, ok, "the fallback keeps the last list")
	assert.Equal(t, "Kappa", e.Code)
	assert.Equal(t, 2, tw.calls)
}

// TestCatalogFallbackWithoutList leaves the catalog empty.
func TestCatalogFallbackWithoutList(t *testing.T) {
	tw := &fakeSource{name: "twitch", err: errors.New("down")}
	c := New([]Source{tw}, time.Hour, nil)
	c.Refresh(t.Context(), "chan")
	_, ok := c.Resolve("chan", "1")
	assert.False(t, ok)
}

// TestTwitchSource passes the fetch through.
func TestTwitchSource(t *testing.T) {
	s := NewTwitchSource(func(context.Context, string) ([]Emote, error) {
		return []Emote{{ID: "9", Code: "H", Image: "i"}}, nil
	})
	assert.Equal(t, "twitch", s.Name())
	emotes, err := s.Emotes(t.Context(), "chan")
	require.NoError(t, err)
	assert.Equal(t, []Emote{{ID: "9", Code: "H", Image: "i"}}, emotes)
}

// TestBTTVSource lists the global and the channel emotes over the v2 API.
func TestBTTVSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/emotes":
				_, _ = w.Write([]byte(`{"emotes":[{"id":"g1","name":"GG","imageUrl":"u/gg"}]}`))
			case "/api/v2/channels/42/emotes":
				_, _ = w.Write([]byte(`{"emotes":[{"id":"c1","name":"CC","imageUrl":"u/cc"}]}`))
			default:
				t.Errorf("unexpected path %s", r.URL.Path)
			}
		}))
		defer srv.Close()
		resolve := func(context.Context, string) (string, error) { return "42", nil }
		s := NewBTTVSource(httpclient.New(httpclient.Options{Name: "bttv", Base: srv.Client()}), srv.URL+"/api/v2", resolve)
		assert.Equal(t, "bttv", s.Name())
		emotes, err := s.Emotes(t.Context(), "chan")
		require.NoError(t, err)
		assert.ElementsMatch(t, []Emote{
			{ID: "g1", Code: "GG", Image: "u/gg"},
			{ID: "c1", Code: "CC", Image: "u/cc"},
		}, emotes)
	})
}

// TestFFZSource lists the room emotes (global and channel) in one call.
func TestFFZSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/room/chan/frankerfacez", r.URL.Path)
			_, _ = w.Write([]byte(`{"emotes":{"global":[{"name":"GG","id":"g1","url":"u/gg"}],"chan":[{"name":"CC","id":"c1","url":"u/cc"}]}}`))
		}))
		defer srv.Close()
		s := NewFFZSource(httpclient.New(httpclient.Options{Name: "ffz", Base: srv.Client()}), srv.URL)
		assert.Equal(t, "ffz", s.Name())
		emotes, err := s.Emotes(t.Context(), "chan")
		require.NoError(t, err)
		assert.ElementsMatch(t, []Emote{
			{ID: "g1", Code: "GG", Image: "u/gg"},
			{ID: "c1", Code: "CC", Image: "u/cc"},
		}, emotes)
	})
}
