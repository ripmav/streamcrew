// SPDX-License-Identifier: MIT

// Package httpserver runs the HTTP server of the core. It serves the health
// endpoints /healthz and /readyz, pprof in developer mode and, from roadmap
// phase 6, the API.
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
	"strings"
	"sync"
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

// Server is the HTTP server. It is a supervisor.Runnable.
type Server struct {
	cfg     Config
	logger  *slog.Logger
	handler http.Handler

	mu        sync.Mutex
	addr      net.Addr
	listening chan struct{}

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
	s := &Server{cfg: cfg, logger: logger, listening: make(chan struct{})}
	s.handler = CrossOriginProtection(originIn(cfg.CorsAllow), s.buildMux(cfg.Dev, ready))
	return s
}

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

// Run implements supervisor.Runnable. It serves until ctx ends and then shuts
// down gracefully. A listen error is permanent, because retrying the same
// address rarely helps and the user has to choose another one.
func (s *Server) Run(ctx context.Context) error {
	ln, err := s.listen(ctx)
	if err != nil {
		return supervisor.Permanent(fmt.Errorf("listen on %s: %w", s.cfg.Addr, err))
	}
	s.setAddr(ln.Addr())

	srv := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelWarn),
		// Requests end when the server stops, so that streams do not hold up
		// the shutdown.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	var wg sync.WaitGroup
	defer wg.Wait()
	serveErr := make(chan error, 1)
	wg.Go(func() {
		if s.cfg.TLSCert != "" {
			if _, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey); err != nil {
				serveErr <- supervisor.Permanent(fmt.Errorf("load tls key pair: %w", err))
				return
			}
			serveErr <- srv.ServeTLS(ln, s.cfg.TLSCert, s.cfg.TLSKey)
			return
		}
		serveErr <- srv.Serve(ln)
	})
	s.logger.InfoContext(ctx, "http server listening", "addr", ln.Addr().String(), "tls", s.cfg.TLSCert != "")

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return errors.Join(fmt.Errorf("shut down http server: %w", err), srv.Close())
		}
		s.logger.WarnContext(ctx, "closing http connections that did not finish in time", "timeout", s.cfg.ShutdownTimeout)
		if err := srv.Close(); err != nil {
			return fmt.Errorf("close http server: %w", err)
		}
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	s.logger.InfoContext(ctx, "http server stopped")
	return nil
}

// listen opens the listener of the configured address: TCP, or a Unix
// socket when the address starts with "unix:". A stale socket file from a
// crashed core is removed before listening; any other existing file is an
// error.
func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	var lc net.ListenConfig
	if !strings.HasPrefix(s.cfg.Addr, "unix:") {
		return lc.Listen(ctx, "tcp", s.cfg.Addr)
	}
	path := strings.TrimPrefix(s.cfg.Addr, "unix:")
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

func (s *Server) setAddr(addr net.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addr == nil {
		close(s.listening)
	}
	s.addr = addr
}

func (s *Server) buildMux(dev bool, ready func() bool) *http.ServeMux {
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
	if dev {
		mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
	// Everything that is not a health or pprof route goes to the API (roadmap
	// 6.2). The more specific patterns above win over the "/" catch-all.
	mux.HandleFunc("/", s.serveAPI)
	return mux
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
