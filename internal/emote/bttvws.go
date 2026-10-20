// SPDX-License-Identifier: MIT

package emote

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/ripmav/streamcrew/internal/breaker"
)

// BTTVSocketBase is the official BetterTTV socket URL (plan
// docs/superpowers/plans/2026-10-20-bttv-integration.md, phase 2; the
// protocol was verified live on 2026-10-20).
const BTTVSocketBase = "wss://sockets.betterttv.net/ws"

// bttvSocketTick joins new channels and keeps the connection alive
// (the server sends no keepalive, so the socket pings on its own).
const bttvSocketTick = 30 * time.Second

// SocketChannel is one channel of the BetterTTV socket: Login is the key
// in the emote catalog, ID the Twitch channel ID of the join name
// "twitch:<id>".
type SocketChannel struct {
	Login string
	ID    string
}

// CatalogUpdater is the part of the emote catalog the BetterTTV socket
// needs: the BTTV list of a channel, read and updated.
type CatalogUpdater interface {
	SourceList(source, channel string) []Emote
	Update(source, channel string, emotes []Emote)
}

// SocketOptions configures NewBTTVSocket.
type SocketOptions struct {
	// Base is the socket URL; empty for the official one.
	Base string
	// Logger logs the state of the socket; nil discards the log.
	Logger *slog.Logger
	// Channels returns the channels to join; it is polled while the
	// connection is alive, and a new channel joins the running
	// connection.
	Channels func(ctx context.Context) []SocketChannel
	// Catalog is updated by the emote events.
	Catalog CatalogUpdater
	// Breaker is the circuit breaker of the socket endpoint
	// (Code-ADR-0007); the handshake runs through it, a dropped
	// session does not. If nil, the layer is skipped.
	Breaker *breaker.Breaker
}

// BTTVSocket is the BetterTTV WebSocket client (plan phase 2): it joins
// the operated channels, applies the emote events create, update and
// delete to the catalog, and reconnects with a backoff. The polling
// source with its TTL stays the safety net and the initial list; the
// socket only accelerates the channel emote changes.
type BTTVSocket struct {
	base     string
	log      *slog.Logger
	channels func(ctx context.Context) []SocketChannel
	catalog  CatalogUpdater
	brk      *breaker.Breaker
}

// NewBTTVSocket makes the BetterTTV socket.
func NewBTTVSocket(o SocketOptions) *BTTVSocket {
	if o.Base == "" {
		o.Base = BTTVSocketBase
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &BTTVSocket{base: o.Base, log: o.Logger, channels: o.Channels, catalog: o.Catalog, brk: o.Breaker}
}

// Run dials the socket, joins the channels, and applies the emote events
// until the context ends; a dropped connection reconnects with an
// exponential backoff.
func (s *BTTVSocket) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		err := s.session(ctx)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			s.log.WarnContext(ctx, "the bttv socket ended, it reconnects", "error", err, "backoff", backoff)
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
	return ctx.Err()
}

// frame is one read message of the socket.
type frame struct {
	typ     websocket.MessageType
	payload []byte
	err     error
}

// session is one connection: it joins the channels, pings the connection
// on the tick, and applies the emote events until the context ends or the
// connection drops.
func (s *BTTVSocket) session(ctx context.Context) error {
	opts := &websocket.DialOptions{
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext,
			},
		},
	}
	var conn *websocket.Conn
	dial := func(ctx context.Context) error {
		var err error
		conn, _, err = websocket.Dial(ctx, s.base, opts) //nolint:bodyclose // the body is consumed by Dial
		return err
	}
	if s.brk != nil {
		if err := s.brk.Execute(ctx, dial); err != nil {
			return err
		}
	} else if err := dial(ctx); err != nil {
		return err
	}
	sessCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.log.InfoContext(ctx, "the bttv socket connected")
	defer s.log.InfoContext(ctx, "the bttv socket disconnected")
	defer func() { _ = conn.CloseNow() }()

	byID := make(map[string]string) // join name -> login
	joined := make(map[string]bool)
	if err := s.joinNew(ctx, conn, byID, joined); err != nil {
		return err
	}
	frames := make(chan frame, 1)
	go s.read(sessCtx, conn, frames)
	tick := time.NewTicker(bttvSocketTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := s.joinNew(ctx, conn, byID, joined); err != nil {
				return err
			}
			if err := conn.Ping(ctx); err != nil {
				return err
			}
		case f := <-frames:
			if f.err != nil {
				return f.err
			}
			s.handle(ctx, f, byID)
		}
	}
}

