// SPDX-License-Identifier: MIT

package helix

import (
	"context"
	"net/url"
)

// Game is a category (Helix GET /games, POST /games/search).
type Game struct {
	// ID is the category ID.
	ID string `json:"id"`
	// Name is the category name.
	Name string `json:"name"`
	// BoxArtURL is the box art picture.
	BoxArtURL string `json:"box_art_url,omitempty"`
	// ImageURL is the smaller picture.
	ImageURL string `json:"image_url,omitempty"`
	// Language is the broadcast language (BCP 47).
	Language string `json:"language,omitempty"`
	// OfrtIDs are the OFR (Official Recommendation) IDs.
	OfrtIDs []string `json:"ofrt_ids,omitempty"`
	// Tags are the category tags.
	Tags []string `json:"tags,omitempty"`
}

// GetGames looks up categories by their IDs and/or name and language
// (Helix GET /games), walking all pages (max 25 categories per
// request).
func (c *Client) GetGames(ctx context.Context, ids []string, language, name string) ([]Game, error) {
	q := url.Values{}
	for _, id := range ids {
		q.Add("id", id)
	}
	if language != "" {
		q.Set("language", language)
	}
	if name != "" {
		q.Set("name", name)
	}
	req, err := c.request(ctx, "GET", "/games", q, nil)
	if err != nil {
		return nil, err
	}
	var games []Game
	err = c.http.EachPage[Game](ctx, req, 0, func(items []Game, _ string) error {
		games = append(games, items...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return games, nil
}

// SearchGames searches categories by name (Helix POST /games/search,
// up to 25 results, no pagination).
func (c *Client) SearchGames(ctx context.Context, query string) ([]Game, error) {
	req, err := c.request(ctx, "POST", "/games/search", url.Values{"query": {query}}, nil)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data []Game `json:"data"`
	}
	if err := c.http.DoJSON(ctx, req, &envelope); err != nil {
		return nil, err
	}
	return envelope.Data, nil
}
