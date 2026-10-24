// SPDX-License-Identifier: MIT

// Package twitch is the Twitch platform adapter (roadmap 4.3): the
// EventSub WebSocket client (Code-ADR-0015), the subscription manager
// on the Helix endpoints (roadmap 4.2) and the mapping of the received
// events onto the event model (spec events.md) through the
// connector.Receiver port, like the mock platform.
package twitch

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/coder/websocket"

	"github.com/ripmav/streamcrew/internal/breaker"
)

// defaultBase is the official EventSub WebSocket base URL.
const defaultBase = "wss://eventsub.wss.twitch.tv/ws"

// ErrToken wraps the error of a connection whose access token could not
// be fetched; Run ends without a backoff, the platform polls the account
// again.
var ErrToken = errors.New("eventsub: the access token is unavailable")

// dialTimeout bounds the handshake of a connection.
const dialTimeout = 10 * time.Second

// dropWait is how long Run waits after an unexpected drop before the
// next attempt (Code-ADR-0015, Revision 2026-10-21): a fixed 10 s, no
// lifetime budget.
const dropWait = 10 * time.Second

// reconnectWindow is how long the session stays open after a
// session_reconnect until the new session's welcome must have arrived:
// the 30 s the server keeps the old connection alive (EventSub
// Reference "Reconnect message").
const reconnectWindow = 30 * time.Second

// pongFactor is how many keepalive intervals without a frame the
// connection tolerates before it is closed.
const pongFactor = 2

// Handler receives what the connection delivers: the events and the
// revocation signals.
type Handler interface {
	// OnMessage takes a received event, in the order received.
	OnMessage(ctx context.Context, e Event)
	// OnRevoked is called when the server reports that the access
	// token is revoked (status authorization_revoked); the client
	// reconnects with a fresh token.
	OnRevoked(ctx context.Context)
	// OnSubscriptionRevoked is called when the server reports that a
	// single subscription was revoked (status user_removed or
	// version_removed); the session stays open, the caller replaces
	// the subscription.
	OnSubscriptionRevoked(ctx context.Context, sub RevokedSubscription)
}

// Options configures New.
type Options struct {
	// Base is the EventSub WebSocket base (tests use the test server).
	Base string
	// Token supplies the access token of the streamer account for the
	// connection; it is called for every connection, so a revoked
	// token is replaced by the refresh of the auth service. The
	// reconnect connection uses its URL as is and needs no token.
	Token TokenFunc
	// HTTPClient is the client for the handshake (tests use the test
	// server's client); if nil, a plain client is used.
	HTTPClient *http.Client
	// Logger logs the connection lifecycle (Code-ADR-0003); if nil,
	// the log is discarded.
	Logger *slog.Logger
	// Session is called with the ID of every established session
	// (after the session_welcome, also after every reconnect); nil
	// discards the ID. The subscription manager uses it for the
	// websocket transport of the subscriptions and re-points the
	// subscriptions right after every new session (the 10 s
	// subscription window, EventSub Reference "Welcome message").
	Session func(id string)
	// Breaker is the circuit breaker of the EventSub endpoint
	// (Code-ADR-0007); the handshake runs through it, a dropped
	// session does not. If nil, the layer is skipped.
	Breaker *breaker.Breaker
	// DropWait is the wait after an unexpected drop before the next
	// attempt (Code-ADR-0015, Revision 2026-10-21); if zero or
	// negative, dropWait (10 s) is used.
	DropWait time.Duration
}

// TokenFunc supplies an access token for the connection.
type TokenFunc func(ctx context.Context) (string, error)

// Client is the EventSub WebSocket client (Code-ADR-0015): it
// connects, reads the session_welcome, hands the notification events
// to the handler, stays alive by the server's session_keepalive
// frames (no frame for pongFactor intervals closes the connection),
// reconnects on session_reconnect at the given URL — the old session
// stays open until the new session's welcome, so no event is lost —
// and on unexpected drops after a fixed drop wait. The client sends
// no application messages: the mock server closes clients that do
// (close code 4001), and the server's keepalive frames already carry
// the liveness. Run drives one connection at a time; a Client is not
// shared between concurrent Runs.
type Client struct {
	base      string
	token     TokenFunc
	httpc     *http.Client
	log       *slog.Logger
	onSession func(string)
	brk       *breaker.Breaker
	dropWait  time.Duration
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
	drop := o.DropWait
	if drop <= 0 {
		drop = dropWait
	}
	return &Client{base: base, token: o.Token, httpc: httpc, log: log, onSession: o.Session, brk: o.Breaker, dropWait: drop}
}

