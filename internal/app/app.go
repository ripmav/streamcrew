// SPDX-License-Identifier: Apache-2.0

// Package app is the composition root of the core (Code-ADR-0002): it builds
// all components from the startup configuration, registers the long-running
// ones with the supervisor and runs them until the context ends.
//
// "streamcrew serve" uses it directly; the public start API core.Run
// (ADR-0006, roadmap phase 6) will be a thin wrapper around the same calls.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/config"
	"github.com/ripmav/streamcrew/internal/httpserver"
	"github.com/ripmav/streamcrew/internal/logging"
	"github.com/ripmav/streamcrew/internal/supervisor"
)

// logFileName is the name of the log file in the log directory.
const logFileName = "streamcrew.log"

// Option configures an App.
type Option func(*options)

type options struct {
	console io.Writer
}

// WithConsole sets the destination of the console log; the default is
// os.Stderr.
func WithConsole(w io.Writer) Option {
	return func(o *options) { o.console = w }
}

// App is the wired core.
type App struct {
	cfg      config.Config
	logger   *slog.Logger
	closeLog io.Closer
	sup      *supervisor.Supervisor
	http     *httpserver.Server
	ready    *readiness
}

// New builds the core from a resolved configuration (config.Config.Resolve).
// It creates the data directory and opens the log file.
func New(cfg config.Config, opts ...Option) (*App, error) {
	o := options{console: os.Stderr}
	for _, opt := range opts {
		opt(&o)
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	logger, closeLog, err := newLogger(cfg, o.console)
	if err != nil {
		return nil, err
	}

	a := &App{cfg: cfg, logger: logger, closeLog: closeLog, ready: newReadiness()}
	a.sup = supervisor.New(component(logger, "supervisor"),
		supervisor.WithStatusFunc(a.ready.update),
		supervisor.WithShutdownTimeout(cfg.ShutdownTimeout),
	)
	a.http = httpserver.New(httpserver.Config{
		Addr:            cfg.Listen,
		Dev:             cfg.Dev,
		ShutdownTimeout: cfg.ShutdownTimeout,
	}, component(logger, "http"), a.ready.Ready)

	if err := a.sup.Add("http", a.http, supervisor.WithCritical()); err != nil {
		return nil, errors.Join(err, closeLog.Close())
	}
	return a, nil
}

// Logger returns the root logger of the core.
func (a *App) Logger() *slog.Logger {
	return a.logger
}

// HTTPServer returns the HTTP server, e.g. to find its address.
func (a *App) HTTPServer() *httpserver.Server {
	return a.http
}

// Ready reports whether the core is ready: all runnables run and the core is
// not shutting down.
func (a *App) Ready() bool {
	return a.ready.Ready()
}

// Run runs the core until ctx ends or a critical component fails, then shuts
// it down and closes the log file. The returned error has been logged.
func (a *App) Run(ctx context.Context) error {
	defer a.closeLog.Close()

	stopWatching := context.AfterFunc(ctx, a.ready.stopping)
	defer stopWatching()

	info := buildinfo.Read()
	a.logger.InfoContext(ctx, "streamcrew starting",
		"version", info.Version, "mode", a.cfg.Mode, "data_dir", a.cfg.DataDir, "pid", os.Getpid())
	if a.cfg.Dev {
		a.logger.WarnContext(ctx, "developer mode is on: pprof is served under /debug/pprof/")
	}

	err := a.sup.Run(ctx)
	if err != nil {
		a.logger.ErrorContext(ctx, "streamcrew stopped with an error", "error", err)
		return err
	}
	a.logger.InfoContext(ctx, "streamcrew stopped")
	return nil
}

func newLogger(cfg config.Config, console io.Writer) (*slog.Logger, io.Closer, error) {
	level, err := config.ParseLevel(cfg.Log.Level)
	if err != nil {
		return nil, nil, fmt.Errorf("log level: %w", err)
	}
	levels := make(map[string]slog.Level, len(cfg.Log.ComponentLevel))
	for name, l := range cfg.Log.ComponentLevel {
		if levels[name], err = config.ParseLevel(l); err != nil {
			return nil, nil, fmt.Errorf("log level of %s: %w", name, err)
		}
	}
	lc := logging.Config{
		Level:           level,
		ComponentLevels: levels,
		Console:         console,
		ConsoleFormat:   logging.Format(cfg.Log.Format),
		MaxSize:         int64(cfg.Log.MaxSize) << 20,
		MaxFiles:        cfg.Log.MaxFiles,
	}
	if cfg.Log.File {
		lc.File = filepath.Join(cfg.LogDir(), logFileName)
	}
	logger, closer, err := logging.New(lc)
	if err != nil {
		return nil, nil, fmt.Errorf("set up logging: %w", err)
	}
	return logger, closer, nil
}

func component(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(logging.ComponentKey, name)
}

// readiness derives the readiness of the core from the states of its
// runnables.
type readiness struct {
	mu       sync.Mutex
	states   map[string]supervisor.State
	shutdown bool
}

func newReadiness() *readiness {
	return &readiness{states: make(map[string]supervisor.State)}
}

func (r *readiness) update(st supervisor.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[st.Name] = st.State
}

func (r *readiness) stopping() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shutdown = true
}

// Ready reports whether all runnables run and no shutdown has begun.
func (r *readiness) Ready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shutdown || len(r.states) == 0 {
		return false
	}
	for _, s := range r.states {
		if s != supervisor.StateRunning {
			return false
		}
	}
	return true
}
