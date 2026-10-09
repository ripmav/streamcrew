// SPDX-License-Identifier: MIT

// Package helix is the Twitch Helix API adapter (roadmap 4.2). Every
// endpoint is a typed method on Client: the request carries the Client-Id
// header and, if the client has a token provider, the bearer token. The
// injected httpclient (Code-ADR-0014) does the retries, the breaker
// twitch.helix and the rate limits; pagination follows the Helix
// after/first pattern through httpclient.EachPage.
package helix

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ripmav/streamcrew/internal/httpclient"
)

// defaultBaseURL is the base of the official Helix API.
const defaultBaseURL = "https://api.twitch.tv/helix"

// TokenFunc supplies the bearer token of an authorized call. The
// composition root injects it (Code-ADR-0002), typically
// auth.Service.Token for the account that acts. The seam stays open for
// 4.3 and 4.4, where several accounts (streamer, bot) call Helix.
type TokenFunc func(ctx context.Context) (string, error)

// Options configures New.
type Options struct {
	// ClientID is the client ID of the Twitch app for the Client-Id
	// header of every request.
	ClientID string
	// Token supplies the bearer token; if nil, only public endpoints are
	// called (no Authorization header).
	Token TokenFunc
	// BaseURL overrides the Helix base (tests use the test server).
	BaseURL string
}

// Client is a typed Helix API client on top of the httpclient
// (roadmap 4.2). A Client is safe for concurrent use.
type Client struct {
	http     *httpclient.Client
	clientID string
	token    TokenFunc
	base     string
}

// New creates a Client for the official Helix base unless
// Options.BaseURL names another (tests).
func New(hc *httpclient.Client, o Options) *Client {
	base := o.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	return &Client{
		http:     hc,
		clientID: o.ClientID,
		token:    o.Token,
		base:     strings.TrimRight(base, "/"),
	}
}

// request builds the raw request for an endpoint: body is encoded with
// json/v2 (Code-ADR-0018) and is replayable (httpclient requirement),
// query is appended, and the Client-Id header plus, if a token provider
// is set, the bearer token go on the request.
func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any) (*http.Request, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var req *http.Request
	var err error
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("helix: %s: %w", path, err)
		}
		req, err = http.NewRequestWithContext(ctx, method, u, bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("helix: %s: %w", path, err)
	}
	req.Header.Set("Client-Id", c.clientID)
	if c.token != nil {
		tok, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// doAnswer runs a request that has no body to decode (Helix answers with
// 204): the httpclient closes nothing, so the body is closed here.
func (c *Client) doAnswer(ctx context.Context, req *http.Request) error {
	resp, err := c.http.Do(ctx, req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
