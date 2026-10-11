// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// ffzBase is the official FrankerFaceZ API base.
const ffzBase = "https://api.frankerfacez.com"

// ffzEmote is one emote of the FrankerFaceZ answer (roadmap 4.4, task 9).
type ffzEmote struct {
	Name string `json:"name"`
	ID   string `json:"id"`
	URL  string `json:"url"`
}

// ffzRoom is the FrankerFaceZ room answer: the emotes grouped by scope,
// "global" and the channel login.
type ffzRoom struct {
	Emotes map[string][]ffzEmote `json:"emotes"`
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

// Emotes lists the global and the channel emotes in one room call
// (roadmap 4.4, task 9).
func (s ffzSource) Emotes(ctx context.Context, channel string) ([]Emote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api/v1/room/"+url.PathEscape(channel)+"/frankerfacez", nil)
	if err != nil {
		return nil, fmt.Errorf("ffz: room %q: %w", channel, err)
	}
	var room ffzRoom
	if err := s.http.DoJSON(ctx, req, &room); err != nil {
		return nil, err
	}
	var out []Emote
	for _, emotes := range room.Emotes {
		for _, e := range emotes {
			out = append(out, Emote{ID: e.ID, Code: e.Name, Image: e.URL})
		}
	}
	return out, nil
}