// Run drives the connection until the context is canceled: connect,
// session_welcome, keepalive, events, and the reconnects. A
// session_reconnect is already running when the old session reports
// it (its welcome arrived while the old session was still open, so
// nothing was lost); Run adopts it. An unexpected drop waits
// dropWait (10 s, Code-ADR-0015, Revision 2026-10-21) at the base
// URL — an open breaker with a known retry-after waits the longer of
// the two, so it is probed at its own pace, not every dropWait.
func (c *Client) Run(ctx context.Context, h Handler) error {
	dialURL := c.base
	var pending func() (next, error)
	for ctx.Err() == nil {
		var (
			n   next
			err error
		)
		if pending != nil {
			n, err = pending()
			pending = nil
		} else {
			n, err = c.session(ctx, h, dialURL, false, nil)
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the canceled context ends Run; the drop error does not matter
		}
		if n.fatal {
			return err
		}
		if n.run != nil {
			// The session_reconnect already ran the new session
			// (the old one stayed open until its welcome); adopt it.
			pending = n.run
			continue
		}
		c.log.WarnContext(ctx, "the eventsub connection is down", "error", err)
		dialURL = n.url
		if !n.immediately {
			d := nextWait(c.dropWait, err, time.Now())
			c.log.DebugContext(ctx, "wait before the eventsub reconnect", "wait", d, "url", n.url)
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
	immediately bool
	// fatal ends Run without a backoff (a missing token).
	fatal bool
	// run carries the already-running session of a
	// session_reconnect: Run adopts it instead of dialing.
	run func() (next, error)
}

// nextWait is the wait before the reconnect after an unexpected drop:
// the fixed drop wait, except when the breaker rejected the attempt and
// knows its retry-after — then the longer of the two waits
// (Code-ADR-0015, Revision 2026-10-21).
func nextWait(drop time.Duration, err error, now time.Time) time.Duration {
	var u *breaker.UnavailableError
	if errors.As(err, &u) && !u.RetryAfter.IsZero() {
		if until := u.RetryAfter.Sub(now); until > drop {
			return until
		}
	}
	return drop
}

// redactDialError strips the access token from a dial error: the
// *url.Error of the websocket handshake carries the full dial URL with
// the token query, and the log redactor only masks by attribute key,
// so the token would otherwise land in the log (code review 2026-10-24).
func redactDialError(err error) error {
	ue, ok := errors.AsType[*url.Error](err)
	if !ok || ue.URL == "" {
		return err
	}
	u, perr := url.Parse(ue.URL)
	if perr != nil {
		return err
	}
	u.RawQuery = "redacted"
	u.Fragment = ""
	return &url.Error{Op: ue.Op, URL: u.String(), Err: ue.Err}
}

// sessionID returns a random UUID for the Twitch-Session-Id header the
// EventSub WebSocket handshake requires; without it Twitch answers 403.
func sessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // a failed read makes a shorter-looking ID, which Twitch would reject and the client would retry
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// session runs one connection at the given URL (the base for a fresh
// start, the reconnect URL for a session_reconnect) with the access
// token, unless asIs (the reconnect URL is used as is, the docs say
// "do not modify it"; the relative URLs are resolved against the
// base). It reports the outcome of the handshake to welcomed (nil for
// the welcome, the error otherwise) and returns where to reconnect
// next and the error that dropped the connection (nil when the
// context ended).
func (c *Client) session(ctx context.Context, h Handler, dialURL string, asIs bool, welcomed chan<- error) (next, error) {
	conn, interval, err := c.dialAndWelcome(ctx, dialURL, asIs)
	if err != nil {
		sendWelcome(welcomed, err)
		if errors.Is(err, ErrToken) {
			// The token is gone: a backoff would only repeat the
			// failure; the platform polls the account again.
			return next{fatal: true}, err
		}
		return next{url: c.base}, err
	}
	sendWelcome(welcomed, nil)
	defer conn.Close(websocket.StatusNormalClosure, "")
	return c.loop(ctx, h, conn, interval)
}

// sendWelcome hands the outcome of the handshake to the supervisor of
// a session_reconnect (the old session waits for the welcome of the
// new one); the send never blocks.
func sendWelcome(welcomed chan<- error, err error) {
	if welcomed == nil {
		return
	}
	select {
	case welcomed <- err:
	default:
	}
}

// dialAndWelcome opens the connection at the given URL and reads the
// session_welcome; asIs uses the URL as is (the reconnect URL), the
// fresh start merges the access token into the query.
func (c *Client) dialAndWelcome(ctx context.Context, dialURL string, asIs bool) (*websocket.Conn, time.Duration, error) {
	var u *url.URL
	var err error
	if asIs {
		// The reconnect URL is used as is (EventSub Reference
		// "Reconnect message": "Use the reconnect URL as is; do not
		// modify it"); a relative URL is made absolute against the
		// base, the query stays.
		u, err = c.resolveAsIs(dialURL)
	} else {
		tok, err := c.token(ctx)
		if err != nil {
			// The token is gone: a backoff would only repeat the
			// failure; the platform polls the account again.
			return nil, 0, fmt.Errorf("%w: %w", ErrToken, err)
		}
		// The URL may already carry its own query (the mock
		// server's reconnect identifier), so merge the token into it.
		u, err = url.Parse(dialURL)
		if err == nil {
			q := u.Query()
			q.Set("access_token", tok)
			// The documented keepalive parameter (values 10-600, default
			// 10): without it, Twitch's CDN WAF answers the handshake
			// with a CloudFront 403 block page before the token is even
			// checked, so every fresh start fails (log analysis
			// 2026-10-23, access_token-only dials 403).
			q.Set("keepalive_timeout_seconds", "10")
			u.RawQuery = q.Encode()
		}
	}
	if err != nil {
		return nil, 0, fmt.Errorf("eventsub: the url is invalid: %w", err)
	}

	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	// The response body is consumed by the library (the handshake).
	headers := http.Header{"Twitch-Session-Id": []string{sessionID()}}
	var conn *websocket.Conn
	dial := func(ctx context.Context) error {
		var err error
		conn, _, err = websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: c.httpc, HTTPHeader: headers}) //nolint:bodyclose // the body is consumed by Dial
		return err
	}
	// The log carries the dial target without the query: the access
	// token is in it and stays out of the log.
	c.log.DebugContext(ctx, "dial the eventsub connection", "host", u.Host, "path", u.Path)
	if c.brk != nil {
		err = c.brk.Execute(dctx, dial)
	} else {
		err = dial(dctx)
	}
	if err != nil {
		return nil, 0, redactDialError(err)
	}
	c.log.InfoContext(ctx, "the eventsub connection is up")

	op, payload, err := conn.Read(ctx)
	if err != nil {
		return nil, 0, err
	}
	if op != websocket.MessageText {
		return nil, 0, errors.New("eventsub: the first answer is not text")
	}
	var env envelope
	if err := jsonv2.Unmarshal(payload, &env); err != nil {
		return nil, 0, err
	}
	if env.Metadata.MessageType != "session_welcome" {
		return nil, 0, fmt.Errorf("eventsub: the first answer is %q, not session_welcome", env.Metadata.MessageType)
	}
	var w welcome
	if err := jsonv2.Unmarshal(env.Payload, &w); err != nil {
		return nil, 0, err
	}
	seconds := w.Session.KeepaliveTimeoutSeconds
	if seconds <= 0 {
		seconds = w.Session.KeepaliveIntervalSeconds
	}
	if seconds <= 0 {
		seconds = 10
	}
	interval := time.Duration(seconds) * time.Second
	c.log.InfoContext(ctx, "the eventsub session", "session", w.Session.ID, "keepalive", interval)
	if c.onSession != nil {
		c.onSession(w.Session.ID)
	}
	return conn, interval, nil
}

