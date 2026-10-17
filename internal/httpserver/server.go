// SPDX-License-Identifier: MIT

// Package httpserver runs the HTTP server of the core. It serves the health
// endpoints /healthz and /readyz, pprof in developer mode and, from roadmap
// phase 6, the API. From roadmap 6.6 the listen address, the TLS
// certificate, the developer mode, the CORS allowlist and the shutdown
// timeout can change at runtime (Rebind, SetDev, SetCorsAllow).
package httpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ripmav/streamcrew/internal/supervisor"
)

// Timeouts of the server. There is no read or write timeout for whole
// requests, because the API will stream live data over long-lived requests.
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
)

// Config configures a Server.
type Config struct {
	// Addr is the listen address, e.g. "127.0.0.1:8740". Port 0 selects a
	// free port. The "unix:" prefix, e.g. "unix:/path/core.sock", listens
	// on a Unix socket (the desktop default).
	Addr string
	// Dev serves pprof under /debug/pprof/. Only for loopback addresses.
	Dev bool
	// CorsAllow lists the origins allowed to call the server cross-origin
	// (CORS); an empty list refuses every cross-origin request.
	CorsAllow []string
	// TLSCert and TLSKey are the PEM files of the server certificate and
	// key; both set switches the server to TLS, none stays plain HTTP.
	TLSCert string
	TLSKey  string
	// ShutdownTimeout limits the graceful shutdown of open connections;
	// connections still open afterwards are closed. It must stay below the
	// supervisor's shutdown timeout, because net/http waits up to 5 s for
	// connections on which a client has not sent a request yet (e.g. browser
	// preconnects).
	ShutdownTimeout time.Duration
}

// serving is one active listener with its http.Server.
type serving struct {
	ln     net.Listener
	srv    *http.Server
	err    chan error
	cancel context.CancelFunc // ends the request context of this server
}

// Server is the HTTP server. It is a supervisor.Runnable.
type Server struct {
	logger  *slog.Logger
	handler http.Handler
	ready   func() bool
	// timeout supplies the shutdown timeout at shutdown time, so it can
	// change at runtime (roadmap 6.6).
	timeout func() time.Duration

	corsAllow atomic.Value // holds []string

	mu        sync.Mutex
	cfg       Config
	addr      net.Addr
	listening chan struct{}
	cur       *serving
	dev       atomic.Bool

	apiMu      sync.RWMutex
	apiHandler http.Handler
}

// New returns a server. ready reports whether the core is ready to serve
// requests (/readyz); a nil ready always reports ready. A nil logger
// discards the log output.
func New(cfg Config, logger *slog.Logger, ready func() bool) *Server {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if ready == nil {
		ready = func() bool { return true }
	}
	s := &Server{
		logger:    logger,
		ready:     ready,
		cfg:       cfg,
		listening: make(chan struct{}),
	}
	s.corsAllow.Store(cfg.CorsAllow)
	s.dev.Store(cfg.Dev)
	s.handler = CrossOriginProtection(s.corsAllowed, s.buildMux(ready))
	return s
}

// SetShutdownTimeoutFunc names the function that supplies the shutdown
// timeout at shutdown time (roadmap 6.6).
func (s *Server) SetShutdownTimeoutFunc(f func() time.Duration) {
	s.timeout = f
}

// SetDev switches the pprof routes under /debug/pprof/ (roadmap 6.6);
// without developer mode they answer 404.
func (s *Server) SetDev(dev bool) { s.dev.Store(dev) }

// SetCorsAllow replaces the CORS allowlist (roadmap 6.6); an empty list
// refuses every cross-origin request.
func (s *Server) SetCorsAllow(allow []string) { s.corsAllow.Store(allow) }

// Handler returns the handler with all routes of the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Listening returns a channel that is closed once the server listens.
func (s *Server) Listening() <-chan struct{} {
	return s.listening
}

// Addr returns the address the server listens on, or nil before it listens.
// Addr returns the address the server listens on. It blocks until the first
// listener is up, so a caller that saw the server run (e.g. the core being
// ready) never sees a nil address.
// Addr reports the address the server listens on. ok is false while the
// first listener is not up; callers that need the address (and would hang
// on a failed listen) check it, e.g. the core after readiness.
func (s *Server) Addr() (addr net.Addr, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr, s.addr != nil
}

