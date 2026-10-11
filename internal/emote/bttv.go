// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// bttvBase is the official BetterTTV API base.
const bttvBase = "https://api.betterttv.net"

// bttvEmote is one emote of the BetterTTV answer (roadmap 4.4, task 9).
type bttvEmote struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// bttvAnswer is the BetterTTV answer of the emote endpoints.
type bttvAnswer struct {
	Emotes []bttvEmote `json:"emotes"`
}

// bttvSource is the Source of the BetterTTV emotes (roadmap 4.4, task 9).
type bttvSource struct {
	http *httpclient.Client
	base string
}

// NewBTTVSource makes the Source of the BetterTTV emotes over hc. base
// overrides the API base; empty for the official one.
func NewBTTVSource(hc *httpclient.Client, base string) Source {
	if base == "" {
		base = bttvBase
	}
	return bttvSource{http: hc, base: base}
}

// Name implements Source.
func (bttvSource) Name() string { return "bttv" }

// Emotes lists the global and the channel emotes (roadmap 4.4, task 9).
func (s bttvSource) Emotes(ctx context.Context, channel string) ([]Emote, error) {
	global, err := s.list(ctx, "/3/emotes")
	if err != nil {
		return nil, err
	}
	channelEmotes, err := s.list(ctx, "/3/channels/"+url.PathEscape(channel)+"/emotes")
	if err != nil {
		return nil, err
	}
	return append(global, channelEmotes...), nil
}

// list decodes one BetterTTV emote endpoint.
func (s bttvSource) list(ctx context.Context, path string) ([]Emote, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, fmt.Errorf("bttv: %s: %w", path, err)
	}
	var answer bttvAnswer
	if err := s.http.DoJSON(ctx, req, &answer); err != nil {
		return nil, err
	}
	out := make([]Emote, 0, len(answer.Emotes))
	for _, e := range answer.Emotes {
		out = append(out, Emote{ID: e.ID, Code: e.Code, Image: e.URL})
	}
	return out, nil
}
