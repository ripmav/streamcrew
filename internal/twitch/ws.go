// SPDX-License-Identifier: MIT

// Package twitch is the Twitch platform adapter (roadmap 4.3): the
// EventSub WebSocket client (Code-ADR-0015), the subscription manager
// on the Helix endpoints (roadmap 4.2) and the mapping of the received
// events onto the event model (spec events.md) through the
// connector.Receiver port, like the mock platform.
package twitch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/coder/websocket"
	"github.com/sethvargo/go-retry"
)

// defaultBase is the official EventSub WebSocket base URL.
const defaultBase = "wss://eventsub.wss.twitch.tv/ws"

// dialTimeout bounds the handshake of a connection.
const dialTimeout = 10 * time.Second

// pongFactor is how many keepalive intervals without a PING the
// connection tolerates before it is closed.
const pongFactor = 2

// Handler receives what the connection delivers: the events and the
// revocation signal.
type Handler interface {
	// OnMessage takes a received event, in the order received.
	OnMessage(ctx context.Context, e Event)
	// OnRevoked is called when the server reports that the access
	// token is revoked; the client reconnects with a fresh token.
	OnRevoked(ctx context.Context)
}

// Options configures New.
type Options struct {
	// Base is the EventSub WebSocket base (tests use the test server).
	Base string
	// Token supplies the access token of the streamer account for the
	// connection; it is called for every connection, so a revoked
	// token is replaced by the refresh of the auth service.
	Token TokenFunc
	// HTTPClient is the client for the handshake (tests use the test
	// server's client); if nil, a plain client is used.
	HTTPClient *http.Client
	// Logger logs the connection lifecycle (Code-ADR-0003); if nil,
	// the log is discarded.
	Logger *slog.Logger
}

// TokenFunc supplies an access token for the connection.
type TokenFunc func(ctx context.Context) (string, error)

// Client is the EventSub WebSocket client (Code-ADR-0015): it
// connects, reads the hello, answers the PING keepalive texts with
// PONG, hands the events to the handler, reconnects on
// session_reconnect with the session ID (no event loss, the repeats
// are dropped by the B22 dedup) and on unexpected drops with a
// backoff. Run drives one connection at a time; a Client is not
// shared between concurrent Runs.
type Client struct {
	base  string
	token TokenFunc
	httpc *http.Client
	log   *slog.Logger
}

// New creates a Client.
func New(o Options) *Client {
	base := o.Base
	if base == "" {
		base = defaultBase
	}
	httpc := o.HTTPClient
	if httpc == nil {
		httpc = &http.Client{}
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Client{base: base, token: o.Token, httpc: httpc, log: log}
}

// Run drives the connection until the context is canceled: connect,
// hello, keepalive, events, and the reconnects. A session_reconnect
// reconnects at once at the given URL with the session ID; an
// unexpected drop reconnects at the base URL after an exponential
// backoff (base 1 s, full jitter, 30 s budget, the httpclient values
// of Code-ADR-0014 point 4).
func (c *Client) Run(ctx context.Context, h Handler) error {
	backoff := retry.WithMaxDuration(30*time.Second,
		retry.WithFullJitter(retry.NewExponential(time.Second)))
	dialURL, sessionID := c.base, ""
	for ctx.Err() == nil {
		next, err := c.session(ctx, h, dialURL, sessionID)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the canceled context ends Run; the drop error does not matter
		}
		c.log.WarnContext(ctx, "the eventsub connection is down", "error", err)
		dialURL, sessionID = next.url, next.sessionID
		if !next.immediately {
			d, _ := backoff.Next()
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(d):
			}
		}
	}
	return nil
}

// next tells Run where and how quickly to reconnect.
type next struct {
	url         string
	sessionID   string
	immediately bool
}

