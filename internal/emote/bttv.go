// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// bttvBase is the public BetterTTV emote API base (plan
// docs/superpowers/plans/2026-10-20-bttv-integration.md; the API moved to
// api.betterttv.net with /2 routes, the api.bttv.com domain is parked).
const bttvBase = "https://api.betterttv.net"

// bttvImageTemplate is the fallback image URL when the answer carries no
// urlTemplate: //cdn.betterttv.net/emote/{id}/{1x|2x|3x}.
const bttvImageTemplate = "https://cdn.betterttv.net/emote/{{id}}/{{image}}"

// bttvEmote is one emote of the BetterTTV answer (roadmap 4.4, task 9);
// the imageType (png/gif) is not tracked (plan, decision E1).
type bttvEmote struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// bttvAnswer is the BetterTTV answer of the emote endpoints.
type bttvAnswer struct {
	URLTemplate string      `json:"urlTemplate"`
	Emotes      []bttvEmote `json:"emotes"`
}

// bttvSource is the Source of the BetterTTV emotes (roadmap 4.4, task 9):
// the global emotes plus the emotes of the channel, answered by the public
// emote API without a token; a channel without BTTV data answers 404 and
// is an empty list like for FrankerFaceZ.
type bttvSource struct {
	http *httpclient.Client
	base string
}

// NewBTTVSource makes the Source of the BetterTTV emotes (roadmap 4.4,
// task 9).
func NewBTTVSource(http *httpclient.Client, base string) Source {
	if base == "" {
		base = bttvBase
	}
	return bttvSource{http: http, base: base}
}

func (s bttvSource) Name() string { return "bttv" }

// Emotes lists the global BetterTTV emotes and the emotes of the channel;
// the channel is addressed by its login name (the API parameter).
func (s bttvSource) Emotes(ctx context.Context, channel string) ([]Emote, error) {
	global, template, err := s.list(ctx, "/2/emotes")
	if err != nil {
		return nil, err
	}
	chanEmotes, channelTemplate, err := s.list(ctx, "/2/channels/"+url.PathEscape(channel))
	if err != nil {
		return nil, err
	}
	if template == "" {
		template = channelTemplate
	}
	out := make([]Emote, 0, len(global)+len(chanEmotes))
	for _, e := range append(global, chanEmotes...) {
		out = append(out, Emote{ID: e.ID, Code: e.Code, Image: bttvImageBase(template, e.ID)})
	}
	return out, nil
}

// list decodes one BetterTTV emote endpoint; a 404 answer is an empty
// list (a channel without BTTV data is the normal case, not an error).
func (s bttvSource) list(ctx context.Context, path string) ([]bttvEmote, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, "", err
	}
	var answer bttvAnswer
	if err := s.http.DoJSON(ctx, req, &answer); err != nil {
		var se *httpclient.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusNotFound {
			return nil, "", nil
		}
		return nil, "", err
	}
	return answer.Emotes, answer.URLTemplate, nil
}

// bttvImageBase builds the base URL of the emote image without the size
// suffix from the urlTemplate of the answer (or the fallback template),
// as Emote.Image expects it.
func bttvImageBase(template, id string) string {
	if template == "" {
		template = bttvImageTemplate
	}
	u := strings.ReplaceAll(template, "{{id}}", id)
	u = strings.ReplaceAll(u, "{{image}}", "1x")
	u = strings.TrimSuffix(u, "1x")
	u = strings.TrimSuffix(u, "/")
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	return u
}