// Rebind moves the server to cfg (roadmap 6.6): it opens a new listener,
// serves on it and shuts the old one down gracefully. On any error before
// the new listener serves, the old binding stays active.
func (s *Server) Rebind(ctx context.Context, cfg Config) error {
	ln, err := s.listen(ctx, cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	if cfg.TLSCert != "" {
		if _, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey); err != nil {
			if cerr := ln.Close(); cerr != nil {
				s.logger.ErrorContext(ctx, "close listener", "error", cerr)
			}
			return supervisor.Permanent(fmt.Errorf("load tls key pair: %w", err))
		}
	}
	// The request context ends with this serving (shutdown or rebind), so
	// open streams close with it; it does not inherit from the run or
	// caller context on purpose.
	requestCtx, cancel := context.WithCancel(context.Background())
	//
	//nolint:contextcheck // request context decoupled from the caller on purpose
	srv, errc := s.newServe(requestCtx)
	go s.serve(errc, srv, cfg.TLSCert != "", cfg.TLSCert, cfg.TLSKey, ln)

	s.mu.Lock()
	old := s.cur
	s.cur = &serving{ln: ln, srv: srv, err: errc, cancel: cancel}
	s.cfg = cfg
	s.mu.Unlock()
	s.setAddr(ln.Addr())

	if old != nil {
		// Give the old server the shutdown timeout to finish its
		// connections; the new one already serves. Its request context
		// ends with the old server, so open streams close. A failure
		// here does not undo the rebind: the new server already serves.
		if err := s.shutdownServing(ctx, old); err != nil {
			s.logger.ErrorContext(ctx, "old server did not stop", "error", err)
		}
	}
	s.logger.InfoContext(ctx, "http server rebound", "addr", ln.Addr().String(), "tls", cfg.TLSCert != "")
	return nil
}

// Run implements supervisor.Runnable. It serves until ctx ends and then
// shuts down gracefully. A listen error is permanent, because retrying the
// same address rarely helps and the user has to choose another one.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.cfg
	if s.cfg.TLSCert != "" {
		if _, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey); err != nil {
			return supervisor.Permanent(fmt.Errorf("load tls key pair: %w", err))
		}
	}
	ln, err := s.listen(ctx, s.cfg.Addr)
	if err != nil {
		return supervisor.Permanent(fmt.Errorf("listen on %s: %w", s.cfg.Addr, err))
	}
	// The request context ends with this serving (shutdown or rebind), so
	// open streams close with it; it does not inherit from the run or
	// caller context on purpose.
	requestCtx, cancel := context.WithCancel(context.Background())
	//
	//nolint:contextcheck // request context decoupled from the caller on purpose
	srv, errc := s.newServe(requestCtx)
	go s.serve(errc, srv, s.cfg.TLSCert != "", s.cfg.TLSCert, s.cfg.TLSKey, ln)

	s.mu.Lock()
	s.cur = &serving{ln: ln, srv: srv, err: errc, cancel: cancel}
	s.mu.Unlock()
	s.setAddr(ln.Addr())
	s.logger.InfoContext(ctx, "http server listening", "addr", ln.Addr().String(), "tls", cfg.TLSCert != "")

	<-ctx.Done()
	return s.stop(context.WithoutCancel(ctx))
}

// newServe builds the http.Server for ln, serving until the request
// context ends, so streams do not hold up the shutdown.
func (s *Server) newServe(ctx context.Context) (*http.Server, chan error) {
	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	return srv, make(chan error, 1)
}

// serve serves srv on ln until it stops and reports the result on errc.
func (s *Server) serve(errc chan error, srv *http.Server, tlsOn bool, cert, key string, ln net.Listener) {
	if tlsOn {
		errc <- srv.ServeTLS(ln, cert, key)
		return
	}
	errc <- srv.Serve(ln)
}

// stop shuts down the current server and returns a non-nil error if the
// server failed for a reason other than the shutdown.
func (s *Server) stop(ctx context.Context) error {
	s.mu.Lock()
	cur := s.cur
	s.mu.Unlock()
	if cur == nil {
		return nil
	}
	err := s.shutdownServing(ctx, cur)
	if serveErr := <-cur.err; !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, context.Canceled) {
		return errors.Join(err, fmt.Errorf("serve http: %w", serveErr))
	}
	if err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "http server stopped")
	return nil
}