// resolveAsIs makes the reconnect URL usable: an absolute URL is used
// as is, a relative one is resolved against the base (the test server
// sends the relative one); the query is never touched.
func (c *Client) resolveAsIs(dialURL string) (*url.URL, error) {
	u, err := url.Parse(dialURL)
	if err != nil {
		return nil, err
	}
	if u.IsAbs() {
		return u, nil
	}
	return resolveReconnectURL(c.base, dialURL)
}

// loop reads the connection until it drops: it hands the events to
// the handler, reacts to session_reconnect and revocation, and closes
// the connection when no frame has come for pongFactor keepalive
// intervals.
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

	// The keepalive supervision: any frame resets the deadline; no
	// frame for pongFactor intervals closes the connection.
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
			// Any frame is proof of life: the server keeps the session
			// alive with the session_keepalive frames.
			deadline = time.Now().Add(pongFactor * interval)
			var env envelope
			if err := jsonv2.Unmarshal(i.data, &env); err != nil {
				c.log.WarnContext(ctx, "drop an unreadable eventsub frame", "error", err)
				continue
			}
			c.log.DebugContext(ctx, "the eventsub frame", "type", env.Metadata.MessageType)
			switch env.Metadata.MessageType {
			case "session_welcome", "session_keepalive":
				// the connection is alive, nothing to do
			case "notification":
				var e Event
				if err := jsonv2.Unmarshal(env.Payload, &e); err != nil {
					c.log.WarnContext(ctx, "drop an unreadable eventsub event", "error", err)
					continue
				}
				// The dedup key is the event object, not the whole
				// payload: the subscription shell (session ID, created
				// at) changes on a reconnect, the event object (and its
				// message ID) does not, so a redelivery is still the
				// repeat it is (B22).
				e.ID = string(e.Body)
				h.OnMessage(ctx, e)
			case "session_reconnect":
				var r reconnect
				if err := jsonv2.Unmarshal(env.Payload, &r); err != nil {
					return reconnectBase()
				}
				u, err := c.resolveAsIs(r.Session.ReconnectURL)
				if err != nil {
					return reconnectBase()
				}
				c.log.InfoContext(ctx, "the eventsub session reconnects", "session", r.Session.ID)
				// The docs (EventSub Reference "Reconnect message"):
				// the new session is dialed at once at the given URL
				// and the old session stays open until the new one's
				// welcome, so no event is lost. The old session
				// receives events until the new one has the welcome.
				// Without a welcome within the reconnect window the
				// old session serves the rest of it and is dropped
				// (the server closes it with code 4004 at the latest).
				welcomed := make(chan error, 1)
				out := make(chan result, 1)
				nctx, ncancel := context.WithCancel(ctx)
				// The context of the new session is canceled on every
				// path that does not adopt the new session (the adopter
				// owns it).
				adopted := false
				defer func() {
					if !adopted {
						ncancel()
					}
				}()
				go func() {
					n, e := c.session(nctx, h, u.String(), true, welcomed)
					out <- result{n, e}
				}()
				windowEnd := time.Now().Add(reconnectWindow)
				var welcomedErr error
				select {
				case <-ctx.Done():
					ncancel()
					return next{}, ctx.Err()
				case welcomedErr = <-welcomed:
				case <-time.After(reconnectWindow):
					welcomedErr = errors.New("eventsub: the reconnect window expired")
				}
				if welcomedErr != nil {
					ncancel()
					c.log.WarnContext(ctx, "the reconnect session did not welcome in time", "error", welcomedErr)
					select {
					case <-ctx.Done():
						return next{}, ctx.Err()
					case <-time.After(time.Until(windowEnd)):
					}
					return reconnectBase()
				}
				// The new session has the welcome: the old session may
				// close (the caller's deferred close), and Run adopts
				// the new one.
				adopted = true
				return next{run: func() (next, error) {
					res := <-out
					return res.n, res.err
				}}, nil
			case "revocation":
				var rv revocation
				if err := jsonv2.Unmarshal(env.Payload, &rv); err != nil {
					c.log.WarnContext(ctx, "drop an unreadable revocation", "error", err)
					continue
				}
				c.log.InfoContext(ctx, "an eventsub subscription is revoked", "type", rv.Subscription.Type, "status", rv.Subscription.Status)
				switch rv.Subscription.Status {
				case "authorization_revoked":
					// The token is gone: a fresh start (new token, no
					// session) at the base URL.
					h.OnRevoked(ctx)
					return next{url: c.base}, errors.New("eventsub: the token is revoked")
				case "user_removed", "version_removed":
					// One subscription is gone, the session stays (the
					// docs): the caller replaces the subscription.
					h.OnSubscriptionRevoked(ctx, rv.Subscription)
				default:
					c.log.WarnContext(ctx, "an unknown revocation status", "status", rv.Subscription.Status)
				}
			default:
				c.log.WarnContext(ctx, "drop an unknown eventsub frame type", "type", env.Metadata.MessageType)
			}
		case <-ticker.C:
			if time.Now().After(deadline) {
				c.log.WarnContext(ctx, "no keepalive frame, closing the connection")
				_ = conn.Close(websocket.StatusGoingAway, "no keepalive")
				return reconnectBase()
			}
		}
	}
}

// result is the outcome of the session the session_reconnect started
// in parallel.
type result struct {
	n   next
	err error
}

// resolveReconnectURL makes a relative reconnect URL absolute against
// the base URL (Twitch sends both shapes; the test server sends the
// relative one).
func resolveReconnectURL(base, rel string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	p, err := url.Parse(rel)
	if err != nil {
		return nil, err
	}
	if p.IsAbs() {
		return p, nil
	}
	b.Path = path.Join(b.Path, p.Path)
	b.RawQuery = p.RawQuery
	return b, nil
}
