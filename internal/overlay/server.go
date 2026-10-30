// SPDX-License-Identifier: MIT

package overlay

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ripmav/streamcrew/internal/capability"
	"github.com/ripmav/streamcrew/internal/config"
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

// TokenCheck validates the query parameter token against the API tokens
// of the profile (B5, ADR-0013 §5): ok is true only for a stored token
// that carries the overlay scope, so a strong token never needs to land
// in an OBS URL. The app wires it to the apitoken store.
type TokenCheck func(ctx context.Context, value string) bool

// Config configures a Server (B1, B5).
type Config struct {
	// Listen is the TCP address, e.g. "127.0.0.1:8742". Port 0 selects
	// a free port.
	Listen string
	// Mode is the operating mode of the core; without a token the
	// endpoints are only open in desktop and daemon mode with a
	// loopback listen address (B5).
	Mode config.Mode
	// TokenCheck validates the query parameter token, nil means no
	// token can open the endpoints.
	TokenCheck TokenCheck
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
	// local allows token-less access: desktop or daemon mode with a
	// loopback listen address (B5).
	local bool

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
		local: (cfg.Mode == config.ModeDesktop || cfg.Mode == config.ModeDaemon) &&
			loopbackListen(cfg.Listen),
	}
	s.handler = s.buildMux()
	return s, nil
}

// Service returns the overlay service of the server.
func (s *Server) Service() *Service { return s.cfg.Service }

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

// loopbackHost reports whether the host names a loopback address:
// localhost or a loopback IP such as 127.0.0.1 or [::1], with any port.
// Against the Host header, not the destination, it stops DNS-rebinding:
// a foreign site whose name resolves to 127.0.0.1 keeps its own Host
// header in the browser (B5, Review 341-7).
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// loopbackListen reports whether the listen address binds a loopback
// host; an unparseable address fails closed (B5).
func loopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return loopbackHost(host)
}

// authorized checks the access to the endpoints (B5): a token in the
// query parameter must be a stored token with the overlay scope,
// otherwise the endpoints answer 401, an invalid or revoked token as
// well. Without a token the access is only allowed in desktop and
// daemon mode with a loopback listen address and a Host header that
// names a loopback address. It returns the raw token value, so the
// server can re-check it against a revocation on every send of an open
// WebSocket.
func (s *Server) authorized(r *http.Request) (token string, ok bool) {
	token = r.URL.Query().Get("token")
	if token != "" {
		return token, s.cfg.TokenCheck != nil && s.cfg.TokenCheck(r.Context(), token)
	}
	return "", s.local && loopbackHost(r.Host)
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
// embedded HTML with the endpoint ID and name injected, escaped for
// the title and the inline script (Review 342-7). An unknown endpoint
// answers 404 (B2).
// scriptJSON returns the JSON of v with < escaped, so that it can be
// inlined in a <script> block without breaking out of it (Review
// 342-7); a string never fails to marshal.
func scriptJSON(v any) string {
	data, _ := json.Marshal(v)
	return strings.ReplaceAll(string(data), "<", `\u003c`)
}

func (s *Server) servePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, ok := s.cfg.Service.Endpoint(id)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if _, ok := s.authorized(r); !ok {
		s.refuse(r.Context(), w, r)
		return
	}
	// The ID and name come from the section and the URL; the title is
	// HTML-escaped and the config JSON-escaped, so the name cannot
	// break out of either (Review 342-7).
	config := fmt.Sprintf(`{"id":%s,"name":%s}`, scriptJSON(e.ID), scriptJSON(e.Name))
	page := strings.NewReplacer(
		"OVERLAY_TITLE", html.EscapeString(e.Name),
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
// protocol with the runtime (B3, B4, B7, B14). A connection with a
// token re-checks it on every send, so a revocation closes it on the
// next message (B5).
func (s *Server) serveWS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.cfg.Service.Endpoint(id); !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	token, ok := s.authorized(r)
	if !ok {
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
		// A revoked token is re-checked on every send (B5): the
		// connection closes and no further message is queued.
		if token != "" && s.cfg.TokenCheck != nil && !s.cfg.TokenCheck(ctx, token) {
			s.logger.WarnContext(ctx, "overlay: token revoked, closing the connection", "path", r.URL.Path)
			_ = conn.Close(websocket.StatusPolicyViolation, "token revoked")
			return
		}
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
	// The writer runs in its own goroutine; when the readLoop ends with
	// the connection, the derived context ends with it, so the writer
	// and its queue do not linger until the shutdown (Review 342-14).
	writeCtx, cancelWrite := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { s.writeLoop(writeCtx, conn, out) })
	s.readLoop(ctx, conn, id, slot)
	cancelWrite()
	wg.Wait()
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
		// The browser TTS finished (B41); the seq ends the speech of
		// the tts.speak that carries it.
		s.cfg.Service.TTSEnded(ctx, endpoint, int64Field(msg.Data, "seq"))
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

// int64Field returns the integer field name of the decoded data, 0 if
// the data is not an object with such a field. json/v2 decodes a number
// into any as a float64.
func int64Field(data any, name string) int64 {
	m, ok := data.(map[string]any)
	if !ok {
		return 0
	}
	f, ok := m[name].(float64)
	if !ok {
		return 0
	}
	return int64(f)
}