// shutdownServing shuts down one server and waits for it to stop.
func (s *Server) shutdownServing(ctx context.Context, sv *serving) error {
	timeout := 5 * time.Second
	if s.timeout != nil {
		timeout = s.timeout()
	}
	if d := s.cfg.ShutdownTimeout; d > 0 && s.timeout == nil {
		timeout = d
	}
	// End the request context before the graceful shutdown, so open
	// streams close and the shutdown does not wait for them.
	if sv.cancel != nil {
		sv.cancel()
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	err := sv.srv.Shutdown(shutdownCtx)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			// The close errors are logged, not joined: the shutdown
			// error is the one that matters.
			s.logger.ErrorContext(ctx, "close http server", "error", sv.srv.Close())
			s.logger.ErrorContext(ctx, "close http listener", "error", sv.ln.Close())
			return fmt.Errorf("shut down http server: %w", err)
		}
		s.logger.WarnContext(ctx, "closing http connections that did not finish in time", "timeout", timeout)
		s.logger.ErrorContext(ctx, "close http server", "error", sv.srv.Close())
		// The listener is closed by http.Server; closing it again is
		// not an error to report.
		_ = sv.ln.Close()
	}
	return nil
}

// listen opens the listener of the configured address: TCP, or a Unix
// socket when the address starts with "unix:". A stale socket file from a
// crashed core is removed before listening; any other existing file is an
// error.
func (s *Server) listen(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	if !strings.HasPrefix(addr, "unix:") {
		return lc.Listen(ctx, "tcp", addr)
	}
	path := strings.TrimPrefix(addr, "unix:")
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return lc.Listen(ctx, "unix", path)
}

// corsAllowed reports whether the origin is on the current CORS allowlist.
func (s *Server) corsAllowed(origin string) bool {
	allow, _ := s.corsAllow.Load().([]string)
	return slices.Contains(allow, origin)
}

func (s *Server) setAddr(addr net.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addr == nil {
		close(s.listening)
	}
	s.addr = addr
}

func (s *Server) buildMux(ready func() bool) *http.ServeMux {
	mux := http.NewServeMux()
	// The health routes are registered without a method so they win over the
	// "/" catch-all for every method; anything but GET is a method error, as
	// it was before the API catch-all existed.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeText(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ready() {
			writeText(w, http.StatusOK, "ready")
			return
		}
		writeText(w, http.StatusServiceUnavailable, "not ready")
	})
	// The pprof routes are always mounted and dispatch on the developer
	// mode, so it can change at runtime (roadmap 6.6).
	mux.HandleFunc("GET /debug/pprof/", s.serveDev(pprof.Index))
	mux.HandleFunc("GET /debug/pprof/cmdline", s.serveDev(pprof.Cmdline))
	mux.HandleFunc("GET /debug/pprof/profile", s.serveDev(pprof.Profile))
	mux.HandleFunc("GET /debug/pprof/symbol", s.serveDev(pprof.Symbol))
	mux.HandleFunc("POST /debug/pprof/symbol", s.serveDev(pprof.Symbol))
	mux.HandleFunc("GET /debug/pprof/trace", s.serveDev(pprof.Trace))
	// Everything that is not a health or pprof route goes to the API
	// (roadmap 6.2). The more specific patterns above win over the "/"
	// catch-all.
	mux.HandleFunc("/", s.serveAPI)
	return mux
}

// serveDev serves a pprof route while the developer mode is on, 404
// otherwise.
func (s *Server) serveDev(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.dev.Load() {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		next(w, r)
	}
}

// SetAPI names the handler for the API routes; it is safe for concurrent use
// and may be called after the server is built, because the API needs services
// that are wired after the HTTP server (Code-ADR-0002, late binding).
func (s *Server) SetAPI(h http.Handler) {
	s.apiMu.Lock()
	s.apiHandler = h
	s.apiMu.Unlock()
}

// serveAPI dispatches a request to the API handler, or answers 404 before the
// API is set.
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	s.apiMu.RLock()
	h := s.apiHandler
	s.apiMu.RUnlock()
	if h == nil {
		http.Error(w, "the API is not available", http.StatusNotFound)
		return
	}
	h.ServeHTTP(w, r)
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