// read pumps the messages of the connection into the channel.
func (s *BTTVSocket) read(ctx context.Context, conn *websocket.Conn, out chan<- frame) {
	for {
		typ, payload, err := conn.Read(ctx)
		select {
		case out <- frame{typ: typ, payload: payload, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// joinNew joins the channels not joined yet on the running connection.
func (s *BTTVSocket) joinNew(ctx context.Context, conn *websocket.Conn, byID map[string]string, joined map[string]bool) error {
	for _, ch := range s.channels(ctx) {
		if ch.ID == "" || ch.Login == "" {
			continue
		}
		name := "twitch:" + ch.ID
		if joined[name] {
			continue
		}
		if err := writeJSON(ctx, conn, "join_channel", map[string]string{"name": name}); err != nil {
			return err
		}
		joined[name] = true
		byID[name] = ch.Login
	}
	return nil
}

// writeJSON sends one socket message {"name": ..., "data": ...}.
func writeJSON(ctx context.Context, conn *websocket.Conn, name string, data any) error {
	payload, err := json.Marshal(struct {
		Name string `json:"name"`
		Data any    `json:"data"`
	}{Name: name, Data: data})
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, payload)
}

// handle applies one emote event of the socket to the catalog; the other
// events (lookup_user, presence, …) are ignored (plan phase 2).
func (s *BTTVSocket) handle(ctx context.Context, f frame, byID map[string]string) {
	if f.typ != websocket.MessageText {
		return
	}
	var m struct {
		Name string          `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(f.payload, &m); err != nil {
		s.log.DebugContext(ctx, "the bttv socket frame is unknown", "error", err)
		return
	}
	var ev struct {
		Channel string `json:"channel"`
		Emote   struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		} `json:"emote"`
		EmoteID string `json:"emoteId"`
	}
	if err := json.Unmarshal(m.Data, &ev); err != nil {
		s.log.DebugContext(ctx, "the bttv socket event is unknown", "name", m.Name, "error", err)
		return
	}
	switch m.Name {
	case "emote_create", "emote_update":
		s.apply(ctx, byID[ev.Channel], ev.Emote.ID, ev.Emote.Code, false)
	case "emote_delete":
		s.apply(ctx, byID[ev.Channel], ev.EmoteID, "", true)
	default:
		s.log.DebugContext(ctx, "the bttv socket event is ignored", "name", m.Name)
	}
}

// apply adds, changes, or removes the emote in the BTTV list of the
// channel in the catalog; an unknown channel (no join) is ignored.
func (s *BTTVSocket) apply(ctx context.Context, login, id, code string, del bool) {
	if login == "" || id == "" {
		return
	}
	list := s.catalog.SourceList("bttv", login)
	out := make([]Emote, 0, len(list)+1)
	found := false
	for _, e := range list {
		if del && e.ID == id {
			found = true
			continue
		}
		if !del && e.ID == id {
			found = true
			e.Code = code
		}
		out = append(out, e)
	}
	if !del && !found {
		out = append(out, Emote{ID: id, Code: code, Image: bttvImageBase(bttvImageTemplate, id)})
	}
	s.catalog.Update("bttv", login, out)
	s.log.InfoContext(ctx, "the bttv emote of the catalog was updated", "channel", login, "emote", id, "deleted", del)
}