// session runs one connection at the given URL (base with the access
// token for a fresh start, the reconnect URL with the session ID for
// a session_reconnect). It returns where to reconnect next and the
// error that dropped the connection (nil when the context ended).
func (c *Client) session(ctx context.Context, h Handler, dialURL, sessionID string) (next, error) {
	q := url.Values{}
	if sessionID != "" {
		q.Set("eventsub-session-id", sessionID)
	}
	tok, err := c.token(ctx)
	if err != nil {
		return next{}, err
	}
	q.Set("access_token", tok)
	u := dialURL + "?" + q.Encode()

	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	// The response body is consumed by the library (the handshake).
	conn, _, err := websocket.Dial(dctx, u, &websocket.DialOptions{HTTPClient: c.httpc}) //nolint:bodyclose // the body is consumed by Dial
	if err != nil {
		return next{}, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	c.log.InfoContext(ctx, "the eventsub connection is up")

	op, payload, err := conn.Read(ctx)
	if err != nil {
		return next{}, err
	}
	if op != websocket.MessageText {
		return next{}, errors.New("eventsub: the first answer is not text")
	}
	var env envelope
	if err := jsonv2.Unmarshal(payload, &env); err != nil {
		return next{}, err
	}
	if env.Type != "hello" {
		return next{}, fmt.Errorf("eventsub: the first answer is %q, not hello", env.Type)
	}
	var hello hello
	if err := jsonv2.Unmarshal(env.Payload, &hello); err != nil {
		return next{}, err
	}
	interval := time.Duration(hello.KeepaliveIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	c.log.InfoContext(ctx, "the eventsub session", "session", hello.SessionID, "keepalive", interval)

	return c.loop(ctx, h, conn, interval)
}

// loop reads the connection until it drops: it answers the PING
// keepalive texts, hands the events to the handler, reacts to
// session_reconnect and revocation, and closes the connection when no
// PING has come for pongFactor intervals.
func (c *Client) loop(ctx context.Context, h Handler, conn *websocket.Conn, interval time.Duration) (next, error) {
	type frameIn struct {
		op      websocket.MessageType
		data    []byte
		dropErr error
	}
	in := make(chan frameIn, 1)
	go func() {
		for {
			op, data, err := conn.Read(ctx)
			if err != nil {
				in <- frameIn{dropErr: err}
				return
			}
			in <- frameIn{op: op, data: data}
		}
	}()

	// The keepalive supervision: a PING resets the deadline; no PING
	// for pongFactor intervals closes the connection.
	deadline := time.Now().Add(pongFactor * interval)
	ticker := time.NewTicker(interval / 2)
	defer ticker.Stop()

	reconnectBase := func() (next, error) {
		return next{url: c.base}, errors.New("eventsub: connection dropped")
	}
	for {
		select {
		case <-ctx.Done():
			return next{}, ctx.Err()
		case i := <-in:
			if i.dropErr != nil {
				if ctx.Err() != nil {
					return next{}, ctx.Err()
				}
				return reconnectBase()
			}
			if i.op != websocket.MessageText {
				continue
			}
			var env envelope
			if err := jsonv2.Unmarshal(i.data, &env); err != nil {
				c.log.WarnContext(ctx, "drop an unreadable eventsub frame", "error", err)
				continue
			}
			switch env.Type {
			case "PING":
				deadline = time.Now().Add(pongFactor * interval)
				if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"PONG"}`)); err != nil {
					return reconnectBase()
				}
			case "PONG":
				// our answer, nothing to do
			case "message":
				var e Event
				if err := jsonv2.Unmarshal(env.Payload, &e); err != nil {
					c.log.WarnContext(ctx, "drop an unreadable eventsub event", "error", err)
					continue
				}
				h.OnMessage(ctx, e)
			case "session_reconnect":
				var r sessionReconnect
				if err := jsonv2.Unmarshal(env.Payload, &r); err != nil {
					return reconnectBase()
				}
				c.log.InfoContext(ctx, "the eventsub session reconnects", "reason", r.Reason)
				return next{url: resolveReconnectURL(c.base, r.ReconnectURL), sessionID: r.SessionID, immediately: true},
					fmt.Errorf("eventsub: session reconnect (%s)", r.Reason)
			case "revocation":
				h.OnRevoked(ctx)
				// The token is gone: a fresh start (new token, no
				// session) at the base URL.
				return next{url: c.base}, errors.New("eventsub: the token is revoked")
			default:
				c.log.WarnContext(ctx, "drop an unknown eventsub frame type", "type", env.Type)
			}
		case <-ticker.C:
			if time.Now().After(deadline) {
				c.log.WarnContext(ctx, "no keepalive ping, closing the connection")
				_ = conn.Close(websocket.StatusGoingAway, "no keepalive")
				return reconnectBase()
			}
		}
	}
}

// resolveReconnectURL makes a relative reconnect URL absolute against
// the base URL (Twitch sends both shapes; the test server sends the
// relative one).
func resolveReconnectURL(base, rel string) string {
	b, err := url.Parse(base)
	if err != nil {
		return rel
	}
	p, err := url.Parse(rel)
	if err != nil || p.IsAbs() {
		return rel
	}
	b.Path = path.Join(b.Path, p.Path)
	b.RawQuery = p.RawQuery
	return b.String()
}
