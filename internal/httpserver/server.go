// SPDX-License-Identifier: Apache-2.0

// Package httpserver runs the HTTP server of the core. It serves the health
// endpoints /healthz and /readyz, pprof in developer mode and, from roadmap
// phase 6, the API.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
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
	// free port.
	Addr string
	// Dev serves pprof under /debug/pprof/. Only for loopback addresses.
	Dev bool
	// ShutdownTimeout limits the graceful shutdown of open connections.
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
	s.handler = newMux(cfg.Dev, ready)
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
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// Run implements supervisor.Runnable. It serves until ctx ends and then shuts
// down gracefully. A listen error is permanent, because retrying the same
// address rarely helps and the user has to choose another one.
func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", s.cfg.Addr)
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
	wg.Go(func() { serveErr <- srv.Serve(ln) })
	s.logger.InfoContext(ctx, "http server listening", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve http: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return errors.Join(fmt.Errorf("shut down http server: %w", err), srv.Close())
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve http: %w", err)
	}
	s.logger.InfoContext(ctx, "http server stopped")
	return nil
}

func (s *Server) setAddr(addr net.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.addr == nil {
		close(s.listening)
	}
	s.addr = addr
}

func newMux(dev bool, ready func() bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeText(w, http.StatusOK, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
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
	return mux
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body + "\n"))
}
