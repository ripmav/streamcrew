// SPDX-License-Identifier: MIT

package overlay

import (
	"context"
	"crypto/subtle"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/overlay/web"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

// Server timeouts, like the API server (internal/httpserver): no read or
// write timeout for whole requests, because the runtime keeps the
// WebSocket open as long as the browser source is alive.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 10 * time.Second
)

// Config configures a Server (B1, B5).
type Config struct {
	// Listen is the TCP address, e.g. "127.0.0.1:8742". Port 0 selects
	// a free port.
	Listen string
	// Token protects the endpoints: when set, the page, the WebSocket
	// and the media URLs require the value as the query parameter
	// token, otherwise they answer 401. Empty leaves the endpoints
	// open (B5).
	Token string
	// Version is the version of the core, reported by hello (B4).
	Version string
	// Service is the overlay state the server presents; required.
	Service *Service
	// MediaDir is the media directory of the profile, served under
	// /media/media/ (B6).
	MediaDir string
	// Capabilities reports the rights the core has now; without
	// host:fs the media route serves nothing (B6).
	Capabilities capability.Source
	// Root returns the directory of a released root name; nil serves
	// only the media directory.
	Root func(name string) (dir string, ok bool)
	// Logger logs the server; nil discards the output.
	Logger *slog.Logger
}

// Server is the overlay HTTP server. It is a supervisor.Runnable and is
// not critical: a failure does not stop the core, the API stays
// reachable (ADR-0019 §1, B1).
type Server struct {
	cfg     Config
	logger  *slog.Logger
	handler http.Handler

	addrMu    sync.Mutex
	addr      net.Addr
	listening chan struct{}
}

// NewServer returns a server; a nil Service is an error.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Service == nil {
		return nil, errors.New("overlay: no service")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		cfg:       cfg,
		logger:    cfg.Logger,
		listening: make(chan struct{}),
	}
	s.handler = s.buildMux()
	return s, nil
}

// Listening returns a channel that is closed once the server listens.
func (s *Server) Listening() <-chan struct{} { return s.listening }

// Addr returns the address the server listens on, or ok false before it
// listens.
func (s *Server) Addr() (net.Addr, bool) {
	s.addrMu.Lock()
	defer s.addrMu.Unlock()
	return s.addr, s.addr != nil
}

// Run implements supervisor.Runnable. It serves until ctx ends and then
// shuts down gracefully. A listen error is permanent, like the API
// server: retrying the same address rarely helps.
//
//nolint:contextcheck // the request context is decoupled from ctx on purpose, like the API server
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Listen)
	if err != nil {
		return supervisor.Permanent(fmt.Errorf("overlay: listen on %s: %w", s.cfg.Listen, err))
	}
	s.addrMu.Lock()
	if s.addr == nil {
		close(s.listening)
	}
	s.addr = ln.Addr()
	s.addrMu.Unlock()

	// The request context ends with this serving, so the open
	// WebSockets close with the shutdown; it does not inherit from the
	// run context on purpose.
	requestCtx, cancel := context.WithCancel(context.Background())
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()
	s.logger.InfoContext(ctx, "overlay server listening", "addr", ln.Addr().String())

	<-ctx.Done()
	cancel()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		s.logger.ErrorContext(ctx, "close overlay server", "error", srv.Close())
		s.logger.ErrorContext(ctx, "close overlay listener", "error", ln.Close())
		return fmt.Errorf("shut down overlay server: %w", err)
	}
	if serveErr := <-serveErr; !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, context.Canceled) {
		return fmt.Errorf("serve overlay: %w", serveErr)
	}
	s.logger.InfoContext(ctx, "overlay server stopped")
	return nil
}

func (s *Server) buildMux() *http.ServeMux {
	mux := http.NewServeMux()
	// The endpoints (B2, B3): the page and the WebSocket of the named
	// endpoint.
	mux.HandleFunc("GET /overlay/{id}", s.servePage)
	mux.HandleFunc("GET /overlay/{id}/ws", s.serveWS)
	// The media (B6): /media/<root>/<path...>.
	mux.HandleFunc("GET /media/{root}/{path...}", s.serveMedia)
	// Without the capability the media route serves nothing (B6): the
	// handler answers 404 for every path.
	return mux
}

// authorized checks the optional token (B5): without a set token the
// endpoints are open; with one, the query parameter token must match.
func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Token == "" {
		return true
	}
	token := r.URL.Query().Get("token")
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) == 1
}

