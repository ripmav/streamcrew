// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// ffzBase is the official FrankerFaceZ API base.
const ffzBase = "https://api.frankerfacez.com"

// ffzEmote is one emote of the FrankerFaceZ v1 room answer (roadmap 4.4,
// task 9); the image URL is per size, the base is the 1x URL without its
// suffix.
type ffzEmote struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	URLs map[string]string `json:"urls"`
}

// ffzSet is one emote set of the room answer.
type ffzSet struct {
	Emoticons []ffzEmote `json:"emoticons"`
}

// ffzRoom is the FrankerFaceZ v1 room answer: the room data and its emote
// sets.
type ffzRoom struct {
	Sets map[string]ffzSet `json:"sets"`
}

// ffzSource is the Source of the FrankerFaceZ emotes (roadmap 4.4, task 9).
type ffzSource struct {
	http *httpclient.Client
	base string
}

// NewFFZSource makes the Source of the FrankerFaceZ emotes over hc. base
// overrides the API base; empty for the official one.
func NewFFZSource(hc *httpclient.Client, base string) Source {
	if base == "" {
		base = ffzBase
	}
	return ffzSource{http: hc, base: base}
}

// Name implements Source.
func (ffzSource) Name() string { return "ffz" }

// Emotes lists the emote sets of the channel room in one call
// (roadmap 4.4, task 9). A 404 is a channel without a room, an empty
// list.
// shortcut: the global FFZ emotes are not listed; the room endpoint
// returns only the room's sets. Add a global call when the endpoint is
// verified, if the global emotes matter.
func (s ffzSource) Emotes(ctx context.Context, channel string) ([]Emote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/v1/room/"+url.PathEscape(channel), nil)
	if err != nil {
		return nil, fmt.Errorf("ffz: room %q: %w", channel, err)
	}
	var room ffzRoom
	if err := s.http.DoJSON(ctx, req, &room); err != nil {
		var se *httpclient.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	var out []Emote
	for _, set := range room.Sets {
		for _, e := range set.Emoticons {
			out = append(out, Emote{ID: e.ID, Code: e.Name, Image: imageBase(e.URLs)})
		}
	}
	return out, nil
}

// imageBase is the emote image URL without its size suffix (the 1x URL).
func imageBase(urls map[string]string) string {
	return strings.TrimSuffix(urls["1"], "/1")
}
