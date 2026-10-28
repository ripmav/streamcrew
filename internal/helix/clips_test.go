// SPDX-License-Identifier: MIT

package helix_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClipsDecodeTheRealAPIShape covers ListClips and LatestClip: the
// Helix API sends the duration as a JSON number (e.g. 29.9), not a
// string.
func TestClipsDecodeTheRealAPIShape(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var calls int
		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/clips", r.URL.Path)
			assert.Equal(t, "123", r.URL.Query().Get("broadcaster_id"))
			calls++
			if calls == 2 {
				assert.Equal(t, "1", r.URL.Query().Get("first"))
			}
			// A real Helix sample answer.
			_, _ = w.Write([]byte(`{"data":[{"id":"dQw4w9WgXc","broadcaster_id":"123","broadcaster_name":"Channel","creator_id":"456","creator_name":"Clipper","game_name":"Game","title":"A Clip","url":"https://clips.twitch.tv/Foo-Bar_9","view_count":42,"created_at":"2024-01-01T00:00:00Z","duration":29.9}],"pagination":{}}`))
		}))
		defer srv.Close()
		c := newClient(t, srv, "tok")

		clips, err := c.ListClips(t.Context(), "123")
		require.NoError(t, err)
		require.Len(t, clips, 1)
		assert.Equal(t, 29.9, clips[0].Duration)
		assert.Equal(t, "https://clips.twitch.tv/Foo-Bar_9", clips[0].URL)

		latest, err := c.LatestClip(t.Context(), "123")
		require.NoError(t, err)
		require.NotNil(t, latest)
		assert.Equal(t, 29.9, latest.Duration)
		assert.Equal(t, "https://clips.twitch.tv/Foo-Bar_9", latest.URL)
	})
}