// refuse answers 401 and logs a warning with the address, without the
// token value (B48).
func (s *Server) refuse(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	s.logger.WarnContext(ctx, "overlay: unauthorized request", "addr", r.RemoteAddr, "path", r.URL.Path)
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// indexHTML is the document the page route serves: the embedded bundle
// with the endpoint injected as window.__OVERLAY__ (B3, ADR-0019 §2).
// The bundle is inlined, so the browser source needs exactly one
// request.
const indexHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>OVERLAY_TITLE</title>
</head>
<body>
<script>window.__OVERLAY__ = OVERLAY_CONFIG;</script>
<script>
OVERLAY_BUNDLE
</script>
</body>
</html>
`

// servePage serves the runtime of the named endpoint (B2, B3): the
// embedded HTML with the endpoint ID and name injected. An unknown
// endpoint answers 404 (B2).
func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := s.cfg.Service.Endpoint(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.authorized(r) {
		s.refuse(r.Context(), w, r)
		return
	}
	config := fmt.Sprintf(`{"id":%q,"name":%q}`, e.ID, e.Name)
	page := strings.NewReplacer(
		"OVERLAY_TITLE", e.Name,
		"OVERLAY_CONFIG", config,
		"OVERLAY_BUNDLE", web.Bundle,
	).Replace(indexHTML)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The runtime changes with the core; never keep it in a cache (B3).
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

// connOut is the send queue of one runtime; the writer goroutine drains
// it. A full queue drops the message: the runtime re-syncs its state on
// the next reconnect (B4, B7).
const connOutQueue = 32

// serveWS upgrades the WebSocket of the named endpoint and runs the
// protocol with the runtime (B3, B4, B7, B14).
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.cfg.Service.Endpoint(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.authorized(r) {
		s.refuse(r.Context(), w, r)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.DebugContext(r.Context(), "overlay: websocket accept failed", "error", err)
		return
	}
	ctx := r.Context()
	out := make(chan []byte, connOutQueue)
	send := func(msg Message) {
		data := encode(msg)
		select {
		case out <- data:
		default:
			s.logger.DebugContext(ctx, "overlay: send queue full, message dropped", "type", msg.Type)
		}
	}
	slot, err := s.cfg.Service.Attach(ctx, id, send)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, err.Error())
		return
	}
	go s.writeLoop(ctx, conn, out)
	s.readLoop(ctx, conn, id, slot)
}

// writeLoop drains the send queue onto the connection until the context
// ends or the connection fails.
func (s *Server) writeLoop(ctx context.Context, conn *websocket.Conn, out <-chan []byte) {
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
			return
		case data := <-out:
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

// readLoop reads the messages of the runtime until the connection ends,
// then detaches the slot (B49). Closing the connection is idempotent, so
// the writer and the reader may both close it.
func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn, endpoint string, slot ClientSlot) {
	defer conn.Close(websocket.StatusNormalClosure, "")
	defer s.cfg.Service.Detach(ctx, endpoint, slot.ID)
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		s.handleClientMessage(ctx, endpoint, slot, data)
	}
}

// handleClientMessage dispatches one message of the runtime (B4, B7,
// B14). Unknown types and broken JSON are ignored: the runtime resends
// what matters on its next reconnect.
func (s *Server) handleClientMessage(ctx context.Context, endpoint string, slot ClientSlot, data []byte) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		s.logger.DebugContext(ctx, "overlay: unreadable message", "slot", slot.ID, "error", err)
		return
	}
	switch msg.Type {
	case TypeReady:
		// The runtime introduced itself (B7); the server repeats the
		// full state, so a message lost during the connection is
		// caught (B4).
		if err := s.cfg.Service.SyncState(ctx, endpoint, slot.Send); err != nil {
			s.logger.DebugContext(ctx, "overlay: state sync failed", "slot", slot.ID, "error", err)
		}
	case TypeItemEnded:
		if id := stringField(msg.Data, "id"); id != "" {
			s.cfg.Service.ItemEnded(ctx, endpoint, id)
		}
	case TypeTTSEnded:
		// The browser TTS finished (B41); 7.3 waits for it, 7.1
		// ignores it.
	default:
		s.logger.DebugContext(ctx, "overlay: unknown message", "slot", slot.ID, "type", msg.Type)
	}
}

// stringField returns the string field name of the decoded data, empty
// if the data is not an object with a string field (json/v2 decodes an
// object into a map[string]any).
func stringField(data any, name string) string {
	m, ok := data.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[name].(string)
	return s
}
