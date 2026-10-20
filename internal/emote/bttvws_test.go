// SPDX-License-Identifier: MIT

package emote

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ripmav/streamcrew/internal/breaker"
	"github.com/ripmav/streamcrew/internal/httpclient"
)

// wsTestServer accepts socket connections and hands the connection to
// the test over the conn channel; it records the client's text frames in
// incoming.
type wsTestServer struct {
	conn     chan *websocket.Conn
	incoming chan string
}

func newWSTestServer(t *testing.T, dropFirst bool) (*wsTestServer, *httptest.Server) {
	t.Helper()
	s := &wsTestServer{conn: make(chan *websocket.Conn, 4), incoming: make(chan string, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		if dropFirst {
			_ = c.CloseNow()
			return
		}
		s.conn <- c
		s.serve(r.Context(), c)
	}))
	t.Cleanup(srv.Close)
	return s, srv
}

// serve blocks the handler (and keeps the connection open) until the
// connection ends; it records the text frames.
func (s *wsTestServer) serve(ctx context.Context, c *websocket.Conn) {
	for {
		typ, payload, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText {
			s.incoming <- string(payload)
		}
	}
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
}

func wsWriteText(t *testing.T, c *websocket.Conn, s string) {
	t.Helper()
	require.NoError(t, c.Write(t.Context(), websocket.MessageText, []byte(s)))
}

// TestBTTVSocketEmoteEvents joins the channel and applies the emote
// events create, update and delete to the catalog; events of unknown
// channels are ignored.
func TestBTTVSocketEmoteEvents(t *testing.T) {
	t.Parallel()
	s, srv := newWSTestServer(t, false)
	catalog := New(nil, time.Millisecond, nil)
	sock := NewBTTVSocket(SocketOptions{
		Base:    wsURL(srv),
		Catalog: catalog,
		Channels: func(context.Context) []SocketChannel {
			return []SocketChannel{{Login: "chan", ID: "123"}}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = sock.Run(ctx) }()

	require.NoError(t, waitJoin(t, s, `"twitch:123"`))
	var conn *websocket.Conn
	select {
	case conn = <-s.conn:
	case <-time.After(2 * time.Second):
		t.Fatal("no server connection")
	}

	// Create adds the emote to the catalog.
	wsWriteText(t, conn, `{"name":"emote_create","data":{"channel":"twitch:123","emote":{"id":"e1","code":"New","imageType":"png"}}}`)
	require.Eventually(t, func() bool {
		l := catalog.SourceList("bttv", "chan")
		return len(l) == 1 && l[0].ID == "e1" && l[0].Code == "New" && l[0].Image == "https://cdn.betterttv.net/emote/e1"
	}, 2*time.Second, 10*time.Millisecond)

	// Update changes the code of the emote.
	wsWriteText(t, conn, `{"name":"emote_update","data":{"channel":"twitch:123","emote":{"id":"e1","code":"Renamed"}}}`)
	require.Eventually(t, func() bool {
		l := catalog.SourceList("bttv", "chan")
		return len(l) == 1 && l[0].Code == "Renamed"
	}, 2*time.Second, 10*time.Millisecond)

	// Delete removes the emote.
	wsWriteText(t, conn, `{"name":"emote_delete","data":{"channel":"twitch:123","emoteId":"e1"}}`)
	require.Eventually(t, func() bool {
		return len(catalog.SourceList("bttv", "chan")) == 0
	}, 2*time.Second, 10*time.Millisecond)

	// Events of unknown channels (no join) are ignored.
	wsWriteText(t, conn, `{"name":"emote_create","data":{"channel":"twitch:999","emote":{"id":"x","code":"X"}}}`)
	time.Sleep(50 * time.Millisecond)
	assert.Empty(t, catalog.SourceList("bttv", "chan"))
}

// waitJoin waits until the join frame with the wanted name was received.
func waitJoin(t *testing.T, s *wsTestServer, want string) error {
	t.Helper()
	for {
		select {
		case frame := <-s.incoming:
			if strings.Contains(frame, want) {
				return nil
			}
		case <-time.After(2 * time.Second):
			return assert.AnError
		}
	}
}

// TestBTTVSocketReconnect reconnects a dropped connection.
func TestBTTVSocketReconnect(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		conns int
	)
	dropped := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		require.NoError(t, err)
		mu.Lock()
		conns++
		first := conns == 1
		mu.Unlock()
		if first {
			close(dropped)
			_ = c.CloseNow()
			return
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	sock := NewBTTVSocket(SocketOptions{
		Base:    wsURL(srv),
		Catalog: New(nil, time.Millisecond, nil),
		Channels: func(context.Context) []SocketChannel {
			return []SocketChannel{{Login: "chan", ID: "123"}}
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = sock.Run(ctx) }()
	<-dropped
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return conns >= 2
	}, 10*time.Second, 50*time.Millisecond)
}

// TestBTTVSocketBreakerOpens covers that the handshake failures run
// through the circuit breaker and open it (Code-ADR-0007).
func TestBTTVSocketBreakerOpens(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	log := slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &buf}, nil))
	sock := NewBTTVSocket(SocketOptions{
		Base:    "ws://" + addr,
		Catalog: New(nil, time.Millisecond, nil),
		Channels: func(context.Context) []SocketChannel {
			return []SocketChannel{{Login: "chan", ID: "123"}}
		},
		Logger:  log,
		Breaker: breaker.New("test.bttv.socket", log, httpclient.Evaluator),
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = sock.Run(ctx) }()
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(buf.String(), "circuit breaker open")
	}, 20*time.Second, 50*time.Millisecond)
}

// lockedWriter serializes the writes of the log handler and the reads of
// the tests on the same buffer.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
